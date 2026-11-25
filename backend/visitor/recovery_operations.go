package visitor

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/graphql"
	"github.com/AyushM7/fleetops/backend/operations"
)

type recoveryInvestigator func(context.Context, operations.RecoveryInput) (fleet.AgentCompletion, error)

type recoveryOperations struct {
	scenario    *demoScenario
	investigate recoveryInvestigator
	source      string
	reviewed    map[string]bool
}

func (c *demoController) startRecovery(ctx context.Context, now time.Time, manager *recoveryOperations) {
	if now.Before(c.nextOperations) {
		return
	}
	c.nextOperations = now.Add(time.Second)
	s := manager.scenario
	view, err := (&graphql.Resolver{Service: s.service}).Query().FleetState(ctx)
	if err != nil || view.Poisoned {
		return
	}
	active := make(map[string]bool, len(view.ActiveIncidents))
	faults := s.service.Snapshot().Faults
	var unreviewed bool
	for _, incident := range view.ActiveIncidents {
		key := incident.RobotID + ":" + incident.Type
		for _, fault := range slices.Backward(faults) {
			if len(fault.Robots) == 1 && fault.Robots[0] == incident.RobotID && string(fault.Type) == incident.Type {
				key += ":" + fault.ID
				break
			}
		}
		active[key] = manager.reviewed[key]
		unreviewed = unreviewed || !active[key]
	}
	manager.reviewed = active
	if !unreviewed {
		return
	}
	deadline := time.Now().Add(30 * time.Second)
	if earlier, ok := ctx.Deadline(); ok && earlier.Before(deadline) {
		deadline = earlier
	}
	private, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	scoped, err := s.factory.auth.Authenticate(private, s.factory.tokens[string(auth.Proposer)])
	if err != nil {
		cancel()
		return
	}
	raw, err := json.Marshal(view)
	if err != nil {
		cancel()
		return
	}
	record, err := s.service.StartAgentRun(scoped, view.ObservationID, fleet.AgentMetadata{
		Version: "agent-run-v1", Contract: "recovery-decision-v3", Objective: "compare_tradeoffs",
		Source: manager.source, RequestedModel: "openai/gpt-6-luna",
	})
	if err != nil {
		cancel()
		return
	}
	for key := range active {
		manager.reviewed[key] = true
	}
	run := c.run
	cycle := &operationsCycle{run: run, ctx: scoped, cancel: cancel, done: make(chan struct{})}
	c.operations = cycle
	run.operationsMode = "checking_assistance"
	c.publishWarehouse("state")
	go func() {
		defer close(cycle.done)
		defer cancel()
		completion, err := manager.investigate(scoped, operations.RecoveryInput{
			ID: record.ID, RuntimeID: record.RuntimeID, ObservationID: record.ObservationID, Snapshot: raw,
		})
		c.finishRecovery(cycle, record, completion, err)
	}()
}

func (c *demoController) finishRecovery(cycle *operationsCycle, run *fleet.AgentRun, completion fleet.AgentCompletion, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	service := cycle.run.operations.service
	allowed := cycle.ctx.Err() == nil && c.run == cycle.run && c.ready && c.running && !c.closed
	failure := "investigation_failed"
	if errors.Is(err, operations.ErrUsage) {
		failure = "invalid_usage"
	}
	if errors.Is(cycle.ctx.Err(), context.DeadlineExceeded) {
		failure = "timeout"
	}
	if !allowed && failure != "timeout" {
		failure = "cancelled"
	}
	if err != nil || !allowed {
		completion = fleet.AgentCompletion{Status: "failed", Failure: failure, SubmissionCheck: "not_requested", Usage: fleet.AgentUsage{Status: "unavailable"}}
	}
	input := fleet.AgentFinish{RuntimeID: run.RuntimeID, ObservationID: run.ObservationID, Completion: completion}
	if allowed && err == nil && completion.Status == "accepted" && completion.Decision != nil && completion.Decision.Kind == "select" {
		input.Completion.SubmissionCheck = "current"
		input.Submit = &fleet.AgentSubmission{ExpiresAt: time.Now().UTC().Add(time.Minute)}
	}
	audit, cancel := context.WithTimeout(context.WithoutCancel(cycle.ctx), 2*time.Second)
	defer cancel()
	_, finishErr := service.FinishAgentRun(audit, run.ID, input)
	if errors.Is(finishErr, fleet.ErrAgentRecord) {
		input.Submit = nil
		input.Completion = fleet.AgentCompletion{Status: "rejected", Failure: "grounding_rejected", SubmissionCheck: "not_requested", Usage: fleet.AgentUsage{Status: "unavailable"}}
		_, finishErr = service.FinishAgentRun(audit, run.ID, input)
	}
	if finishErr != nil {
		slog.Warn("operations_v3_receipt_unavailable")
	}
	if c.operations == cycle {
		c.operations = nil
		if c.run == cycle.run && c.ready && !c.closed {
			c.run.operationsMode = "deterministic"
			c.publishWarehouse("state")
		}
	}
}
