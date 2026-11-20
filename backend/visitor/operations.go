package visitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/operations"
)

type operationsDecider func(context.Context, string, fleet.OperationsObservation) operations.Outcome

type runtimeOperations struct {
	service   *fleet.Service
	authorize func(context.Context) (context.Context, error)
	decide    operationsDecider
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
	go func() {
		defer close(cycle.done)
		defer cancel()
		outcome := manager.decide(private, run.runtimeID, observation)
		c.finishOperations(cycle, observation, outcome)
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

func (c *demoController) finishOperations(cycle *operationsCycle, observed fleet.OperationsObservation, outcome operations.Outcome) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.operations != cycle {
		return
	}
	c.operations = nil
	run := cycle.run
	manager := run.operations
	_ = manager.service.SetOperationsWindow(cycle.ctx, nil)
	if c.run != run {
		return
	}
	run.operationsMode = "deterministic"
	if !c.ready || !c.running || c.closed || cycle.ctx.Err() != nil || outcome.Status != "decided" || outcome.Decision == nil {
		return
	}
	command, err := observed.Prepare(*outcome.Decision)
	if err != nil || command == nil {
		return
	}
	receipt, err := manager.service.ExecuteAutomatic(cycle.ctx, *command)
	if err == nil && receipt.Execution == fleet.Succeeded && receipt.AuditCommitted {
		run.operationsMode = "ai_assisted"
	}
}
