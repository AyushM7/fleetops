//go:build fleetops_test

package fleet_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func visitorUnitGateway(t *testing.T) (*visitorGateway, *atomic.Int32) {
	t.Helper()
	var cleaned atomic.Int32
	m := newVisitorManager(func(context.Context) (*visitorSession, error) {
		return &visitorSession{controller: unitController(), operator: demoRandom(), cleanup: func() error { cleaned.Add(1); return nil }}, nil
	})
	t.Cleanup(func() {
		if err := m.close(); err != nil {
			t.Error(err)
		}
	})
	dist := t.TempDir()
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("<head></head>"), 0600); err != nil {
		t.Fatal(err)
	}
	g, err := newVisitorGateway(m, dist)
	if err != nil {
		t.Fatal(err)
	}
	g.origin = "http://127.0.0.1:12345"
	return g, &cleaned
}

func visitorRequest(g *visitorGateway, cookie *http.Cookie, path, generation, body string, change func(*http.Request)) *httptest.ResponseRecorder {
	method := "POST"
	if path == "/" {
		method = "GET"
	}
	r := httptest.NewRequest(method, g.origin+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:5000"
	if cookie != nil {
		r.AddCookie(cookie)
	}
	r.Header.Set("Origin", g.origin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-FleetOps-Demo", "1")
	r.Header.Set("X-FleetOps-Generation", generation)
	if change != nil {
		change(r)
	}
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	return w
}

func openVisitor(t *testing.T, g *visitorGateway) *http.Cookie {
	t.Helper()
	w := visitorRequest(g, nil, "/", "", "", nil)
	cookies := w.Result().Cookies()
	if w.Code != 200 || len(cookies) != 1 {
		t.Fatal("bootstrap failed", w.Code, w.Body.String())
	}
	c := cookies[0]
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Domain != "" || len(c.Value) != 64 || c.Path != "/" {
		t.Fatal("unsafe visitor cookie")
	}
	if strings.Contains(w.Body.String(), c.Value) {
		t.Fatal("cookie reflected in page")
	}
	return c
}

func TestPUB01HostileRequests(t *testing.T) {
	g, _ := visitorUnitGateway(t)
	cookie := openVisitor(t, g)
	s := g.manager.lookup(cookie.Value)
	for _, path := range []string{"/graphql", "/demo/status", "/demo/restart", "/demo/scenario/healthy-operations", "/demo/play", "/demo/pause", "/demo/step", "/demo/speed/2"} {
		for name, change := range map[string]func(*http.Request){
			"origin":           func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") },
			"missing origin":   func(r *http.Request) { r.Header.Del("Origin") },
			"csrf":             func(r *http.Request) { r.Header.Del("X-FleetOps-Demo") },
			"bearer":           func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+s.operator) },
			"host":             func(r *http.Request) { r.Host = "evil.example" },
			"peer":             func(r *http.Request) { r.RemoteAddr = "192.0.2.1:5000" },
			"fetch site":       func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
			"content type":     func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") },
			"method":           func(r *http.Request) { r.Method = "GET" },
			"query":            func(r *http.Request) { r.URL.RawQuery = "session=" + cookie.Value },
			"no cookie":        func(r *http.Request) { r.Header.Del("Cookie") },
			"forged cookie":    func(r *http.Request) { r.Header.Set("Cookie", g.cookieName+"="+demoRandom()) },
			"duplicate cookie": func(r *http.Request) { r.AddCookie(cookie) },
		} {
			t.Run(path+"/"+name, func(t *testing.T) {
				w := visitorRequest(g, cookie, path, s.controller.run.generation, "{}", change)
				if w.Code < 400 {
					t.Fatal("hostile request accepted", w.Code, w.Body.String())
				}
			})
		}
	}
	w := visitorRequest(g, cookie, "/", "", "", nil)
	if w.Code != 200 || len(w.Result().Cookies()) != 0 || g.manager.lookup(cookie.Value) != s {
		t.Fatal("refresh reset visitor")
	}
	for _, path := range []string{"/demo/unknown", "/demo/scenario/visitor-config", "/private.json", "/history/a.json"} {
		if w := visitorRequest(g, cookie, path, s.controller.run.generation, "{}", func(r *http.Request) {
			if !strings.HasPrefix(path, "/demo/") {
				r.Method = "GET"
			}
		}); w.Code != 404 {
			t.Fatal("private route", path, w.Code)
		}
	}
	g.manager.retire(cookie.Value, s)
	if w := visitorRequest(g, cookie, "/graphql", s.controller.run.generation, "{}", nil); w.Code != 401 {
		t.Fatal("retired capability accepted")
	}
}

