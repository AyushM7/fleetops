package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/safety"
	"github.com/AyushM7/fleetops/backend/sim"
)

func TestOperationsDecisionRequiresGroundedPairs(t *testing.T) {
	pair := sim.JobAssignment{JobID: "J1", RobotID: "R1"}
	observation := OperationsObservation{Assignments: []sim.AssignmentOption{{JobAssignment: pair}}}
	for _, decision := range []OperationsDecision{
		{Kind: "assign", Assignments: []sim.JobAssignment{pair, pair}},
		{Kind: "assign", Assignments: []sim.JobAssignment{{JobID: "J2", RobotID: "R1"}}},
		{Kind: "assign", Assignments: []sim.JobAssignment{{JobID: "J1", RobotID: "M1"}}},
		{Kind: "reassign", Reassignment: &safety.ReassignUnstartedJobRequest{JobID: "J1", FromRobotID: "M1", ToRobotID: "R1"}},
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

func operationsMaintenanceFixture(t *testing.T) (*Service, context.Context, OperationsObservation) {
	t.Helper()
	cfg := sim.WarehouseV2Config()
	cfg.ScheduledBreakdowns = []sim.ScheduledBreakdown{{Tick: 48, RobotID: "R4", Type: sim.FaultSensorFailure}}
	s, err := New(cfg, 42, integrationPool(t), func() time.Time { return testTime }, authorized)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetAutomaticCommandsEnabled(true); err != nil {
		t.Fatal(err)
	}
	ctx := automaticContext(t, s.RuntimeID())
	for range 48 {
		if err := s.Tick(); err != nil {
			t.Fatal(err)
		}
	}
	observed, err := s.ObserveOperations(ctx)
	if err != nil || len(observed.MaintenanceOptions) != 1 || len(observed.ActiveBreakdowns) != 1 {
		t.Fatalf("missing Go repair option: %+v %v", observed.MaintenanceOptions, err)
	}
	return s, ctx, observed
}

func maintenanceOperationsCommand(t *testing.T, observed OperationsObservation) AutomaticCommand {
	t.Helper()
	request := observed.MaintenanceOptions[0].MaintenanceRequest
	command, err := observed.Prepare(OperationsDecision{Kind: "maintenance", Maintenance: &request})
	if err != nil || command == nil {
		t.Fatalf("failed to prepare offered maintenance: %+v %v", command, err)
	}
	return *command
}

func TestOperationsMaintenanceRejectsUnofferedAndStaleDecisionsIntegration(t *testing.T) {
	s, ctx, observed := operationsMaintenanceFixture(t)
	if err := s.SetOperationsWindow(ctx, &observed); err != nil {
		t.Fatal(err)
	}
	for _, denied := range []struct {
		ctx     context.Context
		expires time.Time
		want    error
	}{
		{automaticContext(t, "P"+strings.Repeat("f", 32)), observed.private.ExpiresAt, ErrUnauthorized},
		{ctx, testTime, ErrAutomaticExpired},
	} {
		command := maintenanceOperationsCommand(t, observed)
		command.ExpiresAt = denied.expires
		if result, err := s.ExecuteAutomatic(denied.ctx, command); !errors.Is(err, denied.want) ||
			result.Execution != NotExecuted || !result.AuditCommitted {
			t.Fatalf("runtime or expiry denial missed the audit boundary: %+v %v", result, err)
		}
	}
	option := observed.MaintenanceOptions[0]
	if option.RobotID != "R4" || option.FaultID != "F1" || option.JobID != "J4" || option.ServiceCell != (sim.Point{X: 9, Y: 10}) {
		t.Fatalf("offer lacks grounded evidence: %+v", option)
	}
	for _, request := range []sim.MaintenanceRequest{
		{RobotID: "R4", FaultID: "F2"},
		{RobotID: "R5", FaultID: "F1"},
	} {
		if command, err := observed.Prepare(OperationsDecision{Kind: "maintenance", Maintenance: &request}); command != nil || !errors.Is(err, ErrOperationsDecision) {
			t.Fatalf("Prepare accepted an unoffered request: %+v %v", command, err)
		}
		// Forge the detached offer to exercise the trusted cache at execution.
		forged := observed
		forged.MaintenanceOptions = slices.Clone(observed.MaintenanceOptions)
		forged.MaintenanceOptions[0].MaintenanceRequest = request
		command := maintenanceOperationsCommand(t, forged)
		if result, err := s.ExecuteAutomatic(ctx, command); !errors.Is(err, ErrObservation) || !result.AuditCommitted || result.Execution != NotExecuted {
			t.Fatalf("Go accepted a substituted request: %+v %v", result, err)
		}
	}
	command := maintenanceOperationsCommand(t, observed)
	delete(command.Preconditions, "J4")
	if result, err := s.ExecuteAutomatic(ctx, command); !errors.Is(err, ErrObservation) || !result.AuditCommitted {
		t.Fatalf("bound cargo job version was optional: %+v %v", result, err)
	}
	for s.Snapshot().Tick < 60 {
		if err := s.Tick(); err != nil {
			t.Fatal(err)
		}
	}
	command = maintenanceOperationsCommand(t, observed)
	if result, err := s.ExecuteAutomatic(ctx, command); !errors.Is(err, ErrObservation) || result.Execution != NotExecuted || !result.AuditCommitted {
		t.Fatalf("stale M1 version was admitted: %+v %v", result, err)
	}
	if state := s.Snapshot(); state.Maintenance != nil && (state.Maintenance.Active != nil || len(state.Maintenance.History) != 0) {
		t.Fatalf("a rejected decision consumed an attempt: %+v", state.Maintenance)
	}
}

func TestOperationsMaintenanceSerializesDecisionsAndReplaysReceiptIntegration(t *testing.T) {
	s, ctx, observed := operationsMaintenanceFixture(t)
	closed := maintenanceOperationsCommand(t, observed)
	if result, err := s.ExecuteAutomatic(ctx, closed); !errors.Is(err, ErrObservation) || result.Execution != NotExecuted {
		t.Fatalf("maintenance executed outside a window: %+v %v", result, err)
	}
	if err := s.SetOperationsWindow(ctx, &observed); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		command AutomaticCommand
		result  AutomaticResult
		err     error
	}
	done := make(chan outcome, 2)
	for range 2 {
		command := maintenanceOperationsCommand(t, observed)
		go func() {
			result, err := s.ExecuteAutomatic(ctx, command)
			done <- outcome{command, result, err}
		}()
	}
	var admitted outcome
	for range 2 {
		got := <-done
		if !got.result.AuditCommitted {
			t.Fatalf("missing command receipt: %+v", got)
		}
		if got.err == nil && got.result.Execution == Succeeded && admitted.result.AuditID == 0 {
			admitted = got
		} else if !errors.Is(got.err, ErrObservation) || got.result.Execution != NotExecuted {
			t.Fatalf("second decision was not rejected as stale: %+v", got)
		}
	}
	if err := s.SetAutomaticCommandsEnabled(false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAutomaticCommandsEnabled(true); err != nil {
		t.Fatal(err)
	}
	replay, err := s.ExecuteAutomatic(ctx, admitted.command)
	if admitted.result.AuditID == 0 || err != nil || replay.PriorAuditID != admitted.result.AuditID || replay.Execution != Succeeded || !replay.AuditCommitted {
		t.Fatalf("duplicate dispatch did not replay its receipt: %+v %v", replay, err)
	}
	state := s.Snapshot()
	if state.Maintenance == nil || state.Maintenance.Active == nil || !state.Maintenance.Active.AIDirected ||
		len(state.Maintenance.History) != 0 || state.Robots[3].ActiveFault != sim.FaultSensorFailure {
		t.Fatalf("admission fabricated a physical result: %+v", state.Maintenance)
	}
}

func TestOperationsMaintenanceRejectsRouteBlockedAfterObservationIntegration(t *testing.T) {
	s, ctx, _ := operationsMaintenanceFixture(t)
	observed, err := s.ObserveNewMaintenance(ctx, map[string]bool{})
	if err != nil || len(observed.MaintenanceOptions) != 1 {
		t.Fatalf("missing initial maintenance offer: %+v %v", observed.MaintenanceOptions, err)
	}
	if err := s.SetOperationsWindow(ctx, &observed); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		if err := s.Tick(); err != nil {
			t.Fatal(err)
		}
	}
	command := maintenanceOperationsCommand(t, observed)
	result, err := s.ExecuteAutomatic(ctx, command)
	if err == nil || err.Error() != "maintenance dispatch rejected: service_cell_unreachable" ||
		result.Execution != NotExecuted || !result.AuditCommitted {
		t.Fatalf("blocked route was not rejected at live admission: %+v %v", result, err)
	}
	state := s.Snapshot()
	if state.Tick != 52 || state.Robots[3].JobID != "J4" || state.Robots[3].ActiveFault != sim.FaultSensorFailure ||
		state.Maintenance != nil && (state.Maintenance.Active != nil || len(state.Maintenance.History) != 0) {
		t.Fatalf("a pending or denied decision changed physical ownership or used an attempt: %+v", state.Maintenance)
	}
}

func TestDisablingOperationsRestoresDeterministicMaintenanceIntegration(t *testing.T) {
	s, ctx, observed := operationsMaintenanceFixture(t)
	if err := s.SetOperationsWindow(ctx, &observed); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAutomaticCommandsEnabled(false); err != nil {
		t.Fatal(err)
	}
	if err := s.Tick(); err != nil {
		t.Fatal(err)
	}
	if work := s.Snapshot().Maintenance; work == nil || work.Active == nil || work.Active.AIDirected {
		t.Fatalf("disabling AI failed to release the maintenance window: %+v", work)
	}
}

func TestAutomaticTransportIDs(t *testing.T) {
	for _, robotID := range []string{"R10", "M1", "R11"} {
		command := AutomaticCommand{
			RuntimeID: strings.Repeat("r", 33), ObservationID: strings.Repeat("o", 33), IdempotencyKey: "idempotent",
			Preconditions:        map[string]uint64{"J1": 1, "R1": 1, robotID: 1},
			ReassignUnstartedJob: &safety.ReassignUnstartedJobRequest{JobID: "J1", FromRobotID: "R1", ToRobotID: robotID},
		}
		var want error
		if robotID != "R10" {
			want = ErrAutomaticCommand
		}
		if err := command.validate(); !errors.Is(err, want) {
			t.Errorf("automatic reassignment to %s: %v, want %v", robotID, err, want)
		}
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
	if maintenance := observed.Robots[10]; maintenance.ID != "M1" || maintenance.Role != sim.RoleMaintenance ||
		slices.ContainsFunc(observed.Assignments, func(o sim.AssignmentOption) bool { return o.RobotID == "M1" }) {
		t.Fatalf("operations observation must include M1 without offering transport jobs to it: %+v", maintenance)
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
