package sim

import "fmt"

// WarehouseConfig returns the Phase 2 warehouse geometry with autonomous charging.
func WarehouseConfig() Config {
	c := DefaultConfig()
	c.AutoCharge = true
	c.Recovery = true
	c.AisleYMin, c.AisleYMax = 2, 9
	for y := c.AisleYMin; y <= c.AisleYMax; y++ {
		for _, x := range []int{2, 3, 5, 6, 7, 8, 10, 11, 12, 13, 15, 16, 17} {
			c.Walls = append(c.Walls, Point{X: x, Y: y})
		}
	}
	return c
}

func orthogonalNeighbors(p Point) [4]Point {
	return [4]Point{{X: p.X - 1, Y: p.Y}, {X: p.X + 1, Y: p.Y}, {X: p.X, Y: p.Y - 1}, {X: p.X, Y: p.Y + 1}}
}

func connectedCells(cells []Point) map[Point]bool {
	traversable := make(map[Point]bool, len(cells))
	for _, p := range cells {
		traversable[p] = true
	}
	visited := map[Point]bool{cells[0]: true}
	queue := []Point{cells[0]}
	for head := 0; head < len(queue); head++ {
		for _, neighbor := range orthogonalNeighbors(queue[head]) {
			if traversable[neighbor] && !visited[neighbor] {
				visited[neighbor] = true
				queue = append(queue, neighbor)
			}
		}
	}
	return visited
}

func (c Config) validateConnectivity() error {
	cells := c.JobCells()
	visited := connectedCells(cells)
	if len(visited) != len(cells) {
		return fmt.Errorf("%w: V12 eligible job cells must share one traversable component", ErrInvalidConfig)
	}
	for _, station := range c.Stations {
		accessible := false
		for _, neighbor := range orthogonalNeighbors(station) {
			accessible = accessible || visited[neighbor]
		}
		if !accessible {
			return fmt.Errorf("%w: V12 station %v has no access to the traversable component", ErrInvalidConfig, station)
		}
	}
	// V6 restricts starts to job cells or stations, so the checks above also
	// cover every robot start without making stations BFS transit cells.
	return nil
}

func (c Config) validateInitialAisles() error {
	occupied := make(map[int]bool)
	for _, start := range c.RobotStarts {
		if !c.onAisle(start) {
			continue
		}
		if occupied[start.X] {
			return fmt.Errorf("%w: V13 multiple robot starts occupy aisle column %d", ErrInvalidConfig, start.X)
		}
		occupied[start.X] = true
	}
	return nil
}
