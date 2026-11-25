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
				if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].Domain != "" {
					t.Fatal("unsafe production cookie")
				}
				if w.Header().Get("Strict-Transport-Security") == "" {
					t.Fatal("HSTS missing")
				}
			}
		})
	}
}

func TestPUB02ExternalDocumentNavigation(t *testing.T) {
	g, _ := visitorUnitGateway(t)
	g.config.Mode, g.origin, g.host = "production", "https://fleet.example", "fleet.example"
	g.cookieName = "__Host-fleetops-fixture"
	_, proxy, _ := net.ParseCIDR("127.0.0.1/32")
	g.proxies = []*net.IPNet{proxy}

	request := func(path, site, mode, dest string, cookie *http.Cookie) *http.Request {
		r := rawRequest(g, path)
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("Sec-Fetch-Site", site)
		r.Header.Set("Sec-Fetch-Mode", mode)
		r.Header.Set("Sec-Fetch-Dest", dest)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		return r
	}
	serve := func(r *http.Request) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w
	}
	direct := serve(request("/", "none", "navigate", "document", nil))
	if direct.Code != http.StatusOK || len(direct.Result().Cookies()) != 1 {
		t.Fatal("direct navigation could not start a session", direct.Code)
	}
	github := request("/", "cross-site", "navigate", "document", nil)
	github.Header.Set("Referer", "https://github.com/")
	github.Header.Set("Sec-Fetch-User", "?1")
	first := serve(github)
	cookies := first.Result().Cookies()
	if first.Code != http.StatusOK || len(cookies) != 1 {
		t.Fatal("external link could not start a session", first.Code, first.Body.String())
	}
	cookie := cookies[0]
	if cookie.SameSite != http.SameSiteLaxMode || !cookie.Secure || !cookie.HttpOnly ||
		cookie.Path != "/" || cookie.Domain != "" || cookie.Name != g.cookieName {
		t.Fatal("unsafe visitor cookie")
	}
	session := g.manager.lookup(cookie.Value)
	if session == nil {
		t.Fatal("created session missing")
	}
	deadline := session.expiresAt()
	for _, tc := range []struct {
		name, method, path, mode, dest string
		provideOrigin                  bool
	}{
		{"graphql", "POST", "/graphql", "cors", "empty", false},
		{"recovery", "POST", "/demo/recovery", "cors", "empty", false},
		{"form-POST", "POST", "/", "navigate", "document", false},
		{"SSE", "GET", "/demo/events", "cors", "empty", false},
		{"iframe", "GET", "/", "navigate", "iframe", false},
		{"root fetch", "GET", "/", "cors", "empty", false},
		{"script", "GET", "/assets/main.js", "no-cors", "script", false},
		{"another document", "GET", "/readyz", "navigate", "document", false},
		{"query", "GET", "/?via=github", "navigate", "document", false},
		{"missing metadata", "GET", "/", "", "", false},
		{"foreign origin", "GET", "/", "navigate", "document", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := request(tc.path, "cross-site", tc.mode, tc.dest, cookie)
			r.Method = tc.method
			if tc.provideOrigin {
				r.Header.Set("Origin", "https://github.com")
			}
			if w := serve(r); w.Code != http.StatusForbidden || w.Body.String() != "request boundary denied\n" || w.Header().Get("Set-Cookie") != "" {
				t.Fatal("external request escaped the document boundary", w.Code)
			}
		})
	}
	duplicate := request("/", "cross-site", "navigate", "document", cookie)
	duplicate.Header.Add("Sec-Fetch-Dest", "iframe")
	if w := serve(duplicate); w.Code != http.StatusForbidden {
		t.Fatal("ambiguous destination accepted", w.Code)
	}
	for range visitorLimit - 2 {
		if _, _, err := g.manager.create(); err != nil {
			t.Fatal(err)
		}
	}
	budget := g.creations
	busy := serve(request("/", "cross-site", "navigate", "document", nil))
	if busy.Code != http.StatusServiceUnavailable || busy.Header().Get("Retry-After") != "60" ||
		busy.Header().Get("Set-Cookie") != "" || !strings.Contains(busy.Body.String(), "All simulation slots are in use") ||
		g.creations != budget {
		t.Fatal("external link bypassed capacity or spent creation budget", busy.Code)
	}
	for _, site := range []string{"cross-site", "same-site", "none"} {
		again := serve(request("/", site, "navigate", "document", cookie))
		if again.Code != http.StatusOK || again.Header().Get("Set-Cookie") != "" ||
			g.manager.lookup(cookie.Value) != session || !session.expiresAt().Equal(deadline) || g.creations != budget {
			t.Fatal("navigation did not reuse the existing session", site, again.Code)
		}
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

func TestVisitorCapacityPageAndReadiness(t *testing.T) {
	g, _ := visitorUnitGateway(t)
	cookie := openVisitor(t, g)
	session := g.manager.lookup(cookie.Value)
	for range visitorLimit - 1 {
		if _, _, err := g.manager.create(); err != nil {
			t.Fatal(err)
		}
	}
	g.creations = bucket{tokens: 1, at: time.Now()}
	initialBudget := g.creations

	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		g.ServeHTTP(w, rawRequest(g, path))
		return w
	}
	busy := get("/")
	if busy.Code != http.StatusServiceUnavailable || busy.Header().Get("Retry-After") != "60" ||
		busy.Header().Get("Set-Cookie") != "" || busy.Header().Get("Cache-Control") != "no-store" ||
		!strings.Contains(busy.Header().Get("Content-Type"), "text/html") ||
		!strings.Contains(busy.Body.String(), "All simulation slots are in use") ||
		!strings.Contains(busy.Body.String(), `href="/">Try again`) || g.creations != initialBudget {
		t.Fatalf("bad capacity response: %d %v %s", busy.Code, busy.Header(), busy.Body)
	}
	if css := get("/visitor-busy.css"); css.Code != http.StatusOK || !strings.Contains(css.Header().Get("Content-Type"), "text/css") {
		t.Fatal("capacity stylesheet unavailable", css.Code)
	}
	existing := rawRequest(g, "/")
	existing.AddCookie(cookie)
	current := httptest.NewRecorder()
	g.ServeHTTP(current, existing)
	if current.Code != http.StatusOK || g.creations != initialBudget {
		t.Fatal("at-capacity request with an existing session was rejected or charged", current.Code)
	}
	if ready := get("/readyz"); ready.Code != http.StatusServiceUnavailable ||
		ready.Header().Get("Retry-After") != "60" || !strings.Contains(ready.Body.String(), "capacity exhausted") {
		t.Fatal("readiness did not report capacity", ready.Code, ready.Body)
	}
	g.manager.admit = func(context.Context) error { return errors.New("private database or storage failure") }
	for _, path := range []string{"/", "/readyz"} {
		w := get(path)
		if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "" ||
			strings.Contains(w.Body.String(), "slots") || strings.Contains(w.Body.String(), "private") {
			t.Fatal("infrastructure failure misclassified or disclosed", path, w.Code, w.Body)
		}
	}
	g.manager.admit = nil
	g.manager.retire(cookie.Value, session)
	if ready := get("/readyz"); ready.Code != http.StatusOK {
		t.Fatal("readiness did not recover after cleanup", ready.Code)
	}
	if home := get("/"); home.Code != http.StatusOK || len(home.Result().Cookies()) != 1 {
		t.Fatal("visitor was not admitted after cleanup", home.Code)
	}
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

func TestWarehouseChargeTargetFromEnv(t *testing.T) {
	t.Setenv("FLEETOPS_MODE", "local")
	t.Setenv("FLEETOPS_PUBLIC_ORIGIN", "http://127.0.0.1:8080")
	t.Setenv("FLEETOPS_TRUSTED_PROXIES", "")
	t.Setenv("FLEETOPS_ASSETS_DIR", testAssets(t))
	t.Setenv("FLEETOPS_EVIDENCE_DIR", t.TempDir())
	t.Setenv("FLEETOPS_DATABASE_URL", "postgres://localhost/fleetops_test")
	t.Setenv("FLEETOPS_WAREHOUSE_CHARGE_TARGET", "85")
	cfg, err := ConfigFromEnv()
	if err != nil || cfg.WarehouseChargeTarget != 85 {
		t.Fatalf("target 85: got %d, %v", cfg.WarehouseChargeTarget, err)
	}
	for _, invalid := range []string{"", "0", "20", "84", "86", "100", "101", "100%", "-1"} {
		t.Setenv("FLEETOPS_WAREHOUSE_CHARGE_TARGET", invalid)
		if _, err := ConfigFromEnv(); err == nil {
			t.Fatalf("invalid target %q accepted", invalid)
		}
	}
}

func TestPublicWarehouseStartupIsExplicit(t *testing.T) {
	t.Setenv("FLEETOPS_MODE", "production")
	t.Setenv("FLEETOPS_PUBLIC_ORIGIN", "https://fleetops.example.test")
	t.Setenv("FLEETOPS_TRUSTED_PROXIES", "")
	t.Setenv("FLEETOPS_ASSETS_DIR", testAssets(t))
	t.Setenv("FLEETOPS_EVIDENCE_DIR", t.TempDir())
	t.Setenv("FLEETOPS_DATABASE_URL", "postgres://localhost/fleetops_test")
	t.Setenv("FLEETOPS_LOCAL_START_WAREHOUSE_V2", "1")
	for _, enabled := range []string{"", "1"} {
		t.Setenv("FLEETOPS_START_WAREHOUSE_V2", enabled)
		config, err := ConfigFromEnv()
		if err != nil || config.StartInWarehouseV2 != (enabled == "1") {
			t.Fatalf("production warehouse start %q: got %t, %v", enabled, config.StartInWarehouseV2, err)
		}
	}
}
