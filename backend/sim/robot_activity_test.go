package sim

import (
	"errors"
	"testing"
)

func TestWarehouseV2TransportActivities(t *testing.T) {
	cfg := WarehouseV2Config()
	cfg.InitialJobs = 0
	s := newTestSim(t, cfg, 42)
	s.state.Jobs = []JobState{{ID: "J1", From: Point{0, 10}, To: Point{0, 8}, Status: JobPending}}

	for tick, want := range []struct {
		activity RobotActivity
		job      JobStatus
		position Point
	}{
		{ActivityParked, JobPending, Point{0, 12}},
		{ActivityGoingToPickup, JobAssigned, Point{0, 11}},
		{ActivityPickingUp, JobAssigned, Point{0, 10}},
		{ActivityDelivering, JobInProgress, Point{0, 10}},
		{ActivityDelivering, JobInProgress, Point{0, 9}},
		{ActivityAvailable, JobDone, Point{0, 8}},
	} {
		state := s.Snapshot()
		robot := state.Robots[0]
		if robot.Activity != want.activity || state.Jobs[0].Status != want.job || (Point{robot.X, robot.Y}) != want.position {
			t.Fatalf("tick %d: robot %+v, job %+v, want %+v", tick, robot, state.Jobs[0], want)
		}
		s.Tick()
	}
}

func TestWarehouseV2MaintenanceChargeAndOfflineActivities(t *testing.T) {
	cfg := WarehouseV2Config()
	cfg.InitialJobs = 0
	s := newTestSim(t, cfg, 42)
	if err := s.InjectLowBattery("R11"); !errors.Is(err, ErrUnknownRobot) {
		t.Fatalf("retired robot ID is still live: %v", err)
	}
	requireInjection(t, s.InjectLowBattery("M1"))
	if state := s.Snapshot(); state.Robots[10].Battery != cfg.LowBatteryInjectLevel || state.Faults[0].Robots[0] != "M1" {
		t.Fatalf("low battery did not target M1: %+v, %+v", state.Robots[10], state.Faults)
	}
	s.Tick()
	if robot := s.Snapshot().Robots[10]; robot.Activity != ActivityGoingToCharge {
		t.Fatalf("M1 should travel to charge: %+v", robot)
	}
	for range cfg.Width {
		if s.Snapshot().Robots[10].Activity == ActivityCharging {
			break
		}
		s.Tick()
	}
	if robot := s.Snapshot().Robots[10]; robot.Activity != ActivityCharging {
		t.Fatalf("M1 should reach its charger: %+v", robot)
	}

	s = newTestSim(t, cfg, 42)
	requireInjection(t, s.InjectUnresponsive("M1"))
	for range cfg.UnresponsiveDelay + 1 {
		s.Tick()
	}
	state := s.Snapshot()
	if robot := state.Robots[10]; robot.ID != "M1" || robot.Status != RobotOffline || robot.Activity != ActivityOutOfService ||
		robot.Role != RoleMaintenance || (Point{robot.X, robot.Y}) != *cfg.MaintenanceBay || state.Faults[0].Robots[0] != "M1" {
		t.Fatalf("M1 should remain out of service in its bay: %+v, %+v", robot, state.Faults)
	}
}

func TestWarehouseV2MaintenanceBatteryDepletion(t *testing.T) {
	cfg := WarehouseV2Config()
	cfg.InitialJobs, cfg.AutoCharge, cfg.Recovery, cfg.IdleDrainEvery = 0, false, false, 1
	s := newTestSim(t, cfg, 42)
	s.faultRobot("M1").Battery = 1
	s.Tick()
	if robot := s.Snapshot().Robots[10]; robot.ID != "M1" || robot.Battery != 0 || robot.Status != RobotFaulted || robot.Activity != ActivityFaulted {
		t.Fatalf("M1 should report the existing battery fault state: %+v", robot)
	}
}
