package sim

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"testing"
)

func requireInjection(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("injection failed: %v", err)
	}
}

func assertInjectionFailure(t *testing.T, s *Sim, inject func() error, want error) {
	t.Helper()
	before := s.Snapshot()
	countdowns := maps.Clone(s.unresponsiveTicks)
	if err := inject(); !errors.Is(err, want) {
		t.Fatalf("injection = %v, want %v", err, want)
	}
	if !reflect.DeepEqual(s.Snapshot(), before) || !reflect.DeepEqual(s.unresponsiveTicks, countdowns) {
		t.Fatal("rejected injection changed state, countdowns, or fault records")
	}
}

func TestFaultSingleRobotErrors(t *testing.T) {
	for _, method := range []struct {
		name   string
		inject func(*Sim, string) error
	}{
		{"low battery", (*Sim).InjectLowBattery},
		{"unresponsive", (*Sim).InjectUnresponsive},
	} {
		for _, tc := range []struct {
			name, id string
			status   RobotStatus
			want     error
		}{
			{"unknown", "missing", RobotIdle, ErrUnknownRobot},
			{"offline", "R1", RobotOffline, ErrRobotUnavailable},
			{"faulted", "R1", RobotFaulted, ErrRobotUnavailable},
		} {
			t.Run(method.name+"/"+tc.name, func(t *testing.T) {
				s := newTestSim(t, DefaultConfig(), 1)
				s.state.Robots[0].Status = tc.status
				s.state.Robots[0].Battery = 0
				s.unresponsiveTicks[tc.id] = 2
				s.recordFault(FaultLowBattery, []string{"R2"}, nil)
				assertInjectionFailure(t, s, func() error { return method.inject(s, tc.id) }, tc.want)
			})
		}
	}
}

func TestFaultAisleErrorPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, a, b string
		aisle      int
		statusA    RobotStatus
		statusB    RobotStatus
		want       error
	}{
		{"unknown first", "missing", "R2", -1, RobotIdle, RobotFaulted, ErrUnknownRobot},
		{"unknown second before unavailable", "R1", "missing", -1, RobotOffline, RobotIdle, ErrUnknownRobot},
		{"same unknown", "missing", "missing", -1, RobotIdle, RobotIdle, ErrUnknownRobot},
		{"same before aisle and unavailable", "R1", "R1", -1, RobotFaulted, RobotIdle, ErrSameRobot},
		{"negative aisle before unavailable", "R1", "R2", -1, RobotFaulted, RobotOffline, ErrUnknownAisle},
		{"aisle upper bound", "R1", "R2", 3, RobotIdle, RobotIdle, ErrUnknownAisle},
		{"first offline", "R1", "R2", 0, RobotOffline, RobotIdle, ErrRobotUnavailable},
		{"first faulted", "R1", "R2", 0, RobotFaulted, RobotIdle, ErrRobotUnavailable},
		{"second offline", "R1", "R2", 0, RobotIdle, RobotOffline, ErrRobotUnavailable},
		{"second faulted", "R1", "R2", 0, RobotIdle, RobotFaulted, ErrRobotUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestSim(t, DefaultConfig(), 1)
			s.state.Robots[0].Status, s.state.Robots[1].Status = tc.statusA, tc.statusB
			s.unresponsiveTicks["R3"] = 2
			s.recordFault(FaultLowBattery, []string{"R3"}, nil)
			assertInjectionFailure(t, s, func() error { return s.InjectAisleConflict(tc.a, tc.b, tc.aisle) }, tc.want)
		})
	}
}

func TestFaultLowBatteryMinimum(t *testing.T) {
	for _, tc := range []struct{ battery, level, want int }{
		{100, 15, 15}, {10, 15, 10}, {15, 15, 15}, {100, 7, 7}, {100, 0, 0},
	} {
		t.Run(fmt.Sprintf("%d_to_%d", tc.battery, tc.level), func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.LowBatteryInjectLevel = tc.level
			s := newTestSim(t, cfg, 1)
			s.state.Robots[0].Battery = tc.battery
			s.unresponsiveTicks["R1"] = 2
			want := s.Snapshot()
			want.Robots[0].Battery = tc.want
			want.Faults = []FaultState{{ID: "F1", Type: FaultLowBattery, Robots: []string{"R1"}, Tick: 0}}
			requireInjection(t, s.InjectLowBattery("R1"))
			if !reflect.DeepEqual(s.Snapshot(), want) || s.unresponsiveTicks["R1"] != 2 {
				t.Fatalf("low battery changed unexpected fields: %+v", s.Snapshot())
			}
			assertInvariants(t, s, cfg)
		})
	}
}