func TestPUB01ExpirationCapacityAndCleanup(t *testing.T) {
	g, cleaned := visitorUnitGateway(t)
	g.manager.ttl = 250 * time.Millisecond
	a := openVisitor(t, g)
	first := g.manager.lookup(a.Value)
	deadline := time.Now().Add(3 * time.Second)
	for g.manager.lookup(a.Value) != nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if g.manager.lookup(a.Value) != nil {
		t.Fatal("expiry worker did not revoke session")
	}
	first.mu.Lock()
	closed := first.closed
	first.mu.Unlock()
	if !closed || cleaned.Load() != 1 {
		t.Fatal("expiry failed to stop resources")
	}
	g.manager.ttl = visitorTTL
	for range visitorLimit {
		openVisitor(t, g)
	}
	if w := visitorRequest(g, nil, "/", "", "", nil); w.Code != 503 {
		t.Fatal("capacity unbounded", w.Code)
	}
	if err := g.manager.close(); err != nil {
		t.Fatal(err)
	}
	if cleaned.Load() != visitorLimit+1 {
		t.Fatal("shutdown leaked visitors", cleaned.Load())
	}
	if w := visitorRequest(g, nil, "/", "", "", nil); w.Code != 503 {
		t.Fatal("closed manager initialized visitor")
	}
}

