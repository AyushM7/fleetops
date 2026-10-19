package visitor

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/graphql/model"
)

func testAssets(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	for path, raw := range map[string]string{"index.html": "<head></head>", "assets/main.js": "console.log('fixture')"} {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func integrationAssets(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("FLEETOPS_PUB02_ASSETS"); path != "" {
		return path
	}
	return testAssets(t)
}

func rawRequest(g *visitorGateway, path string) *http.Request {
	r := httptest.NewRequest("GET", path, nil)
	r.Host, r.RemoteAddr = g.host, "127.0.0.1:12345"
	return r
}

func TestPUB02ProductionTLSAndForwarding(t *testing.T) {
	g, _ := visitorUnitGateway(t)
	g.config.Mode, g.config.Origin = "production", "https://fleet.example"
	g.origin, g.host, g.cookieName = g.config.Origin, "fleet.example", "__Host-fleetops-fixture"
	for _, tc := range []struct {
		name                                     string
		tls, proxy                               bool
		peer, proto, forwardedHost, forwardedFor string
		want                                     int
	}{
		{"plaintext", false, false, "127.0.0.1:44", "", "", "", 403},
		{"direct-tls", true, false, "192.0.2.9:44", "", "", "", 200},
		{"untrusted-forwarding", false, false, "192.0.2.9:44", "https", "", "", 403},
		{"trusted-tls-proxy", false, true, "127.0.0.1:44", "https", "fleet.example", "192.0.2.9", 200},
		{"proxy-http", false, true, "127.0.0.1:44", "http", "", "", 403},
		{"proxy-spoofed-host", false, true, "127.0.0.1:44", "https", "evil.example", "", 403},
		{"proxy-chain", false, true, "127.0.0.1:44", "https", "", "192.0.2.1,192.0.2.2", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g.proxies = nil
			if tc.proxy {
				_, network, _ := net.ParseCIDR("127.0.0.1/32")
				g.proxies = append(g.proxies, network)
			}
			r := rawRequest(g, "/")
			r.RemoteAddr = tc.peer
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if tc.proto != "" {
				r.Header.Set("X-Forwarded-Proto", tc.proto)
			}
			if tc.forwardedHost != "" {
				r.Header.Set("X-Forwarded-Host", tc.forwardedHost)
			}
			if tc.forwardedFor != "" {
				r.Header.Set("X-Forwarded-For", tc.forwardedFor)
			}
			w := httptest.NewRecorder()
			g.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatal(w.Code, w.Body.String())
			}
			if w.Code == 200 {
				cookies := w.Result().Cookies()
				if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Domain != "" {
					t.Fatal("unsafe production cookie")
				}
				if w.Header().Get("Strict-Transport-Security") == "" {
					t.Fatal("HSTS missing")
				}
			}
		})
	}
}

func TestPUB02BootstrapAmbiguityAndHeaders(t *testing.T) {
	g, _ := visitorUnitGateway(t)
	cookie := openVisitor(t, g)
	if cookie.Secure {
		t.Fatal("local HTTP cookie marked secure")
	}
	for _, value := range []string{g.cookieName + "=", g.cookieName + "=bad", g.cookieName + "=\"bad\"", g.cookieName + "=" + cookie.Value + "; " + g.cookieName + "=malformed"} {
		r := rawRequest(g, "/")
		r.Header.Set("Cookie", value)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != 401 || len(w.Result().Cookies()) != 0 {
			t.Fatal("ambiguous bootstrap created visitor", w.Code)
		}
	}
	for _, mutate := range []func(*http.Request){
		func(r *http.Request) { r.Header.Add("Origin", g.origin); r.Header.Add("Origin", g.origin) },
		func(r *http.Request) { r.Header.Set("Forwarded", "proto=https") },
		func(r *http.Request) {
			r.Header.Add("Sec-Fetch-Site", "none")
			r.Header.Add("Sec-Fetch-Site", "same-origin")
		},
	} {
		r := rawRequest(g, "/")
		mutate(r)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("ambiguous boundary accepted", w.Code)
		}
	}
	for _, path := range []string{"/assets/", "/assets/../index.html", "/.env", "/demo/inject-fault", "/demo/scenario/custom"} {
		r := rawRequest(g, path)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code < 400 {
			t.Fatal("private or directory route exposed", path)
		}
	}
	r := rawRequest(g, "/assets/main.js")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("asset headers missing")
	}
}

func TestPUB02RateAndConcurrencyBounds(t *testing.T) {
	g, _ := visitorUnitGateway(t)
	for range 96 {
		if !g.rate(rawRequest(g, "/")) {
			t.Fatal("initial request budget unavailable")
		}
	}
	if g.rate(rawRequest(g, "/")) {
		t.Fatal("peer rate unbounded")
	}
	g.requests = bucket{}
	g.peers = map[string]*bucket{}
	for i := 0; i < 256; i++ {
		g.peers[string(rune(i))] = &bucket{at: time.Now()}
	}
	if g.rate(rawRequest(g, "/")) || len(g.peers) != 256 {
		t.Fatal("peer table unbounded")
	}
	for range cap(g.concurrency) {
		g.concurrency <- struct{}{}
	}
	w := httptest.NewRecorder()
	g.ServeHTTP(w, rawRequest(g, "/"))
	if w.Code != 503 {
		t.Fatal("request queued after concurrency exhaustion")
	}
	for range cap(g.concurrency) {
		<-g.concurrency
	}
	g.requests = bucket{}
	g.peers = map[string]*bucket{}
	g.creations = bucket{at: time.Now()}
	w = httptest.NewRecorder()
	g.ServeHTTP(w, rawRequest(g, "/"))
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal("creation rate unbounded", w.Code)
	}
}

