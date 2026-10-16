package sim

import "github.com/AyushM7/fleetops/backend/scheduler"

// Availability and parked obstacles are estimates at planning time, not promises
// about the station when the robot eventually finishes its delivery.
func (s *Sim) chargingExitDistance(from Point, robotID string, distance scheduler.Distance) int {
	best := -1
	for _, station := range s.state.Stations {
		if !s.stationFreeFor(station, robotID) {
			continue
		}
		steps, reachable := distance(scheduler.Cell{X: from.X, Y: from.Y}, scheduler.Cell{X: station.X, Y: station.Y})
		if reachable && (best < 0 || steps < best) {
			best = steps
		}
	}
	return best
}

func (s *Sim) routeAffordable(robot RobotState, target Point, steps int) bool {
	if !s.cfg.AutoCharge {
		return true
	}
	if robot.JobID == "" {
		if s.navigation[robot.ID].relocation {
			return s.relocationAffordable(robot, target, steps)
		}
		// Admission retains one unit; the arrival safety net remains in stepChargeTrip.
		return robot.Battery-steps*s.cfg.MovingDrain >= 1
	}
	for _, job := range s.state.Jobs {
		if job.ID == robot.JobID && job.AssignedRobot == robot.ID {
			return s.missionAffordable(robot, job, target, steps)
		}
	}
	return false
}

func (s *Sim) relocationAffordable(robot RobotState, target Point, steps int) bool {
	exit := s.relocationExit(robot, target)
	return exit >= 0 && robot.Battery-(steps+exit)*s.cfg.MovingDrain >= 1
}

func (s *Sim) missionAffordable(robot RobotState, job JobState, target Point, steps int) bool {
	distance := s.dispatchDistance()
	if job.Status == JobAssigned && target == job.From {
		delivery, reachable := distance(scheduler.Cell{X: job.From.X, Y: job.From.Y}, scheduler.Cell{X: job.To.X, Y: job.To.Y})
		if !reachable {
			return false
		}
		steps += delivery
	} else if job.Status != JobInProgress || target != job.To {
		return false
	}
	// Destination availability is an estimate only at assignment admission.
	exit := s.relocationExit(robot, job.To)
	return exit >= 0 && robot.Battery-(steps+exit)*s.cfg.MovingDrain >= 1
}
