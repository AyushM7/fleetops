package sim

func (s *Sim) planServiceCell(target, m1 RobotState) (maintenancePlan, MaintenanceReason) {
	targetCell := Point{target.X, target.Y}
	var best maintenancePlan
	found, feasible := false, false
	for _, point := range orthogonalNeighbors(targetCell) {
		if s.cfg.CellTypeAt(point) != CellFree || s.protected[point] || s.cfg.narrowPassage(point) {
			continue
		}
		route, ok := s.findNavigationRoute(m1, point, true)
		if !ok {
			continue
		}
		exit := s.maintenanceExitSteps(m1, point)
		if exit < 0 {
			continue
		}
		found = true
		if !s.canFundMaintenance(m1, len(route)+exit, s.cfg.MaintenanceRepairTicks) {
			continue
		}
		if !feasible || len(route) < len(best.route) {
			best = maintenancePlan{fault: target.ActiveFault, target: targetCell, service: point, route: route}
			feasible = true
		}
	}
	if feasible {
		return best, ""
	}
	if found {
		return maintenancePlan{}, maintenanceInsufficient
	}
	return maintenancePlan{}, maintenanceUnreachable
}

func (s *Sim) maintenanceAffordable(robot RobotState, target Point, steps, repairTicks int) bool {
	exit := s.maintenanceExitSteps(robot, target)
	return exit >= 0 && s.canFundMaintenance(robot, steps+exit, repairTicks)
}

func (s *Sim) canFundMaintenance(robot RobotState, steps, repairTicks int) bool {
	available := robot.Battery - repairTicks - max(1, s.cfg.ReserveBattery)
	return available >= 0 && steps <= available/s.cfg.MovingDrain
}

func (s *Sim) maintenanceExitSteps(robot RobotState, from Point) int {
	robot.X, robot.Y = from.X, from.Y
	best := s.availableChargerSteps(robot)
	bay := s.parkingBay(robot)
	if route, ok := s.findNavigationRoute(robot, bay, true); ok {
		// Normal bay parking also budgets a future trip out to a charger.
		exit := 0
		if s.cfg.AutoCharge {
			exit = s.relocationExit(robot, bay)
		}
		if steps := len(route) + exit; exit >= 0 && (best < 0 || steps < best) {
			best = steps
		}
	}
	return best
}

func (s *Sim) availableChargerSteps(robot RobotState) int {
	best := -1
	if !s.cfg.AutoCharge {
		return best
	}
	for _, station := range s.state.Stations {
		if !s.stationFreeFor(station, robot.ID) {
			continue
		}
		if route, ok := s.findNavigationRoute(robot, Point{station.X, station.Y}, true); ok && (best < 0 || len(route) < best) {
			best = len(route)
		}
	}
	return best
}

func (s *Sim) prepareMaintenanceReturn(m1 *RobotState) {
	if m1.Status != RobotIdle || s.occupiedStation(m1.ID) != nil {
		return
	}
	bay := *s.cfg.MaintenanceBay
	if !s.cfg.AutoCharge || !s.needsCharge(*m1) {
		if route, ok := s.navigationRoute(*m1, bay, true); ok {
			s.navigation[m1.ID] = robotNavigation{target: bay, route: route, relocation: true}
			m1.Status = RobotMoving
			return
		}
	}
	if s.cfg.AutoCharge {
		if station, route := s.nearestChargingStation(*m1); station != nil {
			s.beginChargeTrip(m1, station, route)
		}
	}
}

func (s *Sim) resumeRepairedRobot(robot *RobotState) bool {
	if robot.Battery <= 0 {
		return false
	}
	if robot.JobID == "" {
		if station := s.occupiedStation(robot.ID); station != nil {
			s.beginCharging(robot, station)
		} else {
			exit := s.maintenanceExitSteps(*robot, Point{robot.X, robot.Y})
			if exit < 0 || !s.canFundMaintenance(*robot, exit, 0) {
				return false
			}
			robot.Status = RobotIdle
		}
	} else {
		if !s.resumeRepairedJob(robot) {
			return false
		}
	}
	robot.ActiveFault = ""
	delete(s.unresponsiveTicks, robot.ID)
	return true
}

func (s *Sim) resumeRepairedJob(robot *RobotState) bool {
	for _, job := range s.state.Jobs {
		if job.ID != robot.JobID || job.AssignedRobot != robot.ID {
			continue
		}
		target, onward := job.To, 0
		projected := *robot
		if job.Status == JobAssigned {
			target = job.From
			projected.X, projected.Y = job.From.X, job.From.Y
			delivery, ok := s.findNavigationRoute(projected, job.To, true)
			if !ok {
				return false
			}
			onward = len(delivery)
		} else if job.Status != JobInProgress {
			return false
		}
		projected.X, projected.Y = job.To.X, job.To.Y
		exit := s.availableChargerSteps(projected)
		if !s.cfg.AutoCharge {
			exit = s.maintenanceExitSteps(projected, job.To)
		}
		route, ok := s.findNavigationRoute(*robot, target, true)
		if !ok || !s.navigationExitAvailable(*robot, route) || exit < 0 || !s.canFundMaintenance(*robot, len(route)+onward+exit, 0) {
			return false
		}
		s.navigation[robot.ID] = robotNavigation{target: target, route: route}
		robot.Status = RobotMoving
		return true
	}
	return false
}
