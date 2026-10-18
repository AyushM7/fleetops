//go:build fleetops_test

package fleet_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func controllerRequest(c http.Handler, path, generation string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://127.0.0.1"+path, strings.NewReader("{}"))
	r.Header.Set("X-FleetOps-Generation", generation)
	w := httptest.NewRecorder()
	c.ServeHTTP(w, r)
	return w
}

func unitController() *demoController {
	makeRun := func() *demoRun {
		return &demoRun{generation: demoRandom(), handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }), capture: func(context.Context) error { return nil }, advance: func(context.Context) error { return nil }}
	}
	return &demoController{run: makeRun(), ready: true, factory: func(context.Context) (*demoRun, error) { return makeRun(), nil }, record: func(string, string) error { return nil }}
}

func TestDemoControllerRepeatedRestart(t *testing.T) {
	c := unitController()
	seen := map[string]bool{c.run.generation: true}
	for range 20 {
		old := c.run.generation
		w := controllerRequest(c, "/demo/restart", old)
		var status demoStatus
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &status) != nil || !status.Ready || seen[status.Generation] {
			t.Fatal("not a fresh ready replacement", w.Body.String())
		}
		seen[status.Generation] = true
		for _, path := range []string{"/demo/restart", "/graphql"} {
			if got := controllerRequest(c, path, old).Code; got != 409 {
				t.Fatal("old generation accepted", path, got)
			}
		}
		if controllerRequest(c, "/graphql", "").Code != 409 {
			t.Fatal("generation is mandatory")
		}
	}
	if err := c.close(); err != nil {
		t.Fatal(err)
	}
	if controllerRequest(c, "/demo/restart", c.run.generation).Code != 503 || controllerRequest(c, "/graphql", c.run.generation).Code != 503 {
		t.Fatal("closed owner accepted work")
	}
}

func TestDemoControllerDrainsOperations(t *testing.T) {
	for _, operation := range []string{"decision", "tick"} {
		t.Run(operation, func(t *testing.T) {
			c := unitController()
			old := c.run.generation
			entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			block := func() { close(entered); <-release }
			if operation == "decision" {
				c.run.handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { block() })
			} else {
				c.run.advance = func(context.Context) error { block(); return nil }
			}
			var captured atomic.Bool
			c.run.capture = func(context.Context) error { captured.Store(true); return nil }
			go func() {
				defer close(finished)
				if operation == "decision" {
					controllerRequest(c, "/graphql", old)
				} else {
					_ = c.advance(context.Background())
				}
			}()
			<-entered
			response := make(chan *httptest.ResponseRecorder, 1)
			go func() { response <- controllerRequest(c, "/demo/restart", old) }()
			deadline := time.After(2 * time.Second)
			for !c.busy.Load() {
				select {
				case <-deadline:
					t.Fatal("restart did not start")
				default:
					time.Sleep(time.Millisecond)
				}
			}
			if captured.Load() {
				t.Fatal("capture raced active operation")
			}
			if controllerRequest(c, "/demo/restart", old).Code != 409 {
				t.Fatal("duplicate restart accepted")
			}
			close(release)
			<-finished
			select {
			case w := <-response:
				if w.Code != 200 || !captured.Load() {
					t.Fatal("restart failed to drain")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("restart leaked or deadlocked")
			}
			if controllerRequest(c, "/graphql", old).Code != 409 {
				t.Fatal("old decision reached retired handler")
			}
		})
	}
}

