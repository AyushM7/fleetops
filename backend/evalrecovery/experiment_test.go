//go:build fleetops_test

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/sim"
)

func TestPairedExperimentsRepeatAndPreservePhase4(t *testing.T) {
	for _, spec := range scenarios() {
		t.Run(spec.ID, func(t *testing.T) {
			first, err := runExperiment(spec, spec.ExpectedAction, proposal.Approve, "")
			if err != nil {
				t.Fatal(err)
			}
			repeat, err := runExperiment(spec, spec.ExpectedAction, proposal.Approve, "")
			if err != nil {
				t.Fatal(err)
			}
			a, _ := json.Marshal(first)
			b, _ := json.Marshal(repeat)
			if !bytes.Equal(a, b) {
				t.Fatal("experiment report not deterministic")
			}
			if !first.PairedInitialEqual || !reflect.DeepEqual(first.BaselineInitial, first.TreatmentInitial) || first.Seed != 42 || first.EndTick-first.StartTick != 200 {
				t.Fatal("uncontrolled paired inputs")
			}
			if !first.CriterionMet {
				t.Fatalf("criterion failed: %+v", first)
			}
			if spec.ID == "blocked-reassignment" {
				if first.Baseline.Final.Tick != 242 || first.Baseline.Target.Status != sim.JobAssigned || !first.Baseline.Target.Censored || first.Baseline.Target.CompletionTick != nil {
					t.Fatal("baseline not censored at 242")
				}
				if first.Treatment.Target.CompletionTick == nil || *first.Treatment.Target.CompletionTick != 45 || *first.Treatment.Target.TimeToCompletion != 3 || first.Treatment.JobsCompleted != 1 {
					t.Fatal("Phase 4 delivery not reproduced")
				}
			}
			if spec.ExpectedAction == nil && (!reflect.DeepEqual(first.Baseline.Final, first.Treatment.Final) || first.SuccessfulIntervention || first.Intervention.ExecutionAttempted || first.Effects.JobsCompletedDelta != 0) {
				t.Fatal("no-op gained artificial recovery")
			}
			if spec.ExpectedAction != nil && (first.Intervention.Approval != "approved" || first.Intervention.SubmissionDidNotMutate == nil || !*first.Intervention.SubmissionDidNotMutate || first.Intervention.ApprovalDidNotMutate == nil || !*first.Intervention.ApprovalDidNotMutate || first.Intervention.SafetyValidation != "passed_creation_approval_execution") {
				t.Fatal("approval/validation boundary missing")
			}
		})
	}
}
func TestOperatorRejectionDoesNotExecuteOrRecover(t *testing.T) {
	spec := scenarios()[0]
	r, err := runExperiment(spec, spec.ExpectedAction, proposal.Reject, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.SuccessfulIntervention || r.TargetDeliveryImproved || r.CriterionMet || r.Intervention.ExecutionAttempted || !reflect.DeepEqual(r.Baseline.Final, r.Treatment.Final) {
		t.Fatal("rejected proposal executed or recovered")
	}
}
func TestUnsupportedAndIneligibleInterventionsFailWithoutRecovery(t *testing.T) {
	for _, spec := range scenarios()[:3] {
		a := &action{Action: "RepairRobot"}
		if spec.ID != "blocked-reassignment" {
			a = scenarios()[0].ExpectedAction
		}
		r, err := runExperiment(spec, a, proposal.Approve, "")
		if err != nil {
			t.Fatal(err)
		}
		if r.SuccessfulIntervention || r.TargetDeliveryImproved || r.Intervention.ExecutionAttempted || r.Intervention.Submission != "rejected" || !reflect.DeepEqual(r.Baseline.Final, r.Treatment.Final) {
			t.Fatal("invalid action gained execution or recovery")
		}
	}
}
func TestPendingExpiredAndForgedOperatorCannotExecute(t *testing.T) {
	s, err := newScenario(scenarios()[0])
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	p := proposal.Proposal{Action: proposal.ReassignUnstartedJob, State: proposal.Pending, ExpiresAt: now.Add(time.Minute)}
	before := s.Snapshot()
	if attempted, err := executeApproved(s, p, now); attempted || !errors.Is(err, proposal.ErrInvalidTransition) {
		t.Fatal(err)
	}
	p.State, p.Operator.Subject, p.ExpiresAt = proposal.Approved, testOperator, now
	if attempted, err := executeApproved(s, p, now); attempted || !errors.Is(err, proposal.ErrExpired) {
		t.Fatal(err)
	}
	p.Operator.Subject = "model-authored-operator"
	if attempted, err := executeApproved(s, p, now); attempted || !errors.Is(err, proposal.ErrInvalidTransition) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatal("denied execution mutated simulator")
	}
}
func TestCensoringAndAlreadyCompletedJobs(t *testing.T) {
	initial := sim.State{Tick: 42, Jobs: []sim.JobState{{ID: "J1", Status: sim.JobAssigned}, {ID: "J2", Status: sim.JobDone}}}
	m := newMeasurement(initial)
	final := initial
	final.Tick = 242
	m.sample(final)
	r := m.finish(final, "J1")
	if r.JobsCompleted != 0 || !r.Target.Censored || r.Target.CompletionTick != nil || r.Target.TimeToCompletion != nil {
		t.Fatal("incomplete or preexisting delivery counted as new")
	}
	r = newMeasurement(initial).finish(final, "J2")
	if !r.Target.CompletedBeforeWindow || r.Target.Censored || r.Target.CompletionTick != nil {
		t.Fatal("pre-window completion assigned invented delivery tick")
	}
}

func TestMissingTargetIsAnInvalidExperiment(t *testing.T) {
	spec := scenarios()[1]
	spec.TargetJob = "missing"
	if _, err := runExperiment(spec, nil, proposal.Approve, ""); err == nil {
		t.Fatal("missing target admitted as an incomplete/no-op success")
	}
}
func TestObservationIsReadOnlyAndContainsGoClassifications(t *testing.T) {
	s, err := newScenario(scenarios()[0])
	if err != nil {
		t.Fatal(err)
	}
	before := s.Snapshot()
	view, err := agentView(struct{ sim.Reader }{s})
	if err != nil || !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatal("observation mutated state", err)
	}
	if len(view.ActiveIncidents) != 2 || len(view.Candidates) == 0 || view.Candidates[0].ToRobotID != "R3" {
		t.Fatal("expected Go facts unavailable")
	}
}
func TestCommandRequiresTrustedDecisionAndRejectsStaleOrApprovalPayload(t *testing.T) {
	for _, args := range [][]string{{"run"}, {"run", "--test-operator", "model-approved"}} {
		if err := command(args, strings.NewReader("[]"), new(bytes.Buffer)); err == nil {
			t.Fatal("missing explicit operator decision accepted")
		}
	}
	if err := command([]string{"run", "--test-operator", "approve"}, strings.NewReader(`[{"id":"x","approved":true}]`), new(bytes.Buffer)); err == nil {
		t.Fatal("approval payload accepted")
	}
	obs, err := observeScenarios()
	if err != nil {
		t.Fatal(err)
	}
	requests := []request{}
	for _, o := range obs {
		requests = append(requests, request{o.ID, o.InitialHash, o.ExpectedAction})
	}
	requests[0].InitialHash = "stale"
	if _, err := runRequests(requests, proposal.Approve); err == nil {
		t.Fatal("stale snapshot admitted")
	}
	requests[0].ID = requests[1].ID
	if _, err := runRequests(requests, proposal.Approve); err == nil {
		t.Fatal("duplicate or missing scenario admitted")
	}
}
