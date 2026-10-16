package routing

import (
	"container/heap"
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"
)

type testGrid struct{ rows []string }

func (g testGrid) Width() int  { return len(g.rows[0]) }
func (g testGrid) Height() int { return len(g.rows) }

func (g testGrid) Walkable(c Cell) bool {
	return c.X >= 0 && c.Y >= 0 && c.X < g.Width() && c.Y < g.Height() && g.rows[c.Y][c.X] != '#'
}

func (g testGrid) TransitOK(c Cell) bool {
	return g.Walkable(c) && g.rows[c.Y][c.X] != 'S'
}

func TestFindPathRoutes(t *testing.T) {
	for _, tc := range []struct {
		name        string
		rows        []string
		start, goal Cell
		blocked     map[Cell]bool
		want        []Cell
	}{
		{name: "horizontal", rows: []string{"...."}, goal: Cell{X: 3}, want: []Cell{{X: 1}, {X: 2}, {X: 3}}},
		{name: "vertical", rows: []string{".", ".", "."}, goal: Cell{Y: 2}, want: []Cell{{Y: 1}, {Y: 2}}},
		{name: "wall detour", rows: []string{".#.", "..."}, goal: Cell{X: 2}, want: []Cell{{Y: 1}, {X: 1, Y: 1}, {X: 2, Y: 1}, {X: 2}}},
		{name: "station destination", rows: []string{"..S"}, goal: Cell{X: 2}, want: []Cell{{X: 1}, {X: 2}}},
		{name: "station detour", rows: []string{".S.", "..."}, goal: Cell{X: 2}, want: []Cell{{Y: 1}, {X: 1, Y: 1}, {X: 2, Y: 1}, {X: 2}}},
		{name: "dynamic detour", rows: []string{"...", "..."}, goal: Cell{X: 2}, blocked: map[Cell]bool{{X: 1}: true}, want: []Cell{{Y: 1}, {X: 1, Y: 1}, {X: 2, Y: 1}, {X: 2}}},
		{name: "blocked station start", rows: []string{"S.."}, goal: Cell{X: 2}, blocked: map[Cell]bool{{}: true}, want: []Cell{{X: 1}, {X: 2}}},
		{name: "start equals goal", rows: []string{"."}, want: []Cell{}},
		{name: "blocked equal station endpoints", rows: []string{"S"}, blocked: map[Cell]bool{{}: true}, want: []Cell{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := testGrid{rows: tc.rows}
			opt := Options{}
			if tc.blocked != nil {
				opt.Blocked = func(c Cell) bool { return tc.blocked[c] }
			}
			got, err := FindPath(g, tc.start, tc.goal, opt)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("FindPath = %v, %v; want %v, nil", got, err, tc.want)
			}
			assertValidRoute(t, g, tc.start, tc.goal, opt, got)
		})
	}
}

func TestFindPathValidation(t *testing.T) {
	for _, tc := range []struct {
		name        string
		start, goal Cell
		blocked     bool
		want        error
	}{
		{name: "negative start x", start: Cell{X: -1}, want: ErrOutOfBounds},
		{name: "negative start y", start: Cell{Y: -1}, want: ErrOutOfBounds},
		{name: "start at width", start: Cell{X: 3}, want: ErrOutOfBounds},
		{name: "start at height", start: Cell{Y: 2}, want: ErrOutOfBounds},
		{name: "negative goal x", goal: Cell{X: -1}, want: ErrOutOfBounds},
		{name: "negative goal y", goal: Cell{Y: -1}, want: ErrOutOfBounds},
		{name: "goal at width", goal: Cell{X: 3}, want: ErrOutOfBounds},
		{name: "goal at height", goal: Cell{Y: 2}, want: ErrOutOfBounds},
		{name: "bounds before walkability", start: Cell{X: 1}, goal: Cell{X: 3}, blocked: true, want: ErrOutOfBounds},
		{name: "wall start", start: Cell{X: 1}, goal: Cell{X: 2}, blocked: true, want: ErrNotWalkable},
		{name: "wall goal", goal: Cell{X: 1}, blocked: true, want: ErrNotWalkable},
		{name: "equal wall endpoints", start: Cell{X: 1}, goal: Cell{X: 1}, want: ErrNotWalkable},
		{name: "equal outside endpoints", start: Cell{Y: -1}, goal: Cell{Y: -1}, want: ErrOutOfBounds},
		{name: "blocked goal", goal: Cell{X: 2}, blocked: true, want: ErrGoalBlocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opt := Options{Blocked: func(c Cell) bool {
				if tc.want != ErrGoalBlocked {
					t.Fatal("Blocked called before endpoint validation")
				}
				return tc.blocked
			}}
			got, err := FindPath(testGrid{rows: []string{".#.", "..."}}, tc.start, tc.goal, opt)
			if !errors.Is(err, tc.want) || got != nil {
				t.Fatalf("FindPath = %v, %v; want nil, %v", got, err, tc.want)
			}
		})
	}
}

func TestFindPathUnreachable(t *testing.T) {
	for _, rows := range [][]string{{".#."}, {".S."}, {"..."}} {
		opt := Options{}
		if rows[0] == "..." {
			opt.Blocked = func(c Cell) bool { return c.X == 1 }
		}
		got, err := FindPath(testGrid{rows: rows}, Cell{}, Cell{X: 2}, opt)
		if !errors.Is(err, ErrNoPath) || got != nil {
			t.Fatalf("grid %v: FindPath = %v, %v; want nil, ErrNoPath", rows, got, err)
		}
	}
}