func TestDemoControllerFailures(t *testing.T) {
	for _, phase := range []string{"capture", "factory", "record", "identity"} {
		t.Run(phase, func(t *testing.T) {
			c := unitController()
			old := c.run
			failure := errors.New("persistence uncertain")
			switch phase {
			case "capture":
				c.run.capture = func(context.Context) error { return failure }
				c.factory = func(context.Context) (*demoRun, error) { t.Error("factory before capture"); return nil, failure }
			case "factory":
				c.factory = func(context.Context) (*demoRun, error) { return nil, failure }
			case "record":
				c.record = func(string, string) error { return failure }
			case "identity":
				c.factory = func(context.Context) (*demoRun, error) { return old, nil }
			}
			if controllerRequest(c, "/demo/restart", old.generation).Code != 503 || c.ready || c.run != old {
				t.Fatal("failure claimed replacement success")
			}
			if controllerRequest(c, "/graphql", old.generation).Code != 503 {
				t.Fatal("failed/uncertain runtime reused")
			}
		})
	}
	c := unitController()
	old := c.run.generation
	c.run.advance = func(context.Context) error { return errors.New("poisoned") }
	if c.advance(context.Background()) == nil || c.ready {
		t.Fatal("failed tick did not stop runtime")
	}
	if controllerRequest(c, "/demo/restart", old).Code != 200 || c.run.generation == old {
		t.Fatal("fresh replacement after capture failed")
	}
	c.run.capture = func(context.Context) error { return errors.New("disk failure") }
	if c.close() == nil {
		t.Fatal("shutdown swallowed capture failure")
	}
	dir := t.TempDir()
	if err := writeDemoEvidence(dir, "capture", map[string]bool{"ok": true}); err != nil {
		t.Fatal(err)
	}
	if err := writeDemoEvidence(dir, "capture", map[string]bool{"ok": true}); err != nil {
		t.Fatal(err)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 2 {
		t.Fatal("overwrote evidence")
	}
	if writeDemoEvidence(filepath.Join(dir, "missing"), "capture", nil) == nil {
		t.Fatal("missing directory reported success")
	}
}

func TestDemoControllerDisconnectedRestart(t *testing.T) {
	c := unitController()
	old := c.run.generation
	factory := c.factory
	c.factory = func(ctx context.Context) (*demoRun, error) {
		if ctx.Err() != nil {
			t.Fatal("browser disconnect cancelled owned replacement")
		}
		return factory(ctx)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("POST", "http://127.0.0.1/demo/restart", strings.NewReader("{}")).WithContext(ctx)
	r.Header.Set("X-FleetOps-Generation", old)
	cancel()
	w := httptest.NewRecorder()
	c.ServeHTTP(w, r)
	if w.Code != 200 || c.run.generation == old || !c.ready {
		t.Fatal("disconnected restart left ownership uncertain")
	}
	if controllerRequest(c, "/graphql", old).Code != 409 {
		t.Fatal("disconnected old runtime still reachable")
	}
}

func TestDemoControllerGatewaySecurity(t *testing.T) {
	dist := t.TempDir()
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("<head></head>"), 0600); err != nil {
		t.Fatal(err)
	}
	c := unitController()
	g, err := newDemoGateway(c, demoRandom(), dist)
	if err != nil {
		t.Fatal(err)
	}
	defer g.close()
	g.origin = "http://127.0.0.1:12345"
	bootstrap := httptest.NewRequest("GET", g.origin+"/", nil)
	bootstrap.RemoteAddr = "127.0.0.1:5000"
	page := httptest.NewRecorder()
	g.ServeHTTP(page, bootstrap)
	cookie := page.Result().Cookies()[0]
	for _, path := range []string{"/demo/status", "/demo/restart", "/demo/scenario/blocked-reassignment", "/demo/scenario/routine-charging", "/demo/scenario/healthy-operations", "/demo/scenario/blocked-no-candidate", "/demo/play", "/demo/pause", "/demo/step", "/demo/speed/0.5", "/demo/speed/1", "/demo/speed/2"} {
		for name, change := range map[string]func(*http.Request){
			"origin":              func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example") },
			"missing origin":      func(r *http.Request) { r.Header.Del("Origin") },
			"csrf":                func(r *http.Request) { r.Header.Del("X-FleetOps-Demo") },
			"auth":                func(r *http.Request) { r.Header.Del("Cookie") },
			"bearer":              func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+demoRandom()) },
			"host":                func(r *http.Request) { r.Host = "evil.example" },
			"peer":                func(r *http.Request) { r.RemoteAddr = "192.0.2.1:5000" },
			"fetch site":          func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
			"content type":        func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") },
			"method":              func(r *http.Request) { r.Method = "GET" },
			"configuration":       func(r *http.Request) { r.Body = http.NoBody },
			"query configuration": func(r *http.Request) { r.URL.RawQuery = "seed=9" },
			"expired":             func(r *http.Request) { g.deadline = time.Now().Add(-time.Second) },
		} {
			t.Run(path+"/"+name, func(t *testing.T) {
				g.deadline = time.Now().Add(time.Minute)
				r := httptest.NewRequest("POST", g.origin+path, strings.NewReader("{}"))
				r.RemoteAddr = "127.0.0.1:5000"
				r.AddCookie(cookie)
				r.Header.Set("Origin", g.origin)
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("X-FleetOps-Demo", "1")
				r.Header.Set("X-FleetOps-Generation", c.run.generation)
				old := c.run.generation
				change(r)
				w := httptest.NewRecorder()
				g.ServeHTTP(w, r)
				if w.Code < 400 || c.run.generation != old {
					t.Fatal("unsafe lifecycle access", w.Code)
				}
			})
		}
	}
}
