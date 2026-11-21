package visitor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/operations"
	"github.com/AyushM7/fleetops/backend/proposal"
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
	return operationsTestRun(t, service, decide)
}

func operationsTestRun(t *testing.T, service *fleet.Service, decide operationsDecider) *demoController {
	t.Helper()
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

func operationsMaintenanceTestRun(t *testing.T, pool *db.Pool, decide operationsDecider) (*demoController, *fleet.Service) {
	t.Helper()
	cfg := sim.WarehouseV2Config()
	cfg.ScheduledBreakdowns = []sim.ScheduledBreakdown{{Tick: 48, RobotID: "R4", Type: sim.FaultSensorFailure}}
	service, err := fleet.New(cfg, 42, pool, time.Now, func(context.Context, proposal.OperatorIdentity) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	for range 48 {
		if err := service.Tick(); err != nil {
			t.Fatal(err)
		}
	}
	c := operationsTestRun(t, service, decide)
	t.Cleanup(func() { _ = c.close() })
	return c, service
}

func TestOperationsMaintenanceMockDispatchHasSeparatePhysicalOutcomeIntegration(t *testing.T) {
	pool := operationsTestPool(t)
	offered := make(chan sim.MaintenanceOption, 1)
	proceed := make(chan struct{})
	c, service := operationsMaintenanceTestRun(t, pool, func(ctx context.Context, _ string, o fleet.OperationsObservation) operations.Outcome {
		if len(o.MaintenanceOptions) != 1 || len(o.Assignments)+len(o.Reassignments) != 0 {
			t.Errorf("wrong maintenance offers: %+v", o.MaintenanceOptions)
			return operations.Outcome{Status: "invalid_output"}
		}
		option := o.MaintenanceOptions[0]
		offered <- option
		select {
		case <-ctx.Done():
			return operations.Outcome{Status: "model_unavailable"}
		case <-proceed:
			return operations.Outcome{Status: "decided", Decision: &fleet.OperationsDecision{Kind: "maintenance", Maintenance: &option.MaintenanceRequest}}
		}
	})
	now := time.Now()
	c.nextOperations = now.Add(operationsCooldown)
	c.mu.Lock()
	err := c.advanceInteractive(context.Background(), now)
	cycle := c.operations
	c.mu.Unlock()
	if err != nil || cycle == nil {
		t.Fatalf("new repair offer did not start a cycle during transport cooldown: %v", err)
	}
	select {
	case option := <-offered:
		if option.RobotID != "R4" || option.FaultID != "F1" || option.JobID != "J4" || option.RepairTicks != 3 {
			t.Fatalf("mock did not choose the live R4/F1 offer: %+v", option)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("mock did not receive a maintenance offer")
	}
	if state := service.Snapshot(); state.Tick != 49 || state.Maintenance != nil && state.Maintenance.Active != nil {
		t.Fatalf("deterministic dispatch raced or ticks froze: tick=%d maintenance=%+v", state.Tick, state.Maintenance)
	}
	close(proceed)
	waitOperations(t, cycle.done)
	finished := c.run.operationsActivity[0]
	if finished.Status != "applied" || finished.Go == nil || !finished.Go.AuditCommitted ||
		finished.Go.Execution != fleet.Succeeded || c.run.warehouseOperationsMode() != "ai_assisted" {
		var reason string
		if finished.Go != nil {
			_ = pool.QueryRow(context.Background(), "SELECT reason FROM fleet_automatic_audit WHERE id=$1", finished.Go.AuditID).Scan(&reason)
		}
		t.Fatalf("missing confirmed dispatch activity: %+v, Go audit reason %q", finished, reason)
	}
	state := service.Snapshot()
	work := state.Maintenance
	if work == nil || work.Active == nil || work.Active.StartedTick != 49 || work.Active.RepairTicks != 0 || !work.Active.AIDirected ||
		len(work.History) != 0 || state.Robots[10].Y != 0 || state.Robots[3].ActiveFault != sim.FaultSensorFailure {
		t.Fatalf("dispatch receipt implied a physical result: %+v", work)
	}
	var status string
	var payload []byte
	if err := pool.QueryRow(context.Background(), "SELECT status, command FROM fleet_automatic_audit WHERE id=$1", finished.Go.AuditID).
		Scan(&status, &payload); err != nil || status != "applied" {
		t.Fatalf("missing durable command receipt: %q %v", status, err)
	}
	var command fleet.AutomaticCommand
	if err := json.Unmarshal(payload, &command); err != nil || command.DispatchMaintenance == nil ||
		*command.DispatchMaintenance != work.Active.MaintenanceRequest {
		t.Fatalf("receipt does not bind the dispatched fault: %+v %v", command.DispatchMaintenance, err)
	}
	for state.Tick < 150 {
		if err := service.Tick(); err != nil {
			t.Fatal(err)
		}
		state = service.Snapshot()
	}
	if len(state.Maintenance.History) != 1 || state.Maintenance.History[0].Outcome != sim.MaintenanceCompleted ||
		state.Maintenance.History[0].FinishedTick <= work.Active.StartedTick ||
		state.Robots[3].ActiveFault != "" {
		t.Fatalf("the dispatched physical attempt did not actually repair R4: %+v", state.Maintenance)
	}
	for _, job := range state.Jobs {
		if job.ID == "J4" && job.AssignedRobot == "R4" && job.Status == sim.JobDone &&
			job.CompletedTick > state.Maintenance.History[0].FinishedTick {
			t.Logf("Mock selected R4/F1; Go committed dispatch receipt %d at tick 49, simulator verified repair at %d, R4 delivered J4 at %d",
				finished.Go.AuditID, state.Maintenance.History[0].FinishedTick, job.CompletedTick)
			return
		}
	}
	t.Fatal("R4 did not complete its own delivery after verification")
}

func TestOperationsMaintenanceFallbackAfterNoUsableDecisionIntegration(t *testing.T) {
	pool := operationsTestPool(t)
	for _, tc := range []struct {
		name, want string
		decide     operationsDecider
	}{
		{"abstain", "abstained", func(context.Context, string, fleet.OperationsObservation) operations.Outcome {
			return operations.Outcome{Status: "decided", Decision: &fleet.OperationsDecision{Kind: "abstain", Reason: "no_safe_option"}}
		}},
		{"invalid", "invalid_output", func(context.Context, string, fleet.OperationsObservation) operations.Outcome {
			return operations.Outcome{Status: "decided", Decision: &fleet.OperationsDecision{Kind: "maintenance",
				Maintenance: &sim.MaintenanceRequest{RobotID: "R4", FaultID: "F99"}}}
		}},
		{"budget", "budget_denied", func(context.Context, string, fleet.OperationsObservation) operations.Outcome {
			return operations.Outcome{Status: "budget_denied"}
		}},
		{"unavailable", "model_unavailable", func(context.Context, string, fleet.OperationsObservation) operations.Outcome {
			return operations.Outcome{Status: "model_unavailable"}
		}},
		{"timeout", "cancelled", func(ctx context.Context, _ string, _ fleet.OperationsObservation) operations.Outcome {
			<-ctx.Done()
			return operations.Outcome{Status: "model_unavailable"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, service := operationsMaintenanceTestRun(t, pool, tc.decide)
			ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
			defer cancel()
			now := time.Now()
			c.mu.Lock()
			err := c.advanceInteractive(ctx, now)
			cycle := c.operations
			c.mu.Unlock()
			if err != nil || cycle == nil {
				t.Fatalf("failed to start review: %v", err)
			}
			waitOperations(t, cycle.done)
			if activity := c.run.operationsActivity[0]; activity.Status != tc.want || activity.Go != nil {
				t.Fatalf("unusable model response issued a command: %+v", activity)
			}
			if err := c.advanceInteractive(context.Background(), now.Add(warehouseTickInterval)); err != nil {
				t.Fatal(err)
			}
			if work := service.Snapshot().Maintenance; c.operations != nil || work == nil || work.Active == nil || work.Active.AIDirected {
				t.Fatalf("fallback failed or the same review opened twice: %+v", work)
			}
		})
	}
	var receipts int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM fleet_automatic_audit").Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("fallback incorrectly claimed AI execution: %d %v", receipts, err)
	}
}

func TestPausedMaintenanceReviewCannotDispatchAfterResumeIntegration(t *testing.T) {
	pool := operationsTestPool(t)
	started, cancelled, respond := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-respond:
		default:
			close(respond)
		}
	}()
	c, service := operationsMaintenanceTestRun(t, pool, func(ctx context.Context, _ string, o fleet.OperationsObservation) operations.Outcome {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-respond
		return operations.Outcome{Status: "decided", Decision: &fleet.OperationsDecision{
			Kind: "maintenance", Maintenance: &o.MaintenanceOptions[0].MaintenanceRequest,
		}}
	})
	c.mu.Lock()
	err := c.advanceInteractive(context.Background(), time.Now())
	cycle := c.operations
	c.mu.Unlock()
	if err != nil || cycle == nil {
		t.Fatalf("could not start maintenance review: %v", err)
	}
	waitOperations(t, started)
	if w := controllerRequest(c, "/demo/pause", c.run.generation); w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	waitOperations(t, cancelled)
	paused := service.Snapshot()
	if paused.Tick != 49 || (paused.Maintenance != nil && paused.Maintenance.Active != nil) {
		t.Fatalf("pause consumed a maintenance attempt: %+v", paused.Maintenance)
	}
	if w := controllerRequest(c, "/demo/play", c.run.generation); w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	c.mu.Lock()
	err = c.advanceInteractive(context.Background(), c.nextTick)
	c.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	resumed := service.Snapshot()
	if resumed.Tick != 50 || resumed.Maintenance == nil || resumed.Maintenance.Active == nil ||
		resumed.Maintenance.Active.AIDirected {
		t.Fatalf("resume failed to restore deterministic maintenance: %+v", resumed.Maintenance)
	}
	close(respond)
	waitOperations(t, cycle.done)
	if work := service.Snapshot().Maintenance; work == nil || work.Active == nil || *work.Active != *resumed.Maintenance.Active ||
		len(work.History) != 0 || c.run.warehouseOperationsMode() != "deterministic" {
		t.Fatalf("cancelled AI changed the resumed attempt: %+v", work)
	}
	var receipts int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM fleet_automatic_audit").Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("cancelled review reached automatic execution: %d %v", receipts, err)
	}
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
	var journal []operations.Activity
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
	c.run.operations.record = func(_ context.Context, runtime string, activity operations.Activity) error {
		if runtime != c.run.runtimeID {
			t.Errorf("audit used the wrong runtime")
		}
		journal = append(journal, activity)
		return nil
	}
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
	if len(journal) != 3 || journal[0].Stage != "observed" || journal[1].Stage != "proposed" ||
		journal[2].Status != "applied" || journal[2].Go == nil || !journal[2].Go.AuditCommitted {
		t.Fatalf("actual workflow was not audited: %+v", journal)
	}
	batch, _ := c.warehouseEventBatch(c.run, 0)
	var replay warehouseFrame
	if len(batch) != 1 || batch[0].kind != "snapshot" || json.Unmarshal(batch[0].data, &replay) != nil ||
		replay.RuntimeID != c.run.runtimeID || replay.OperationsMode != "ai_assisted" ||
		len(replay.OperationsActivity) != 1 || replay.OperationsActivity[0].Status != "applied" ||
		replay.OperationsActivity[0].ObservationID != journal[0].ObservationID ||
		replay.OperationsActivity[0].Go.AuditID != journal[2].Go.AuditID || !replay.OperationsActivity[0].Recorded {
		t.Fatalf("SSE reconnect did not preserve a confirmed scoped decision: %+v", replay.OperationsActivity)
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

func TestUnavailableAuditPreventsOperationsCallIntegration(t *testing.T) {
	pool := operationsTestPool(t)
	var calls atomic.Int32
	c := operationsTestController(t, pool, func(context.Context, string, fleet.OperationsObservation) operations.Outcome {
		calls.Add(1)
		return operations.Outcome{}
	})
	c.run.operations.record = func(context.Context, string, operations.Activity) error {
		return errors.New("offline audit fixture")
	}
	t.Cleanup(func() { _ = c.close() })
	c.mu.Lock()
	err := c.advanceInteractive(context.Background(), time.Now())
	cycle := c.operations
	c.mu.Unlock()
	if err != nil || cycle == nil {
		t.Fatalf("expected a cycle to fail closed: %v", err)
	}
	waitOperations(t, cycle.done)
	history := c.run.operationsActivity
	if calls.Load() != 0 || len(history) != 1 || history[0].Status != "audit_unavailable" ||
		history[0].Recorded || c.run.warehouseOperationsMode() != "deterministic" {
		t.Fatalf("audit failure was not reported safely: calls=%d events=%+v", calls.Load(), history)
	}
}