func TestFaultRecordsAndOwnership(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AutoDispatch = false
	s := newTestSim(t, cfg, 1)
	s.Tick()
	before, countdowns := s.Snapshot(), maps.Clone(s.unresponsiveTicks)
	requireInjection(t, s.InjectUnresponsive("R1"))
	wantFaults := []FaultState{{ID: "F1", Type: FaultRobotUnresponsive, Robots: []string{"R1"}, Tick: 1}}
	before.Faults = wantFaults
	countdowns["R1"] = cfg.UnresponsiveDelay
	if !reflect.DeepEqual(s.Snapshot(), before) || !reflect.DeepEqual(s.unresponsiveTicks, countdowns) {
		t.Fatal("unresponsive injection changed more than countdown and fault record")
	}
	assertInjectionFailure(t, s, func() error { return s.InjectUnresponsive("R1") }, ErrRobotUnavailable)
	for i := 2; i <= 10; i++ {
		before = s.Snapshot()
		aisle := (i - 2) % len(cfg.AisleColumns)
		requireInjection(t, s.InjectAisleConflict("R2", "R1", aisle))
		wantFaults = append(wantFaults, FaultState{
			ID: fmt.Sprintf("F%d", i), Type: FaultAisleConflict, Robots: []string{"R2", "R1"}, Aisle: &aisle, Tick: 1,
		})
		before.Faults = wantFaults
		if !reflect.DeepEqual(s.Snapshot(), before) || !reflect.DeepEqual(s.unresponsiveTicks, countdowns) {
			t.Fatal("aisle conflict changed physical state, jobs, countdowns, or fault contents")
		}
	}
	snapshot, retained := s.Snapshot(), s.Snapshot()
	if !reflect.DeepEqual(snapshot.Faults, wantFaults) {
		t.Fatalf("fault records = %+v, want %+v", snapshot.Faults, wantFaults)
	}
	encoded, err := json.Marshal(snapshot.Faults[:2])
	if err != nil {
		t.Fatal(err)
	}
	wantJSON := `[{"id":"F1","type":"robot_unresponsive","robots":["R1"],"tick":1},{"id":"F2","type":"aisle_conflict","robots":["R2","R1"],"aisle":0,"tick":1}]`
	if string(encoded) != wantJSON {
		t.Fatalf("optional aisle JSON = %s", encoded)
	}
	snapshot.Faults[0].Robots[0] = "changed"
	snapshot.Faults[1].Robots[0] = "changed"
	*snapshot.Faults[1].Aisle = 99
	if !reflect.DeepEqual(s.Snapshot().Faults, wantFaults) || !reflect.DeepEqual(retained.Faults, wantFaults) {
		t.Fatal("fault snapshot aliases internal or retained snapshot data")
	}
	assertInvariants(t, s, cfg)
}

func TestFaultPublicCountdownPrecedence(t *testing.T) {
	cfg := DefaultConfig()
	cfg.UnresponsiveDelay = 20
	s := newTestSim(t, cfg, 1)
	setJobCells(t, s, "J1", Point{1, 5}, Point{19, 5})
	s.Tick()
	if want := (RobotState{"R1", 1, 10, 99, RobotMoving, "J1"}); s.state.Robots[0] != want {
		t.Fatalf("tick 1: %+v, want %+v", s.state.Robots[0], want)
	}
	requireInjection(t, s.InjectLowBattery("R1"))
	requireInjection(t, s.InjectUnresponsive("R1"))
	wantFaults := []FaultState{
		{ID: "F1", Type: FaultLowBattery, Robots: []string{"R1"}, Tick: 1},
		{ID: "F2", Type: FaultRobotUnresponsive, Robots: []string{"R1"}, Tick: 1},
	}
	if s.state.Robots[0].Battery != 15 || s.unresponsiveTicks["R1"] != 20 || !reflect.DeepEqual(s.state.Faults, wantFaults) {
		t.Fatal("incorrect public injection results at tick 1")
	}
	assertInvariants(t, s, cfg)
	for tick := 2; tick <= 40; tick++ {
		s.Tick()
		want := RobotState{"R1", 1, 11 - tick, max(0, 16-tick), RobotMoving, "J1"}
		jobStatus := JobAssigned
		if tick >= 6 {
			want.X, want.Y, jobStatus = 1+min(tick-6, 10), 5, JobInProgress
		}
		if tick >= 16 {
			want.Status = RobotFaulted
		}
		if tick >= 21 {
			want.Status = RobotOffline
		}
		job := s.state.Jobs[0]
		if s.state.Robots[0] != want || s.unresponsiveTicks["R1"] != max(0, 21-tick) || job.Status != jobStatus || job.AssignedRobot != "R1" || !reflect.DeepEqual(s.state.Faults, wantFaults) {
			t.Fatalf("tick %d: robot %+v, job %+v, countdown %d, faults %+v", tick, s.state.Robots[0], job, s.unresponsiveTicks["R1"], s.state.Faults)
		}
		if tick == 16 || tick == 21 {
			assertInjectionFailure(t, s, func() error { return s.InjectUnresponsive("R1") }, ErrRobotUnavailable)
			assertInjectionFailure(t, s, func() error { return s.InjectLowBattery("R1") }, ErrRobotUnavailable)
		}
		if tick == 6 || tick == 15 || tick == 16 || tick == 20 || tick == 21 || tick == 40 {
			t.Logf("tick %d: %+v countdown=%d job=%s faults=%d", tick, s.state.Robots[0], s.unresponsiveTicks["R1"], job.Status, len(s.state.Faults))
		}
		assertInvariants(t, s, cfg)
	}
}

