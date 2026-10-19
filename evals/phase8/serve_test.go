//go:build fleetops_test

package fleet_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/db"
)

func TestPUB01Serve(t *testing.T) {
	if os.Getenv("FLEETOPS_PUB01_SERVE") != "1" {
		t.Skip("explicit PUB-01 loopback launcher required")
	}
	root, dir := os.Getenv("FLEETOPS_P405_ROOT"), os.Getenv("FLEETOPS_PUB01_RUN_DIR")
	cfg := db.DefaultConfig(os.Getenv("FLEETOPS_TEST_DATABASE_URL"))
	admin, err := db.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	factory := visitorFactory{admin: admin, config: cfg, evidenceDir: dir}
	m := newVisitorManager(factory.create)
	defer func() {
		if err := m.close(); err != nil {
			t.Error("visitor cleanup:", err)
		}
	}()
	g, err := newVisitorGateway(m, filepath.Join(root, "web", "dist"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(g)
	g.origin = "http://" + server.Listener.Addr().String()
	server.Config.ReadHeaderTimeout, server.Config.IdleTimeout = 5*time.Second, 30*time.Second
	server.Config.MaxHeaderBytes = 16384
	server.Start()
	defer server.Close()
	if err := os.WriteFile(filepath.Join(dir, "ready.json"), []byte(fmt.Sprintf(`{"url":%q}`, server.URL)), 0600); err != nil {
		t.Fatal(err)
	}
	fmt.Printf("READY: %s | isolated visitor warehouses | scripted fixed presets; no model inference\n", server.URL)
	deadline := time.NewTimer(20 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			return
		case <-ticker.C:
			if _, err := os.Stat(filepath.Join(dir, "stop")); err == nil {
				return
			}
		}
	}
}