func TestPUB02ReadinessRecoveryAndRetentionBackpressure(t *testing.T) {
	g, _ := visitorUnitGateway(t)
	unavailable := true
	g.manager.admit = func(context.Context) error {
		if unavailable {
			return errors.New("private database detail")
		}
		return nil
	}
	check := func(path string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		g.ServeHTTP(w, rawRequest(g, path))
		if w.Code != want || strings.Contains(w.Body.String(), "private database") {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	check("/healthz", 200)
	check("/readyz", 503)
	unavailable = false
	check("/readyz", 200)
	g.manager.remember(retainVisitorSchema(visitorSchemaOwner{name: "private"}, errors.New("uncertain archive")))
	check("/readyz", 503)
	if _, _, err := g.manager.create(); err == nil {
		t.Fatal("retained evidence did not block admission")
	}
	check("/healthz", 200)
}

func TestPUB02ClosingResourcesConsumeCapacity(t *testing.T) {
	g, _ := visitorUnitGateway(t)
	cookie := openVisitor(t, g)
	s := g.manager.lookup(cookie.Value)
	entered, release := make(chan struct{}), make(chan struct{})
	s.cleanup = func() error { close(entered); <-release; return nil }
	finished := make(chan struct{})
	go func() { g.manager.retire(cookie.Value, s); close(finished) }()
	<-entered
	for range visitorLimit - 1 {
		if _, _, err := g.manager.create(); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := g.manager.create(); err == nil {
		t.Fatal("draining resources did not count toward capacity")
	}
	close(release)
	<-finished
	if _, _, err := g.manager.create(); err != nil {
		t.Fatal("capacity not restored", err)
	}
}

func TestPUB02ConfigRejectsUnsafeDefaults(t *testing.T) {
	base := Config{Mode: "local", Origin: "http://127.0.0.1:8080", AssetsDir: testAssets(t), EvidenceDir: t.TempDir()}
	if err := base.validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Config){
		func(c *Config) { c.Mode = "" }, func(c *Config) { c.Mode = "production" },
		func(c *Config) { c.Origin = "http://0.0.0.0:8080" }, func(c *Config) { c.Origin += "/path" },
		func(c *Config) { c.Origin = "http://user@127.0.0.1:8080" }, func(c *Config) { c.TrustedProxies = []string{"0.0.0.0/0"} },
		func(c *Config) { c.EvidenceDir = filepath.Join(c.AssetsDir, "private") },
	} {
		c := base
		change(&c)
		if c.validate() == nil {
			t.Fatal("unsafe configuration accepted")
		}
	}
}

func TestPUB02StorageBackpressurePreservesEvidence(t *testing.T) {
	dir := t.TempDir()
	file, err := os.Create(filepath.Join(dir, "preserved.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(evidenceLimit + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	file.Close()
	if storageReady(dir) == nil {
		t.Fatal("evidence size budget not enforced")
	}
	if _, err := os.Stat(filepath.Join(dir, "preserved.json")); err != nil {
		t.Fatal("evidence deleted for quota", err)
	}
}

func TestPUB02VisitorBodyAndOperationBudgets(t *testing.T) {
	g, _ := visitorUnitGateway(t)
	cookie := openVisitor(t, g)
	s := g.manager.lookup(cookie.Value)
	generation := s.controller.run.generation
	if w := visitorRequest(g, cookie, "/demo/status", generation, strings.Repeat("x", 65537), nil); w.Code != 413 {
		t.Fatal("control body unbounded", w.Code)
	}
	if w := visitorRequest(g, cookie, "/demo/status", generation, "{}extra", func(r *http.Request) { r.ContentLength = -1 }); w.Code != 400 {
		t.Fatal("unknown-length control body unbounded", w.Code)
	}
	s.mu.Lock()
	s.operations = 2047
	s.mu.Unlock()
	if w := visitorRequest(g, cookie, "/graphql", generation, "{}", nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := visitorRequest(g, cookie, "/graphql", generation, "{}", nil); w.Code != 429 {
		t.Fatal("operation budget unbounded", w.Code)
	}
	if w := visitorRequest(g, cookie, "/demo/status", generation, "{}", nil); w.Code != 200 {
		t.Fatal("budget exhaustion hid status")
	}
}

func TestPUB02AutoplayTickBudget(t *testing.T) {
	c := unitController()
	c.interactive = true
	c.running = true
	c.ticks = 1199
	c.run.inspect = func(context.Context) (*model.FleetState, bool, error) { return &model.FleetState{}, true, nil }
	steps := 0
	c.run.step = func(context.Context) error { steps++; return nil }
	if err := c.advance(context.Background()); err != nil || c.ready || c.running || steps != 1 {
		t.Fatal("autoplay tick budget not enforced", err)
	}
	if err := c.advance(context.Background()); err != nil || steps != 1 {
		t.Fatal("tick after budget exhausted", err)
	}
}
