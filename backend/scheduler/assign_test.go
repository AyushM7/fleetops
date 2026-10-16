package scheduler

import (
	"maps"
	"reflect"
	"slices"
	"testing"
)

func gridDistance(a, b Cell) (int, bool) {
	dx, dy := a.X-b.X, a.Y-b.Y
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	return dx + dy, true
}

func TestAssignSelection(t *testing.T) {
	job := JobInfo{Num: 1, From: Cell{X: 10}, To: Cell{X: 12}}
	for _, tc := range []struct {
		name string
		idle []RobotInfo
		want []Assignment
	}{
		{name: "nearest wins over lower ID", idle: []RobotInfo{
			{Num: 1, Pos: Cell{X: 5}, Battery: 100},
			{Num: 10, Pos: Cell{X: 9}, Battery: 100},
		}, want: []Assignment{{JobNum: 1, RobotNum: 10}}},
		{name: "shorter replaces first candidate", idle: []RobotInfo{
			{Num: 10, Pos: Cell{X: 5}, Battery: 100},
			{Num: 2, Pos: Cell{X: 9}, Battery: 100},
		}, want: []Assignment{{JobNum: 1, RobotNum: 2}}},
		{name: "tie picks numeric 2 before 10", idle: []RobotInfo{
			{Num: 10, Pos: Cell{X: 9}, Battery: 100},
			{Num: 2, Pos: Cell{X: 11}, Battery: 100},
		}, want: []Assignment{{JobNum: 1, RobotNum: 2}}},
		{name: "later higher ID cannot replace tie", idle: []RobotInfo{
			{Num: 2, Pos: Cell{X: 9}, Battery: 100},
			{Num: 10, Pos: Cell{X: 11}, Battery: 100},
		}, want: []Assignment{{JobNum: 1, RobotNum: 2}}},
		{name: "below minimum rejected", idle: []RobotInfo{
			{Num: 1, Pos: job.From, Battery: 29},
			{Num: 2, Pos: Cell{X: 9}, Battery: 30},
		}, want: []Assignment{{JobNum: 1, RobotNum: 2}}},
		{name: "no eligible robot", idle: []RobotInfo{{Num: 1, Pos: job.From, Battery: 29}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Assign(tc.idle, []JobInfo{job}, gridDistance, Params{MinBattery: 30, ReserveBattery: 5, MovingDrain: 1})
			if !slices.Equal(got, tc.want) {
				t.Fatalf("Assign = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAssignBatteryFeasibility(t *testing.T) {
	// Pickup is four steps away, followed by six delivery steps.
	job := JobInfo{Num: 1, From: Cell{X: 4}, To: Cell{X: 10}}
	for _, tc := range []struct {
		name    string
		battery int
		params  Params
		accept  bool
	}{
		{name: "below minimum despite feasible trip", battery: 29, params: Params{MinBattery: 30, MovingDrain: 1}},
		{name: "exact minimum", battery: 30, params: Params{MinBattery: 30, MovingDrain: 1}, accept: true},
		{name: "pickup alone affordable but full trip not", battery: 9, params: Params{MinBattery: 1, MovingDrain: 1}},
		{name: "reserve makes trip infeasible", battery: 14, params: Params{MinBattery: 1, ReserveBattery: 5, MovingDrain: 1}},
		{name: "exact feasibility threshold", battery: 15, params: Params{MinBattery: 1, ReserveBattery: 5, MovingDrain: 1}, accept: true},
		{name: "lower reserve permits trip", battery: 14, params: Params{MinBattery: 1, ReserveBattery: 4, MovingDrain: 1}, accept: true},
		{name: "higher reserve rejects trip", battery: 15, params: Params{MinBattery: 1, ReserveBattery: 6, MovingDrain: 1}},
		{name: "higher movement cost rejects trip", battery: 15, params: Params{MinBattery: 1, ReserveBattery: 5, MovingDrain: 2}},
		{name: "higher movement cost exact threshold", battery: 25, params: Params{MinBattery: 1, ReserveBattery: 5, MovingDrain: 2}, accept: true},
		{name: "no reserve exact threshold", battery: 10, params: Params{MinBattery: 1, MovingDrain: 1}, accept: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Assign([]RobotInfo{{Num: 2, Battery: tc.battery}}, []JobInfo{job}, gridDistance, tc.params)
			var want []Assignment
			if tc.accept {
				want = []Assignment{{JobNum: 1, RobotNum: 2}}
			}
			if !slices.Equal(got, want) {
				t.Fatalf("Assign = %v, want %v", got, want)
			}
		})
	}
}

func TestAssignSkipsNearestInfeasibleRobot(t *testing.T) {
	idle := []RobotInfo{{Num: 1, Pos: Cell{X: 9}, Battery: 30}, {Num: 2, Pos: Cell{X: 8}, Battery: 100}}
	pending := []JobInfo{{Num: 1, From: Cell{X: 10}, To: Cell{X: 40}}}
	want := []Assignment{{JobNum: 1, RobotNum: 2}}
	got := Assign(idle, pending, gridDistance, Params{MinBattery: 30, ReserveBattery: 5, MovingDrain: 1})
	if !slices.Equal(got, want) {
		t.Fatalf("Assign = %v, want %v", got, want)
	}
}

func TestAssignNumericJobOrderAndSingleUse(t *testing.T) {
	idle := []RobotInfo{{Num: 10, Pos: Cell{X: 2}, Battery: 100}, {Num: 2, Battery: 100}}
	pending := []JobInfo{
		{Num: 11, From: Cell{X: 2}, To: Cell{X: 3}},
		{Num: 10, To: Cell{X: 1}},
		{Num: 2, To: Cell{X: 1}},
	}
	want := []Assignment{{JobNum: 2, RobotNum: 2}, {JobNum: 10, RobotNum: 10}}
	got := Assign(idle, pending, gridDistance, Params{MinBattery: 30, ReserveBattery: 5, MovingDrain: 1})
	if !slices.Equal(got, want) {
		t.Fatalf("Assign = %v, want %v", got, want)
	}
}

type trip struct{ from, to Cell }

func TestAssignUnreachableRoutes(t *testing.T) {
	idle := []RobotInfo{{Num: 1, Battery: 100}, {Num: 2, Pos: Cell{X: 1}, Battery: 100}}
	first := JobInfo{Num: 2, From: Cell{X: 2}, To: Cell{X: 3}}
	later := JobInfo{Num: 10, From: Cell{X: 4}, To: Cell{X: 5}}
	for _, tc := range []struct {
		name    string
		missing []trip
		pending []JobInfo
		want    []Assignment
	}{
		{name: "nearest pickup unreachable", missing: []trip{{from: idle[1].Pos, to: first.From}}, pending: []JobInfo{first}, want: []Assignment{{JobNum: 2, RobotNum: 1}}},
		{name: "all pickups unreachable", missing: []trip{{from: idle[0].Pos, to: first.From}, {from: idle[1].Pos, to: first.From}}, pending: []JobInfo{first}},
		{name: "delivery unreachable", missing: []trip{{from: first.From, to: first.To}}, pending: []JobInfo{first}},
		{name: "unreachable earlier delivery permits later job", missing: []trip{{from: first.From, to: first.To}}, pending: []JobInfo{later, first}, want: []Assignment{{JobNum: 10, RobotNum: 2}}},
		{name: "unreachable earlier pickup permits later job", missing: []trip{{from: idle[0].Pos, to: first.From}, {from: idle[1].Pos, to: first.From}}, pending: []JobInfo{later, first}, want: []Assignment{{JobNum: 10, RobotNum: 2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			missing := make(map[trip]bool)
			for _, edge := range tc.missing {
				missing[edge] = true
			}
			before := maps.Clone(missing)
			dist := func(a, b Cell) (int, bool) {
				if missing[trip{from: a, to: b}] {
					return 0, false
				}
				return gridDistance(a, b)
			}
			got := Assign(idle, tc.pending, dist, Params{MinBattery: 30, ReserveBattery: 5, MovingDrain: 1})
			if !slices.Equal(got, tc.want) || !maps.Equal(missing, before) {
				t.Fatalf("Assign = %v, want %v; distance inputs unchanged = %v", got, tc.want, maps.Equal(missing, before))
			}
		})
	}
}

func TestAssignEmptyInputs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		idle    []RobotInfo
		pending []JobInfo
	}{
		{name: "both nil"},
		{name: "nil robots", pending: []JobInfo{{Num: 1}}},
		{name: "empty robots", idle: []RobotInfo{}, pending: []JobInfo{{Num: 1}}},
		{name: "nil jobs", idle: []RobotInfo{{Num: 1, Battery: 100}}},
		{name: "empty jobs", idle: []RobotInfo{{Num: 1, Battery: 100}}, pending: []JobInfo{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dist := func(a, b Cell) (int, bool) {
				t.Fatal("empty input must not call Distance")
				return 0, false
			}
			if got := Assign(tc.idle, tc.pending, dist, Params{}); len(got) != 0 {
				t.Fatalf("Assign = %v, want no assignments", got)
			}
		})
	}
}

func TestAssignDeterminismAndInputOwnership(t *testing.T) {
	idle := []RobotInfo{{Num: 10, Pos: Cell{X: 2}, Battery: 100}, {Num: 2, Battery: 100}, {Num: 1, Battery: 0}}
	pending := []JobInfo{{Num: 10, To: Cell{X: 1}}, {Num: 2, To: Cell{X: 1}}, {Num: 1, From: Cell{X: 50}, To: Cell{X: 100}}}
	beforeIdle, beforePending := slices.Clone(idle), slices.Clone(pending)
	p := Params{MinBattery: 30, ReserveBattery: 5, MovingDrain: 1}
	want := []Assignment{{JobNum: 2, RobotNum: 2}, {JobNum: 10, RobotNum: 10}}
	for repeat := 0; repeat < 100; repeat++ {
		got := Assign(idle, pending, gridDistance, p)
		if !slices.Equal(got, want) {
			t.Fatalf("call %d: Assign = %v, want %v", repeat, got, want)
		}
		if !reflect.DeepEqual(idle, beforeIdle) || !reflect.DeepEqual(pending, beforePending) {
			t.Fatal("Assign mutated caller slices")
		}
		got[0] = Assignment{JobNum: -1, RobotNum: -1}
	}
}

func TestAssignExitCost(t *testing.T) {
	for _, tc := range []struct {
		name          string
		exit, battery int
		accept        bool
	}{
		{"legacy zero exit", 0, 7, true}, {"exit makes job unaffordable", 4, 10, false},
		{"exact exit threshold", 4, 11, true}, {"unreachable exit", -1, 100, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jobs := []JobInfo{{Num: 2, From: Cell{X: 1}, To: Cell{X: 2}, ExitCost: tc.exit}}
			before := slices.Clone(jobs)
			got := Assign([]RobotInfo{{Num: 10, Battery: tc.battery}}, jobs, gridDistance, Params{MinBattery: 1, ReserveBattery: 5, MovingDrain: 1})
			if (len(got) == 1) != tc.accept || !reflect.DeepEqual(before, jobs) {
				t.Fatalf("assignments=%v mutated=%v", got, !reflect.DeepEqual(before, jobs))
			}
		})
	}
	jobs := []JobInfo{{Num: 2, ExitCost: -1}, {Num: 10, To: Cell{X: 1}, ExitCost: 1}}
	if got := Assign([]RobotInfo{{Num: 2, Battery: 100}}, jobs, gridDistance, Params{MovingDrain: 1}); !slices.Equal(got, []Assignment{{JobNum: 10, RobotNum: 2}}) {
		t.Fatalf("unreachable exit stopped later job: %v", got)
	}
}