func TestFaultShortJobBelowAssignmentThreshold(t *testing.T) {
	cfg := DefaultConfig()
	s := newTestSim(t, cfg, 1)
	setJobCells(t, s, "J1", Point{1, 10}, Point{1, 9})
	s.Tick()
	requireInjection(t, s.InjectLowBattery("R1"))
	assertInvariants(t, s, cfg)
	for tick := 2; tick <= 25; tick++ {
		s.Tick()
		want := RobotState{"R1", 1, 9, 14 - tick/20, RobotIdle, ""}
		if s.state.Robots[0] != want || s.state.Jobs[0].Status != JobDone || s.state.Jobs[0].AssignedRobot != "R1" || len(s.state.Faults) != 1 {
			t.Fatalf("tick %d: short job completion or low-battery idle state changed: %+v", tick, s.state.Robots[0])
		}
		for _, job := range s.state.Jobs[1:] {
			if job.AssignedRobot == "R1" {
				t.Fatalf("R1 accepted another job below threshold: %+v", job)
			}
		}
		assertInvariants(t, s, cfg)
	}
}

func TestFaultScheduledInjectionInvariants(t *testing.T) {
	cfg := cfgRobotOnStation()
	s := newTestSim(t, cfg, 2)
	if err := s.SetCharging("R1", "S1"); err != nil {
		t.Fatal(err)
	}
	requireInjection(t, s.InjectLowBattery("R1"))
	wantFaults := []FaultState{{ID: "F1", Type: FaultLowBattery, Robots: []string{"R1"}, Tick: 0}}
	assertInvariants(t, s, cfg)
	for tick := 1; tick <= 100; tick++ {
		switch s.state.Tick {
		case 1:
			requireInjection(t, s.InjectUnresponsive("R1"))
			wantFaults = append(wantFaults, FaultState{ID: "F2", Type: FaultRobotUnresponsive, Robots: []string{"R1"}, Tick: 1})
		case 2:
			requireInjection(t, s.InjectAisleConflict("R2", "R3", 0))
			aisle := 0
			wantFaults = append(wantFaults, FaultState{ID: "F3", Type: FaultAisleConflict, Robots: []string{"R2", "R3"}, Aisle: &aisle, Tick: 2})
		case 5:
			requireInjection(t, s.InjectLowBattery("R4"))
			wantFaults = append(wantFaults, FaultState{ID: "F4", Type: FaultLowBattery, Robots: []string{"R4"}, Tick: 5})
		}
		s.Tick()
		want := RobotState{ID: "R1", Battery: 15 + 2*min(tick, 3), Status: RobotCharging}
		if tick >= 4 {
			want.Status = RobotOffline
		}
		if s.state.Robots[0] != want || s.state.Stations[0].OccupiedBy != "R1" || !reflect.DeepEqual(s.state.Faults, wantFaults) {
			t.Fatalf("tick %d: charging/offline retention or fault records changed", tick)
		}
		assertInvariants(t, s, cfg)
	}
	assertChargingFailure(t, s, "R2", "S1", ErrStationOccupied)
}
