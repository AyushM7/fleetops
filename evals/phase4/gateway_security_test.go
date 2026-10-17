//go:build fleetops_test

package fleet_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDemoGatewaySecurity(t *testing.T) {
	dist := t.TempDir()
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("<html><head></head><body>warehouse</body></html>"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer server-only-credential" || r.Header.Get("Cookie") != "" {
			t.Error("incorrect backend authentication")
		}
		_, _ = w.Write([]byte(`{"data":{}}`))
	})
	g, err := newDemoGateway(backend, "server-only-credential", dist)
	if err != nil {
		t.Fatal(err)
	}
	g.origin = "http://127.0.0.1:12345"
	request := func(method, path string, cookie *http.Cookie, change func(*http.Request)) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, g.origin+path, strings.NewReader(`{"query":"{fleetState{tick}}"}`))
		r.RemoteAddr = "127.0.0.1:54321"
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if method == "POST" {
			r.Header.Set("Origin", g.origin)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-FleetOps-Demo", "1")
		}
		if change != nil {
			change(r)
		}
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w
	}
	page := request("GET", "/", nil, nil)
	cookies := page.Result().Cookies()
	if page.Code != 200 || len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Domain != "" {
		t.Fatal("unsafe bootstrap cookie")
	}
	body, _ := io.ReadAll(page.Result().Body)
	if !strings.Contains(string(body), "fleetops-local-demo") || strings.Contains(string(body), g.operator) || strings.Contains(string(body), cookies[0].Value) {
		t.Fatal("unsafe bootstrap document")
	}
	second := request("GET", "/", nil, nil).Result().Cookies()[0]
	if second.Value == cookies[0].Value {
		t.Fatal("sessions must differ")
	}
	if len(request("GET", "/", cookies[0], nil).Result().Cookies()) != 0 {
		t.Fatal("reload rotated valid session")
	}
	if request("POST", "/graphql", cookies[0], nil).Code != 200 {
		t.Fatal("session cannot authenticate")
	}
	if request("POST", "/graphql", second, nil).Code != 200 {
		t.Fatal("second session cannot authenticate")
	}
	for name, change := range map[string]func(*http.Request){
		"cross origin":   func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example") },
		"null origin":    func(r *http.Request) { r.Header.Set("Origin", "null") },
		"missing origin": func(r *http.Request) { r.Header.Del("Origin") },
		"missing csrf":   func(r *http.Request) { r.Header.Del("X-FleetOps-Demo") },
		"form post":      func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") },
		"external peer":  func(r *http.Request) { r.RemoteAddr = "192.0.2.1:123" },
		"dns rebinding":  func(r *http.Request) { r.Host = "attacker.example:12345" },
		"cross site":     func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
		"browser bearer": func(r *http.Request) { r.Header.Set("Authorization", "Bearer anything") },
	} {
		t.Run(name, func(t *testing.T) {
			if request("POST", "/graphql", cookies[0], change).Code != 403 {
				t.Fatal("unsafe request accepted")
			}
		})
	}
	if request("POST", "/graphql", nil, nil).Code != 401 {
		t.Fatal("unauthenticated endpoint")
	}
	forged := *cookies[0]
	forged.Value = demoRandom()
	if request("POST", "/graphql", &forged, nil).Code != 401 {
		t.Fatal("forged session accepted")
	}
	other, err := newDemoGateway(backend, "different-server-credential", dist)
	if err != nil {
		t.Fatal(err)
	}
	other.origin = g.origin
	r := httptest.NewRequest("POST", g.origin+"/graphql", strings.NewReader("{}"))
	r.RemoteAddr = "127.0.0.1:54321"
	r.Header.Set("Origin", g.origin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-FleetOps-Demo", "1")
	foreign := *cookies[0]
	foreign.Name = other.cookieName
	r.AddCookie(&foreign)
	w := httptest.NewRecorder()
	other.ServeHTTP(w, r)
	other.close()
	if w.Code != 401 {
		t.Fatal("another gateway accepted this session")
	}
	delete(g.sessions, second.Value)
	if request("POST", "/graphql", second, nil).Code != 401 {
		t.Fatal("revoked session accepted")
	}
	if request("POST", "/graphql", cookies[0], nil).Code != 200 {
		t.Fatal("revoking another session leaked state")
	}
	for _, path := range []string{"/session.json", "/index.html", "/assets/../session.json"} {
		if request("GET", path, cookies[0], nil).Code != 404 {
			t.Fatal("unexpected static path", path)
		}
	}
	if request("GET", "/graphql", cookies[0], nil).Code != 405 {
		t.Fatal("GET graphql accepted")
	}
	if request("OPTIONS", "/graphql", cookies[0], nil).Code != 405 {
		t.Fatal("CORS preflight accepted")
	}
	if request("GET", "/", nil, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }).Code != 403 {
		t.Fatal("cross-site bootstrap accepted")
	}
	if calls != 3 {
		t.Fatal("denied request reached backend", calls)
	}
	g.deadline = time.Now().Add(-time.Second)
	if request("POST", "/graphql", cookies[0], nil).Code != 401 {
		t.Fatal("expired session accepted")
	}
	g.close()
	if len(g.sessions) != 0 || g.operator != "" {
		t.Fatal("credentials survived cleanup")
	}
	if request("GET", "/", nil, nil).Code != 401 {
		t.Fatal("closed gateway reopened")
	}
}
