//go:build fleetops_test

package main

import (
	"fmt"
	"reflect"
	"time"

	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/safety"
	"github.com/AyushM7/fleetops/backend/sim"
)

const testOperator = "p5-03-test-operator"

type intervention struct {
	Recommendation         *action                  `json:"recommendation"`
	ApprovalRequired       bool                     `json:"approvalRequired"`
	Submission             string                   `json:"submission"`
	Approval               string                   `json:"approval"`
	Operator               string                   `json:"operator"`
	Execution              string                   `json:"execution"`
	SafetyValidation       string                   `json:"safetyValidation"`
	Reason                 string                   `json:"reason"`
	Events                 []proposal.DecisionEvent `json:"domainEvents"`
	SubmissionDidNotMutate *bool                    `json:"submissionDidNotMutate"`
	ApprovalDidNotMutate   *bool                    `json:"approvalDidNotMutate"`
	ExecutionAttempted     bool                     `json:"executionAttempted"`
}
type effects struct {
	JobsCompletedDelta        int `json:"jobsCompletedDelta"`
	BlockedRobotTicksDelta    int `json:"blockedRobotTicksDelta"`
	ChargingRobotTicksDelta   int `json:"chargingRobotTicksDelta"`
	OccupiedStationTicksDelta int `json:"occupiedStationTicksDelta"`
	FinalStoredBatteryDelta   int `json:"finalStoredBatteryDelta"`
}
type experiment struct {
	Scenario                 scenario     `json:"scenario"`
	Seed                     int64        `json:"seed"`
	Horizon                  int          `json:"horizonTicks"`
	StartTick                int          `json:"startTick"`
	EndTick                  int          `json:"endTick"`
	BaselineInitial          sim.State    `json:"baselineInitial"`
	TreatmentInitial         sim.State    `json:"treatmentInitial"`
	InitialHash              string       `json:"initialHash"`
	PairedInitialEqual       bool         `json:"pairedInitialEqual"`
	MeasurementFaultSchedule []fault      `json:"measurementFaultSchedule"`
	Intervention             intervention `json:"intervention"`
	Baseline                 outcome      `json:"baseline"`
	Treatment                outcome      `json:"treatment"`
	Effects                  effects      `json:"effects"`
	SuccessfulIntervention   bool         `json:"successfulIntervention"`
	TargetDeliveryImproved   bool         `json:"targetDeliveryImproved"`
	Criterion                string       `json:"criterion"`
	CriterionMet             bool         `json:"criterionMet"`
}

func createProposal(l *proposal.Lifecycle, id string, a action, expiry time.Time) (proposal.Result, error) {
	switch a.Action {
	case "SetCharging":
		if a.JobID != "" || a.FromRobotID != "" || a.ToRobotID != "" || a.Purpose != "routine_charging" {
			return proposal.Result{}, proposal.ErrInvalidAction
		}
		return l.Create(id, safety.SetChargingRequest{RobotID: a.RobotID, StationID: a.StationID}, expiry)
	case "ReassignUnstartedJob":
		if a.RobotID != "" || a.StationID != "" || a.Purpose != "incident_recovery" {
			return proposal.Result{}, proposal.ErrInvalidAction
		}
		return l.CreateReassignment(id, safety.ReassignUnstartedJobRequest{JobID: a.JobID, FromRobotID: a.FromRobotID, ToRobotID: a.ToRobotID}, expiry)
	default:
		return proposal.Result{}, proposal.ErrInvalidAction
	}
}
func executeApproved(s sim.Commander, p proposal.Proposal, now time.Time) (bool, error) {
	if p.State != proposal.Approved || p.Operator.Subject != testOperator {
		return false, proposal.ErrInvalidTransition
	}
	if !now.Before(p.ExpiresAt) {
		return false, proposal.ErrExpired
	}
	if err := proposal.Preflight(s, p); err != nil {
		return false, err
	}
	switch p.Action {
	case proposal.SetCharging:
		return true, s.SetCharging(p.Request.RobotID, p.Request.StationID)
	case proposal.ReassignUnstartedJob:
		return true, s.ReassignUnstartedJob(p.Reassignment.JobID, p.Reassignment.FromRobotID, p.Reassignment.ToRobotID)
	default:
		return false, proposal.ErrInvalidAction
	}
}

