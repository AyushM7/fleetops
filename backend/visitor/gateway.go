package visitor

import (
	"context"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type bucket struct {
	tokens float64
	at     time.Time
}

func (b *bucket) take(now time.Time, capacity, rate float64) bool {
	if b.at.IsZero() {
		b.tokens = capacity
	} else {
		b.tokens += now.Sub(b.at).Seconds() * rate
	}
	if b.tokens > capacity {
		b.tokens = capacity
	}
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

type visitorGateway struct {
	manager                  *visitorManager
	config                   Config
	origin, host, cookieName string
	index                    []byte
	assets                   map[string][]byte
	proxies                  []*net.IPNet
	concurrency              chan struct{}
	mu                       sync.Mutex
	requests, creations      bucket
	peers                    map[string]*bucket
}

func newVisitorGateway(m *visitorManager, config Config) (*visitorGateway, error) {
	index, err := os.ReadFile(filepath.Join(config.AssetsDir, "index.html"))
	if err != nil || len(index) > 1<<20 || !strings.Contains(string(index), "<head>") {
		return nil, errors.New("built visitor frontend unavailable")
	}
	info, err := os.Lstat(filepath.Join(config.AssetsDir, "index.html"))
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("regular frontend index required")
	}
	u, _ := url.Parse(config.Origin)
	g := &visitorGateway{manager: m, config: config, origin: config.Origin, host: u.Host,
		index:      []byte(strings.Replace(string(index), "<head>", `<head><meta name="fleetops-local-demo" content="enabled">`, 1)),
		cookieName: "fleetops_" + demoRandom(), concurrency: make(chan struct{}, 32), peers: map[string]*bucket{}, assets: map[string][]byte{}}
	if config.Mode == "production" {
		g.cookieName = "__Host-" + g.cookieName
	}
	for _, value := range config.TrustedProxies {
		_, network, _ := net.ParseCIDR(value)
		g.proxies = append(g.proxies, network)
	}
	var size int64
	err = filepath.WalkDir(filepath.Join(config.AssetsDir, "assets"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("asset symlink denied")
		}
		if entry.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".js", ".css", ".svg", ".png", ".jpg", ".jpeg", ".ico", ".woff", ".woff2":
		default:
			return errors.New("unexpected frontend asset type")
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 {
			return errors.New("asset budget exceeded")
		}
		size += info.Size()
		if size > 32<<20 || len(g.assets) >= 128 {
			return errors.New("asset budget exceeded")
		}
		relative, err := filepath.Rel(config.AssetsDir, path)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		g.assets["/"+filepath.ToSlash(relative)] = raw
		return err
	})
	return g, err
}

func (g *visitorGateway) boundary(r *http.Request) bool {
	if r.Host != g.host || r.URL.RawQuery != "" || r.URL.IsAbs() || len(r.Header.Values("Origin")) > 1 || len(r.Header.Values("Sec-Fetch-Site")) > 1 {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != g.origin {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	trusted := false
	for _, network := range g.proxies {
		trusted = trusted || network.Contains(ip)
	}
	forwarded := false
	for _, name := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP"} {
		if len(r.Header.Values(name)) > 0 {
			forwarded = true
		}
		if len(r.Header.Values(name)) > 1 {
			return false
		}
	}
	if forwarded && !trusted || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Real-IP") != "" {
		return false
	}
	if value := r.Header.Get("X-Forwarded-Host"); value != "" && value != g.host {
		return false
	}
	if value := r.Header.Get("X-Forwarded-For"); value != "" && net.ParseIP(value) == nil {
		return false
	}
	if g.config.Mode == "local" {
		return ip.IsLoopback() && !forwarded && r.TLS == nil
	}
	if r.TLS != nil {
		return !forwarded || r.Header.Get("X-Forwarded-Proto") == "https"
	}
	return trusted && r.Header.Get("X-Forwarded-Proto") == "https"
}

func (g *visitorGateway) rate(r *http.Request) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	if !g.requests.take(now, 128, 64) {
		return false
	}
	peer, _, _ := net.SplitHostPort(r.RemoteAddr)
	b := g.peers[peer]
	if b == nil {
		for key, value := range g.peers {
			if now.Sub(value.at) > 5*time.Minute {
				delete(g.peers, key)
			}
		}
		if len(g.peers) >= 256 {
			return false
		}
		b = &bucket{}
		g.peers[peer] = b
	}
	return b.take(now, 96, 64)
}

