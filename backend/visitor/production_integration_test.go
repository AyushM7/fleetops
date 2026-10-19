package visitor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/db"
)

func TestPUB02HTTPSProductionAndShutdownIntegration(t *testing.T) {
	target := os.Getenv("FLEETOPS_TEST_DATABASE_URL")
	if target == "" {
		if os.Getenv("FLEETOPS_REQUIRE_DB") == "1" {
			t.Fatal("required PostgreSQL unavailable")
		}
		t.Skip("PostgreSQL not configured")
	}
	// A native Linux directory exercises production ownership and directory fsync;
	// Docker Desktop host bind mounts present synthetic 0777 permissions.
	root := os.Getenv("FLEETOPS_PUB02_PRIVATE_EVIDENCE")
	if root == "" {
		root = os.TempDir()
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	evidence, err := os.MkdirTemp(root, "https-production-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("preserved private production evidence:", evidence)
	server := httptest.NewUnstartedServer(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app, err := New(ctx, Config{Mode: "production", Origin: "https://" + server.Listener.Addr().String(), AssetsDir: integrationAssets(t), EvidenceDir: evidence, Database: db.DefaultConfig(target)})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = app
	server.StartTLS()
	t.Cleanup(func() {
		server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := app.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	client := server.Client()
	client.Jar, _ = cookiejar.New(nil)
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !strings.Contains(string(raw), "fleetops-local-demo") || len(response.Cookies()) != 1 || !response.Cookies()[0].Secure || !strings.HasPrefix(response.Cookies()[0].Name, "__Host-") {
		t.Fatal("production bootstrap/cookie failed", response.StatusCode)
	}
	r, _ := http.NewRequest("POST", server.URL+"/demo/status", strings.NewReader("{}"))
	r.Header.Set("Origin", server.URL)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-FleetOps-Demo", "1")
	response, err = client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = io.ReadAll(response.Body)
	response.Body.Close()
	var status demoStatus
	if response.StatusCode != 200 || json.Unmarshal(raw, &status) != nil || status.Evidence == nil || status.Evidence.ProposalSource != demoSourceGoScripted || status.State != "awaiting_decision" {
		t.Fatal("HTTPS production path missing", response.StatusCode, string(raw))
	}
	for _, check := range []struct {
		body string
		want int
	}{
		{strings.Repeat(" ", 65537), 413},
		{`{"query":"mutation{injectLowBattery(robotId:\"R1\")}"}`, 422},
	} {
		operation, _ := http.NewRequest("POST", server.URL+"/graphql", strings.NewReader(check.body))
		operation.Header = r.Header.Clone()
		operation.Header.Del("Cookie")
		operation.Header.Set("X-FleetOps-Generation", status.Generation)
		result, err := client.Do(operation)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(result.Body)
		result.Body.Close()
		if result.StatusCode != check.want {
			t.Fatal("production request boundary failed", result.StatusCode, string(body))
		}
	}
	// Pause only the final cleanup callback, after revocation; the real archive
	// and owned-schema drop remain the production callbacks once released.
	app.gateway.manager.mu.Lock()
	var session *visitorSession
	for _, s := range app.gateway.manager.sessions {
		session = s
	}
	app.gateway.manager.mu.Unlock()
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	session.mu.Lock()
	cleanup := session.cleanup
	session.cleanup = func() error { close(entered); <-release; return cleanup() }
	session.mu.Unlock()
	deadline, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err = app.Shutdown(deadline)
	stop()
	if err == nil || !errors.Is(deadline.Err(), context.DeadlineExceeded) {
		t.Fatal("shutdown did not enforce caller deadline", err)
	}
	<-entered
	revoked := r.Clone(context.Background())
	revoked.Header.Del("Cookie")
	revoked.Body = io.NopCloser(strings.NewReader("{}"))
	response, err = client.Do(revoked)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("shutdown retained browser capabilities", response.StatusCode)
	}
	close(release)
	finish, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	if err := app.Shutdown(finish); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(evidence, "fleetops_visitor_*", "database-final-*.json"))
	if err != nil || len(files) != 1 {
		t.Fatal("shutdown archive missing", err)
	}
	var archive visitorArchive
	raw, err = os.ReadFile(files[0])
	if err != nil || json.Unmarshal(raw, &archive) != nil || len(archive.Proposals) != 1 || len(archive.Audit) != 1 {
		t.Fatal("shutdown lost durable pending workflow evidence", err)
	}
}

func TestPUB02ExpiredSessionDeniedOnEveryRoute(t *testing.T) {
	g, _ := visitorUnitGateway(t)
	cookie := openVisitor(t, g)
	s := g.manager.lookup(cookie.Value)
	s.mu.Lock()
	s.deadline = time.Now().Add(-time.Second)
	generation := s.controller.run.generation
	s.mu.Unlock()
	for _, path := range []string{"/", "/graphql", "/demo/status", "/demo/restart", "/demo/scenario/healthy-operations", "/demo/play", "/demo/pause", "/demo/step", "/demo/speed/0.5", "/demo/speed/1", "/demo/speed/2"} {
		body := "{}"
		if path == "/" {
			body = ""
		}
		if w := visitorRequest(g, cookie, path, generation, body, nil); w.Code != 401 {
			t.Fatal("expired session route accepted", path, w.Code)
		}
	}
}