func TestPUB01ConcurrentInitializationShutdown(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var cleaned atomic.Int32
	m := newVisitorManager(func(context.Context) (*visitorSession, error) {
		close(entered)
		<-release
		return &visitorSession{controller: unitController(), cleanup: func() error { cleaned.Add(1); return nil }}, nil
	})
	var wg sync.WaitGroup
	wg.Go(func() {
		if _, _, err := m.create(); err == nil {
			t.Error("shutdown published initialized session")
		}
	})
	<-entered
	finished := make(chan error, 1)
	go func() { finished <- m.close() }()
	for {
		m.mu.Lock()
		closed := m.closed
		m.mu.Unlock()
		if closed {
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	wg.Wait()
	if err := <-finished; err != nil || cleaned.Load() != 1 {
		t.Fatal("initialization leaked after shutdown", err)
	}
}

func TestPUB01FailuresNeverFallback(t *testing.T) {
	g, _ := visitorUnitGateway(t)
	a, b := openVisitor(t, g), openVisitor(t, g)
	sa, sb := g.manager.lookup(a.Value), g.manager.lookup(b.Value)
	sa.controller.record = func(string, string) error { return errors.New("disk failure") }
	if w := visitorRequest(g, a, "/demo/restart", sa.controller.run.generation, "{}", nil); w.Code != 503 {
		t.Fatal("restart failure accepted")
	}
	if w := visitorRequest(g, a, "/graphql", sa.controller.run.generation, "{}", nil); w.Code != 503 {
		t.Fatal("failed visitor routed elsewhere")
	}
	if w := visitorRequest(g, b, "/graphql", sb.controller.run.generation, "{}", nil); w.Code != 200 {
		t.Fatal("failure affected B")
	}
	g.manager.mu.Lock()
	g.manager.factory = func(context.Context) (*visitorSession, error) { return nil, errors.New("database unavailable") }
	g.manager.mu.Unlock()
	if w := visitorRequest(g, nil, "/", "", "", nil); w.Code != 503 || len(w.Result().Cookies()) != 0 {
		t.Fatal("failed initialization issued capability")
	}
}

func TestPUB01RuntimePanicIsolation(t *testing.T) {
	g, _ := visitorUnitGateway(t)
	a, b := openVisitor(t, g), openVisitor(t, g)
	sa, sb := g.manager.lookup(a.Value), g.manager.lookup(b.Value)
	sa.controller.mu.Lock()
	sa.controller.run.advance = func(context.Context) error { panic("broken runtime") }
	sa.controller.mu.Unlock()
	sa.mu.Lock()
	sa.advance(context.Background())
	sa.mu.Unlock()
	if w := visitorRequest(g, a, "/graphql", sa.controller.run.generation, "{}", nil); w.Code != 503 {
		t.Fatal("panicked runtime accessible")
	}
	if w := visitorRequest(g, b, "/graphql", sb.controller.run.generation, "{}", nil); w.Code != 200 {
		t.Fatal("runtime panic affected B")
	}
}

func TestPUB01ConcurrentVisitorsAndRestarts(t *testing.T) {
	g, _ := visitorUnitGateway(t)
	var wg sync.WaitGroup
	var created atomic.Int32
	for range 32 {
		wg.Go(func() {
			id, session, err := g.manager.create()
			if err != nil {
				return
			}
			created.Add(1)
			cookie := &http.Cookie{Name: g.cookieName, Value: id}
			for range 10 {
				session.mu.Lock()
				generation := session.controller.run.generation
				session.mu.Unlock()
				if w := visitorRequest(g, cookie, "/demo/restart", generation, "{}", nil); w.Code != 200 {
					t.Error("concurrent restart failed", w.Code)
				}
				if w := visitorRequest(g, cookie, "/graphql", generation, "{}", nil); w.Code != 409 {
					t.Error("stale generation accepted", w.Code)
				}
			}
		})
	}
	wg.Wait()
	if created.Load() != visitorLimit {
		t.Fatal("concurrent admission exceeded capacity or leaked reservations", created.Load())
	}
}

func TestPUB01FailedCapabilityIssuanceCleansInitializedRuntime(t *testing.T) {
	g, cleaned := visitorUnitGateway(t)
	g.manager.issue = func() (string, error) { return "", errors.New("entropy unavailable") }
	w := visitorRequest(g, nil, "/", "", "", nil)
	if w.Code != 503 || len(w.Result().Cookies()) != 0 || cleaned.Load() != 1 {
		t.Fatal("failed capability issuance leaked a runtime")
	}
	g.manager.mu.Lock()
	defer g.manager.mu.Unlock()
	if len(g.manager.sessions) != 0 || g.manager.initializing != 0 {
		t.Fatal("failed capability issuance leaked admission")
	}
}

func TestPUB01CaptureFailureStillRevokesAndCloses(t *testing.T) {
	var cleaned atomic.Int32
	m := newVisitorManager(func(context.Context) (*visitorSession, error) {
		return &visitorSession{controller: unitController(), operator: demoRandom(), cleanup: func() error { cleaned.Add(1); return nil }}, nil
	})
	t.Cleanup(func() { _ = m.close() })
	id, a, err := m.create()
	if err != nil {
		t.Fatal(err)
	}
	_, b, err := m.create()
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("capture unavailable")
	a.controller.mu.Lock()
	a.controller.run.capture = func(context.Context) error { return failure }
	a.controller.mu.Unlock()
	m.retire(id, a)
	if m.lookup(id) != nil || !a.closed || a.operator != "" || cleaned.Load() != 1 {
		t.Fatal("failed capture retained capabilities or resources")
	}
	r := httptest.NewRequest("POST", "/graphql", strings.NewReader("{}"))
	r.Header.Set("X-FleetOps-Generation", b.controller.run.generation)
	w := httptest.NewRecorder()
	b.serve(w, r)
	if w.Code != 200 {
		t.Fatal("A capture failure affected B")
	}
	if err := m.close(); !errors.Is(err, failure) || cleaned.Load() != 2 {
		t.Fatal("shutdown swallowed capture failure or leaked resources", err)
	}
}

func TestPUB01RequestDeadlineBoundToSession(t *testing.T) {
	g, _ := visitorUnitGateway(t)
	cookie := openVisitor(t, g)
	s := g.manager.lookup(cookie.Value)
	s.controller.mu.Lock()
	s.controller.run.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadline, ok := r.Context().Deadline()
		if !ok || deadline.After(s.deadline) {
			t.Error("operation outlives visitor capability")
		}
		w.WriteHeader(200)
	})
	s.controller.mu.Unlock()
	if w := visitorRequest(g, cookie, "/graphql", s.controller.run.generation, "{}", nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
}
