package routing

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"reflect"
	"slices"
	"testing"
)

func TestFindPathMatchesBFS(t *testing.T) {
	rng := rand.New(rand.NewSource(202602))
	reachable, unreachable, blockedGoals := 0, 0, 0
	for trial := 0; trial < 500; trial++ {
		t.Run(fmt.Sprintf("grid_%03d", trial), func(t *testing.T) {
			g, start, goal, blocked := randomScenario(rng, trial)
			before := testGrid{rows: slices.Clone(g.rows)}
			beforeBlocked := maps.Clone(blocked)
			opt := Options{Blocked: func(c Cell) bool { return blocked[c] }}
			distance, wantErr := bfsDistance(g, start, goal, blocked)
			got, err := FindPath(g, start, goal, opt)
			if !errors.Is(err, wantErr) {
				t.Fatalf("A* error %v, BFS error %v; grid %v, start %v, goal %v, blocked %v", err, wantErr, g.rows, start, goal, blocked)
			}
			if err == nil {
				reachable++
				if len(got) != distance {
					t.Fatalf("A* length %d, BFS distance %d; grid %v, start %v, goal %v", len(got), distance, g.rows, start, goal)
				}
				assertValidRoute(t, g, start, goal, opt, got)
			} else {
				if got != nil {
					t.Fatalf("failed search returned route %v", got)
				}
				if errors.Is(err, ErrNoPath) {
					unreachable++
				} else if errors.Is(err, ErrGoalBlocked) {
					blockedGoals++
				}
			}
			if !reflect.DeepEqual(g, before) || !maps.Equal(blocked, beforeBlocked) {
				t.Fatal("search mutated caller-owned grid or obstacles")
			}
		})
	}
	if reachable == 0 || unreachable == 0 || blockedGoals == 0 {
		t.Fatalf("insufficient scenarios: reachable=%d unreachable=%d blockedGoals=%d", reachable, unreachable, blockedGoals)
	}
	t.Logf("500 seeded grids: %d reachable with BFS-equal lengths, %d unreachable, %d blocked goals", reachable, unreachable, blockedGoals)
}

func randomScenario(rng *rand.Rand, trial int) (testGrid, Cell, Cell, map[Cell]bool) {
	width, height := 3+rng.Intn(10), 3+rng.Intn(10)
	start := Cell{X: rng.Intn(width), Y: rng.Intn(height)}
	goal := Cell{X: rng.Intn(width), Y: rng.Intn(height)}
	if trial%10 == 0 {
		start, goal = Cell{}, Cell{X: width - 1, Y: height - 1}
	}
	blocked := make(map[Cell]bool)
	rows := make([]string, height)
	for y := 0; y < height; y++ {
		row := make([]byte, width)
		for x := 0; x < width; x++ {
			cell := Cell{X: x, Y: y}
			row[x] = '.'
			roll := rng.Intn(100)
			if roll < 30 {
				row[x] = '#'
			} else if roll < 42 {
				row[x] = 'S'
			}
			if rng.Intn(100) < 15 {
				blocked[cell] = true
			}
			if trial%10 == 1 {
				row[x] = '.'
				delete(blocked, cell)
			}
			if trial%10 == 0 && y == 1 {
				row[x] = '#'
			}
			if cell == start || cell == goal {
				row[x] = '.'
			}
		}
		rows[y] = string(row)
	}
	return testGrid{rows: rows}, start, goal, blocked
}

// The reference uses FIFO traversal and map distances, independent of A*'s
// heuristic, heap, indexing, neighbor order, and parent reconstruction.
func bfsDistance(g testGrid, start, goal Cell, blocked map[Cell]bool) (int, error) {
	if start == goal {
		return 0, nil
	}
	if blocked[goal] {
		return -1, ErrGoalBlocked
	}
	distances := map[Cell]int{start: 0}
	queue := []Cell{start}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, delta := range []Cell{{Y: -1}, {Y: 1}, {X: -1}, {X: 1}} {
			next := Cell{X: current.X + delta.X, Y: current.Y + delta.Y}
			if _, seen := distances[next]; seen || !g.Walkable(next) || blocked[next] {
				continue
			}
			if next == goal {
				return distances[current] + 1, nil
			}
			if !g.TransitOK(next) {
				continue
			}
			distances[next] = distances[current] + 1
			queue = append(queue, next)
		}
	}
	return -1, ErrNoPath
}
