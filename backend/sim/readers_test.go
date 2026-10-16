package sim

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/AyushM7/fleetops/backend/routing"
)

func TestPlanRoute(t *testing.T) {
	for _, tc := range []struct {
		name     string
		from, to Point
		want     []Point
		wantErr  error
	}{
		{name: "around shelf", from: Point{1, 2}, to: Point{4, 2}, want: []Point{{1, 1}, {2, 1}, {3, 1}, {4, 1}, {4, 2}}},
		{name: "station destination", from: Point{2, 0}, to: Point{1, 0}, want: []Point{{1, 0}}},
		{name: "station start", from: Point{1, 0}, to: Point{2, 0}, want: []Point{{2, 0}}},
		{name: "station is not transit", from: Point{0, 0}, to: Point{2, 0}, want: []Point{{0, 1}, {1, 1}, {2, 1}, {2, 0}}},
		{name: "same point", from: Point{1, 1}, to: Point{1, 1}, want: []Point{}},
		{name: "outside start", from: Point{-1, 0}, to: Point{1, 1}, wantErr: routing.ErrOutOfBounds},
		{name: "outside goal", from: Point{1, 1}, to: Point{20, 0}, wantErr: routing.ErrOutOfBounds},
		{name: "wall start", from: Point{2, 2}, to: Point{1, 1}, wantErr: routing.ErrNotWalkable},
		{name: "wall goal", from: Point{1, 1}, to: Point{2, 2}, wantErr: routing.ErrNotWalkable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := WarehouseConfig()
			cfg.Stations[0] = Point{1, 0}
			s := newTestSim(t, cfg, 42)
			got, err := s.PlanRoute(tc.from, tc.to)
			if !errors.Is(err, tc.wantErr) || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("PlanRoute = %v, %v; want %v, %v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestRoutingGridAdapter(t *testing.T) {
	cfg := WarehouseConfig()
	g := routingGrid{cfg: cfg}
	if g.Width() != cfg.Width || g.Height() != cfg.Height {
		t.Fatal("adapter dimensions differ from config")
	}
	for _, tc := range []struct {
		point             routing.Cell
		walkable, transit bool
	}{
		{point: routing.Cell{X: 1, Y: 1}, walkable: true, transit: true},
		{point: routing.Cell{X: 4, Y: 2}, walkable: true, transit: true},
		{point: routing.Cell{}, walkable: true},
		{point: routing.Cell{X: 2, Y: 2}},
		{point: routing.Cell{X: -1}},
		{point: routing.Cell{X: 20}},
		{point: routing.Cell{Y: 12}},
	} {
		if g.Walkable(tc.point) != tc.walkable || g.TransitOK(tc.point) != tc.transit {
			t.Errorf("adapter cell %v: walkable=%v transit=%v", tc.point, g.Walkable(tc.point), g.TransitOK(tc.point))
		}
	}
}

func TestAisleOccupantAndSnapshot(t *testing.T) {
	s := newTestSim(t, WarehouseConfig(), 42)
	// R10 comes first in storage, but R2 has the lower numeric ID.
	s.state.Robots[0].ID, s.state.Robots[0].X, s.state.Robots[0].Y = "R10", 4, 2
	s.state.Robots[1].X, s.state.Robots[1].Y = 4, 9
	s.state.Robots[2].X, s.state.Robots[2].Y = 9, 1
	s.state.Robots[3].X, s.state.Robots[3].Y = 9, 10
	s.state.Robots[4].X, s.state.Robots[4].Y = 14, 2
	want := []AisleState{{ID: 0, OccupiedBy: "R2"}, {ID: 1}, {ID: 2, OccupiedBy: "R5"}}
	before := stateJSON(t, s.state)
	for _, aisle := range want {
		got, err := s.AisleOccupant(aisle.ID)
		if err != nil || got != aisle.OccupiedBy {
			t.Fatalf("aisle %d: %q, %v; want %q", aisle.ID, got, err, aisle.OccupiedBy)
		}
	}
	for _, id := range []int{-1, 3, 100} {
		if got, err := s.AisleOccupant(id); got != "" || !errors.Is(err, ErrUnknownAisle) {
			t.Fatalf("invalid aisle %d: %q, %v", id, got, err)
		}
	}
	retained := s.Snapshot()
	if !reflect.DeepEqual(retained.Aisles, want) || stateJSON(t, s.state) != before {
		t.Fatal("snapshot occupancy is stale or reads mutated internal state")
	}
	snapshot := s.Snapshot()
	snapshot.Aisles[0].OccupiedBy = "changed"
	snapshot.Aisles[1].ID = 99
	if !reflect.DeepEqual(s.Snapshot().Aisles, want) || !reflect.DeepEqual(retained.Aisles, want) {
		t.Fatal("aisle snapshots share storage")
	}
	slices.Reverse(s.state.Robots)
	if !reflect.DeepEqual(s.Snapshot().Aisles, want) {
		t.Fatal("aisle occupancy depends on robot storage order")
	}
}

func TestAisleOccupancyAfterTick(t *testing.T) {
	cfg := DefaultConfig()
	cfg.InitialJobs = 1
	s := newTestSim(t, cfg, 42)
	setJobCells(t, s, "J1", Point{4, 10}, Point{5, 10})
	for tick := 1; tick <= 5; tick++ {
		s.Tick()
		want := ""
		if tick == 2 {
			want = "R2"
		}
		got, err := s.AisleOccupant(0)
		if err != nil || got != want || s.Snapshot().Aisles[0].OccupiedBy != want || s.state.Aisles[0].OccupiedBy != want {
			t.Fatalf("tick %d: occupant=%q, error=%v, snapshot=%v, internal=%v", tick, got, err, s.Snapshot().Aisles, s.state.Aisles)
		}
		assertInvariants(t, s, cfg)
	}
}

func TestOccupiedAislesOnRoute(t *testing.T) {
	s := newTestSim(t, WarehouseConfig(), 42)
	s.state.Robots[0].X, s.state.Robots[0].Y = 4, 2
	s.state.Robots[1].X, s.state.Robots[1].Y = 9, 3
	s.state.Robots[2].X, s.state.Robots[2].Y = 14, 4
	route := []Point{{14, 4}, {9, 2}, {4, 2}, {9, 9}, {14, 4}}
	for _, tc := range []struct {
		name, robot string
		route       []Point
		want        []int
		wantErr     error
	}{
		{name: "ascending distinct other aisles", robot: "R1", route: route, want: []int{1, 2}},
		{name: "different caller", robot: "R2", route: route, want: []int{0, 2}},
		{name: "unoccupied caller", robot: "R6", route: route, want: []int{0, 1, 2}},
		{name: "outside aisle ranges", robot: "R6", route: []Point{{4, 1}, {9, 10}, {14, 11}}, want: []int{}},
		{name: "free cells", robot: "R6", route: []Point{{1, 1}}, want: []int{}},
		{name: "nil route", robot: "R1", want: []int{}},
		{name: "empty route", robot: "R1", route: []Point{}, want: []int{}},
		{name: "invalid robot", robot: "R99", route: route, wantErr: ErrUnknownRobot},
		{name: "invalid robot nil route", robot: "R99", wantErr: ErrUnknownRobot},
		{name: "empty robot ID", wantErr: ErrUnknownRobot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := slices.Clone(tc.route)
			got, err := s.OccupiedAislesOnRoute(tc.route, tc.robot)
			if !errors.Is(err, tc.wantErr) || !reflect.DeepEqual(got, tc.want) || !slices.Equal(tc.route, before) {
				t.Fatalf("occupied aisles = %v, %v; want %v, %v", got, err, tc.want, tc.wantErr)
			}
		})
	}
	s.state.Robots[0].X = 1
	if got, err := s.OccupiedAislesOnRoute([]Point{{4, 2}}, "R6"); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("free aisle = %v, %v", got, err)
	}
}

func TestReadersDeterministicAndReadOnly(t *testing.T) {
	cfg := WarehouseConfig()
	s, control := newTestSim(t, cfg, 42), newTestSim(t, cfg, 42)
	for _, sim := range []*Sim{s, control} {
		sim.state.Robots[0].X, sim.state.Robots[0].Y = 4, 2
	}
	before, snapshot := stateJSON(t, s.state), s.Snapshot()
	beforeConfig, countdowns := s.cfg.Clone(), maps.Clone(s.unresponsiveTicks)
	wantRoute := []Point{{1, 1}, {2, 1}, {3, 1}, {4, 1}, {4, 2}}
	for repeat := 0; repeat < 100; repeat++ {
		route, err := s.PlanRoute(Point{1, 2}, Point{4, 2})
		if err != nil || !reflect.DeepEqual(route, wantRoute) {
			t.Fatalf("call %d PlanRoute = %v, %v", repeat, route, err)
		}
		if occupant, err := s.AisleOccupant(0); occupant != "R1" || err != nil {
			t.Fatalf("call %d AisleOccupant = %q, %v", repeat, occupant, err)
		}
		ids, err := s.OccupiedAislesOnRoute(route, "R2")
		if err != nil || !reflect.DeepEqual(ids, []int{0}) {
			t.Fatalf("call %d OccupiedAislesOnRoute = %v, %v", repeat, ids, err)
		}
		route[0], ids[0] = Point{-1, -1}, 99
	}
	if stateJSON(t, s.state) != before || !reflect.DeepEqual(s.Snapshot(), snapshot) || !reflect.DeepEqual(s.cfg, beforeConfig) || !maps.Equal(s.unresponsiveTicks, countdowns) {
		t.Fatal("READ APIs changed simulator state, configuration, or countdowns")
	}
	for tick := 0; tick < 20; tick++ {
		s.Tick()
		control.Tick()
		if !reflect.DeepEqual(s.Snapshot(), control.Snapshot()) {
			t.Fatal("READ APIs changed subsequent simulation or RNG behavior")
		}
	}
}

func TestWaitingInvariantThresholds(t *testing.T) {
	cfg := DefaultConfig()
	s := newTestSim(t, cfg, 42)
	s.Tick()
	for _, tc := range []struct {
		name    string
		wait    int
		blocked bool
		valid   bool
	}{
		{name: "moving no wait", valid: true},
		{name: "below threshold", wait: cfg.BlockedAfter - 1, valid: true},
		{name: "exact threshold", wait: cfg.BlockedAfter, blocked: true, valid: true},
		{name: "above threshold", wait: cfg.BlockedAfter + 1, blocked: true, valid: true},
		{name: "missing blocked flag", wait: cfg.BlockedAfter},
		{name: "early blocked flag", wait: cfg.BlockedAfter - 1, blocked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := s.Snapshot()
			state.Robots[0].WaitTicks, state.Robots[0].Blocked = tc.wait, tc.blocked
			err := checkInvariants(state, cfg)
			if tc.valid && err != nil || !tc.valid && (err == nil || !strings.HasPrefix(err.Error(), "I14:")) {
				t.Fatalf("waiting invariant = %v, valid=%v", err, tc.valid)
			}
		})
	}
}

