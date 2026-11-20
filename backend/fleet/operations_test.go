package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/AyushM7/fleetops/backend/safety"
	"github.com/AyushM7/fleetops/backend/sim"
)

func TestOperationsDecisionRequiresGroundedPairs(t *testing.T) {
	pair := sim.JobAssignment{JobID: "J1", RobotID: "R1"}
	observation := OperationsObservation{Assignments: []sim.AssignmentOption{{JobAssignment: pair}}}
	for _, decision := range []OperationsDecision{
		{Kind: "assign", Assignments: []sim.JobAssignment{pair, pair}},
		{Kind: "assign", Assignments: []sim.JobAssignment{{JobID: "J2", RobotID: "R1"}}},
		{Kind: "reassign", Reassignment: &safety.ReassignUnstartedJobRequest{JobID: "J1", FromRobotID: "R11", ToRobotID: "R1"}},
		{Kind: "abstain", Reason: "arbitrary_reason"},
		{Kind: "inject_fault"},
	} {
		if command, err := observation.Prepare(decision); command != nil || !errors.Is(err, ErrOperationsDecision) {
			t.Fatalf("ungrounded decision accepted: %+v %v", command, err)
		}
	}
	if command, err := observation.Prepare(OperationsDecision{Kind: "abstain", Reason: "no_safe_option"}); command != nil || err != nil {
		t.Fatalf("abstention should do nothing: %+v %v", command, err)
	}
}

func TestOperationsAssignmentReachesAutomaticBoundaryIntegration(t *testing.T) {
	s, pool, _, ctx := automaticFixture(t)
	if _, err := s.ObserveOperations(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unscoped operations read: %v", err)
	}
	observed, err := s.ObserveOperations(ctx)
	if err != nil || len(observed.Assignments) == 0 || len(observed.Robots) != 11 || len(observed.Stations) != 4 {
		t.Fatalf("operations observation missing warehouse evidence: %v", err)
	}
	raw, err := json.Marshal(observed)
	if err != nil || strings.Contains(string(raw), s.RuntimeID()) || strings.Contains(string(raw), "observationId") {
		t.Fatalf("private metadata leaked: %v", err)
	}
	option := observed.Assignments[0]
	if option.PickupSteps+option.DeliverySteps+option.ChargingExitSteps == 0 || option.RequiredBattery == 0 {
		t.Fatalf("missing route evidence: %+v", option)
	}
	command, err := observed.Prepare(OperationsDecision{Kind: "assign", Assignments: []sim.JobAssignment{option.JobAssignment}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.ExecuteAutomatic(ctx, *command)
	if err != nil || result.Execution != Succeeded || !result.AuditCommitted {
		t.Fatalf("Go did not confirm assignment: %+v %v", result, err)
	}
	automaticAuditStatus(t, pool, result.AuditID, "applied", "test-host")
	stale, err := observed.Prepare(OperationsDecision{Kind: "assign", Assignments: []sim.JobAssignment{option.JobAssignment}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExecuteAutomatic(ctx, *stale); !errors.Is(err, ErrObservation) {
		t.Fatalf("Go should reject a stale model decision: %v", err)
	}
}
