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
	if dx*dx+dy*dy != 1 || alreadyMoved || r.cells[from] != robot.ID || r.cells[to] != "" {
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
	if s.cfg.MaintenanceBay == nil || len(route) < 2 {
		return -1
	}
	first, second := route[0], route[1]
	position := Point{mover.X, mover.Y}
	if s.cfg.onAisle(first) && (!s.cfg.onAisle(position) || first.X != position.X) {
		// Enter an aisle only if its planned exit is free.
		for _, point := range route[1:] {
			if !s.cfg.onAisle(point) || point.X != first.X {
				return s.robotIndexAt(point)
			}
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
	if s.cfg.CellTypeAt(first) != CellFree {
		return -1
	}
	x, y := route[0].X, route[0].Y
	if x == route[1].X {
		if s.cfg.CellTypeAt(Point{x - 1, y}) != CellWall || s.cfg.CellTypeAt(Point{x + 1, y}) != CellWall {
			return -1
		}
	} else if s.cfg.CellTypeAt(Point{x, y - 1}) != CellWall || s.cfg.CellTypeAt(Point{x, y + 1}) != CellWall {
		return -1
	}
	// Wait outside a one-cell pinch until there is room to leave it.
	return s.robotIndexAt(second)
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
