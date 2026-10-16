package routing

import "errors"

var (
	ErrOutOfBounds = errors.New("routing: endpoint outside grid")
	ErrNotWalkable = errors.New("routing: endpoint not walkable")
	ErrGoalBlocked = errors.New("routing: goal blocked")
	ErrNoPath      = errors.New("routing: no path")
)
