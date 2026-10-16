// Package routing provides pure, deterministic grid pathfinding.
package routing

// Cell identifies one grid position.
type Cell struct {
	X, Y int
}

// Grid distinguishes valid endpoints from cells that permit transit.
type Grid interface {
	Width() int
	Height() int
	Walkable(c Cell) bool
	TransitOK(c Cell) bool
}

// Options supplies additional dynamic obstacles for one search.
type Options struct {
	Blocked func(Cell) bool
}

func inBounds(c Cell, width, height int) bool {
	return c.X >= 0 && c.X < width && c.Y >= 0 && c.Y < height
}

func manhattan(a, b Cell) int {
	dx, dy := a.X-b.X, a.Y-b.Y
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	return dx + dy
}
