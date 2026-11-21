package sim

import (
	"errors"
	"reflect"
	"testing"
)

func breakdownConfig() Config {
	cfg := WarehouseV2Config()
	cfg.InitialJobs, cfg.AutoDispatch, cfg.NewJobEvery = 0, false, 10000
	cfg.MaintenanceRepairTicks = 0
	return cfg
}

func TestWarehouseV2MotorBreakdownPreservesPickupAssignment(t *testing.T) {
	cfg := breakdownConfig()
	s := newTestSim(t, cfg, 42)
	s.state.Jobs = []JobState{{ID: "J1", Type: JobNormal, From: Point{0, 10}, To: Point{0, 8}, Status: JobPending}}
	if err := s.AssignJobBatch(AssignJobBatchRequest{Assignments: []JobAssignment{{JobID: "J1", RobotID: "R1"}}}); err != nil {
		t.Fatal(err)
	}
	s.Tick()
	before := s.Snapshot()
	requireInjection(t, s.injectBreakdown("R1", FaultMotorFailure))
	want := before.Robots[0]
	want.Status, want.Activity, want.ActiveFault = RobotFaulted, ActivityFaulted, FaultMotorFailure
	if before.Robots[0].Activity != ActivityGoingToPickup {
		t.Fatal(before.Robots[0])
	}
	for range cfg.BlockedAfter + 1 {
		s.Tick()
		assertInvariants(t, s, cfg)
	}
	after := s.Snapshot()
	if after.Robots[0] != want || after.Jobs[0].Status != JobAssigned || after.Jobs[0].AssignedRobot != "R1" || after.Jobs[0].CargoRobotID != "" {
		t.Fatalf("pickup assignment changed after breakdown: before=%+v after=%+v job=%+v", before.Robots[0], after.Robots[0], after.Jobs[0])
	}
	if got := after.Faults; !reflect.DeepEqual(got, []FaultState{{ID: "F1", Type: FaultMotorFailure, Robots: []string{"R1"}, Tick: before.Tick}}) {
		t.Fatal(got)
	}
	if after.Robots[10] != before.Robots[10] {
		t.Fatalf("M1 changed when R1 stopped: %+v", after.Robots[10])
	}
	if err := s.ReassignUnstartedJob("J1", "R1", "R2"); !errors.Is(err, ErrReassignment) {
		t.Fatalf("failed robot unexpectedly transferred its assignment: %v", err)
	}
}

func TestWarehouseV2SensorBreakdownKeepsCargoAndBlocksAisle(t *testing.T) {
	cfg := breakdownConfig()
	s := newTestSim(t, cfg, 42)
	s.state.Jobs = []JobState{
		{ID: "J1", Type: JobNormal, From: Point{4, 3}, To: Point{4, 8}, Status: JobInProgress, AssignedRobot: "R1"},
		{ID: "J2", Type: JobNormal, From: Point{4, 6}, To: Point{4, 10}, Status: JobAssigned, AssignedRobot: "R2"},
	}
	carrier, follower := s.faultRobot("R1"), s.faultRobot("R2")
	carrier.X, carrier.Y, carrier.Status, carrier.JobID = 4, 4, RobotMoving, "J1"
	follower.X, follower.Y, follower.Status, follower.JobID = 4, 1, RobotMoving, "J2"
	s.prepareNavigation(carrier, s.state.Jobs[0].To)
	s.prepareNavigation(follower, s.state.Jobs[1].From)
	before := s.Snapshot()
	requireInjection(t, s.injectBreakdown("R1", FaultSensorFailure))
	for range cfg.RecoverAfter + 1 {
		s.Tick()
		assertInvariants(t, s, cfg)
	}
	after := s.Snapshot()
	robot, waiting, job := after.Robots[0], after.Robots[1], after.Jobs[0]
	if robot.X != before.Robots[0].X || robot.Y != before.Robots[0].Y || robot.Battery != before.Robots[0].Battery ||
		robot.Status != RobotFaulted || robot.ActiveFault != FaultSensorFailure || robot.JobID != "J1" ||
		job.Status != JobInProgress || job.AssignedRobot != "R1" || job.CargoRobotID != "R1" {
		t.Fatalf("sensor breakdown changed position or cargo custody: robot=%+v job=%+v", robot, job)
	}
	if waiting.X != 4 || waiting.Y != 1 || !waiting.Blocked || after.Aisles[0].OccupiedBy != "R1" {
		t.Fatalf("follower did not respect the occupied aisle: robot=%+v aisle=%+v", waiting, after.Aisles[0])
	}
	if err := s.ReassignUnstartedJob("J1", "R1", "R2"); !errors.Is(err, ErrReassignment) {
		t.Fatalf("failed carrier unexpectedly transferred cargo: %v", err)
	}
}

