//go:build fleetops_test

package fleet_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"sync"
	"time"
)

const visitorLimit = 8
const visitorTTL = 20 * time.Minute

type visitorSession struct {
	mu         sync.Mutex
	controller *demoController
	operator   string
	deadline   time.Time
	closed     bool
	cleanup    func() error
	done       chan struct{}
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
	m.mu.Lock()
	if m.closed || len(m.sessions)+m.initializing >= visitorLimit {
		m.mu.Unlock()
		return "", nil, errors.New("visitor capacity unavailable")
	}
	m.initializing++
	m.initializers.Add(1)
	factory, issue := m.factory, m.issue
	m.mu.Unlock()
	defer m.initializers.Done()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s, err := factory(ctx)
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
		return "", nil, errors.New("visitor initialization unavailable")
	}
	s.deadline = time.Now().Add(m.ttl)
	s.done = make(chan struct{})
	m.sessions[id] = s
	m.workers.Add(1)
	m.mu.Unlock()
	go m.run(id, s)
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
	timer := time.NewTimer(time.Until(s.deadline))
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
			expired := !time.Now().Before(s.deadline)
			if !s.closed && !expired {
				deadline := time.Now().Add(10 * time.Second)
				if s.deadline.Before(deadline) {
					deadline = s.deadline
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
	if m.sessions[id] == s {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	m.remember(s.close())
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

func (m *visitorManager) close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	close(m.done)
	sessions := m.sessions
	m.sessions = map[string]*visitorSession{}
	m.mu.Unlock()
	m.initializers.Wait()
	m.workers.Wait()
	for _, s := range sessions {
		m.remember(s.close())
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return errors.Join(m.cleanupErrors...)
}

func (s *visitorSession) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if recover() != nil {
			s.stopRuntime()
			http.Error(w, "visitor runtime unavailable; reconcile unknown outcomes", 503)
		}
	}()
	if s.closed || !time.Now().Before(s.deadline) {
		http.Error(w, "visitor session expired; open a new session", 401)
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), s.deadline)
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
	s.controller.ready, s.controller.running = false, false
}