func TestInvariantsRejectSharedAisleAndPosition(t *testing.T) {
	cfg := DefaultConfig()
	s := newTestSim(t, cfg, 42)
	for i := 0; i < 2; i++ {
		s.state.Robots[i].X, s.state.Robots[i].Y = 4, 1
	}
	state := s.Snapshot()
	if err := checkInvariants(state, cfg); err == nil || !strings.HasPrefix(err.Error(), "I12:") || state.Aisles[0].OccupiedBy != "R1" {
		t.Fatalf("shared position not rejected or occupancy read changed: %v, aisles=%v", err, state.Aisles)
	}
	s.state.Robots[1].Y = 2
	state = s.Snapshot()
	if err := checkInvariants(state, cfg); err == nil || !strings.HasPrefix(err.Error(), "I13:") || state.Aisles[0].OccupiedBy != "R1" {
		t.Fatalf("shared aisle not rejected or occupancy read changed: %v, aisles=%v", err, state.Aisles)
	}
}

func TestNonMovingRobotsCannotWait(t *testing.T) {
	for _, status := range []RobotStatus{RobotIdle, RobotCharging, RobotFaulted, RobotOffline} {
		t.Run(string(status), func(t *testing.T) {
			robot := RobotState{ID: "R1", Status: status, WaitTicks: 1}
			if err := checkRobotInvariants(robot, DefaultConfig(), nil); err == nil || !strings.HasPrefix(err.Error(), "I14:") {
				t.Fatalf("non-moving robot waiting: %v", err)
			}
		})
	}
}

