package sim

import (
	"slices"

	"github.com/AyushM7/fleetops/backend/routing"
)

func protectedCells(cfg Config) map[Point]bool {
	protected := make(map[Point]bool)
	for _, x := range cfg.AisleColumns {
		for y := cfg.AisleYMin; y <= cfg.AisleYMax; y++ {
			protected[Point{X: x, Y: y}] = true
		}
	}
	for _, station := range cfg.Stations {
		protected[station] = true
		for _, neighbor := range orthogonalNeighbors(station) {
			cell := cfg.CellTypeAt(neighbor)
			if cell != CellWall && cell != CellOutOfBounds {
				protected[neighbor] = true
			}
		}
	}
	return protected
}

func (s *Sim) tripDestinations() map[Point]string {
	claimed := make(map[Point]string)
	for _, robot := range s.state.Robots {
		if robot.Status != RobotMoving {
			continue
		}
		// Do not park an evacuee on a known movement corridor, even after a hold.
		if nav, ok := s.navigation[robot.ID]; ok && nav.target == s.recoveryTarget(robot) {
			for _, cell := range nav.route {
				claimed[cell] = robot.ID
			}
		}
		if robot.JobID == "" {
			if nav, ok := s.navigation[robot.ID]; ok {
				claimed[nav.target] = robot.ID
			}
			continue
		}
		for _, job := range s.state.Jobs {
			if job.ID != robot.JobID || job.AssignedRobot != robot.ID {
				continue
			}
			if job.Status == JobAssigned {
				claimed[job.From] = robot.ID
				claimed[job.To] = robot.ID
			} else if job.Status == JobInProgress {
				claimed[job.To] = robot.ID
			}
		}
	}
	return claimed
}

func (s *Sim) clearIdleAisles(skip []bool) {
	if !s.cfg.Recovery {
		return
	}
	claimed := s.tripDestinations()
	cleared := make(map[string]bool)
	order := make([]int, len(s.state.Robots))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(a, b int) int { return idNumber(s.state.Robots[a].ID) - idNumber(s.state.Robots[b].ID) })
	for _, i := range order {
		robot := &s.state.Robots[i]
		if skip[i] || robot.Status != RobotIdle || robot.JobID != "" {
			continue
		}
		if !s.protected[Point{X: robot.X, Y: robot.Y}] {
			owner, witness, obstructs := s.idleObstructionForPass(*robot, claimed, skip, cleared)
			if !obstructs {
				continue
			}
			for _, cell := range witness {
				claimed[cell] = owner.ID
			}
		}
		nav, ok := s.claimedRelocationRoute(*robot, claimed)
		if ok {
			s.clearNavigation(robot)
			s.navigation[robot.ID] = nav
			robot.Status = RobotMoving
			claimed[nav.target] = robot.ID
			cleared[robot.ID] = true
		}
	}
}

func (s *Sim) clearingMission(robot RobotState) (Point, bool) {
	if robot.Status != RobotMoving {
		return Point{}, false
	}
	for _, job := range s.state.Jobs {
		if robot.JobID == job.ID && job.AssignedRobot == robot.ID {
			if job.Status == JobAssigned {
				return job.From, true
			}
			if job.Status == JobInProgress {
				return job.To, true
			}
		}
	}
	if robot.JobID == "" && !s.navigation[robot.ID].relocation {
		if station := s.reservedStation(robot.ID); station != nil {
			return Point{X: station.X, Y: station.Y}, true
		}
	}
	return Point{}, false
}

func (s *Sim) idleObstruction(blocker RobotState) (RobotState, []Point, bool) {
	return s.idleObstructionForPass(blocker, s.tripDestinations(), nil, nil)
}

func (s *Sim) idleObstructionForPass(blocker RobotState, claimed map[Point]string, skip []bool, cleared map[string]bool) (RobotState, []Point, bool) {
	position := Point{X: blocker.X, Y: blocker.Y}
	for _, i := range s.recoveryOrder() {
		robot := s.state.Robots[i]
		target, active := s.clearingMission(robot)
		if !active {
			continue
		}
		nav := s.navigation[robot.ID]
		if position == target || nav.target == target && len(nav.route) > 0 && nav.route[0] == position {
			return robot, nil, true
		}
		for _, job := range s.state.Jobs {
			if job.ID == robot.JobID && job.AssignedRobot == robot.ID && job.Status == JobAssigned && position == job.To {
				return robot, nil, true
			}
		}
		if !s.clearingNeedsReplan(robot, target) {
			continue
		}
		if _, feasible := s.clearingAnalysisRoute(robot, target, nil); feasible {
			continue
		}
		if s.directBlockerCanClear(i, claimed, skip, cleared) {
			continue
		}
		if witness, feasible := s.clearingAnalysisRoute(robot, target, &blocker); feasible {
			return robot, witness, true
		}
	}
	return RobotState{}, nil, false
}

