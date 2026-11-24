package visitor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/sim"
)

func warehouseTestRun(seed int64) (*demoRun, error) {
	service, err := fleet.NewVisitorWarehouse(seed, &db.Pool{})
	if err != nil {
		return nil, err
	}
	return &demoRun{scenarioID: warehouseScenario, generation: demoRandom(), runtimeID: service.RuntimeID(),
		capture:   func(context.Context) error { return nil },
		step:      func(context.Context) error { return service.Tick() },
		warehouse: service.Snapshot}, nil
}

func TestWarehouseServerControlsAndPacing(t *testing.T) {
	const seed = 42
	c := unitController()
	c.interactive = true
	c.selectFactory = func(_ context.Context, id string) (*demoRun, error) {
		if id != warehouseScenario {
			return nil, errors.New("unexpected test scenario")
		}
		return warehouseTestRun(seed)
	}
	if w := controllerRequest(c, "/demo/scenario/"+warehouseScenario, c.run.generation); w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	generation := c.run.generation
	initial := c.run.warehouse()
	if initial.Seed != seed || len(initial.Robots) != 11 || len(initial.Stations) != 4 {
		t.Fatalf("unexpected initial warehouse: %+v", initial)
	}
	for _, path := range []string{"/demo/step", "/demo/speed/2"} {
		if w := controllerRequest(c, path, generation); w.Code != http.StatusConflict {
			t.Fatalf("browser-controlled tick route %s returned %d", path, w.Code)
		}
	}
	if w := controllerRequest(c, "/demo/play", generation); w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	ctx := context.Background()
	latePulse := c.nextTick.Add(time.Minute)
	for _, now := range []time.Time{c.nextTick.Add(-time.Millisecond), latePulse, latePulse, latePulse.Add(warehouseTickInterval - time.Millisecond)} {
		if err := c.advanceInteractive(ctx, now); err != nil {
			t.Fatal(err)
		}
	}
	if tick := c.run.warehouse().Tick; tick != 1 {
		t.Fatalf("late and repeated clock pulses should cause one tick, got %d", tick)
	}
	if w := controllerRequest(c, "/demo/pause", generation); w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := c.advanceInteractive(ctx, latePulse.Add(time.Hour)); err != nil || c.run.warehouse().Tick != 1 {
		t.Fatalf("pause failed, tick %d, error %v", c.run.warehouse().Tick, err)
	}
	if w := controllerRequest(c, "/demo/play", generation); w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := c.advanceInteractive(ctx, c.nextTick); err != nil || c.run.warehouse().Tick != 2 {
		t.Fatalf("resume failed, tick %d, error %v", c.run.warehouse().Tick, err)
	}
	if w := controllerRequest(c, "/demo/stop", generation); w.Code != http.StatusOK || !c.stopped {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := controllerRequest(c, "/demo/play", generation); w.Code != http.StatusConflict {
		t.Fatal("stopped generation resumed", w.Code)
	}
	if w := controllerRequest(c, "/demo/restart", generation); w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	if c.run.generation == generation || c.running || c.stopped || !reflect.DeepEqual(initial, c.run.warehouse()) {
		t.Fatal("same-seed restart did not return a fresh paused generation with the original world")
	}
}

func TestWarehouseVisitorLifecycleIntegration(t *testing.T) {
	var seed atomic.Int64
	var cleaned atomic.Int64
	m := newVisitorManager(func(context.Context) (*visitorSession, error) {
		run, err := warehouseTestRun(seed.Add(1))
		if err != nil {
			return nil, err
		}
		return &visitorSession{controller: &demoController{run: run, ready: true, interactive: true}, cleanup: func() error {
			cleaned.Add(1)
			return nil
		}}, nil
	})
	t.Cleanup(func() { _ = m.close() })
	server := httptest.NewUnstartedServer(nil)
	defer server.Close()
	origin := "http://" + server.Listener.Addr().String()
	gateway, err := newVisitorGateway(m, Config{Mode: "local", Origin: origin, AssetsDir: testAssets(t)})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = gateway
	server.Start()

	open := func() *http.Cookie {
		t.Helper()
		res, err := server.Client().Get(server.URL + "/")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK || len(res.Cookies()) != 1 {
			t.Fatalf("session bootstrap returned %d", res.StatusCode)
		}
		return res.Cookies()[0]
	}
	request := func(cookie *http.Cookie, path, generation string) (*http.Response, demoStatus) {
		t.Helper()
		req, err := http.NewRequest("POST", server.URL+path, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(cookie)
		req.Header.Set("Origin", server.URL)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-FleetOps-Demo", "1")
		req.Header.Set("X-FleetOps-Generation", generation)
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var status demoStatus
		if res.StatusCode == http.StatusOK {
			if err := json.NewDecoder(res.Body).Decode(&status); err != nil {
				t.Fatal(err)
			}
		}
		return res, status
	}

	a, b := open(), open()
	_, beforeA := request(a, "/demo/status", "")
	_, beforeB := request(b, "/demo/status", "")
	if beforeA.Warehouse == nil || beforeB.Warehouse == nil || beforeA.Warehouse.Seed == beforeB.Warehouse.Seed {
		t.Fatal("visitor snapshots did not identify independent seeded worlds")
	}
	if res, _ := request(b, "/demo/play", beforeA.Generation); res.StatusCode != http.StatusConflict {
		t.Fatalf("foreign generation play returned %d", res.StatusCode)
	}
	if res, _ := request(a, "/demo/play", beforeA.Generation); res.StatusCode != http.StatusOK {
		t.Fatalf("play returned %d", res.StatusCode)
	}
	tickDeadline := time.Now().Add(5 * time.Second)
	for {
		if res, status := request(a, "/demo/status", ""); res.StatusCode == http.StatusOK && status.Warehouse != nil && status.Warehouse.Tick > 0 {
			break
		}
		if time.Now().After(tickDeadline) {
			t.Fatal("warehouse worker did not advance a running visitor")
		}
		time.Sleep(30 * time.Millisecond)
	}
	if _, status := request(b, "/demo/status", ""); !reflect.DeepEqual(status.Warehouse, beforeB.Warehouse) {
		t.Fatal("visitor A's worker advanced visitor B")
	}

	session := m.lookup(a.Value)
	session.mu.Lock()
	session.deadline.Store(new(time.Now().Add(-time.Second)))
	session.mu.Unlock()
	select {
	case <-session.done:
	case <-time.After(5 * time.Second):
		t.Fatal("expired visitor worker was not retired")
	}
	if res, _ := request(a, "/demo/status", ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired visitor returned %d", res.StatusCode)
	}
	if err := m.close(); err != nil || cleaned.Load() != 2 {
		t.Fatalf("visitor cleanup count %d, error %v", cleaned.Load(), err)
	}
}

func TestWarehouseConfiguredChargeTargetIntegration(t *testing.T) {
	f, pool, owner, dir := visitorArchiveFixture(t)
	t.Cleanup(func() { cleanupVisitorArchiveFixture(t, f, owner, dir) })
	ctx := context.Background()
	authenticator, tokens, _, err := visitorCredentials()
	if err != nil {
		t.Fatal(err)
	}
	factory := demoFactory{pool: pool, auth: authenticator, tokens: tokens, evidenceDir: dir, warehouseChargeTarget: 100}
	run, err := factory.createScenario(ctx, warehouseScenario)
	if err != nil {
		t.Fatal(err)
	}
	cfg := sim.WarehouseV2Config()
	cfg.ChargeRelease = 100
	want, err := sim.New(cfg, run.warehouse().Seed)
	if err != nil {
		t.Fatal(err)
	}
	for range 1000 {
		want.Tick()
		if err := run.step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := run.warehouse(); !reflect.DeepEqual(got, want.Snapshot()) {
		t.Fatalf("configured warehouse diverged at tick %d", got.Tick)
	}
}
