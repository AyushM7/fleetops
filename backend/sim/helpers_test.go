package sim

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	want := Config{
		Width:                 20,
		Height:                12,
		AisleColumns:          []int{4, 9, 14},
		AisleYMin:             1,
		AisleYMax:             10,
		Walls:                 []Point{},
		Stations:              []Point{{0, 0}, {19, 0}},
		RobotStarts:           []Point{{1, 11}, {3, 11}, {5, 11}, {7, 11}, {9, 11}, {11, 11}},
		InitialJobs:           8,
		NewJobEvery:           10,
		MaxJobs:               20,
		MovingDrain:           1,
		IdleDrainEvery:        20,
		ChargeGain:            2,
		MinAssignBattery:      30,
		UnresponsiveDelay:     3,
		LowBatteryInjectLevel: 15,
		AutoDispatch:          true,
		ReplanAfter:           3,
		BlockedAfter:          10,
		ReserveBattery:        5,
		AutoCharge:            false,
		ChargeBelow:           40,
		Recovery:              false,
		RecoverAfter:          15,
		HoldTicks:             3,
	}
	if got := DefaultConfig(); !reflect.DeepEqual(got, want) {
		t.Fatalf("defaults = %+v, want %+v", got, want)
	}
}

func TestGridLookups(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Walls = []Point{{2, 2}, {4, 3}}
	cfg.Stations = append(cfg.Stations, Point{4, 3}, Point{9, 2})
	for _, tc := range []struct {
		point Point
		cell  CellType
	}{
		{Point{0, 0}, CellStation}, {Point{19, 0}, CellStation},
		{Point{0, 11}, CellFree}, {Point{19, 11}, CellFree},
		{Point{2, 2}, CellWall}, {Point{4, 3}, CellWall},
		{Point{9, 2}, CellStation}, {Point{4, 1}, CellAisle},
		{Point{14, 10}, CellAisle}, {Point{4, 0}, CellFree},
		{Point{4, 11}, CellFree}, {Point{5, 5}, CellFree},
		{Point{-1, 0}, CellOutOfBounds}, {Point{20, 0}, CellOutOfBounds},
		{Point{0, -1}, CellOutOfBounds}, {Point{0, 12}, CellOutOfBounds},
		{Point{-100, -100}, CellOutOfBounds}, {Point{100, 100}, CellOutOfBounds},
	} {
		if got := cfg.CellTypeAt(tc.point); got != tc.cell {
			t.Errorf("CellTypeAt(%v) = %q, want %q", tc.point, got, tc.cell)
		}
		if got := cfg.InBounds(tc.point); got != (tc.cell != CellOutOfBounds) {
			t.Errorf("InBounds(%v) = %v", tc.point, got)
		}
	}
	for _, id := range []int{-1, 0, 1, 2, 3, 100} {
		column, ok := cfg.AisleColumn(id)
		wantOK := id >= 0 && id < 3
		if ok != wantOK || (wantOK && column != []int{4, 9, 14}[id]) {
			t.Errorf("AisleColumn(%d) = (%d, %v)", id, column, ok)
		}
	}
}

func TestJobCells(t *testing.T) {
	cfg := DefaultConfig()
	cells := cfg.JobCells()
	if len(cells) != 238 || cells[0] != (Point{1, 0}) || cells[len(cells)-1] != (Point{19, 11}) {
		t.Fatalf("unexpected default job cells: %v", cells)
	}
	cfg.Walls = []Point{{1, 0}, {5, 5}}
	cells = cfg.JobCells()
	if len(cells) != 236 {
		t.Fatalf("wall-filtered count = %d, want 236", len(cells))
	}
	for i, point := range cells {
		if cell := cfg.CellTypeAt(point); cell != CellFree && cell != CellAisle {
			t.Errorf("ineligible job cell: %v", point)
		}
		if i > 0 && point.Y*cfg.Width+point.X <= cells[i-1].Y*cfg.Width+cells[i-1].X {
			t.Errorf("job cells not strictly row-major at %d", i)
		}
	}
	cells[0] = Point{-1, -1}
	if cfg.JobCells()[0] != (Point{2, 0}) {
		t.Fatal("returned job cells share mutable storage")
	}
}

