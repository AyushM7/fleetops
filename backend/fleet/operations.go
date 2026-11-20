package fleet

import (
	"context"
	"errors"
	"slices"

	"github.com/AyushM7/fleetops/backend/safety"
	"github.com/AyushM7/fleetops/backend/sim"
)

var ErrOperationsDecision = errors.New("invalid or ungrounded operations decision")

const operationsPendingLimit = 16

type OperationsReassignment struct {
	safety.ReassignUnstartedJobRequest
	PickupSteps       int `json:"pickupSteps"`
	DeliverySteps     int `json:"deliverySteps"`
	ChargingExitSteps int `json:"chargingExitSteps"`
	RequiredBattery   int `json:"requiredBattery"`
}

// OperationsObservation keeps private command metadata with the Go host.
type OperationsObservation struct {
	private       AutomaticObservation
	Tick          int                      `json:"tick"`
	Robots        []sim.RobotState         `json:"robots"`
	Jobs          []sim.JobState           `json:"jobs"`
	Stations      []sim.StationState       `json:"stations"`
	Aisles        []sim.AisleState         `json:"aisles"`
	Traffic       []sim.TrafficDeadlock    `json:"traffic"`
	Assignments   []sim.AssignmentOption   `json:"assignments"`
	Reassignments []OperationsReassignment `json:"reassignments"`
}

func (o OperationsObservation) ID() string { return o.private.ObservationID }

func (s *Service) ObserveOperations(ctx context.Context) (OperationsObservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	observation, err := s.readAutomaticStateLocked(ctx)
	if err != nil {
		return OperationsObservation{}, err
	}
	snapshot := observation.Snapshot
	observation.Snapshot = sim.State{}
	out := OperationsObservation{
		private: observation, Tick: snapshot.Tick, Robots: snapshot.Robots, Stations: snapshot.Stations,
		Aisles: snapshot.Aisles, Traffic: append([]sim.TrafficDeadlock{}, snapshot.TrafficDeadlocks...),
		Jobs: []sim.JobState{}, Assignments: []sim.AssignmentOption{}, Reassignments: []OperationsReassignment{},
	}
	pending := 0
	for _, job := range snapshot.Jobs {
		switch job.Status {
		case sim.JobAssigned, sim.JobInProgress:
			out.Jobs = append(out.Jobs, job)
		case sim.JobPending:
			if pending < operationsPendingLimit {
				out.Jobs = append(out.Jobs, job)
				pending++
			}
		}
	}
	out.Assignments = s.commander.OperationsAssignments(out.Jobs)
	for _, job := range out.Jobs {
		if job.Status != sim.JobAssigned {
			continue
		}
		for _, robot := range snapshot.Robots {
			if robot.Status != sim.RobotIdle || robot.Role != sim.RoleTransport {
				continue
			}
			plan, err := s.commander.PreviewReassignment(job.ID, job.AssignedRobot, robot.ID)
			if err == nil {
				out.Reassignments = append(out.Reassignments, OperationsReassignment{
					ReassignUnstartedJobRequest: safety.ReassignUnstartedJobRequest{JobID: job.ID, FromRobotID: job.AssignedRobot, ToRobotID: robot.ID},
					PickupSteps:                 plan.PickupSteps, DeliverySteps: plan.DeliverySteps,
					ChargingExitSteps: plan.ChargingExitSteps, RequiredBattery: plan.RequiredBattery,
				})
			}
		}
	}
	return out, nil
}

type OperationsDecision struct {
	Kind         string                              `json:"kind"`
	Reason       string                              `json:"reason,omitempty"`
	Assignments  []sim.JobAssignment                 `json:"assignments,omitempty"`
	Reassignment *safety.ReassignUnstartedJobRequest `json:"reassignment,omitempty"`
}

// Prepare builds a command from trusted evidence for independent Go validation.
func (o OperationsObservation) Prepare(decision OperationsDecision) (*AutomaticCommand, error) {
	command := &AutomaticCommand{
		RuntimeID: o.private.RuntimeID, ObservationID: o.private.ObservationID,
		ExpiresAt: o.private.ExpiresAt, Preconditions: map[string]uint64{},
	}
	add := func(ids ...string) {
		for _, id := range ids {
			command.Preconditions[id] = o.private.Versions[id]
		}
	}
	switch {
	case decision.Kind == "abstain" && decision.Assignments == nil && decision.Reassignment == nil:
		switch decision.Reason {
		case "no_safe_option", "prefer_existing_dispatch", "insufficient_headroom":
			return nil, nil
		}
		return nil, ErrOperationsDecision
	case decision.Kind == "assign" && decision.Reason == "" && decision.Reassignment == nil:
		command.AssignJobBatch = &sim.AssignJobBatchRequest{Assignments: slices.Clone(decision.Assignments)}
		if command.AssignJobBatch.Validate() != nil {
			return nil, ErrOperationsDecision
		}
		for _, pair := range decision.Assignments {
			if !slices.ContainsFunc(o.Assignments, func(option sim.AssignmentOption) bool { return option.JobAssignment == pair }) {
				return nil, ErrOperationsDecision
			}
			add(pair.JobID, pair.RobotID)
		}
	case decision.Kind == "reassign" && decision.Reason == "" && decision.Assignments == nil && decision.Reassignment != nil:
		pair := *decision.Reassignment
		if !slices.ContainsFunc(o.Reassignments, func(option OperationsReassignment) bool { return option.ReassignUnstartedJobRequest == pair }) {
			return nil, ErrOperationsDecision
		}
		command.ReassignUnstartedJob = &pair
		add(pair.JobID, pair.FromRobotID, pair.ToRobotID)
	default:
		return nil, ErrOperationsDecision
	}
	key, err := newID()
	if err != nil {
		return nil, err
	}
	command.IdempotencyKey = key
	if command.validate() != nil {
		return nil, ErrOperationsDecision
	}
	return command, nil
}
