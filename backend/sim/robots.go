package sim

import (
	"cmp"
	"slices"
)

func (s *Sim) stepRobots() {
	slices.SortFunc(s.state.Robots, func(a, b RobotState) int {
		return cmp.Compare(idNumber(a.ID), idNumber(b.ID))
	})
	for i := range s.state.Robots {
		robot := &s.state.Robots[i]
		if s.unresponsiveTicks[robot.ID] > 0 {
			s.unresponsiveTicks[robot.ID]--
			if s.unresponsiveTicks[robot.ID] == 0 {
				robot.Status = RobotOffline
				continue
			}
		}
		if robot.Status == RobotCharging {
			s.stepCharging(robot)
			continue
		}
		if robot.Status == RobotIdle {
			s.stepIdle(robot)
		}
		if robot.Status == RobotMoving {
			s.stepMoving(robot)
		}
	}
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
	if s.state.Tick%s.cfg.IdleDrainEvery == 0 {
		robot.Battery = max(0, robot.Battery-1)
	}
	if robot.Battery == 0 {
		robot.Status = RobotFaulted
		return
	}
	if !s.cfg.AutoDispatch || robot.Battery < s.cfg.MinAssignBattery || robot.JobID != "" {
		return
	}
	if job := lowestPendingJob(s.state.Jobs); job != nil {
		job.Status, job.AssignedRobot = JobAssigned, robot.ID
		robot.Status, robot.JobID = RobotMoving, job.ID
	}
}

func (s *Sim) stepMoving(robot *RobotState) {
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
		position := Point{robot.X, robot.Y}
		next := stepToward(position, target)
		if next == position {
			return
		}
		robot.X, robot.Y = next.X, next.Y
		updateArrival(robot, job)
		robot.Battery = max(0, robot.Battery-s.cfg.MovingDrain)
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

func stepToward(position, target Point) Point {
	switch {
	case position.X < target.X:
		position.X++
	case position.X > target.X:
		position.X--
	case position.Y < target.Y:
		position.Y++
	case position.Y > target.Y:
		position.Y--
	}
	return position
}
