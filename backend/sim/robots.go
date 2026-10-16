package sim

import (
	"cmp"
	"slices"
)

func (s *Sim) stepRobots() {
	slices.SortFunc(s.state.Robots, func(a, b RobotState) int {
		return cmp.Compare(idNumber(a.ID), idNumber(b.ID))
	})
	// Pass A finishes for all robots before dispatch or movement begins.
	skip := make([]bool, len(s.state.Robots))
	for i := range s.state.Robots {
		skip[i] = s.prepareRobot(&s.state.Robots[i])
	}
	s.assignPending(skip)   // Pass B.
	s.seekCharging(skip)    // Pass C.
	s.clearIdleAisles(skip) // Pass C2.
	moved := make([]bool, len(s.state.Robots))
	for i := range s.state.Robots { // Pass D retains numeric movement priority.
		robot := &s.state.Robots[i]
		if skip[i] {
			continue
		}
		if robot.Status == RobotMoving {
			before := Point{X: robot.X, Y: robot.Y}
			s.stepMoving(robot)
			moved[i] = before != (Point{X: robot.X, Y: robot.Y})
		}
		if robot.Status != RobotMoving {
			s.finishNavigation(robot)
		}
	}
	s.recoverTraffic(moved) // Pass E cannot move a robot twice.
}

func (s *Sim) prepareRobot(robot *RobotState) bool {
	if s.unresponsiveTicks[robot.ID] > 0 {
		s.unresponsiveTicks[robot.ID]--
		if s.unresponsiveTicks[robot.ID] == 0 {
			robot.Status = RobotOffline
			s.finishNavigation(robot)
			return true
		}
	}
	if robot.Status != RobotMoving {
		s.finishNavigation(robot)
	}
	if s.prepareRecovery(robot) {
		return true
	}
	if robot.Status == RobotCharging {
		s.stepCharging(robot)
		return true
	}
	if robot.Status == RobotIdle {
		s.stepIdle(robot)
	}
	return false
}

func (s *Sim) stepCharging(robot *RobotState) {
	robot.Battery = min(100, robot.Battery+s.cfg.ChargeGain)
	if robot.Battery < 100 {
		return
	}
	robot.Status = RobotIdle
	for i := range s.state.Stations {
		if s.state.Stations[i].OccupiedBy == robot.ID {
			s.state.Stations[i].OccupiedBy = ""
		}
	}
}

func (s *Sim) stepIdle(robot *RobotState) {
	if s.queuedForCharging(*robot) {
		return
	}
	if s.state.Tick%s.cfg.IdleDrainEvery == 0 {
		robot.Battery = max(0, robot.Battery-1)
	}
	if robot.Battery == 0 {
		robot.Status = RobotFaulted
		return
	}
}

// Low-power waiting applies only while an affordable charger is unavailable.
func (s *Sim) queuedForCharging(robot RobotState) bool {
	if !s.cfg.AutoCharge || robot.Status != RobotIdle || robot.JobID != "" || robot.Battery >= s.cfg.ChargeBelow {
		return false
	}
	for _, station := range s.state.Stations {
		if robot.X == station.X && robot.Y == station.Y && station.OccupiedBy == "" && station.ReservedBy == "" {
			return false
		}
	}
	if station, _ := s.nearestChargingStation(robot); station != nil {
		return false
	}
	exit := s.relocationExit(robot, Point{X: robot.X, Y: robot.Y})
	return exit >= 0 && robot.Battery-exit*s.cfg.MovingDrain >= 1
}

func (s *Sim) stepMoving(robot *RobotState) {
	if robot.JobID == "" {
		if s.navigation[robot.ID].relocation {
			s.stepRelocation(robot)
		} else {
			s.stepChargeTrip(robot)
		}
		return
	}
	for i := range s.state.Jobs {
		job := &s.state.Jobs[i]
		if job.ID != robot.JobID || job.AssignedRobot != robot.ID {
			continue
		}
		if job.Status != JobAssigned && job.Status != JobInProgress {
			return
		}
		if updateArrival(robot, job) {
			return
		}
		target := job.From
		if job.Status == JobInProgress {
			target = job.To
		}
		if !s.advanceNavigation(robot, target) {
			return
		}
		previousStatus := job.Status
		updateArrival(robot, job)
		robot.Battery = max(0, robot.Battery-s.cfg.MovingDrain)
		if previousStatus == JobAssigned && job.Status == JobInProgress {
			s.prepareNavigation(robot, job.To)
		}
		if robot.Battery == 0 {
			robot.Status = RobotFaulted
		}
		return
	}
}

func updateArrival(robot *RobotState, job *JobState) bool {
	position := Point{robot.X, robot.Y}
	if job.Status == JobAssigned && position == job.From {
		job.Status = JobInProgress
	}
	if job.Status == JobInProgress && position == job.To {
		job.Status = JobDone
		robot.Status, robot.JobID = RobotIdle, ""
		return true
	}
	return false
}
