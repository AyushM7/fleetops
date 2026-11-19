package sim

import "testing"

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
	requireInjection(t, s.InjectLowBattery("R11"))
	s.Tick()
	if robot := s.Snapshot().Robots[10]; robot.Activity != ActivityGoingToCharge {
		t.Fatalf("R11 should travel to charge: %+v", robot)
	}
	for range cfg.Width {
		if s.Snapshot().Robots[10].Activity == ActivityCharging {
			break
		}
		s.Tick()
	}
	if robot := s.Snapshot().Robots[10]; robot.Activity != ActivityCharging {
		t.Fatalf("R11 should reach its charger: %+v", robot)
	}

	s = newTestSim(t, cfg, 42)
	requireInjection(t, s.InjectUnresponsive("R11"))
	for range cfg.UnresponsiveDelay {
		s.Tick()
	}
	if robot := s.Snapshot().Robots[10]; robot.Activity != ActivityOutOfService || robot.Role != RoleMaintenance {
		t.Fatalf("R11 should be out of service: %+v", robot)
	}
}
