package visitor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const visitorLimit = 8
const visitorTTL = 20 * time.Minute

var errVisitorCapacity = errors.New("visitor capacity exhausted")
var errVisitorCreationRate = errors.New("visitor creation rate exceeded")

type visitorSession struct {
	mu         sync.Mutex
	controller *demoController
	operator   string
	deadline   atomic.Pointer[time.Time]
	closed     bool
	cleanup    func() error
	done       chan struct{}
	operations int
	restarts   int
	streams    int
}

type visitorManager struct {
	mu            sync.Mutex
	sessions      map[string]*visitorSession
	initializing  int
	closed        bool
	factory       func(context.Context) (*visitorSession, error)
	issue         func() (string, error)
	done          chan struct{}
	workers       sync.WaitGroup
	initializers  sync.WaitGroup
	cleanupErrors []error
	ttl           time.Duration
	owned         int
	blocked       bool
	closeStarted  bool
	closeFinished chan struct{}
	admit         func(context.Context) error
}

func newVisitorManager(factory func(context.Context) (*visitorSession, error)) *visitorManager {
	return &visitorManager{sessions: map[string]*visitorSession{}, factory: factory, issue: visitorRandom, done: make(chan struct{}), ttl: visitorTTL}
}

func visitorRandom() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", errors.New("visitor entropy unavailable")
	}
	return hex.EncodeToString(raw[:]), nil
}

func (m *visitorManager) create() (string, *visitorSession, error) {
	return m.createWithLimit(nil)
}

func (m *visitorManager) createWithLimit(allowCreation func() bool) (string, *visitorSession, error) {
	m.mu.Lock()
	if m.closed || m.blocked {
		m.mu.Unlock()
		return "", nil, errors.New("visitor admission unavailable")
	}
	if m.owned >= visitorLimit {
		m.mu.Unlock()
		return "", nil, errVisitorCapacity
	}
	if allowCreation != nil && !allowCreation() {
		m.mu.Unlock()
		return "", nil, errVisitorCreationRate
	}
	m.initializing++
	m.owned++
	m.initializers.Add(1)
	factory, issue := m.factory, m.issue
	m.mu.Unlock()
	defer m.initializers.Done()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var s *visitorSession
	var err error
	if m.admit != nil {
		err = m.admit(ctx)
	}
	if err == nil {
		s, err = factory(ctx)
	}
	var id string
	if err == nil && s != nil {
		id, err = issue()
	}
	m.mu.Lock()
	m.initializing--
	if err != nil || s == nil || m.closed {
		m.mu.Unlock()
		if errors.Is(err, errVisitorRetained) {
			m.remember(err)
		}
		if s != nil {
			m.remember(s.close())
		}
		m.mu.Lock()
		m.owned--
		m.mu.Unlock()
		return "", nil, errors.New("visitor initialization unavailable")
	}
	s.deadline.Store(new(time.Now().Add(m.ttl)))
	s.done = make(chan struct{})
	m.sessions[id] = s
	m.workers.Add(1)
	m.mu.Unlock()
	go m.run(id, s)
	slog.Info("visitor_session_started")
	return id, s, nil
}

func (m *visitorManager) lookup(id string) *visitorSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	return m.sessions[id]
}

func (m *visitorManager) run(id string, s *visitorSession) {
	defer m.workers.Done()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(time.Until(s.expiresAt()))
	defer timer.Stop()
	for {
		select {
		case <-m.done:
			return
		case <-s.done:
			return
		case <-timer.C:
			m.retire(id, s)
			return
		case <-ticker.C:
			s.mu.Lock()
			sessionDeadline := s.expiresAt()
			expired := !time.Now().Before(sessionDeadline)
			if !s.closed && !expired {
				deadline := time.Now().Add(10 * time.Second)
				if sessionDeadline.Before(deadline) {
					deadline = sessionDeadline
				}
				ctx, cancel := context.WithDeadline(context.Background(), deadline)
				s.advance(ctx)
				cancel()
			}
			s.mu.Unlock()
			if expired {
				m.retire(id, s)
				return
			}
		}
	}
}