func TestGoldenLegacyFieldsCompatibility(t *testing.T) {
	data, err := os.ReadFile("testdata/golden_seed42_tick200.json")
	if err != nil {
		t.Fatal(err)
	}
	var snapshot State
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	if err := checkInvariants(snapshot, DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	// Freeze the approved P2-06 behavior as well as the invariant checks above.
	assertGoldenHash(t, state, "64b4ba40163fe4cffc91cc72274d89ff1500e50e132536a5d62587dd9b3705f5")
	for _, entry := range state["robots"].([]any) {
		robot := entry.(map[string]any)
		for _, key := range []string{"x", "y", "battery", "status", "jobId", "waitTicks", "blocked"} {
			delete(robot, key)
		}
	}
	for _, entry := range state["jobs"].([]any) {
		job := entry.(map[string]any)
		delete(job, "status")
		delete(job, "assignedRobot")
	}
	for _, entry := range state["aisles"].([]any) {
		delete(entry.(map[string]any), "occupiedBy")
	}
	// The P2-05 projection retains seed/tick, ordered IDs, job geometry,
	// complete station/fault state, and all fields outside approved dynamics.
	assertGoldenHash(t, state, "b15412af30aa08a45bdd00800d43136e52364dc41396cc29e66af8353f3d95f8")
}

func assertGoldenHash(t *testing.T, state map[string]any, want string) {
	t.Helper()
	canonical, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(canonical)); got != want {
		t.Fatalf("golden regression fields changed: SHA256=%s, want %s", got, want)
	}
}
