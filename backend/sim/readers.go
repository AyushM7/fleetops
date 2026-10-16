package sim

import "github.com/AyushM7/fleetops/backend/routing"

type routingGrid struct{ cfg Config }

var _ routing.Grid = routingGrid{}

func (g routingGrid) Width() int  { return g.cfg.Width }
func (g routingGrid) Height() int { return g.cfg.Height }

func (g routingGrid) Walkable(c routing.Cell) bool {
	switch g.cfg.CellTypeAt(Point{X: c.X, Y: c.Y}) {
	case CellFree, CellAisle, CellStation:
		return true
	default:
		return false
	}
}

func (g routingGrid) TransitOK(c routing.Cell) bool {
	cell := g.cfg.CellTypeAt(Point{X: c.X, Y: c.Y})
	return cell == CellFree || cell == CellAisle
}

// PlanRoute is READ: it plans through static geometry, ignoring other robots.
func (s *Sim) PlanRoute(from, to Point) ([]Point, error) {
	route, err := routing.FindPath(routingGrid{cfg: s.cfg}, routing.Cell{X: from.X, Y: from.Y}, routing.Cell{X: to.X, Y: to.Y}, routing.Options{})
	if err != nil {
		return nil, err
	}
	points := make([]Point, len(route))
	for i, cell := range route {
		points[i] = Point{X: cell.X, Y: cell.Y}
	}
	return points, nil
}

// AisleOccupant is READ: it returns the lowest numeric robot ID in an aisle.
func (s *Sim) AisleOccupant(aisleID int) (string, error) {
	column, ok := s.cfg.AisleColumn(aisleID)
	if !ok {
		return "", ErrUnknownAisle
	}
	return s.occupantAtColumn(column), nil
}

func (s *Sim) occupantAtColumn(column int) string {
	occupant := ""
	for _, robot := range s.state.Robots {
		if robot.X == column && robot.Y >= s.cfg.AisleYMin && robot.Y <= s.cfg.AisleYMax && (occupant == "" || idNumber(robot.ID) < idNumber(occupant)) {
			occupant = robot.ID
		}
	}
	return occupant
}

func (s *Sim) snapshotAisles() []AisleState {
	aisles := make([]AisleState, len(s.cfg.AisleColumns))
	for id, column := range s.cfg.AisleColumns {
		aisles[id] = AisleState{ID: id, OccupiedBy: s.occupantAtColumn(column)}
	}
	return aisles
}

// OccupiedAislesOnRoute is READ: it lists route aisles held by other robots.
func (s *Sim) OccupiedAislesOnRoute(route []Point, forRobot string) ([]int, error) {
	known := false
	for _, robot := range s.state.Robots {
		known = known || robot.ID == forRobot
	}
	if !known {
		return nil, ErrUnknownRobot
	}
	ids := make([]int, 0)
	for id, column := range s.cfg.AisleColumns {
		occupant := s.occupantAtColumn(column)
		if occupant == "" || occupant == forRobot {
			continue
		}
		for _, point := range route {
			if point.X == column && point.Y >= s.cfg.AisleYMin && point.Y <= s.cfg.AisleYMax {
				ids = append(ids, id)
				break
			}
		}
	}
	return ids, nil
}
