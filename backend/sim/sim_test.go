package sim

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
)

func newTestSim(t *testing.T, cfg Config, seed int64) *Sim {
	t.Helper()
	s, err := New(cfg, seed)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	return s
}

func TestNewInitialState(t *testing.T) {
	cfg := DefaultConfig()
	s := newTestSim(t, cfg, 42)
	state := s.state
	if state.Tick != 0 || state.Seed != 42 || len(state.Robots) != 6 || len(state.Jobs) != 8 || len(state.Stations) != 2 {
		t.Fatalf("unexpected initial state: %+v", state)
	}
	if state.Robots == nil || state.Jobs == nil || state.Stations == nil || state.Faults == nil || len(state.Faults) != 0 {
		t.Fatal("initial slices must be non-nil and faults empty")
	}
	for i, p := range cfg.RobotStarts {
		want := RobotState{ID: fmt.Sprintf("R%d", i+1), X: p.X, Y: p.Y, Battery: 100, Status: RobotIdle}
		if state.Robots[i] != want {
			t.Errorf("robot %d = %+v, want %+v", i, state.Robots[i], want)
		}
		if remaining, ok := s.unresponsiveTicks[want.ID]; !ok || remaining != 0 {
			t.Errorf("missing or nonzero countdown for %s", want.ID)
		}
	}
	for i, p := range cfg.Stations {
		want := StationState{ID: fmt.Sprintf("S%d", i+1), X: p.X, Y: p.Y}
		if state.Stations[i] != want {
			t.Errorf("station %d = %+v, want %+v", i, state.Stations[i], want)
		}
	}
	for i, job := range state.Jobs {
		if job.ID != fmt.Sprintf("J%d", i+1) || job.Status != JobPending || job.AssignedRobot != "" {
			t.Errorf("unexpected initial job: %+v", job)
		}
	}
	cfg.InitialJobs = 0
	empty := newTestSim(t, cfg, 42)
	if empty.state.Jobs == nil || len(empty.state.Jobs) != 0 {
		t.Fatal("zero initial jobs must produce a non-nil empty slice")
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Width = 0
	if s, err := New(cfg, 42); s != nil || !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New() = (%v, %v), want nil and ErrInvalidConfig", s, err)
	}
}

func TestConfigOwnershipAndCellAt(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Walls = []Point{{2, 2}}
	original := cfg.Clone()
	s := newTestSim(t, cfg, 42)
	before := newTestSim(t, cfg, 42).state
	cfg.Width = 4
	cfg.AisleColumns[0] = 3
	cfg.Walls[0] = Point{3, 2}
	cfg.Stations[0] = Point{1, 0}
	cfg.RobotStarts[0] = Point{2, 11}
	if !reflect.DeepEqual(s.cfg, original) {
		t.Fatal("simulator configuration aliases the caller")
	}
	for _, p := range []Point{{1, 0}, {4, 1}, {0, 0}, {2, 2}, {-1, 0}, {20, 0}, {0, -1}, {0, 12}} {
		if got := s.CellAt(p); got != original.CellTypeAt(p) {
			t.Errorf("CellAt(%v) = %s, want %s", p, got, original.CellTypeAt(p))
		}
	}
	if !reflect.DeepEqual(s.state, before) {
		t.Fatal("configuration mutation or CellAt changed simulator state")
	}
}

func TestTickWithAutoDispatchDisabled(t *testing.T) {
	cfg := cfgRobotOnStation()
	cfg.AutoDispatch = false
	s := newTestSim(t, cfg, 42)
	robots := slices.Clone(s.state.Robots)
	for i := 0; i < 20; i++ {
		s.Tick()
	}
	for i, robot := range s.state.Robots {
		if robot.X != robots[i].X || robot.Y != robots[i].Y || robot.JobID != "" {
			t.Fatalf("robot moved or received a job with dispatch disabled: %+v", robot)
		}
	}
	if len(s.state.Faults) != 0 {
		t.Fatal("unexpected automatic fault records")
	}
	for _, job := range s.state.Jobs {
		if job.Status != JobPending || job.AssignedRobot != "" {
			t.Fatal("jobs must remain pending with dispatch disabled")
		}
	}
}

func stateJSON(t *testing.T, state State) string {
	t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	return string(data)
}

