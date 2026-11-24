package sim

import (
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/AyushM7/fleetops/backend/scheduler"
)

var (
	ErrInvalidAssignmentBatch = errors.New("assignment batch requires one to ten distinct pending job and transport robot IDs")
	ErrAssignmentConflict     = errors.New("job is not pending or robot is unavailable for transport")
	ErrAssignmentInfeasible   = errors.New("assignment has no reachable, energy-feasible mission and charging exit")
	ErrAssignmentLayout       = errors.New("assignment batches require the V2 warehouse")
)

type JobAssignment struct {
	JobID   string `json:"jobId"`
	RobotID string `json:"robotId"`
}

type AssignJobBatchRequest struct {
	Assignments []JobAssignment `json:"assignments"`
}

func (r AssignJobBatchRequest) Validate() error {
	if len(r.Assignments) == 0 || len(r.Assignments) > warehouseTransportCount {
		return ErrInvalidAssignmentBatch
	}
	jobs := make(map[string]bool, len(r.Assignments))
	robots := make(map[string]bool, len(r.Assignments))
	for _, assignment := range r.Assignments {
		if !canonicalAssignmentID(assignment.JobID, 'J') || !IsV2TransportID(assignment.RobotID) ||
			jobs[assignment.JobID] || robots[assignment.RobotID] {
			return ErrInvalidAssignmentBatch
		}
		jobs[assignment.JobID], robots[assignment.RobotID] = true, true
	}
	return nil
}

func IsV2TransportID(id string) bool {
	return canonicalAssignmentID(id, 'R') && idNumber(id) <= warehouseTransportCount
}

func canonicalAssignmentID(id string, prefix byte) bool {
	if len(id) < 2 || len(id) > 20 || id[0] != prefix {
		return false
	}
	number := idNumber(id)
	return number > 0 && strconv.Itoa(number) == id[1:]
}

// AssignJobBatch is COMMAND. Validate the entire batch against one state before
// changing ownership. Caller serializes execution with other commands and ticks.
func (s *Sim) AssignJobBatch(request AssignJobBatchRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if s.cfg.MaintenanceBay == nil {
		return ErrAssignmentLayout
	}
	distance := s.travelDistance(true)
	params := s.assignmentParams()
	type pair struct {
		robot *RobotState
		job   *JobState
	}
	checked := make([]pair, 0, len(request.Assignments))
	for _, assignment := range request.Assignments {
		ji := slices.IndexFunc(s.state.Jobs, func(job JobState) bool { return job.ID == assignment.JobID })
		ri := slices.IndexFunc(s.state.Robots, func(robot RobotState) bool { return robot.ID == assignment.RobotID })
		if ji < 0 || ri < 0 {
			return fmt.Errorf("%w: %s to %s", ErrAssignmentConflict, assignment.JobID, assignment.RobotID)
		}
		job, robot := &s.state.Jobs[ji], &s.state.Robots[ri]
		if _, err := s.previewAssignment(*job, *robot, distance, params); err != nil {
			return fmt.Errorf("%w: %s to %s", err, job.ID, robot.ID)
		}
		checked = append(checked, pair{robot, job})
	}
	for _, assignment := range checked {
		s.beginJob(assignment.robot, assignment.job)
	}
	return nil
}

type AssignmentOption struct {
	JobAssignment
	PickupSteps       int `json:"pickupSteps"`
	DeliverySteps     int `json:"deliverySteps"`
	ChargingExitSteps int `json:"chargingExitSteps"`
	RequiredBattery   int `json:"requiredBattery"`
}

// OperationsAssignments previews the same admission rules used by AssignJobBatch.
func (s *Sim) OperationsAssignments(jobs []JobState) []AssignmentOption {
	options := []AssignmentOption{}
	distance, params := s.travelDistance(true), s.assignmentParams()
	for _, job := range jobs {
		if job.Status != JobPending {
			continue
		}
		for _, robot := range s.state.Robots {
			option, err := s.previewAssignment(job, robot, distance, params)
			if err == nil {
				options = append(options, option)
			}
		}
	}
	return options
}

func (s *Sim) previewAssignment(job JobState, robot RobotState, distance scheduler.Distance, params scheduler.Params) (AssignmentOption, error) {
	if job.Status != JobPending || job.Deferred || job.AssignedRobot != "" || !s.canAcceptJob(robot) ||
		s.reservedStation(robot.ID) != nil || s.unresponsiveTicks[robot.ID] > 0 {
		return AssignmentOption{}, ErrAssignmentConflict
	}
	from, to := scheduler.Cell{X: job.From.X, Y: job.From.Y}, scheduler.Cell{X: job.To.X, Y: job.To.Y}
	info := scheduler.JobInfo{Num: idNumber(job.ID), From: from, To: to}
	if s.cfg.AutoCharge {
		info.ExitCost = s.chargingExitDistance(job.To, robot.ID, distance)
	}
	candidate := scheduler.RobotInfo{Num: idNumber(robot.ID), Pos: scheduler.Cell{X: robot.X, Y: robot.Y}, Battery: robot.Battery}
	if len(scheduler.Assign([]scheduler.RobotInfo{candidate}, []scheduler.JobInfo{info}, distance, params)) == 0 {
		return AssignmentOption{}, ErrAssignmentInfeasible
	}
	pickup, _ := distance(candidate.Pos, from)
	delivery, _ := distance(from, to)
	return AssignmentOption{
		JobAssignment: JobAssignment{JobID: job.ID, RobotID: robot.ID},
		PickupSteps:   pickup, DeliverySteps: delivery, ChargingExitSteps: info.ExitCost,
		RequiredBattery: max(params.MinBattery, (pickup+delivery+info.ExitCost)*params.MovingDrain+params.ReserveBattery),
	}, nil
}
