package visitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/operations"
)

type operationsDecider func(context.Context, string, fleet.OperationsObservation) operations.Outcome

type operationsRecorder func(context.Context, string, operations.Activity) error

type runtimeOperations struct {
	service   *fleet.Service
	authorize func(context.Context) (context.Context, error)
	decide    operationsDecider
	record    operationsRecorder
}

func newRuntimeOperations(service *fleet.Service, decide operationsDecider) (*runtimeOperations, error) {
	runtime := service.RuntimeID()
	token, err := visitorRandom()
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(token))
	raw, err := json.Marshal([]auth.Credential{{
		Subject: "operations-manager", Role: auth.Automatic, RuntimeID: runtime, SHA256: hex.EncodeToString(digest[:]),
	}})
	if err != nil {
		return nil, err
	}
	authenticator, err := auth.New(string(raw))
	if err != nil {
		return nil, err
	}
	if err := service.SetAutomaticCommandsEnabled(true); err != nil {
		return nil, err
	}
	return &runtimeOperations{service: service, authorize: func(ctx context.Context) (context.Context, error) {
		return authenticator.Authenticate(ctx, token)
	}, decide: decide}, nil
}

type operationsCycle struct {
	run    *demoRun
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

const operationsCooldown = 30 * time.Second

// Called under the controller gate just before a due tick. The model runs
// outside the gate and receives a detached observation without credentials.
func (c *demoController) startOperations(ctx context.Context, now time.Time) {
	run := c.run
	manager := run.operations
	if manager == nil || c.operations != nil || now.Before(c.nextOperations) {
		return
	}
	c.nextOperations = now.Add(3 * time.Second)
	deadline := time.Now().Add(10 * time.Second)
	if earlier, ok := ctx.Deadline(); ok && earlier.Before(deadline) {
		deadline = earlier
	}
	private, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	scoped, err := manager.authorize(private)
	if err != nil {
		cancel()
		return
	}
	observation, err := manager.service.ObserveOperations(scoped)
	if err != nil || len(observation.Assignments)+len(observation.Reassignments) == 0 {
		cancel()
		return
	}
	if err := manager.service.SetOperationsWindow(scoped, &observation); err != nil {
		cancel()
		return
	}
	cycle := &operationsCycle{run: run, ctx: scoped, cancel: cancel, done: make(chan struct{})}
	c.operations, c.nextOperations = cycle, now.Add(operationsCooldown)
	run.operationsMode = "checking_assistance"
	activity := operations.Activity{ObservationID: observation.ID(), Tick: observation.Tick,
		ObservedAt: time.Now().UTC(), Stage: "observed", Status: "observed"}
	c.publishOperations(run, activity)
	go func() {
		defer close(cycle.done)
		defer cancel()
		c.runOperations(cycle, observation, activity)
	}()
}
func (c *demoController) cancelOperations() {
	if cycle := c.operations; cycle != nil {
		cycle.cancel()
		_ = cycle.run.operations.service.SetOperationsWindow(cycle.ctx, nil)
		cycle.run.operationsMode = "deterministic"
	}
	if c.run.warehouse != nil {
		c.run.operationsMode = "deterministic"
	}
}

func (c *demoController) runOperations(cycle *operationsCycle, observed fleet.OperationsObservation, activity operations.Activity) {
	run := cycle.run
	manager := run.operations
	persist := func() bool {
		activity.Recorded = manager.record != nil
		if manager.record != nil && manager.record(context.WithoutCancel(cycle.ctx), run.runtimeID, activity) != nil {
			activity.Recorded = false
			return false
		}
		return true
	}
	var command *fleet.AutomaticCommand
	if persist() {
		result := manager.decide(cycle.ctx, run.runtimeID, observed)
		activity.SetModel(result)
		activity.Status = result.Status
		if result.Status == "decided" && result.Decision != nil {
			var err error
			command, err = observed.Prepare(*result.Decision)
			switch {
			case err != nil:
				activity.Status = "invalid_output"
			case command == nil:
				activity.Status, activity.Decision = "abstained", result.Decision
			default:
				at := time.Now().UTC()
				activity.Stage, activity.Status, activity.ProposedAt, activity.Decision = "proposed", "proposed", &at, result.Decision
				if !persist() {
					command, activity.Status = nil, "audit_unavailable"
				}
			}
		}
	} else {
		activity.Status = "audit_unavailable"
	}
	c.applyOperations(cycle, &activity, command)
	at := time.Now().UTC()
	activity.Stage, activity.FinishedAt = "finished", &at
	persist()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.operations == cycle {
		c.operations = nil
		if c.run == run && c.ready && !c.closed {
			c.publishOperations(run, activity)
		}
	}
}

func (c *demoController) applyOperations(cycle *operationsCycle, activity *operations.Activity, command *fleet.AutomaticCommand) {
	c.mu.Lock()
	defer c.mu.Unlock()
	run := cycle.run
	manager := run.operations
	_ = manager.service.SetOperationsWindow(cycle.ctx, nil)
	if c.run != run {
		activity.Status = "cancelled"
		return
	}
	run.operationsMode = "deterministic"
	if !c.ready || !c.running || c.closed || cycle.ctx.Err() != nil {
		activity.Status = "cancelled"
		return
	}
	if command == nil {
		return
	}
	c.publishOperations(run, *activity)
	receipt, err := manager.service.ExecuteAutomatic(cycle.ctx, *command)
	activity.Go = &receipt
	switch {
	case err == nil && receipt.Execution == fleet.Succeeded && receipt.AuditCommitted:
		activity.Status, run.operationsMode = "applied", "ai_assisted"
	case receipt.Execution == fleet.Unknown || !receipt.AuditCommitted:
		activity.Status = "execution_unconfirmed"
	case receipt.Execution == fleet.Failed:
		activity.Status = "execution_failed"
	default:
		activity.Status, activity.GoReason = "go_rejected", "admission_rejected"
		if errors.Is(err, fleet.ErrObservation) || errors.Is(err, fleet.ErrAutomaticExpired) {
			activity.GoReason = "stale_observation"
		}
	}
}

func (c *demoController) publishOperations(run *demoRun, activity operations.Activity) {
	const history = 12
	if n := len(run.operationsActivity); n > 0 && run.operationsActivity[n-1].ObservationID == activity.ObservationID {
		run.operationsActivity[n-1] = activity
	} else {
		if len(run.operationsActivity) == history {
			copy(run.operationsActivity, run.operationsActivity[1:])
			run.operationsActivity = run.operationsActivity[:history-1]
		}
		run.operationsActivity = append(run.operationsActivity, activity)
	}
	c.publishWarehouse("operations")
}

func (r *demoRun) warehouseOperationsMode() string {
	if r.operationsMode == "" {
		return "deterministic"
	}
	return r.operationsMode
}
