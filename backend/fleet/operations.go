package fleet

import (
	"context"
	"errors"
	"slices"

	"github.com/AyushM7/fleetops/backend/auth"
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
	private            AutomaticObservation
	Tick               int                        `json:"tick"`
	Robots             []sim.RobotState           `json:"robots"`
	Jobs               []sim.JobState             `json:"jobs"`
	Stations           []sim.StationState         `json:"stations"`
	Aisles             []sim.AisleState           `json:"aisles"`
	Traffic            []sim.TrafficDeadlock      `json:"traffic"`
	Assignments        []sim.AssignmentOption     `json:"assignments"`
	Reassignments      []OperationsReassignment   `json:"reassignments"`
	ActiveBreakdowns   []sim.MaintenanceBreakdown `json:"activeBreakdowns"`
	MaintenanceOptions []sim.MaintenanceOption    `json:"maintenanceOptions"`
	Maintenance        *sim.MaintenanceState      `json:"maintenance,omitempty"`
}

func (o OperationsObservation) ID() string { return o.private.ObservationID }

func (s *Service) ObserveOperations(ctx context.Context) (OperationsObservation, error) {
	return s.observeOperations(ctx, nil)
}

// ObserveNewMaintenance checks for a fresh eligible fault during the transport cooldown.
func (s *Service) ObserveNewMaintenance(ctx context.Context, reviewed map[string]bool) (OperationsObservation, error) {
	return s.observeOperations(ctx, reviewed)
}

func (s *Service) observeOperations(ctx context.Context, reviewed map[string]bool) (OperationsObservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	subject, err := s.automaticObservationSubject(ctx)
	if err != nil {
		return OperationsObservation{}, err
	}
	breakdowns, options := s.commander.OperationsMaintenance()
	if reviewed != nil && !slices.ContainsFunc(options, func(option sim.MaintenanceOption) bool { return !reviewed[option.FaultID] }) {
		return OperationsObservation{}, nil
	}
	if err := ctx.Err(); err != nil {
		return OperationsObservation{}, err
	}
	observation, err := s.captureAutomaticStateLocked(subject)
	if err != nil {
		return OperationsObservation{}, err
	}
	snapshot := observation.Snapshot
	observation.Snapshot = sim.State{}
	out := OperationsObservation{
		private: observation, Tick: snapshot.Tick, Robots: snapshot.Robots, Stations: snapshot.Stations,
		Aisles: snapshot.Aisles, Traffic: append([]sim.TrafficDeadlock{}, snapshot.TrafficDeadlocks...),
		Jobs: []sim.JobState{}, Assignments: []sim.AssignmentOption{}, Reassignments: []OperationsReassignment{},
		ActiveBreakdowns: breakdowns, MaintenanceOptions: options, Maintenance: snapshot.Maintenance,
	}
	s.automaticReads[len(s.automaticReads)-1].maintenance = slices.Clone(options)
	if work := out.Maintenance; work != nil {
		// Live blockers are included in ActiveBreakdowns. Keep only recent attempt history.
		work.Issues = nil
		work.History = work.History[max(0, len(work.History)-8):]
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
	if reviewed != nil {
		return out, nil
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
	Maintenance  *sim.MaintenanceRequest             `json:"maintenance,omitempty"`
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
	case decision.Kind == "abstain" && decision.Assignments == nil && decision.Reassignment == nil && decision.Maintenance == nil:
		switch decision.Reason {
		case "no_safe_option", "prefer_existing_dispatch", "insufficient_headroom":
			return nil, nil
		}
		return nil, ErrOperationsDecision
	case decision.Kind == "assign" && decision.Reason == "" && decision.Reassignment == nil && decision.Maintenance == nil:
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
	case decision.Kind == "reassign" && decision.Reason == "" && decision.Assignments == nil && decision.Reassignment != nil && decision.Maintenance == nil:
		pair := *decision.Reassignment
		if !slices.ContainsFunc(o.Reassignments, func(option OperationsReassignment) bool { return option.ReassignUnstartedJobRequest == pair }) {
			return nil, ErrOperationsDecision
		}
		command.ReassignUnstartedJob = &pair
		add(pair.JobID, pair.FromRobotID, pair.ToRobotID)
	case decision.Kind == "maintenance" && decision.Reason == "" && decision.Assignments == nil && decision.Reassignment == nil && decision.Maintenance != nil:
		request := *decision.Maintenance
		index := slices.IndexFunc(o.MaintenanceOptions, func(option sim.MaintenanceOption) bool { return option.MaintenanceRequest == request })
		if index < 0 {
			return nil, ErrOperationsDecision
		}
		command.DispatchMaintenance = &request
		add(request.RobotID, sim.MaintenanceRobotID)
		if job := o.MaintenanceOptions[index].JobID; job != "" {
			add(job)
		}
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

// SetOperationsWindow coordinates an authenticated decision window with Go
// auto-dispatch. Passing nil releases the window even after cancellation.
func (s *Service) SetOperationsWindow(ctx context.Context, observation *OperationsObservation) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	principal, _ := auth.FromContext(ctx)
	if err := s.automaticAccess(principal, s.runtimeID); err != nil {
		return err
	}
	if observation == nil {
		s.operationsWindow = ""
		s.commander.SetOperationsDispatchHold(nil)
		return nil
	}
	if s.poisoned {
		return ErrPoisoned
	}
	private := observation.private
	if private.RuntimeID != s.runtimeID {
		return ErrObservation
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ref := AutomaticCommand{ObservationID: private.ObservationID, ExpiresAt: private.ExpiresAt, Preconditions: private.Versions}
	if err := s.checkAutomaticObservation(ref, principal.Subject, s.clock().UTC()); err != nil {
		return err
	}
	if s.operationsWindow == private.ObservationID {
		return nil
	}
	options := slices.Clone(observation.Assignments)
	for _, candidate := range observation.Reassignments {
		options = append(options, sim.AssignmentOption{JobAssignment: sim.JobAssignment{RobotID: candidate.ToRobotID}})
	}
	s.commander.SetOperationsDispatchHold(options)
	s.commander.HoldOperationsMaintenance()
	s.operationsWindow = private.ObservationID
	return nil
}