func TestCloneAndHelperPurity(t *testing.T) {
	source := DefaultConfig()
	source.Walls = []Point{{2, 2}}
	want := DefaultConfig()
	want.Walls = []Point{{2, 2}}
	clone := source.Clone()
	clone.AisleColumns[0] = 0
	clone.Walls[0] = Point{3, 3}
	clone.Stations[0] = Point{1, 0}
	clone.RobotStarts[0] = Point{2, 11}
	source.InBounds(Point{-1, -1})
	source.CellTypeAt(Point{2, 2})
	source.AisleColumn(0)
	source.JobCells()
	if !reflect.DeepEqual(source, want) {
		t.Fatalf("clone or helpers mutated source: %+v", source)
	}
	fresh := DefaultConfig()
	fresh.AisleColumns[0] = 0
	fresh.Stations[0] = Point{1, 0}
	fresh.RobotStarts[0] = Point{2, 11}
	want.Walls = []Point{}
	if !reflect.DeepEqual(DefaultConfig(), want) {
		t.Fatal("DefaultConfig shares slice storage")
	}
}

func TestJSONContracts(t *testing.T) {
	zero := 0
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"point", Point{2, 3}, `{"x":2,"y":3}`},
		{"robot", RobotState{ID: "R1", X: 2, Y: 3, Battery: 100, Status: RobotIdle, JobID: ""}, `{"id":"R1","x":2,"y":3,"battery":100,"status":"idle","jobId":"","waitTicks":0,"blocked":false}`},
		{"unassigned job", JobState{ID: "J1", From: Point{1, 2}, To: Point{3, 4}, Status: JobPending, AssignedRobot: ""}, `{"id":"J1","from":{"x":1,"y":2},"to":{"x":3,"y":4},"status":"pending","assignedRobot":""}`},
		{"done job", JobState{ID: "J1", From: Point{}, To: Point{}, Status: JobDone, AssignedRobot: "R1"}, `{"id":"J1","from":{"x":0,"y":0},"to":{"x":0,"y":0},"status":"done","assignedRobot":"R1"}`},
		{"station", StationState{ID: "S1", X: 0, Y: 0, OccupiedBy: ""}, `{"id":"S1","x":0,"y":0,"occupiedBy":"","reservedBy":""}`},
		{"fault with aisle zero", FaultState{ID: "F1", Type: FaultAisleConflict, Robots: []string{"R1"}, Aisle: &zero, Tick: 7}, `{"id":"F1","type":"aisle_conflict","robots":["R1"],"aisle":0,"tick":7}`},
		{"fault without aisle", FaultState{ID: "F2", Type: FaultLowBattery, Robots: []string{"R2"}, Aisle: nil, Tick: 8}, `{"id":"F2","type":"low_battery","robots":["R2"],"tick":8}`},
		{"state", State{Tick: 9, Seed: 42, Robots: []RobotState{}, Jobs: []JobState{}, Stations: []StationState{}, Faults: []FaultState{}, Aisles: []AisleState{}}, `{"tick":9,"seed":42,"robots":[],"jobs":[],"stations":[],"faults":[],"aisles":[]}`},
		{"cells", []CellType{CellFree, CellAisle, CellStation, CellWall, CellOutOfBounds}, `["free","aisle","station","wall","out_of_bounds"]`},
		{"robot statuses", []RobotStatus{RobotIdle, RobotMoving, RobotCharging, RobotFaulted, RobotOffline}, `["idle","moving","charging","faulted","offline"]`},
		{"job statuses", []JobStatus{JobPending, JobAssigned, JobInProgress, JobDone}, `["pending","assigned","in_progress","done"]`},
		{"fault types", []FaultType{FaultLowBattery, FaultRobotUnresponsive, FaultAisleConflict}, `["low_battery","robot_unresponsive","aisle_conflict"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.value)
			if err != nil || string(got) != tc.want {
				t.Fatalf("JSON = %s, error = %v; want %s", got, err, tc.want)
			}
		})
	}
}

func TestSentinelErrors(t *testing.T) {
	seen := make(map[error]bool)
	for _, err := range []error{
		ErrInvalidConfig, ErrUnknownRobot, ErrUnknownStation, ErrUnknownAisle,
		ErrSameRobot, ErrRobotBusy, ErrNotAtStation, ErrStationOccupied, ErrRobotUnavailable,
	} {
		if err == nil || seen[err] || err.Error() == "" {
			t.Fatalf("missing, duplicate, or empty sentinel: %v", err)
		}
		seen[err] = true
	}
}
