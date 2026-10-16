package routing

import "container/heap"

type candidate struct {
	index, cost, heuristic, sequence int
}

type frontier []candidate

func (q frontier) Len() int { return len(q) }

func (q frontier) Less(i, j int) bool {
	a, b := q[i], q[j]
	if a.cost+a.heuristic != b.cost+b.heuristic {
		return a.cost+a.heuristic < b.cost+b.heuristic
	}
	if a.heuristic != b.heuristic {
		return a.heuristic < b.heuristic
	}
	// Row-major indices compare y first, then x.
	if a.index != b.index {
		return a.index < b.index
	}
	return a.sequence < b.sequence
}

func (q frontier) Swap(i, j int) { q[i], q[j] = q[j], q[i] }

func (q *frontier) Push(value any) { *q = append(*q, value.(candidate)) }

func (q *frontier) Pop() any {
	last := len(*q) - 1
	value := (*q)[last]
	*q = (*q)[:last]
	return value
}

// FindPath returns a shortest route after start through goal, without changing g.
func FindPath(g Grid, start, goal Cell, opt Options) ([]Cell, error) {
	width, height := g.Width(), g.Height()
	if err := validateEndpoints(g, start, goal, opt, width, height); err != nil {
		return nil, err
	}
	if start == goal {
		return []Cell{}, nil
	}
	scores, parents := make([]int, width*height), make([]int, width*height)
	for i := range scores {
		scores[i], parents[i] = -1, -1
	}
	startIndex, goalIndex := start.Y*width+start.X, goal.Y*width+goal.X
	scores[startIndex] = 0
	open := &frontier{{index: startIndex, heuristic: manhattan(start, goal)}}
	closed := make([]bool, len(scores))
	sequence := 0
	for expanded := 0; open.Len() > 0 && expanded < len(scores); {
		current := heap.Pop(open).(candidate)
		if closed[current.index] || current.cost != scores[current.index] {
			continue
		}
		closed[current.index] = true
		expanded++
		if current.index == goalIndex {
			return reconstruct(parents, startIndex, goalIndex, width), nil
		}
		cell := Cell{X: current.index % width, Y: current.index / width}
		neighbors := [4]Cell{{X: cell.X + 1, Y: cell.Y}, {X: cell.X - 1, Y: cell.Y}, {X: cell.X, Y: cell.Y + 1}, {X: cell.X, Y: cell.Y - 1}}
		for _, next := range neighbors {
			if !inBounds(next, width, height) {
				continue
			}
			index, cost := next.Y*width+next.X, current.cost+1
			if closed[index] || scores[index] >= 0 && cost >= scores[index] {
				continue
			}
			if !canEnter(g, next, goal, opt) {
				continue
			}
			scores[index], parents[index] = cost, current.index
			sequence++
			heap.Push(open, candidate{index: index, cost: cost, heuristic: manhattan(next, goal), sequence: sequence})
		}
	}
	return nil, ErrNoPath
}

func validateEndpoints(g Grid, start, goal Cell, opt Options, width, height int) error {
	if !inBounds(start, width, height) || !inBounds(goal, width, height) {
		return ErrOutOfBounds
	}
	if !g.Walkable(start) || !g.Walkable(goal) {
		return ErrNotWalkable
	}
	if start != goal && opt.Blocked != nil && opt.Blocked(goal) {
		return ErrGoalBlocked
	}
	return nil
}

func canEnter(g Grid, cell, goal Cell, opt Options) bool {
	return g.Walkable(cell) && (cell == goal || g.TransitOK(cell)) && (opt.Blocked == nil || !opt.Blocked(cell))
}

func reconstruct(parents []int, start, goal, width int) []Cell {
	var reversed []Cell
	for index := goal; index != start; index = parents[index] {
		reversed = append(reversed, Cell{X: index % width, Y: index / width})
	}
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	return reversed
}