// Revocation precedes draining and persistence; failed cleanup cannot restore access.
func (m *visitorManager) retire(id string, s *visitorSession) {
	m.mu.Lock()
	if m.sessions[id] != s {
		m.mu.Unlock()
		return
	}
	delete(m.sessions, id)
	m.mu.Unlock()
	m.remember(s.close())
	slog.Info("visitor_session_revoked")
	m.mu.Lock()
	m.owned--
	m.mu.Unlock()
}

func (m *visitorManager) remember(err error) {
	if err == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.cleanupErrors) < visitorLimit {
		m.cleanupErrors = append(m.cleanupErrors, err)
	}
	if errors.Is(err, errVisitorRetained) {
		m.blocked = true
	}
	slog.Error("visitor_cleanup_incomplete", "schema_retained", errors.Is(err, errVisitorRetained))
}

func (s *visitorSession) close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.done != nil {
		close(s.done)
	}
	err := s.controller.close()
	s.operator = ""
	return errors.Join(err, s.cleanup())
}

func (m *visitorManager) revoke() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed {
		m.closed = true
		close(m.done)
	}
}

func (m *visitorManager) close() error {
	m.revoke()
	m.mu.Lock()
	if m.closeStarted {
		done := m.closeFinished
		m.mu.Unlock()
		<-done
		m.mu.Lock()
		defer m.mu.Unlock()
		return errors.Join(m.cleanupErrors...)
	}
	m.closeStarted = true
	m.closeFinished = make(chan struct{})
	sessions := m.sessions
	m.sessions = map[string]*visitorSession{}
	m.mu.Unlock()
	m.initializers.Wait()
	m.workers.Wait()
	var cleanup sync.WaitGroup
	for _, s := range sessions {
		cleanup.Go(func() { m.remember(s.close()) })
	}
	cleanup.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	close(m.closeFinished)
	return errors.Join(m.cleanupErrors...)
}

func (s *visitorSession) expiresAt() time.Time { return *s.deadline.Load() }

func (s *visitorSession) tryRequest(w http.ResponseWriter) bool {
	if s.mu.TryLock() {
		return true
	}
	// A long tick or write may hold the lock past the session deadline.
	if !time.Now().Before(s.expiresAt()) {
		http.Error(w, "visitor session expired", http.StatusUnauthorized)
	} else {
		http.Error(w, "visitor busy; refresh status", http.StatusTooManyRequests)
	}
	return false
}

func (s *visitorSession) serve(w http.ResponseWriter, r *http.Request) {
	if !s.tryRequest(w) {
		return
	}
	defer s.mu.Unlock()
	defer func() {
		if recover() != nil {
			s.stopRuntime()
			http.Error(w, "visitor runtime unavailable; reconcile unknown outcomes", 503)
		}
	}()
	deadline := s.expiresAt()
	if s.closed || !time.Now().Before(deadline) {
		http.Error(w, "visitor session expired; open a new session", 401)
		return
	}
	if r.URL.Path != "/demo/status" {
		if s.operations >= 2048 {
			http.Error(w, "visitor operation budget exhausted", 429)
			return
		}
		s.operations++
	}
	if r.URL.Path == "/demo/restart" || strings.HasPrefix(r.URL.Path, "/demo/scenario/") {
		if s.restarts >= 8 {
			http.Error(w, "visitor restart budget exhausted", 429)
			return
		}
		s.restarts++
	}
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	defer cancel()
	forward := r.Clone(ctx)
	forward.Header = r.Header.Clone()
	forward.Header.Del("Cookie")
	forward.Header.Set("Authorization", "Bearer "+s.operator)
	s.controller.ServeHTTP(w, forward)
}

func (s *visitorSession) advance(ctx context.Context) {
	defer func() {
		if recover() != nil {
			s.stopRuntime()
		}
	}()
	_ = s.controller.advance(ctx)
}

func (s *visitorSession) stopRuntime() {
	s.controller.mu.Lock()
	defer s.controller.mu.Unlock()
	s.controller.cancelOperations()
	s.controller.ready, s.controller.running = false, false
	s.controller.run.events.signal()
}
