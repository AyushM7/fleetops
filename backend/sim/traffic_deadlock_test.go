package sim

import "testing"

func TestWarehouseV2Seed1PocketRetreatKeepsExitIntegration(t *testing.T) {
	cfg := WarehouseV2Config()
	cfg.AutoDispatch, cfg.InitialJobs, cfg.NewJobEvery = false, 0, 10000
	s := newTestSim(t, cfg, 1)
	s.state.Tick = 2825
	s.state.Jobs = []JobState{{ID: "J202", From: Point{12, 1}, To: Point{11, 0}, Status: JobInProgress, AssignedRobot: "R7"}}
	r7, r8 := &s.state.Robots[6], &s.state.Robots[7]
	r7.X, r7.Y, r7.Battery, r7.Status, r7.JobID, r7.WaitTicks, r7.Blocked = 12, 1, 5, RobotMoving, "J202", 14, true
	r8.X, r8.Y, r8.Battery, r8.Status, r8.WaitTicks, r8.Blocked = 11, 1, 18, RobotMoving, 14, true
	s.navigation["R7"] = robotNavigation{target: Point{11, 0}, route: []Point{{11, 1}, {11, 0}}}
	s.navigation["R8"] = robotNavigation{target: Point{14, 0}, route: []Point{{12, 1}, {13, 1}, {13, 0}, {14, 0}}, relocation: true}
	r3 := &s.state.Robots[2]
	r3.X, r3.Y, r3.Battery, r3.Status = 12, 0, 10, RobotCharging
	s.state.Stations[2].OccupiedBy = r3.ID

	reachedStaging := false
	for range 40 {
		before := s.Snapshot()
		s.Tick()
		after := s.Snapshot()
		assertInvariants(t, s, cfg)
		assertV2TrafficStep(t, before, after)
		reachedStaging = reachedStaging || r8.X < 11 && (r8.Status == RobotIdle || r8.Status == RobotCharging)
	}
	if !reachedStaging || s.state.Jobs[0].Status != JobDone {
		t.Fatalf("relocating robot failed to reach staging or transport failed to deliver: R7=%+v R8=%+v cycles=%+v",
			*r7, *r8, s.state.TrafficDeadlocks)
	}
}

func TestWarehouseV2Seed2KeepsFarMouthClearIntegration(t *testing.T) {
	cfg := WarehouseV2Config()
	cfg.AutoDispatch, cfg.InitialJobs, cfg.NewJobEvery = false, 0, 10000
	s := newTestSim(t, cfg, 2)
	s.state.Tick = 3561
	s.state.Jobs = []JobState{
		{ID: "J249", From: Point{11, 1}, To: Point{4, 0}, Status: JobInProgress, AssignedRobot: "R5"},
		{ID: "J255", From: Point{4, 1}, To: Point{17, 0}, Status: JobInProgress, AssignedRobot: "R2"},
	}
	r2, r5, r10 := &s.state.Robots[1], &s.state.Robots[4], &s.state.Robots[9]
	r2.X, r2.Y, r2.Battery, r2.Status, r2.JobID, r2.WaitTicks = 9, 1, 51, RobotMoving, "J255", cfg.ReplanAfter-1
	r5.X, r5.Y, r5.Battery, r5.Status, r5.JobID = 11, 1, 11, RobotMoving, "J249"
	r10.X, r10.Y, r10.Battery, r10.Status = 9, 11, 15, RobotMoving
	s.state.Stations[2].ReservedBy = "R10"
	s.navigation["R2"] = robotNavigation{target: Point{17, 0}, route: []Point{{10, 1}, {11, 1}, {12, 1}, {13, 1}, {13, 0}, {14, 0}, {15, 0}, {16, 0}, {17, 0}}}
	s.navigation["R5"] = robotNavigation{target: Point{4, 0}, route: []Point{{10, 1}, {9, 1}, {8, 1}, {7, 1}, {6, 1}, {5, 1}, {5, 0}, {4, 0}}}
	s.navigation["R10"] = robotNavigation{target: Point{12, 0}, route: []Point{
		{9, 10}, {9, 9}, {9, 8}, {9, 7}, {9, 6}, {9, 5}, {9, 4}, {9, 3}, {9, 2}, {9, 1}, {10, 1}, {11, 1}, {11, 0}, {12, 0},
	}}
	s.Tick()
	if r10.Y != 11 || s.navigation["R2"].route[0] != (Point{9, 2}) {
		t.Fatalf("R10 entered the south mouth while R2 replanned: R10=%+v R2 route=%v", *r10, s.navigation["R2"].route)
	}
	docked := false
	for range 50 {
		before := s.Snapshot()
		s.Tick()
		after := s.Snapshot()
		assertInvariants(t, s, cfg)
		assertV2TrafficStep(t, before, after)
		docked = docked || s.state.Stations[2].OccupiedBy == r10.ID
	}
	if s.state.Jobs[0].Status != JobDone || s.state.Jobs[1].Status != JobDone || !docked {
		t.Fatalf("transport or charging progress failed: R2=%+v R5=%+v R10=%+v cycles=%+v", *r2, *r5, *r10, s.state.TrafficDeadlocks)
	}
}