func TestWarehouseV2BreakdownRetainsDockAndReleasesUnusedReservation(t *testing.T) {
	cfg := breakdownConfig()
	t.Run("occupied", func(t *testing.T) {
		s := newTestSim(t, cfg, 42)
		robot, station := s.faultRobot("R1"), s.state.Stations[0]
		robot.X, robot.Y, robot.Battery = station.X, station.Y, 15
		if err := s.SetCharging(robot.ID, station.ID); err != nil {
			t.Fatal(err)
		}
		requireInjection(t, s.InjectUnresponsive("R1"))
		for tick := 0; tick <= cfg.UnresponsiveDelay+1; tick++ {
			snap := s.Snapshot()
			stopped := snap.Robots[0]
			want := RobotFaulted
			if tick >= cfg.UnresponsiveDelay {
				want = RobotOffline
			}
			if stopped.Status != want || stopped.ActiveFault != FaultRobotUnresponsive || stopped.Battery != 15 ||
				stopped.X != station.X || stopped.Y != station.Y || snap.Stations[0].OccupiedBy != stopped.ID || len(snap.Faults) != 1 {
				t.Fatalf("tick %d: stopped robot or occupied dock changed: %+v, %+v", tick, stopped, snap.Stations[0])
			}
			assertInvariants(t, s, cfg)
			s.Tick()
		}
	})
	t.Run("reserved", func(t *testing.T) {
		s := newTestSim(t, cfg, 42)
		requireInjection(t, s.InjectLowBattery("M1"))
		s.Tick()
		if s.reservedStation("M1") == nil {
			t.Fatal("expected M1 to reserve a charger")
		}
		requireInjection(t, s.injectBreakdown("M1", FaultMotorFailure))
		_, hasRoute := s.navigation["M1"]
		if hasRoute || s.reservedStation("M1") != nil || s.Snapshot().Robots[10].ActiveFault != FaultMotorFailure {
			t.Fatal("failed M1 retained an executable route or unused reservation")
		}
		assertInvariants(t, s, cfg)
	})
}

func TestWarehouseV2DepletionCreatesOneCriticalBatteryOccurrence(t *testing.T) {
	cfg := breakdownConfig()
	s := newTestSim(t, cfg, 42)
	s.state.Jobs = []JobState{{ID: "J1", Type: JobNormal, From: Point{0, 10}, To: Point{0, 8}, Status: JobPending}}
	if err := s.AssignJobBatch(AssignJobBatchRequest{Assignments: []JobAssignment{{JobID: "J1", RobotID: "R1"}}}); err != nil {
		t.Fatal(err)
	}
	s.faultRobot("R1").Battery = cfg.MovingDrain
	s.Tick()
	for range 4 {
		s.Tick()
	}
	assertInvariants(t, s, cfg)
	got := s.Snapshot()
	r := got.Robots[0]
	if r.Status != RobotFaulted || r.ActiveFault != FaultCriticalBattery || r.Battery != 0 || r.X != 0 || r.Y != 11 || r.JobID != "J1" ||
		!reflect.DeepEqual(got.Faults, []FaultState{{ID: "F1", Type: FaultCriticalBattery, Robots: []string{"R1"}, Tick: 1}}) {
		t.Fatalf("unexpected natural depletion state: robot=%+v history=%+v", r, got.Faults)
	}
}