func TestFindPathDeterministicTies(t *testing.T) {
	for _, tc := range []struct {
		name        string
		rows        []string
		start, goal Cell
		want        []Cell
	}{
		{name: "open forward", rows: []string{"...", "...", "..."}, goal: Cell{X: 2, Y: 2}, want: []Cell{{X: 1}, {X: 2}, {X: 2, Y: 1}, {X: 2, Y: 2}}},
		{name: "open reverse", rows: []string{"...", "...", "..."}, start: Cell{X: 2, Y: 2}, want: []Cell{{X: 2, Y: 1}, {X: 2}, {X: 1}, {}}},
		{name: "U obstacle", rows: []string{".......", "..#.#..", "..#.#..", "..###..", "......."}, start: Cell{X: 3, Y: 2}, goal: Cell{X: 3, Y: 4}, want: []Cell{{X: 3, Y: 1}, {X: 3}, {X: 2}, {X: 1}, {X: 1, Y: 1}, {X: 1, Y: 2}, {X: 1, Y: 3}, {X: 1, Y: 4}, {X: 2, Y: 4}, {X: 3, Y: 4}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := testGrid{rows: tc.rows}
			for repeat := 0; repeat < 100; repeat++ {
				got, err := FindPath(g, tc.start, tc.goal, Options{})
				if err != nil || !slices.Equal(got, tc.want) {
					t.Fatalf("call %d: route %v, error %v; want %v", repeat, got, err, tc.want)
				}
				assertValidRoute(t, g, tc.start, tc.goal, Options{}, got)
			}
		})
	}
}

func TestFindPathInputAndOutputOwnership(t *testing.T) {
	g := testGrid{rows: []string{"...#", "..S.", "...."}}
	before := testGrid{rows: slices.Clone(g.rows)}
	blocked := map[Cell]bool{{X: 1}: true}
	wantBlocked := maps.Clone(blocked)
	start, goal := Cell{}, Cell{X: 3, Y: 2}
	opt := Options{Blocked: func(c Cell) bool { return blocked[c] }}
	first, err := FindPath(g, start, goal, opt)
	if err != nil {
		t.Fatal(err)
	}
	want := slices.Clone(first)
	first[0] = Cell{X: -1, Y: -1}
	second, err := FindPath(g, start, goal, opt)
	if err != nil || !slices.Equal(second, want) {
		t.Fatalf("route storage leaked between calls: %v, %v", second, err)
	}
	if !reflect.DeepEqual(g, before) || !maps.Equal(blocked, wantBlocked) || start != (Cell{}) || goal != (Cell{X: 3, Y: 2}) {
		t.Fatal("caller inputs changed")
	}
}

func TestFindPathDoesNotCheckStartBlockage(t *testing.T) {
	start, goal := Cell{}, Cell{X: 2}
	opt := Options{Blocked: func(c Cell) bool {
		if c == start {
			t.Fatal("start must be exempt from Blocked")
		}
		return false
	}}
	if _, err := FindPath(testGrid{rows: []string{"S.."}}, start, goal, opt); err != nil {
		t.Fatal(err)
	}
	if route, err := FindPath(testGrid{rows: []string{"S"}}, start, start, opt); err != nil || len(route) != 0 {
		t.Fatalf("equal endpoints = %v, %v", route, err)
	}
}

func TestFrontierPriority(t *testing.T) {
	// Indices in a width-three grid distinguish both row and column ordering.
	want := []candidate{
		{index: 8, cost: 0, heuristic: 0, sequence: 6},
		{index: 8, cost: 1, heuristic: 0, sequence: 5},
		{index: 0, cost: 0, heuristic: 1, sequence: 4},
		{index: 1, cost: 0, heuristic: 1, sequence: 2},
		{index: 3, cost: 0, heuristic: 1, sequence: 1},
		{index: 3, cost: 0, heuristic: 1, sequence: 3},
	}
	q := frontier(slices.Clone(want))
	slices.Reverse(q)
	heap.Init(&q)
	for _, expected := range want {
		if got := heap.Pop(&q).(candidate); got != expected {
			t.Fatalf("priority = %+v, want %+v", got, expected)
		}
	}
}

func assertValidRoute(t *testing.T, g Grid, start, goal Cell, opt Options, route []Cell) {
	t.Helper()
	if start == goal {
		if len(route) != 0 {
			t.Fatal("equal endpoints require an empty route")
		}
		return
	}
	if len(route) == 0 || route[len(route)-1] != goal {
		t.Fatalf("route does not end at goal: %v", route)
	}
	seen := map[Cell]bool{start: true}
	previous := start
	for i, cell := range route {
		dx, dy := cell.X-previous.X, cell.Y-previous.Y
		if dx*dx+dy*dy != 1 || seen[cell] || !g.Walkable(cell) || i < len(route)-1 && !g.TransitOK(cell) || opt.Blocked != nil && opt.Blocked(cell) {
			t.Fatalf("invalid route step %d: %v -> %v in %v", i, previous, cell, route)
		}
		seen[cell], previous = true, cell
	}
}