// This is an isolated command experiment, not fleet.Service or a durable audit workflow.
// Only the trusted test process supplies the operator decision; stdin has no approval fields.
func applyTreatment(s *sim.Sim, id string, a *action, decision proposal.Decision) (intervention, error) {
	r := intervention{Recommendation: a, ApprovalRequired: a != nil, Submission: "not_requested", Approval: "not_requested", Execution: "not_requested", SafetyValidation: "not_applicable", Events: []proposal.DecisionEvent{}}
	if a == nil {
		return r, nil
	}
	if decision != proposal.Approve && decision != proposal.Reject {
		return r, fmt.Errorf("explicit trusted test operator decision required")
	}
	now := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC).Add(time.Duration(s.Snapshot().Tick) * time.Second)
	l, err := proposal.New(struct{ sim.Reader }{s}, func() time.Time { return now })
	if err != nil {
		return r, err
	}
	before := s.Snapshot()
	created, err := createProposal(l, id, *a, now.Add(time.Minute))
	if created.Event.Attempt != "" {
		r.Events = append(r.Events, created.Event)
	}
	creationUnchanged := reflect.DeepEqual(before, s.Snapshot())
	r.SubmissionDidNotMutate = &creationUnchanged
	if !creationUnchanged {
		return r, fmt.Errorf("proposal creation mutated simulator")
	}
	if err != nil {
		r.Submission, r.SafetyValidation, r.Reason = "rejected", "rejected", err.Error()
		return r, nil
	}
	r.Submission, r.SafetyValidation = string(created.Proposal.State), "passed_creation_preflight"
	decided, err := l.Decide(id, decision, proposal.OperatorIdentity{Subject: testOperator})
	r.Events = append(r.Events, decided.Event)
	approvalUnchanged := reflect.DeepEqual(before, s.Snapshot())
	r.ApprovalDidNotMutate = &approvalUnchanged
	if !approvalUnchanged {
		return r, fmt.Errorf("domain decision mutated simulator")
	}
	if err != nil {
		r.Approval, r.Reason = "rejected", err.Error()
		return r, nil
	}
	r.Approval, r.Operator = string(decided.Proposal.State), decided.Proposal.Operator.Subject
	if decided.Proposal.State != proposal.Approved {
		return r, nil
	}
	r.ExecutionAttempted, err = executeApproved(s, decided.Proposal, now)
	if err != nil {
		r.Execution, r.SafetyValidation, r.Reason = "failed", "rejected_at_execution", err.Error()
		return r, nil
	}
	r.Execution, r.SafetyValidation = "succeeded", "passed_creation_approval_execution"
	return r, nil
}

func runExperiment(spec scenario, a *action, decision proposal.Decision, expectedHash string) (experiment, error) {
	b, err := newScenario(spec)
	if err != nil {
		return experiment{}, err
	}
	t, err := newScenario(spec)
	if err != nil {
		return experiment{}, err
	}
	initial, other := b.Snapshot(), t.Snapshot()
	r := experiment{Scenario: spec, Seed: initial.Seed, Horizon: horizon, StartTick: initial.Tick, EndTick: initial.Tick + horizon,
		BaselineInitial: initial, TreatmentInitial: other, InitialHash: stateHash(initial), PairedInitialEqual: reflect.DeepEqual(initial, other), MeasurementFaultSchedule: []fault{}}
	if !r.PairedInitialEqual {
		return r, fmt.Errorf("paired initial state mismatch")
	}
	if expectedHash != "" && r.InitialHash != expectedHash {
		return r, fmt.Errorf("stale observation hash for %s", spec.ID)
	}
	if spec.TargetJob != "" {
		found := false
		for _, job := range initial.Jobs {
			if job.ID == spec.TargetJob {
				found = true
			}
		}
		if !found {
			return r, fmt.Errorf("target job missing from initial snapshot")
		}
	}
	r.Intervention, err = applyTreatment(t, spec.ID, a, decision)
	if err != nil {
		return r, err
	}
	bm, tm := newMeasurement(initial), newMeasurement(other)
	for range horizon {
		b.Tick()
		t.Tick()
		bm.sample(b.Snapshot())
		tm.sample(t.Snapshot())
	}
	r.Baseline, r.Treatment = bm.finish(b.Snapshot(), spec.TargetJob), tm.finish(t.Snapshot(), spec.TargetJob)
	if spec.TargetJob != "" && (r.Baseline.Target.Status == "missing" || r.Treatment.Target.Status == "missing") {
		return r, fmt.Errorf("target job missing from measured outcomes")
	}
	r.SuccessfulIntervention = r.Intervention.Execution == "succeeded"
	r.TargetDeliveryImproved = r.SuccessfulIntervention && r.Baseline.Target != nil && r.Treatment.Target != nil && r.Baseline.Target.Censored && !r.Treatment.Target.Censored
	r.Effects = effects{r.Treatment.JobsCompleted - r.Baseline.JobsCompleted, r.Treatment.BlockedRobotTicks - r.Baseline.BlockedRobotTicks,
		r.Treatment.ChargingRobotTicks - r.Baseline.ChargingRobotTicks, r.Treatment.OccupiedStationTicks - r.Baseline.OccupiedStationTicks, r.Treatment.TotalFinalBattery - r.Baseline.TotalFinalBattery}
	r.Criterion, r.CriterionMet = criterion(spec, r)
	return r, nil
}
func criterion(spec scenario, r experiment) (string, bool) {
	switch spec.Goal {
	case "target_delivery":
		return "Go command succeeds; target delivered in treatment, censored in baseline", r.TargetDeliveryImproved
	case "battery":
		return "Go charging command succeeds; R1 final battery exceeds baseline (not a throughput claim)", r.SuccessfulIntervention && r.Treatment.Final.Robots[0].Battery > r.Baseline.Final.Robots[0].Battery
	default:
		return "No recommendation or execution; final paired states identical", r.Intervention.Recommendation == nil && !r.Intervention.ExecutionAttempted && reflect.DeepEqual(r.Baseline.Final, r.Treatment.Final)
	}
}
