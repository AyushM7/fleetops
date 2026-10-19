package visitor

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/db"
)

// The test owns only this bounded TCP tunnel. Dropping its connections exercises
// actual pgx disconnect/reconnect behavior without stopping shared PostgreSQL.
type databaseTunnel struct {
	listener    net.Listener
	mu          sync.Mutex
	paused      bool
	connections map[net.Conn]bool
	workers     sync.WaitGroup
	acceptDone  chan struct{}
}

func tunnelDatabase(t *testing.T, target string) (*databaseTunnel, string) {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	upstream := u.Host
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tunnel := &databaseTunnel{listener: listener, connections: map[net.Conn]bool{}, acceptDone: make(chan struct{})}
	go func() {
		defer close(tunnel.acceptDone)
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			tunnel.mu.Lock()
			if tunnel.paused || len(tunnel.connections) >= 36 {
				tunnel.mu.Unlock()
				client.Close()
				continue
			}
			tunnel.connections[client] = true
			tunnel.workers.Go(func() {
				defer client.Close()
				server, err := net.DialTimeout("tcp", upstream, 2*time.Second)
				if err != nil {
					tunnel.mu.Lock()
					delete(tunnel.connections, client)
					tunnel.mu.Unlock()
					return
				}
				defer server.Close()
				tunnel.mu.Lock()
				if tunnel.paused {
					tunnel.mu.Unlock()
					return
				}
				tunnel.connections[server] = true
				tunnel.mu.Unlock()
				done := make(chan struct{})
				go func() { io.Copy(client, server); client.Close(); close(done) }()
				io.Copy(server, client)
				server.Close()
				<-done
				tunnel.mu.Lock()
				delete(tunnel.connections, client)
				delete(tunnel.connections, server)
				tunnel.mu.Unlock()
			})
			tunnel.mu.Unlock()
		}
	}()
	t.Cleanup(func() { listener.Close(); <-tunnel.acceptDone; tunnel.pause(true); tunnel.workers.Wait() })
	u.Host = listener.Addr().String()
	return tunnel, u.String()
}

func (p *databaseTunnel) pause(value bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paused = value
	if value {
		for connection := range p.connections {
			connection.Close()
			delete(p.connections, connection)
		}
	}
}

func TestPUB02PostgreSQLOutageAndRecoveryIntegration(t *testing.T) {
	target := os.Getenv("FLEETOPS_TEST_DATABASE_URL")
	if target == "" {
		if os.Getenv("FLEETOPS_REQUIRE_DB") == "1" {
			t.Fatal("required PostgreSQL unavailable")
		}
		t.Skip("PostgreSQL not configured")
	}
	tunnel, databaseURL := tunnelDatabase(t, target)
	server := httptest.NewUnstartedServer(nil)
	app, err := New(context.Background(), Config{Mode: "local", Origin: "http://" + server.Listener.Addr().String(), AssetsDir: integrationAssets(t), EvidenceDir: visitorTestEvidenceDir(t), Database: db.DefaultConfig(databaseURL)})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = app
	server.Start()
	t.Cleanup(func() {
		tunnel.pause(false)
		server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := app.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	check := func(path string, want int) {
		t.Helper()
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		if response.StatusCode != want {
			t.Fatal(path, response.StatusCode, string(raw))
		}
	}
	check("/readyz", 200)
	check("/", 200)
	tunnel.pause(true)
	check("/healthz", 200)
	check("/readyz", 503)
	check("/", 503)
	tunnel.pause(false)
	deadline := time.Now().Add(8 * time.Second)
	for {
		response, err := http.Get(server.URL + "/readyz")
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("readiness did not recover after real connection loss")
		}
		time.Sleep(100 * time.Millisecond)
	}
	check("/", 200)
}