func TestSnapshotOwnership(t *testing.T) {
	s := newTestSim(t, DefaultConfig(), 42)
	aisle := 0
	s.recordFault(FaultAisleConflict, []string{"R1", "R2"}, &aisle)
	before := stateJSON(t, s.state)
	retained := s.Snapshot()
	if stateJSON(t, retained) != before {
		t.Fatal("snapshot does not match complete internal state")
	}
	for attempt := 0; attempt < 2; attempt++ {
		snapshot := s.Snapshot()
		snapshot.Tick, snapshot.Seed = -1, -1
		snapshot.Robots[0].Battery = 0
		snapshot.Jobs[0].From.X = -1
		snapshot.Stations[0].OccupiedBy = "changed"
		snapshot.Faults[0].ID = "changed"
		snapshot.Faults[0].Type = FaultLowBattery
		snapshot.Faults[0].Tick = -1
		snapshot.Faults[0].Robots[0] = "changed"
		*snapshot.Faults[0].Aisle = 99
		if stateJSON(t, s.state) != before || stateJSON(t, s.Snapshot()) != before || stateJSON(t, retained) != before {
			t.Fatal("snapshot mutation affected internal state or another snapshot")
		}
	}
}

func TestSnapshotJSONContract(t *testing.T) {
	cfg := DefaultConfig()
	cfg.InitialJobs = 0
	snapshot := newTestSim(t, cfg, 42).Snapshot()
	if snapshot.Robots == nil || snapshot.Jobs == nil || snapshot.Stations == nil || snapshot.Faults == nil {
		t.Fatal("all snapshot slices must be non-nil")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stateJSON(t, snapshot)), &fields); err != nil {
		t.Fatal(err)
	}
	keys := []string{"tick", "seed", "robots", "jobs", "stations", "faults"}
	if len(fields) != len(keys) {
		t.Fatalf("unexpected State keys: %v", fields)
	}
	for _, key := range keys {
		if _, ok := fields[key]; !ok {
			t.Errorf("missing State key %s", key)
		}
	}
	if string(fields["tick"]) != "0" || string(fields["seed"]) != "42" || string(fields["jobs"]) != "[]" || string(fields["faults"]) != "[]" {
		t.Fatalf("incorrect scalar or empty-array JSON: %s", stateJSON(t, snapshot))
	}
	// Exercise all four empty arrays even though valid configs require robots and stations.
	empty := (&Sim{}).Snapshot()
	want := `{"tick":0,"seed":0,"robots":[],"jobs":[],"stations":[],"faults":[]}`
	if got := stateJSON(t, empty); got != want {
		t.Fatalf("empty State = %s, want %s", got, want)
	}
}

func TestRecordFaultOwnershipAndOrder(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AutoDispatch = false
	s := newTestSim(t, cfg, 42)
	robots := []string{"R1", "R2"}
	aisle := 0
	for i := 1; i <= 10; i++ {
		s.Tick()
		if i%2 == 1 {
			s.recordFault(FaultAisleConflict, robots, &aisle)
		} else {
			s.recordFault(FaultRobotUnresponsive, nil, nil)
		}
	}
	robots[0], aisle = "changed", 99
	snapshot := s.Snapshot()
	if len(snapshot.Faults) != 10 || len(s.state.Faults) != 10 {
		t.Fatal("expected exactly ten fault records")
	}
	for i, fault := range snapshot.Faults {
		want := FaultState{ID: fmt.Sprintf("F%d", i+1), Type: FaultRobotUnresponsive, Robots: []string{}, Tick: i + 1}
		if i%2 == 0 {
			zero := 0
			want.Type, want.Robots, want.Aisle = FaultAisleConflict, []string{"R1", "R2"}, &zero
		}
		if !reflect.DeepEqual(fault, want) || !reflect.DeepEqual(s.state.Faults[i], want) {
			t.Fatalf("fault %d = %+v, want %+v", i+1, fault, want)
		}
	}
	var encoded struct {
		Faults []map[string]json.RawMessage `json:"faults"`
	}
	if err := json.Unmarshal([]byte(stateJSON(t, snapshot)), &encoded); err != nil {
		t.Fatal(err)
	}
	for i, fault := range encoded.Faults {
		if i%2 == 0 {
			if string(fault["aisle"]) != "0" {
				t.Fatalf("fault %d lost aisle zero", i+1)
			}
		} else if _, present := fault["aisle"]; present || string(fault["robots"]) != "[]" {
			t.Fatalf("fault %d must omit aisle and retain empty robots array", i+1)
		}
		if string(fault["id"]) != fmt.Sprintf(`"F%d"`, i+1) {
			t.Fatalf("fault JSON order incorrect at %d", i)
		}
	}
	snapshot.Faults[0].Robots[0] = "mutated"
	*snapshot.Faults[0].Aisle = 55
	if s.state.Faults[0].Robots[0] != "R1" || *s.state.Faults[0].Aisle != 0 || *s.state.Faults[2].Aisle != 0 {
		t.Fatal("fault records share caller or snapshot storage")
	}
}
