package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplicationPublicVisitorsIntegration(t *testing.T) {
	databaseURL := os.Getenv("FLEETOPS_TEST_DATABASE_URL")
	if databaseURL == "" {
		if os.Getenv("FLEETOPS_REQUIRE_DB") == "1" {
			t.Fatal("required database unavailable")
		}
		t.Skip("PostgreSQL not configured")
	}
	assets := os.Getenv("FLEETOPS_PUB02_ASSETS")
	if assets == "" {
		t.Fatal("built frontend required")
	}
	evidence, err := os.MkdirTemp(os.Getenv("FLEETOPS_PUB02_EVIDENCE"), "application-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("preserved application evidence:", evidence)
	server := httptest.NewUnstartedServer(nil)
	for key, value := range map[string]string{"FLEETOPS_DATABASE_URL": databaseURL, "FLEETOPS_MODE": "local", "FLEETOPS_PUBLIC_ORIGIN": "http://" + server.Listener.Addr().String(), "FLEETOPS_ASSETS_DIR": assets, "FLEETOPS_EVIDENCE_DIR": evidence, "FLEETOPS_TRUSTED_PROXIES": "", "FLEETOPS_AUTH_TOKENS": "", "FLEETOPS_AUTO_TICK": ""} {
		t.Setenv(key, value)
	}
	handler, cleanup, err := application(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = handler
	server.Start()
	t.Cleanup(func() {
		server.Close()
		if err := cleanup(); err != nil {
			t.Error(err)
		}
	})
	var generations []string
	for range 2 {
		jar, _ := cookiejar.New(nil)
		client := &http.Client{Jar: jar}
		response, err := client.Get(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 || !strings.Contains(string(raw), `name="fleetops-local-demo"`) || len(response.Cookies()) != 1 {
			t.Fatal("normal application did not bootstrap isolated frontend")
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
		var status struct {
			Generation string
			Evidence   struct{ ProposalSource string }
		}
		if response.StatusCode != 200 || json.Unmarshal(raw, &status) != nil || status.Generation == "" || status.Evidence.ProposalSource != "go-scripted" {
			t.Fatal("production visitor runtime or provenance missing", string(raw))
		}
		generations = append(generations, status.Generation)
	}
	if generations[0] == generations[1] {
		t.Fatal("normal server shared fleet runtime")
	}
	server.Close()
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(evidence, "fleetops_visitor_*", "database-final-*.json"))
	if err != nil || len(files) != 2 {
		t.Fatal("normal server shutdown did not archive visitors", len(files), err)
	}
}
