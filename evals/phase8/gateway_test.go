//go:build fleetops_test

package fleet_test

import (
	"io"
	"net/http"
	"strings"
	"time"
)

// Retain DEMO-09's loopback, Host, Origin, Fetch-Site and asset restrictions.
type visitorGateway struct {
	*demoGateway
	manager *visitorManager
}

func newVisitorGateway(m *visitorManager, dist string) (*visitorGateway, error) {
	g, err := newDemoGateway(nil, "", dist)
	if err != nil {
		return nil, err
	}
	return &visitorGateway{demoGateway: g, manager: m}, nil
}

func (g *visitorGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	if !g.localRequest(r) {
		http.Error(w, "local origin required", 403)
		return
	}
	if r.URL.Path == "/graphql" || strings.HasPrefix(r.URL.Path, "/demo/") {
		g.operation(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	if r.URL.Path == "/" {
		g.bootstrap(w, r)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/assets/") || strings.Contains(r.URL.Path, "..") {
		http.NotFound(w, r)
		return
	}
	g.assets.ServeHTTP(w, r)
}

func (g *visitorGateway) session(r *http.Request) *visitorSession {
	var id string
	count := 0
	for _, c := range r.Cookies() {
		if c.Name == g.cookieName {
			id = c.Value
			count++
		}
	}
	if count != 1 {
		return nil
	}
	return g.manager.lookup(id)
}

func (g *visitorGateway) bootstrap(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || len(r.Header.Values("Authorization")) != 0 {
		http.Error(w, "visitor bootstrap denied", 400)
		return
	}
	s := g.session(r)
	if s != nil {
		s.mu.Lock()
		valid := !s.closed && time.Now().Before(s.deadline)
		s.mu.Unlock()
		if !valid {
			s = nil
		}
	}
	if s == nil {
		id, created, err := g.manager.create()
		if err != nil {
			http.Error(w, "visitor session unavailable", 503)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: g.cookieName, Value: id, Path: "/", HttpOnly: true,
			SameSite: http.SameSiteStrictMode, Expires: created.deadline})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(g.index)
}

func (g *visitorGateway) operation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	if r.Header.Get("Origin") != g.origin || r.Header.Get("X-FleetOps-Demo") != "1" ||
		r.Header.Get("Content-Type") != "application/json" || len(r.Header.Values("Authorization")) != 0 || r.URL.RawQuery != "" {
		http.Error(w, "visitor request denied", 403)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/demo/") {
		if !visitorOperation(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 3))
		if err != nil || string(body) != "{}" {
			http.Error(w, "fixed operation requires an empty object", 400)
			return
		}
	}
	s := g.session(r)
	if s == nil {
		http.Error(w, "visitor session required", 401)
		return
	}
	s.serve(w, r)
}

func visitorOperation(path string) bool {
	return path == "/demo/status" || path == "/demo/restart" || isTimeControl(path) ||
		strings.HasPrefix(path, "/demo/scenario/") && allowedScenario(strings.TrimPrefix(path, "/demo/scenario/"))
}
