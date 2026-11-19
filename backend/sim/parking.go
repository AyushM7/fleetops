package sim

func (s *Sim) isBay(p Point) bool {
	cell := s.cfg.CellTypeAt(p)
	return cell == CellParking || cell == CellMaintenanceBay
}

func (s *Sim) parkingBay(robot RobotState) Point {
	if robot.Role == RoleMaintenance {
		return *s.cfg.MaintenanceBay
	}
	return s.cfg.RobotStarts[idNumber(robot.ID)-1]
}

func (s *Sim) seekParking(skip []bool) {
	if s.cfg.MaintenanceBay == nil {
		return
	}
	for i := range s.state.Robots {
		robot := &s.state.Robots[i]
		position := Point{robot.X, robot.Y}
		target := s.parkingBay(*robot)
		if robot.Status != RobotIdle || position == target {
			delete(s.idleTicks, robot.ID)
			continue
		}
		if skip[i] {
			continue
		}
		s.idleTicks[robot.ID] = min(s.cfg.ParkingGraceTicks, s.idleTicks[robot.ID]+1)
		docked := s.occupiedStation(robot.ID) != nil
		if !docked && !s.protected[position] && s.idleTicks[robot.ID] < s.cfg.ParkingGraceTicks {
			continue
		}
		if route, reachable := s.navigationRoute(*robot, target, true); reachable {
			s.navigation[robot.ID] = robotNavigation{target: target, route: route, relocation: true}
			robot.Status = RobotMoving
		} else if docked {
			if nav, feasible := s.relocationRoute(*robot); feasible {
				s.navigation[robot.ID], robot.Status = nav, RobotMoving
			}
		}
	}
}
