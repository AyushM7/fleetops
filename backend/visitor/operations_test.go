package visitor

import (
	"context"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/operations"
	"github.com/AyushM7/fleetops/backend/sim"
)

func operationsTestPool(t *testing.T) *db.Pool {
	t.Helper()
	url := os.Getenv("FLEETOPS_TEST_DATABASE_URL")
	if url == "" && os.Getenv("FLEETOPS_REQUIRE_DB") != "1" {
		t.Skip("FLEETOPS_TEST_DATABASE_URL not configured")
	}
	ctx := context.Background()
	cfg := db.DefaultConfig(url)
	admin, err := db.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	cfg.Schema = "fleetops_cycle_test_" + demoRandom()[:20]
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+cfg.Schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+cfg.Schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	pool, err := db.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return pool
}

func operationsTestController(t *testing.T, pool *db.Pool, decide operationsDecider) *demoController {
	t.Helper()
	service, err := fleet.NewVisitorWarehouse(42, pool)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := newRuntimeOperations(service, decide)
	if err != nil {
		t.Fatal(err)
	}
	run := &demoRun{
		scenarioID: warehouseScenario, generation: demoRandom(), runtimeID: service.RuntimeID(),
		operations: manager, warehouse: service.Snapshot,
		step: func(context.Context) error { return service.Tick() }, capture: func(context.Context) error { return nil },
	}
	return &demoController{run: run, ready: true, interactive: true, running: true}
}

func waitOperations(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("operations worker did not finish")
	}
}

func TestOperationsCycleKeepsTicksMovingAndAppliesOnlyToItsRuntimeIntegration(t *testing.T) {
	pool := operationsTestPool(t)
	offered := make(chan sim.JobAssignment, 1)
	proceed := make(chan struct{})
	c := operationsTestController(t, pool, func(ctx context.Context, runtime string, observed fleet.OperationsObservation) operations.Outcome {
		pair := observed.Assignments[0].JobAssignment
		offered <- pair
		select {
		case <-ctx.Done():
			return operations.Outcome{}
		case <-proceed:
			return operations.Outcome{Status: "decided", Decision: &fleet.OperationsDecision{Kind: "assign", Assignments: []sim.JobAssignment{pair}}}
		}
	})
	t.Cleanup(func() { _ = c.close() })
	other, err := fleet.NewVisitorWarehouse(42, pool)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := c.advanceInteractive(context.Background(), now); err != nil || c.operations == nil {
		t.Fatalf("first pulse did not start an autonomous decision: %v", err)
	}
	cycle := c.operations
	var pair sim.JobAssignment
	select {
	case pair = <-offered:
	case <-time.After(2 * time.Second):
		t.Fatal("model fixture did not receive a grounded option")
	}
	for i := 1; i <= 3; i++ {
		c.mu.Lock()
		err := c.advanceInteractive(context.Background(), now.Add(time.Duration(i)*warehouseTickInterval))
		c.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := c.run.warehouse(); got.Tick != 4 {
		t.Fatalf("model latency blocked simulator progress: tick=%d", got.Tick)
	}
	close(proceed)
	waitOperations(t, cycle.done)
	var owner string
	for _, job := range c.run.warehouse().Jobs {
		if job.ID == pair.JobID {
			owner = job.AssignedRobot
		}
	}
	if owner != pair.RobotID || c.run.operationsMode != "ai_assisted" {
		t.Fatalf("Go did not apply the observed assignment: %s, %s", owner, c.run.operationsMode)
	}
	for _, job := range other.Snapshot().Jobs {
		if job.AssignedRobot != "" {
			t.Fatal("operations command crossed runtime boundaries")
		}
	}
}

func TestPauseCancelsOperationsAndRestoresDeterministicDispatchIntegration(t *testing.T) {
	pool := operationsTestPool(t)
	var calls atomic.Int32
	c := operationsTestController(t, pool, func(ctx context.Context, _ string, observed fleet.OperationsObservation) operations.Outcome {
		calls.Add(1)
		<-ctx.Done()
		return operations.Outcome{Status: "decided", Decision: &fleet.OperationsDecision{
			Kind: "assign", Assignments: []sim.JobAssignment{observed.Assignments[0].JobAssignment},
		}}
	})
	t.Cleanup(func() { _ = c.close() })
	now := time.Now()
	if err := c.advanceInteractive(context.Background(), now); err != nil || c.operations == nil {
		t.Fatalf("could not start operations: %v", err)
	}
	cycle := c.operations
	if w := controllerRequest(c, "/demo/pause", c.run.generation); w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	waitOperations(t, cycle.done)
	var applied int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM fleet_automatic_audit").Scan(&applied); err != nil || applied != 0 {
		t.Fatalf("cancelled decision reached the command boundary: %d %v", applied, err)
	}
	if w := controllerRequest(c, "/demo/play", c.run.generation); w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := c.advanceInteractive(context.Background(), c.nextTick); err != nil {
		t.Fatal(err)
	}
	var active int
	for _, job := range c.run.warehouse().Jobs {
		if job.AssignedRobot != "" {
			active++
		}
	}
	if calls.Load() != 1 || active == 0 || c.run.operationsMode != "deterministic" {
		t.Fatalf("deterministic fallback did not resume, active=%d mode=%s", active, c.run.operationsMode)
	}
}

func TestExpiredVisitorCancelsOperationsAndDrainsWorkerIntegration(t *testing.T) {
	pool := operationsTestPool(t)
	started, cancelled, released := make(chan struct{}), make(chan struct{}), make(chan struct{})
	c := operationsTestController(t, pool, func(ctx context.Context, _ string, observed fleet.OperationsObservation) operations.Outcome {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return operations.Outcome{Status: "decided", Decision: &fleet.OperationsDecision{
			Kind: "assign", Assignments: []sim.JobAssignment{observed.Assignments[0].JobAssignment},
		}}
	})
	m := newVisitorManager(func(context.Context) (*visitorSession, error) {
		return &visitorSession{controller: c, cleanup: func() error { close(released); return nil }}, nil
	})
	m.ttl = time.Second
	t.Cleanup(func() { _ = m.close() })
	id, _, err := m.create()
	if err != nil {
		t.Fatal(err)
	}
	waitOperations(t, started)
	waitOperations(t, cancelled)
	waitOperations(t, released)
	if m.lookup(id) != nil {
		t.Fatal("expired runtime remained reachable")
	}
	var attempts int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM fleet_automatic_audit").Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("expired decision reached automatic execution: %d %v", attempts, err)
	}
}
