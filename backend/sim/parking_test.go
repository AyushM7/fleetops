package sim

import "testing"

func TestWarehouseV2LowBatteryWaitsInStandbyWhenChargersBlocked(t *testing.T) {
	cfg := WarehouseV2Config()
	cfg.InitialJobs = 0
	s := newTestSim(t, cfg, 42)
	for i, station := range s.state.Stations {
		blocker := &s.state.Robots[i+1]
		blocker.X, blocker.Y, blocker.Status = station.X, station.Y, RobotOffline
	}
	robot := &s.state.Robots[0]
	robot.Battery = cfg.ChargeBelow
	for range 2 * cfg.IdleDrainEvery {
		s.Tick()
	}
	if robot.Battery != cfg.ChargeBelow || robot.Status != RobotIdle {
		t.Fatalf("waiting for an unblocked charger consumed the robot's energy: %+v", robot)
	}
}

func TestWarehouseV2ChargesAndUndocksBeforeReturningToBay(t *testing.T) {
	cfg := WarehouseV2Config()
	cfg.InitialJobs, cfg.AutoDispatch = 0, false
	s := newTestSim(t, cfg, 42)
	requireInjection(t, s.InjectLowBattery("M1"))

	var charger StationState
	for range cfg.Width {
		s.Tick()
		assertInvariants(t, s, cfg)
		if station := s.occupiedStation("M1"); station != nil {
			charger = *station
			break
		}
	}
	if charger.ID != "C2" || charger.OccupiedBy != "M1" || charger.ReservedBy != "" || s.Snapshot().Robots[10].Activity != ActivityCharging {
		t.Fatalf("M1 did not dock at the nearest charger with deterministic tie breaking: %+v", charger)
	}
	robot := &s.state.Robots[10]
	for range cfg.ChargeRelease {
		if robot.Status != RobotCharging {
			break
		}
		before := robot.Battery
		s.Tick()
		if robot.Battery != min(cfg.ChargeRelease, before+cfg.ChargeGain) {
			t.Fatalf("charging did not apply tick gain: before %d, after %d", before, robot.Battery)
		}
	}
	if state := s.Snapshot().Robots[10]; state.Activity != ActivityUndocking || s.occupiedStation("M1") == nil {
		t.Fatalf("reaching the release threshold freed an occupied dock: %+v", state)
	}
	s.Tick()
	assertInvariants(t, s, cfg)
	if state := s.Snapshot().Robots[10]; state.Activity != ActivityGoingToPark || s.occupiedStation("M1") != nil {
		t.Fatalf("physical undocking should release the charger and begin return: %+v", state)
	}
	for range cfg.Width {
		s.Tick()
		assertInvariants(t, s, cfg)
	}
	if state := s.Snapshot().Robots[10]; state.Activity != ActivityParked || (Point{state.X, state.Y}) != *cfg.MaintenanceBay {
		t.Fatalf("M1 did not return to its dedicated bay: %+v", state)
	}
}

func TestWarehouseV2ParkingGraceAndDeparture(t *testing.T) {
	cfg := WarehouseV2Config()
	cfg.InitialJobs = 0
	cfg.NewJobEvery = 2 * cfg.ParkingGraceTicks
	s := newTestSim(t, cfg, 42)
	robot := &s.state.Robots[0]
	robot.Y = 10
	for range cfg.ParkingGraceTicks - 1 {
		s.Tick()
		if robot.Y != 10 || robot.Status != RobotIdle {
			t.Fatal("robot moved before the parking grace interval elapsed")
		}
	}
	s.Tick()
	if state := s.Snapshot().Robots[0]; state.Activity != ActivityGoingToPark || state.Y != 11 {
		t.Fatalf("expected first physical step to the bay: %+v", state)
	}
	s.Tick()
	if state := s.Snapshot().Robots[0]; state.Activity != ActivityParked || state.Y != 12 {
		t.Fatalf("expected arrival at the bay: %+v", state)
	}
	s.state.Jobs = []JobState{{ID: "J1", From: Point{0, 10}, To: Point{0, 8}, Status: JobPending}}
	s.Tick()
	if state := s.Snapshot().Robots[0]; state.Activity != ActivityGoingToPickup || state.JobID != "J1" || state.Y != 11 {
		t.Fatalf("parked robot could not depart for a new job: %+v", state)
	}
}

