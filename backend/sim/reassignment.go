package sim

import (
	"errors"

	"github.com/AyushM7/fleetops/backend/routing"
)

var (
	ErrReassignment       = errors.New("job reassignment requires distinct healthy robots with battery >=30%, an uncollected blocked source job, and an idle recipient")
	ErrReassignmentRoute  = errors.New("reassignment route or charging exit is blocked")
	ErrReassignmentEnergy = errors.New("reassignment has insufficient mission energy")
)

// ReassignmentPlan is snapshot-scoped admission evidence, never a reservation.
type ReassignmentPlan struct {
	PickupSteps, DeliverySteps, ChargingExitSteps, RequiredBattery int
}

// PreviewReassignment is READ. Every leg avoids current robot occupancy and
// occupied aisles, including the route from delivery to an available charger.
func (s *Sim) PreviewReassignment(jobID, fromRobotID, toRobotID string) (ReassignmentPlan, error) {
	job, _, recipient, err := s.reassignmentEntities(jobID, fromRobotID, toRobotID)
	if err != nil {
		return ReassignmentPlan{}, err
	}
	pickup, ok := s.reassignmentDistance(*recipient, Point{recipient.X, recipient.Y}, job.From)
	if !ok {
		return ReassignmentPlan{}, ErrReassignmentRoute
	}
	delivery, ok := s.reassignmentDistance(*recipient, job.From, job.To)
	if !ok {
		return ReassignmentPlan{}, ErrReassignmentRoute
	}
	exit := -1
	for _, station := range s.state.Stations {
		if station.OccupiedBy != "" || station.ReservedBy != "" {
			continue
		}
		steps, reachable := s.reassignmentDistance(*recipient, job.To, Point{station.X, station.Y})
		if reachable && (exit < 0 || steps < exit) {
			exit = steps
		}
	}
	if exit < 0 {
		return ReassignmentPlan{}, ErrReassignmentRoute
	}
	required := (pickup+delivery+exit)*s.cfg.MovingDrain + s.cfg.ReserveBattery
	if recipient.Battery < required || recipient.Battery < s.cfg.MinAssignBattery {
		return ReassignmentPlan{}, ErrReassignmentEnergy
	}
	return ReassignmentPlan{pickup, delivery, exit, required}, nil
}

func (s *Sim) reassignmentEntities(jobID, fromRobotID, toRobotID string) (*JobState, *RobotState, *RobotState, error) {
	var job *JobState
	var source, recipient *RobotState
	for i := range s.state.Jobs {
		if s.state.Jobs[i].ID == jobID {
			job = &s.state.Jobs[i]
		}
	}
	for i := range s.state.Robots {
		r := &s.state.Robots[i]
		if r.ID == fromRobotID {
			source = r
		}
		if r.ID == toRobotID {
			recipient = r
		}
	}
	if job == nil || source == nil || recipient == nil || source == recipient {
		return nil, nil, nil, ErrReassignment
	}
	if source.Status != RobotMoving || source.Battery < 30 || source.JobID != job.ID || !source.Blocked || source.WaitTicks < s.cfg.BlockedAfter || job.Status != JobAssigned || job.AssignedRobot != source.ID || (Point{source.X, source.Y}) == job.From {
		return nil, nil, nil, ErrReassignment
	}
	if recipient.Status != RobotIdle || recipient.Battery < 30 || recipient.JobID != "" || s.unresponsiveTicks[source.ID] > 0 || s.unresponsiveTicks[recipient.ID] > 0 {
		return nil, nil, nil, ErrReassignment
	}
	for _, other := range s.state.Robots {
		if other.ID != source.ID && other.JobID == job.ID {
			return nil, nil, nil, ErrReassignment
		}
	}
	for _, other := range s.state.Jobs {
		if other.ID != job.ID && (other.Status == JobAssigned || other.Status == JobInProgress) && other.AssignedRobot == recipient.ID {
			return nil, nil, nil, ErrReassignment
		}
	}
	for _, station := range s.state.Stations {
		if station.OccupiedBy == recipient.ID || station.ReservedBy == recipient.ID {
			return nil, nil, nil, ErrReassignment
		}
	}
	return job, source, recipient, nil
}

func (s *Sim) reassignmentDistance(robot RobotState, from, to Point) (int, bool) {
	path, err := s.reassignmentRoute(robot, from, to)
	return len(path), err == nil
}

func (s *Sim) reassignmentRoute(robot RobotState, from, to Point) ([]Point, error) {
	path, err := routing.FindPath(routingGrid{cfg: s.cfg}, routing.Cell{X: from.X, Y: from.Y}, routing.Cell{X: to.X, Y: to.Y}, routing.Options{Blocked: func(cell routing.Cell) bool {
		point := Point{cell.X, cell.Y}
		for _, other := range s.state.Robots {
			if other.ID == robot.ID {
				continue
			}
			if point == (Point{other.X, other.Y}) || s.cfg.onAisle(point) && s.cfg.onAisle(Point{other.X, other.Y}) && other.X == point.X {
				return true
			}
		}
		return false
	}})
	if err != nil {
		return nil, err
	}
	points := make([]Point, len(path))
	for i, cell := range path {
		points[i] = Point{cell.X, cell.Y}
	}
	return points, nil
}

// ReassignUnstartedJob is COMMAND. The service serializes this atomic transfer
// with ticks and performs authorization, approval, expiry, and durable auditing.
// Success means ownership transferred; delivery occurs through normal ticks.
func (s *Sim) ReassignUnstartedJob(jobID, fromRobotID, toRobotID string) error {
	if _, err := s.PreviewReassignment(jobID, fromRobotID, toRobotID); err != nil {
		return err
	}
	job, source, recipient, err := s.reassignmentEntities(jobID, fromRobotID, toRobotID)
	if err != nil {
		return err
	}
	path, err := s.reassignmentRoute(*recipient, Point{recipient.X, recipient.Y}, job.From)
	if err != nil {
		return err
	}
	s.clearNavigation(source)
	s.clearNavigation(recipient)
	source.Status, source.JobID = RobotIdle, ""
	job.AssignedRobot = recipient.ID
	recipient.Status, recipient.JobID = RobotMoving, job.ID
	s.navigation[recipient.ID] = robotNavigation{target: job.From, route: path}
	return nil
}