func (s *Sim) directBlockerCanClear(owner int, claimed map[Point]string, skip []bool, cleared map[string]bool) bool {
	next, required := s.requiredCell(owner)
	if !required {
		return false
	}
	for i, robot := range s.state.Robots {
		if i == owner || next != (Point{X: robot.X, Y: robot.Y}) {
			continue
		}
		// An accepted clearance still occupies its starting cell until Pass D.
		// Defer only this mission's indirect analysis; never assume a free cell.
		if cleared[robot.ID] {
			return true
		}
		if robot.Status != RobotIdle || robot.JobID != "" || len(skip) > i && skip[i] {
			return false
		}
		_, feasible := s.claimedRelocationRoute(robot, claimed)
		return feasible
	}
	return false
}

func (s *Sim) clearingNeedsReplan(robot RobotState, target Point) bool {
	if recovery := s.recovery[robot.ID]; recovery.resume {
		return recovery.hold == 0
	}
	nav, exists := s.navigation[robot.ID]
	return !exists || nav.target != target || len(nav.route) == 0 || s.navigationBlocked(robot, nav.route[0], true)
}

func (s *Sim) clearingAnalysisRoute(robot RobotState, target Point, ignored *RobotState) ([]Point, bool) {
	route, err := routing.FindPath(routingGrid{cfg: s.cfg}, routing.Cell{X: robot.X, Y: robot.Y}, routing.Cell{X: target.X, Y: target.Y}, routing.Options{
		Blocked: func(cell routing.Cell) bool {
			point := Point{X: cell.X, Y: cell.Y}
			// Only outside-P idle candidates are removed: none can own an aisle.
			return (ignored == nil || point != (Point{X: ignored.X, Y: ignored.Y})) && s.navigationBlocked(robot, point, true)
		},
	})
	if err != nil || !s.routeAffordable(robot, target, len(route)) {
		return nil, false
	}
	points := make([]Point, len(route))
	for i, cell := range route {
		points[i] = Point{X: cell.X, Y: cell.Y}
	}
	return points, true
}

func (s *Sim) relocationRoute(robot RobotState) (robotNavigation, bool) {
	return s.claimedRelocationRoute(robot, s.tripDestinations())
}

func (s *Sim) claimedRelocationRoute(robot RobotState, claimed map[Point]string) (robotNavigation, bool) {
	best := robotNavigation{relocation: true}
	found := false
	// Row-major candidates preserve Y/X ties without depending on map order.
	for y := 0; y < s.cfg.Height; y++ {
		for x := 0; x < s.cfg.Width; x++ {
			target := Point{X: x, Y: y}
			if target == (Point{X: robot.X, Y: robot.Y}) || s.cfg.CellTypeAt(target) != CellFree || s.protected[target] || claimed[target] != "" && claimed[target] != robot.ID || s.navigationBlocked(robot, target, true) {
				continue
			}
			route, err := routing.FindPath(routingGrid{cfg: s.cfg}, routing.Cell{X: robot.X, Y: robot.Y}, routing.Cell{X: x, Y: y}, routing.Options{
				Blocked: func(c routing.Cell) bool { return s.navigationBlocked(robot, Point{X: c.X, Y: c.Y}, true) },
			})
			if err != nil || found && len(route) >= len(best.route) {
				continue
			}
			exit := s.relocationExit(robot, target)
			if exit < 0 || robot.Battery-(len(route)+exit)*s.cfg.MovingDrain < 1 {
				continue
			}
			best.target, best.route = target, make([]Point, len(route))
			for i, cell := range route {
				best.route[i] = Point{X: cell.X, Y: cell.Y}
			}
			found = true
		}
	}
	return best, found
}

func (s *Sim) relocationExit(robot RobotState, from Point) int {
	robot.X, robot.Y = from.X, from.Y
	best := -1
	for _, station := range s.cfg.Stations {
		route, err := routing.FindPath(routingGrid{cfg: s.cfg}, routing.Cell{X: from.X, Y: from.Y}, routing.Cell{X: station.X, Y: station.Y}, routing.Options{
			Blocked: func(c routing.Cell) bool {
				point := Point{X: c.X, Y: c.Y}
				return point != station && s.navigationBlocked(robot, point, false)
			},
		})
		if err == nil && (best < 0 || len(route) < best) {
			best = len(route)
		}
	}
	return best
}

func (s *Sim) stepRelocation(robot *RobotState) {
	target := s.navigation[robot.ID].target
	if (Point{X: robot.X, Y: robot.Y}) != target {
		if !s.advanceNavigation(robot, target) {
			return
		}
		robot.Battery = max(0, robot.Battery-s.cfg.MovingDrain)
	}
	if robot.Battery == 0 {
		robot.Status = RobotFaulted
	} else if (Point{X: robot.X, Y: robot.Y}) == target {
		robot.Status = RobotIdle
	}
}
