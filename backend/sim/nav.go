package sim

import "github.com/AyushM7/fleetops/backend/routing"

type robotNavigation struct {
	target     Point
	route      []Point
	relocation bool
}

func (s *Sim) clearNavigation(robot *RobotState) {
	robot.WaitTicks, robot.Blocked = 0, false
	delete(s.navigation, robot.ID)
	if recovery, ok := s.recovery[robot.ID]; ok {
		recovery.hold, recovery.resume = 0, false
		s.recovery[robot.ID] = recovery
	}
}

func (s *Sim) navigationBlocked(robot RobotState, point Point, dynamic bool) bool {
	position := Point{X: robot.X, Y: robot.Y}
	if point == position {
		return false
	}
	for _, other := range s.state.Robots {
		if other.ID == robot.ID || !dynamic && other.Status != RobotOffline && other.Status != RobotFaulted && other.Status != RobotCharging {
			continue
		}
		if point == (Point{X: other.X, Y: other.Y}) {
			return true
		}
		if s.cfg.onAisle(point) && other.X == point.X && s.cfg.onAisle(Point{X: other.X, Y: other.Y}) {
			if !s.cfg.onAisle(position) || position.X != point.X {
				return true
			}
		}
	}
	return false
}

func (s *Sim) planNavigation(robot *RobotState, target Point, dynamic bool) bool {
	points, ok := s.navigationRoute(*robot, target, dynamic)
	if !ok {
		return false
	}
	s.navigation[robot.ID] = robotNavigation{target: target, route: points, relocation: s.navigation[robot.ID].relocation}
	return true
}

func (s *Sim) navigationRoute(robot RobotState, target Point, dynamic bool) ([]Point, bool) {
	route, err := routing.FindPath(routingGrid{cfg: s.cfg}, routing.Cell{X: robot.X, Y: robot.Y}, routing.Cell{X: target.X, Y: target.Y}, routing.Options{
		Blocked: func(cell routing.Cell) bool {
			return s.navigationBlocked(robot, Point{X: cell.X, Y: cell.Y}, dynamic)
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

func (s *Sim) prepareNavigation(robot *RobotState, target Point) {
	if nav, ok := s.navigation[robot.ID]; ok {
		// A rejected target change must retry on the existing replanning clock.
		if nav.target == target || s.cfg.AutoCharge && robot.WaitTicks > 0 {
			return
		}
	}
	if _, exists := s.navigation[robot.ID]; !s.cfg.AutoCharge || !exists {
		s.navigation[robot.ID] = robotNavigation{target: target}
	}
	s.planNavigation(robot, target, false)
}

func (s *Sim) waitForNavigation(robot *RobotState, target Point) {
	robot.WaitTicks++
	robot.Blocked = robot.WaitTicks >= s.cfg.BlockedAfter
	if robot.WaitTicks%s.cfg.ReplanAfter == 0 {
		// Even a successful replan leaves this tick stationary and preserves waiting.
		s.planNavigation(robot, target, true)
	}
}

func (s *Sim) advanceNavigation(robot *RobotState, target Point) bool {
	s.prepareNavigation(robot, target)
	nav := s.navigation[robot.ID]
	if nav.target != target || len(nav.route) == 0 || s.navigationBlocked(*robot, nav.route[0], true) {
		s.waitForNavigation(robot, target)
		return false
	}
	next := nav.route[0]
	robot.X, robot.Y = next.X, next.Y
	robot.WaitTicks, robot.Blocked = 0, false
	nav.route = nav.route[1:]
	s.navigation[robot.ID] = nav
	return true
}
