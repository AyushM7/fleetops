//go:build fleetops_test

package fleet_test

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// This gateway exists only in the temporary, explicitly opted-in demo harness.
// Cookies are opaque capabilities, never backend bearer credentials.
type demoGateway struct {
	backend    http.Handler
	assets     http.Handler
	index      []byte
	operator   string
	origin     string
	cookieName string
	deadline   time.Time
	mu         sync.Mutex
	sessions   map[string]bool
}

func demoRandom() string {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("demo entropy unavailable")
	}
	return hex.EncodeToString(raw[:])
}

func newDemoGateway(backend http.Handler, operator, dist string) (*demoGateway, error) {
	index, err := os.ReadFile(filepath.Join(dist, "index.html"))
	if err != nil {
		return nil, err
	}
	return &demoGateway{backend: backend, assets: http.FileServer(http.Dir(dist)),
		index:    []byte(strings.Replace(string(index), "<head>", "<head><meta name=\"fleetops-local-demo\" content=\"enabled\">", 1)),
		operator: operator, cookieName: "fleetops_demo_" + demoRandom(), deadline: time.Now().Add(20 * time.Minute), sessions: map[string]bool{}}, nil
}

func (g *demoGateway) close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	clear(g.sessions)
	g.operator = ""
}

func (g *demoGateway) localRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return false
	}
	if "http://"+r.Host != g.origin {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != g.origin {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	return true
}

func (g *demoGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	if !g.localRequest(r) {
		http.Error(w, "local origin required", 403)
		return
	}
	g.mu.Lock()
	active := time.Now().Before(g.deadline) && g.operator != ""
	g.mu.Unlock()
	if !active {
		http.Error(w, "demo session ended", 401)
		return
	}
	if r.URL.Path == "/graphql" || r.URL.Path == "/demo/status" || r.URL.Path == "/demo/restart" || isTimeControl(r.URL.Path) {
		g.graphql(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	if r.URL.Path == "/" {
		g.openSession(w, r)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(g.index)
		return
	}
	// No directory listings, private files, or SPA fallback on unknown routes.
	if !strings.HasPrefix(r.URL.Path, "/assets/") || strings.Contains(r.URL.Path, "..") {
		http.NotFound(w, r)
		return
	}
	g.assets.ServeHTTP(w, r)
}

func (g *demoGateway) openSession(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if cookie, err := r.Cookie(g.cookieName); err == nil && g.sessions[cookie.Value] {
		return
	}
	// Bound in-memory sessions even if a local client repeatedly drops cookies.
	if len(g.sessions) >= 256 {
		return
	}
	id := demoRandom()
	g.sessions[id] = true
	http.SetCookie(w, &http.Cookie{Name: g.cookieName, Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Expires: g.deadline})
}

func (g *demoGateway) graphql(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	// Exact Origin plus a custom header protects every operation, including mutations.
	if r.Header.Get("Origin") != g.origin || r.Header.Get("X-FleetOps-Demo") != "1" ||
		r.Header.Get("Content-Type") != "application/json" || len(r.Header.Values("Authorization")) != 0 {
		http.Error(w, "demo request denied", 403)
		return
	}
	g.mu.Lock()
	cookie, err := r.Cookie(g.cookieName)
	valid := err == nil && g.sessions[cookie.Value]
	operator := g.operator
	g.mu.Unlock()
	if !valid || operator == "" {
		http.Error(w, "demo session required", 401)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/demo/") {
		body, err := io.ReadAll(io.LimitReader(r.Body, 3))
		if err != nil || string(body) != "{}" || r.URL.RawQuery != "" {
			http.Error(w, "fixed demo operation requires an empty object", 400)
			return
		}
	}
	forward := r.Clone(r.Context())
	forward.Header = r.Header.Clone()
	forward.Header.Del("Cookie")
	forward.Header.Set("Authorization", "Bearer "+operator)
	g.backend.ServeHTTP(w, forward)
}