func TestWarehouseV2LimitsChargingReservations(t *testing.T) {
	cfg := WarehouseV2Config()
	cfg.InitialJobs = 0
	s := newTestSim(t, cfg, 42)
	for i := range s.state.Robots {
		s.state.Robots[i].Battery = cfg.ChargeBelow
	}
	s.state.Jobs = []JobState{{ID: "J1", From: Point{0, 10}, To: Point{0, 8}, Status: JobPending}}
	s.Tick()
	state := s.Snapshot()
	assertInvariants(t, s, cfg)
	parked := 0
	for _, robot := range state.Robots {
		if robot.Activity == ActivityParked {
			parked++
		}
	}
	if parked != 7 || state.Jobs[0].Status != JobPending {
		t.Fatalf("expected four charger trips, seven robots waiting in bays and no transport assignment: %+v", state.Robots)
	}
	for _, station := range state.Stations {
		if station.ReservedBy == "" {
			t.Fatalf("unreserved charger despite eligible waiters: %+v", station)
		}
	}
}

func TestWarehouseV2SwapsCrossingChargerReservations(t *testing.T) {
	cfg := WarehouseV2Config()
	cfg.InitialJobs, cfg.AutoDispatch = 0, false
	s := newTestSim(t, cfg, 42)
	left, right := &s.state.Robots[0], &s.state.Robots[1]
	left.X, left.Y, left.Status, left.Battery = 7, 1, RobotMoving, 15
	right.X, right.Y, right.Status, right.Battery = 9, 1, RobotMoving, 15
	left.WaitTicks, left.Blocked = cfg.RecoverAfter, true
	s.state.Stations[1].ReservedBy, s.state.Stations[2].ReservedBy = right.ID, left.ID
	s.navigation[left.ID] = robotNavigation{target: Point{12, 0}, route: []Point{{8, 1}, {9, 1}, {10, 1}, {11, 1}, {11, 0}, {12, 0}}}
	s.navigation[right.ID] = robotNavigation{target: Point{6, 0}, route: []Point{{8, 1}, {7, 1}, {7, 0}, {6, 0}}}
	s.Tick()
	assertInvariants(t, s, cfg)
	if s.state.Stations[1].ReservedBy != left.ID || s.state.Stations[2].ReservedBy != right.ID {
		t.Fatalf("crossing charger trips failed to swap reservations: %+v", s.state.Stations)
	}
}

func TestWarehouseV2DispatchBudgetsMissionAndChargingExit(t *testing.T) {
	cfg := WarehouseV2Config()
	cfg.InitialJobs = 0
	routeTest := newTestSim(t, cfg, 42)
	job := JobState{ID: "J1", From: Point{19, 11}, To: Point{0, 1}, Status: JobPending}
	budget := cfg.ReserveBattery
	for _, leg := range [][2]Point{{cfg.RobotStarts[0], job.From}, {job.From, job.To}, {job.To, cfg.Stations[0]}} {
		route, err := routeTest.PlanRoute(leg[0], leg[1])
		if err != nil {
			t.Fatal(err)
		}
		budget += len(route) * cfg.MovingDrain
	}
	for _, battery := range []int{budget - 1, budget} {
		s := newTestSim(t, cfg, 42)
		s.state.Jobs = []JobState{job}
		for i := 1; i < len(s.state.Robots); i++ {
			s.state.Robots[i].Status = RobotOffline
		}
		s.state.Robots[0].Battery = battery
		s.Tick()
		if assigned := s.Snapshot().Jobs[0].AssignedRobot == "R1"; assigned != (battery == budget) {
			t.Fatalf("battery %d, required %d: assignment = %v", battery, budget, assigned)
		}
	}
}
