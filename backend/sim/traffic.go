package sim

import (
	"cmp"
	"slices"
)

type trafficEdge struct{ from, to Point }

type trafficReservations struct {
	cells map[Point]string
	edges map[string]trafficEdge
}

func newTrafficReservations(robots []RobotState) *trafficReservations {
	r := &trafficReservations{cells: make(map[Point]string, len(robots)), edges: make(map[string]trafficEdge, len(robots))}
	for _, robot := range robots {
		r.cells[Point{robot.X, robot.Y}] = robot.ID
	}
	return r
}

func (s *Sim) reserveMove(robot RobotState, to Point) bool {
	r := s.traffic
	if r == nil {
		return true
	}
	from := Point{robot.X, robot.Y}
	dx, dy := from.X-to.X, from.Y-to.Y
	_, alreadyMoved := r.edges[robot.ID]
	if dx*dx+dy*dy != 1 || alreadyMoved || r.cells[from] != robot.ID || r.cells[to] != "" || s.exitClaim(robot, to) >= 0 {
		return false
	}
	for _, edge := range r.edges {
		if edge.from == to && edge.to == from {
			return false
		}
	}
	delete(r.cells, from)
	r.cells[to] = robot.ID
	r.edges[robot.ID] = trafficEdge{from, to}
	return true
}

func (s *Sim) crossingBlocker(mover RobotState, route []Point) int {
	if s.cfg.MaintenanceBay == nil || len(route) == 0 {
		return -1
	}
	if owner := s.exitClaim(mover, route[0]); owner != -1 || len(route) < 2 {
		return owner
	}
	first, second := route[0], route[1]
	position := Point{mover.X, mover.Y}
	if s.cfg.onAisle(first) && (!s.cfg.onAisle(position) || first.X != position.X) {
		if exit, ok := s.passageExit(first, route[1:]); ok {
			return s.exitBlocker(mover, exit)
		}
		return -1
	}
	if first.X == second.X && !s.cfg.onAisle(first) && s.cfg.onAisle(second) && !s.cfg.onAisle(position) {
		for i, robot := range s.state.Robots {
			if robot.X == second.X && s.cfg.onAisle(Point{robot.X, robot.Y}) {
				return i
			}
		}
	}
	if !s.cfg.narrowPassage(first) {
		return -1
	}
	// Wait outside a one-cell pinch until there is room to leave it.
	return s.exitBlocker(mover, second)
}

func (s *Sim) exitClaim(mover RobotState, target Point) int {
	owner := -1
	for i, robot := range s.state.Robots {
		if robot.Status != RobotMoving {
			continue
		}
		route := s.navigation[robot.ID].route
		exit, ok := s.passageExit(Point{robot.X, robot.Y}, route)
		if !ok || exit != target {
			continue
		}
		if owner == -1 || robot.WaitTicks > s.state.Robots[owner].WaitTicks {
			owner = i
		}
	}
	if owner >= 0 && s.state.Robots[owner].ID == mover.ID {
		return -1
	}
	return owner
}

func (s *Sim) exitBlocker(mover RobotState, exit Point) int {
	if occupant := s.robotIndexAt(exit); occupant >= 0 && s.state.Robots[occupant].ID != mover.ID {
		return occupant
	}
	return s.exitClaim(mover, exit)
}

func (s *Sim) passageExit(position Point, route []Point) (Point, bool) {
	if s.cfg.onAisle(position) {
		for _, point := range route {
			if !s.cfg.onAisle(point) || point.X != position.X {
				return point, true
			}
		}
	} else if len(route) > 0 && s.cfg.narrowPassage(position) {
		return route[0], true
	}
	return Point{}, false
}

func (c Config) narrowPassage(p Point) bool {
	if c.CellTypeAt(p) != CellFree {
		return false
	}
	neighbors := orthogonalNeighbors(p)
	return c.CellTypeAt(neighbors[0]) == CellWall && c.CellTypeAt(neighbors[1]) == CellWall ||
		c.CellTypeAt(neighbors[2]) == CellWall && c.CellTypeAt(neighbors[3]) == CellWall
}

func (s *Sim) robotIndexAt(position Point) int {
	for i, robot := range s.state.Robots {
		if (Point{robot.X, robot.Y}) == position {
			return i
		}
	}
	return -1
}

func (s *Sim) nextStepBlocked(robot RobotState, route []Point) bool {
	return s.navigationBlocked(robot, route[0], true) || s.crossingBlocker(robot, route) >= 0
}

func (s *Sim) movementOrder() []int {
	order := make([]int, len(s.state.Robots))
	for i := range order {
		order[i] = i
	}
	if s.traffic == nil || len(order) == 0 {
		return order
	}
	first := (s.state.Tick + len(order) - 1) % len(order)
	slices.SortFunc(order, func(a, b int) int {
		if waitOrder := cmp.Compare(s.state.Robots[b].WaitTicks, s.state.Robots[a].WaitTicks); waitOrder != 0 {
			return waitOrder
		}
		return cmp.Compare((a+len(order)-first)%len(order), (b+len(order)-first)%len(order))
	})
	return order
}