func (g *visitorGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	if g.config.Mode == "production" {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
	}
	if !g.boundary(r) {
		http.Error(w, "request boundary denied", 403)
		return
	}
	if r.Method == http.MethodGet && r.ContentLength != 0 {
		http.Error(w, "GET body denied", 413)
		return
	}
	if r.URL.Path == "/healthz" {
		g.health(w, r)
		return
	}
	select {
	case g.concurrency <- struct{}{}:
		defer func() { <-g.concurrency }()
	default:
		http.Error(w, "visitor server busy", 503)
		return
	}
	if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
		g.health(w, r)
		return
	}
	if !g.rate(r) {
		w.Header().Set("Retry-After", "2")
		http.Error(w, "request rate exceeded", 429)
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
	raw, ok := g.assets[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if contentType := mime.TypeByExtension(filepath.Ext(r.URL.Path)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if strings.HasSuffix(r.URL.Path, ".js") {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	}
	if strings.HasSuffix(r.URL.Path, ".css") {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	}
	_, _ = w.Write(raw)
}

func (g *visitorGateway) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	if r.URL.Path == "/readyz" {
		g.manager.mu.Lock()
		ready := !g.manager.closed && !g.manager.blocked && g.manager.owned < visitorLimit
		g.manager.mu.Unlock()
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if !ready || g.manager.admit != nil && g.manager.admit(ctx) != nil {
			http.Error(w, "visitor readiness unavailable", 503)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"status":"ok"}`)
}

func (g *visitorGateway) credential(r *http.Request) (string, int) {
	id, count := "", 0
	for _, header := range r.Header.Values("Cookie") {
		for _, pair := range strings.Split(header, ";") {
			name, value, _ := strings.Cut(strings.TrimSpace(pair), "=")
			if name == g.cookieName {
				count++
				id = value
			}
		}
	}
	return id, count
}

func (g *visitorGateway) session(r *http.Request) *visitorSession {
	id, count := g.credential(r)
	if count != 1 || len(id) != 64 {
		return nil
	}
	return g.manager.lookup(id)
}

func (g *visitorGateway) bootstrap(w http.ResponseWriter, r *http.Request) {
	if len(r.Header.Values("Authorization")) != 0 || r.ContentLength > 0 {
		http.Error(w, "visitor bootstrap denied", 400)
		return
	}
	_, count := g.credential(r)
	s := g.session(r)
	if count > 0 && s == nil {
		http.Error(w, "visitor session invalid; clear site cookies to begin again", 401)
		return
	}
	if s != nil {
		if !s.mu.TryLock() {
			http.Error(w, "visitor busy", 429)
			return
		}
		valid := !s.closed && time.Now().Before(s.deadline)
		s.mu.Unlock()
		if !valid {
			http.Error(w, "visitor session expired", 401)
			return
		}
	} else {
		g.mu.Lock()
		allowed := g.creations.take(time.Now(), 8, 1.0/60)
		g.mu.Unlock()
		if !allowed {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "visitor creation rate exceeded", 429)
			return
		}
		id, created, err := g.manager.create()
		if err != nil {
			http.Error(w, "visitor session unavailable", 503)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: g.cookieName, Value: id, Path: "/", HttpOnly: true, Secure: g.config.Mode == "production", SameSite: http.SameSiteStrictMode, Expires: created.deadline, MaxAge: int(visitorTTL.Seconds())})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(g.index)
}

func (g *visitorGateway) operation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	if len(r.Header.Values("Content-Type")) != 1 || r.Header.Get("Origin") != g.origin || len(r.Header.Values("X-FleetOps-Demo")) != 1 || r.Header.Get("X-FleetOps-Demo") != "1" || r.Header.Get("Content-Type") != "application/json" || len(r.Header.Values("Authorization")) != 0 || len(r.Header.Values("X-FleetOps-Generation")) > 1 || len(r.Header.Values("X-FleetOps-Tick")) > 1 {
		http.Error(w, "visitor request denied", 403)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/demo/") {
		if !visitorOperation(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		if r.ContentLength > 2 {
			http.Error(w, "operation body exceeded", 413)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 3))
		if err != nil || string(body) != "{}" {
			http.Error(w, "fixed operation requires empty object", 400)
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
	return path == "/demo/status" || path == "/demo/restart" || isTimeControl(path) || strings.HasPrefix(path, "/demo/scenario/") && allowedScenario(strings.TrimPrefix(path, "/demo/scenario/"))
}