func TestWarehouseV2ZeroBatteryInjectionCannotEnterStandby(t *testing.T) {
	cfg := breakdownConfig()
	cfg.LowBatteryInjectLevel = 0
	s := newTestSim(t, cfg, 42)
	before := s.Snapshot().Robots[0]
	requireInjection(t, s.InjectLowBattery("R1"))
	s.Tick()
	state := s.Snapshot()
	after := state.Robots[0]
	if after.Status != RobotFaulted || after.ActiveFault != FaultCriticalBattery || after.Battery != 0 ||
		after.X != before.X || after.Y != before.Y || len(state.Faults) != 2 ||
		state.Faults[0].Type != FaultLowBattery || state.Faults[1].Type != FaultCriticalBattery ||
		state.Faults[1].Tick != 0 {
		t.Fatalf("zero battery did not stop R1 at injection: robot=%+v history=%+v", after, state.Faults)
	}
	assertInvariants(t, s, cfg)
}

func TestWarehouseV2ScheduledBreakdownValidationAndFirstCause(t *testing.T) {
	entry := ScheduledBreakdown{Tick: 0, RobotID: "R1", Type: FaultMotorFailure}
	for _, test := range []struct {
		name     string
		scenario []ScheduledBreakdown
	}{
		{"negative tick", []ScheduledBreakdown{{Tick: -1, RobotID: "R1", Type: FaultMotorFailure}}},
		{"retired ID", []ScheduledBreakdown{{RobotID: "R11", Type: FaultSensorFailure}}},
		{"unknown fault", []ScheduledBreakdown{{RobotID: "R1", Type: FaultAisleConflict}}},
		{"duplicate robot", []ScheduledBreakdown{entry, entry}},
		{"out of order", []ScheduledBreakdown{{Tick: 2, RobotID: "R2", Type: FaultMotorFailure}, entry}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := breakdownConfig()
			cfg.ScheduledBreakdowns = test.scenario
			if _, err := New(cfg, 42); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("invalid schedule accepted: %v", err)
			}
		})
	}
	legacy := DefaultConfig()
	legacy.ScheduledBreakdowns = []ScheduledBreakdown{entry}
	if _, err := New(legacy, 42); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("legacy layout accepted a V2 schedule: %v", err)
	}
	cfg := breakdownConfig()
	cfg.AutoDispatch = true
	cfg.ScheduledBreakdowns = []ScheduledBreakdown{{Tick: 1, RobotID: "R1", Type: FaultMotorFailure}}
	s := newTestSim(t, cfg, 42)
	cfg.ScheduledBreakdowns[0].RobotID = "R2"
	requireInjection(t, s.injectBreakdown("R1", FaultSensorFailure))
	s.state.Jobs = []JobState{{ID: "J1", Type: JobNormal, From: Point{0, 10}, To: Point{0, 8}, Status: JobPending}}
	if err := s.AssignJobBatch(AssignJobBatchRequest{Assignments: []JobAssignment{{JobID: "J1", RobotID: "R1"}}}); !errors.Is(err, ErrAssignmentConflict) {
		t.Fatalf("disabled idle robot admitted for dispatch: %v", err)
	}
	s.Tick()
	snap := s.Snapshot()
	if snap.Robots[0].ActiveFault != FaultSensorFailure || snap.Robots[0].JobID != "" ||
		snap.Robots[1].ActiveFault != "" || snap.Jobs[0].AssignedRobot == "" || snap.Jobs[0].AssignedRobot == "R1" ||
		len(snap.Faults) != 1 || snap.Faults[0].Tick != 0 || snap.Faults[0].Type != FaultSensorFailure {
		t.Fatalf("scenario overwrote first failure or dispatched failed robot: %+v %+v", snap.Robots[:2], snap.Faults)
	}
}
