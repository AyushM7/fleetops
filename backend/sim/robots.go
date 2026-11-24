package sim

import (
	"cmp"
	"slices"
)

const MaintenanceRobotID = "M1"

// CompareRobotIDs orders canonical robot IDs, with maintenance after transport.
func CompareRobotIDs(a, b string) int {
	if a == b {
		return 0
	}
	if a == MaintenanceRobotID {
		return 1
	}
	if b == MaintenanceRobotID {
		return -1
	}
	if order := cmp.Compare(len(a), len(b)); order != 0 {
		return order
	}
	return cmp.Compare(a, b)
}

func compareRobots(a, b RobotState) int { return CompareRobotIDs(a.ID, b.ID) }

func (s *Sim) stepRobots() {
	slices.SortFunc(s.state.Robots, compareRobots)
	// Pass A finishes for all robots before dispatch or movement begins.
	skip := make([]bool, len(s.state.Robots))
	for i := range s.state.Robots {
		skip[i] = s.prepareRobot(&s.state.Robots[i])
	}
	s.assignPending(skip) // Pass B.
	s.scheduleMaintenance()
	s.seekCharging(skip) // Pass C.
	s.seekParking(skip)
	s.clearIdleAisles(skip) // Pass C2.
	if s.cfg.MaintenanceBay != nil {
		s.traffic = newTrafficReservations(s.state.Robots)
	}
	usedMovement := make([]bool, len(s.state.Robots))
	for _, i := range s.movementOrder() {
		robot := &s.state.Robots[i]
		if skip[i] {
			continue
		}
		if robot.Status == RobotMoving {
			before := Point{X: robot.X, Y: robot.Y}
			pickedUp := s.stepMoving(robot)
			usedMovement[i] = pickedUp || before != (Point{X: robot.X, Y: robot.Y})
		}
		if robot.Status != RobotMoving {
			s.finishNavigation(robot)
		}
	}
	events := s.recoverTraffic(usedMovement)
	if s.traffic != nil {
		s.releaseVacatedStations()
		s.state.TrafficDeadlocks = s.state.TrafficDeadlocks[:0]
		for _, event := range events {
			if len(event.cycle) > 0 {
				s.state.TrafficDeadlocks = append(s.state.TrafficDeadlocks, TrafficDeadlock{event.cycle, event.robot})
			}
		}
		s.traffic = nil
	}
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
	if robot.ActiveFault != "" {
		s.restoreDockPower(robot)
		return true
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
	target := 100
	if s.cfg.MaintenanceBay != nil && s.cfg.AutoCharge {
		target = s.cfg.ChargeRelease
	}
	if robot.Battery < target {
		robot.Battery = min(target, robot.Battery+s.cfg.ChargeGain)
	}
	if robot.Battery < target {
		return
	}
	robot.Status = RobotIdle
	if station := s.occupiedStation(robot.ID); station != nil && s.cfg.MaintenanceBay == nil {
		station.OccupiedBy = ""
	}
}

func (s *Sim) stepIdle(robot *RobotState) {
	if s.cfg.MaintenanceBay != nil && s.cfg.AutoCharge && s.needsCharge(*robot) {
		// Low-power standby preserves the energy needed to reach a dock after traffic clears.
		return
	}
	if s.queuedForCharging(*robot) {
		return
	}
	if s.state.Tick%s.cfg.IdleDrainEvery == 0 {
		robot.Battery = max(0, robot.Battery-1)
	}
	if robot.Battery == 0 {
		s.depleteBattery(robot)
		return
	}
}

// Low-power waiting applies only while an affordable charger is unavailable.
func (s *Sim) queuedForCharging(robot RobotState) bool {
	if !s.cfg.AutoCharge || robot.Status != RobotIdle || robot.JobID != "" || !s.needsCharge(robot) {
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

func (s *Sim) stepMoving(robot *RobotState) (pickedUp bool) {
	if s.stepPassageYield(robot) {
		return false
	}
	if s.maintenanceTravel(*robot) {
		s.stepMaintenanceTravel(robot)
		return false
	}
	if robot.JobID == "" {
		if s.navigation[robot.ID].relocation {
			s.stepRelocation(robot)
		} else {
			s.stepChargeTrip(robot)
		}
		return false
	}
	for i := range s.state.Jobs {
		job := &s.state.Jobs[i]
		if job.ID != robot.JobID || job.AssignedRobot != robot.ID {
			continue
		}
		if job.Status != JobAssigned && job.Status != JobInProgress {
			return false
		}
		if s.cfg.MaintenanceBay != nil && job.Status == JobAssigned && (Point{robot.X, robot.Y}) == job.From {
			job.Status = JobInProgress
			s.prepareNavigation(robot, job.To)
			// Pickup must finish at the physical source before a recovery movement.
			return true
		}
		if s.updateArrival(robot, job) {
			return false
		}
		target := job.From
		if job.Status == JobInProgress {
			target = job.To
		}
		if !s.advanceNavigation(robot, target) {
			return false
		}
		previousStatus := job.Status
		if s.cfg.MaintenanceBay == nil || previousStatus == JobInProgress {
			s.updateArrival(robot, job)
		}
		robot.Battery = max(0, robot.Battery-s.cfg.MovingDrain)
		if previousStatus == JobAssigned && job.Status == JobInProgress {
			s.prepareNavigation(robot, job.To)
		}
		if robot.Battery == 0 {
			s.depleteBattery(robot)
		}
		return false
	}
	return false
}

func (s *Sim) robotActivity(robot RobotState) RobotActivity {
	position := Point{robot.X, robot.Y}
	switch robot.Status {
	case RobotFaulted:
		return ActivityFaulted
	case RobotOffline:
		return ActivityOutOfService
	case RobotCharging:
		return ActivityCharging
	case RobotServicing:
		if s.maintenance.Phase == MaintenanceAtTarget {
			return ActivityAtRepairSite
		}
		return ActivityRepairing
	case RobotMoving:
		if s.maintenanceTravel(robot) {
			return ActivityGoingToRepair
		}
		switch {
		case s.occupiedStation(robot.ID) != nil:
			return ActivityUndocking
		case robot.WaitTicks > 0:
			return ActivityWaitingForTraffic
		case s.reservedStation(robot.ID) != nil:
			return ActivityGoingToCharge
		case s.navigation[robot.ID].relocation:
			if s.isBay(s.navigation[robot.ID].target) {
				return ActivityGoingToPark
			}
			return ActivityRelocating
		}
		for _, job := range s.state.Jobs {
			if job.ID == robot.JobID && job.AssignedRobot == robot.ID {
				switch {
				case job.Status == JobInProgress:
					return ActivityDelivering
				case position == job.From:
					return ActivityPickingUp
				default:
					return ActivityGoingToPickup
				}
			}
		}
	case RobotIdle:
		if s.occupiedStation(robot.ID) != nil {
			return ActivityUndocking
		}
		if s.isBay(position) {
			return ActivityParked
		}
	}
	return ActivityAvailable
}

func (s *Sim) updateArrival(robot *RobotState, job *JobState) bool {
	position := Point{robot.X, robot.Y}
	if job.Status == JobAssigned && position == job.From {
		job.Status = JobInProgress
	}
	if job.Status == JobInProgress && position == job.To {
		job.Status = JobDone
		robot.Status, robot.JobID = RobotIdle, ""
		if s.cfg.MaintenanceBay != nil {
			s.completeJob(job)
		}
		return true
	}
	return false
}
