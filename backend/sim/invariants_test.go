package sim

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func checkInvariants(s State, cfg Config, navigation ...map[string]robotNavigation) error {
	if len(s.Jobs) > cfg.MaxJobs {
		return fmt.Errorf("I10: %d jobs exceed limit %d", len(s.Jobs), cfg.MaxJobs)
	}
	robots := make(map[string]RobotState, len(s.Robots))
	jobs := make(map[string]JobState, len(s.Jobs))
	for _, robot := range s.Robots {
		robots[robot.ID] = robot
	}
	for _, job := range s.Jobs {
		jobs[job.ID] = job
	}
	for _, robot := range s.Robots {
		if err := checkRobotInvariants(robot, cfg, jobs); err != nil {
			return err
		}
	}
	if err := checkTrafficInvariants(s.Robots, cfg); err != nil {
		return err
	}
	for _, job := range s.Jobs {
		if err := checkJobInvariants(job, robots); err != nil {
			return err
		}
	}
	if err := checkReservationInvariants(s, cfg, robots, navigation...); err != nil {
		return err
	}
	if err := checkTripInvariants(s, cfg, navigation...); err != nil {
		return err
	}
	if err := checkStationInvariants(s.Stations, robots); err != nil {
		return err
	}
	if err := checkFaultInvariants(s.Faults, s.Tick, robots); err != nil {
		return err
	}
	if err := checkEntityOrder(s); err != nil {
		return err
	}
	return checkAisleInvariants(s, cfg)
}

func checkTripInvariants(state State, cfg Config, navigation ...map[string]robotNavigation) error {
	for _, station := range state.Stations {
		if station.ReservedBy == "" {
			continue
		}
		for _, robot := range state.Robots {
			if robot.ID != station.ReservedBy && robot.X == station.X && robot.Y == station.Y {
				return fmt.Errorf("I20: %s reservation occupied by %s", station.ID, robot.ID)
			}
		}
	}
	if len(navigation) == 0 {
		return nil
	}
	protected := protectedCells(cfg)
	destinations := make(map[Point]string)
	for _, robot := range state.Robots {
		if robot.Status != RobotMoving || robot.JobID != "" {
			continue
		}
		nav, recorded := navigation[0][robot.ID]
		reservations := 0
		for _, station := range state.Stations {
			if station.ReservedBy == robot.ID {
				reservations++
				if !recorded || nav.target != (Point{X: station.X, Y: station.Y}) {
					return fmt.Errorf("I19: %s charging route lost destination", robot.ID)
				}
			}
		}
		if !recorded || nav.relocation && reservations != 0 || !nav.relocation && reservations != 1 {
			return fmt.Errorf("I19: %s missing or ambiguous trip", robot.ID)
		}
		if nav.relocation {
			if protected[nav.target] || destinations[nav.target] != "" {
				return fmt.Errorf("I19: %s protected or duplicate relocation target", robot.ID)
			}
			destinations[nav.target] = robot.ID
		}
	}
	return nil
}

func checkRobotInvariants(robot RobotState, cfg Config, jobs map[string]JobState) error {
	if robot.Battery < 0 || robot.Battery > 100 {
		return fmt.Errorf("I1: %s battery %d", robot.ID, robot.Battery)
	}
	if !cfg.InBounds(Point{robot.X, robot.Y}) || cfg.CellTypeAt(Point{robot.X, robot.Y}) == CellWall {
		return fmt.Errorf("I2: %s outside %dx%d grid", robot.ID, cfg.Width, cfg.Height)
	}
	if !slices.Contains([]RobotStatus{RobotIdle, RobotMoving, RobotCharging, RobotFaulted, RobotOffline}, robot.Status) {
		return fmt.Errorf("I3: %s invalid robot status %q", robot.ID, robot.Status)
	}
	if robot.JobID != "" {
		job, exists := jobs[robot.JobID]
		if !exists || job.AssignedRobot != robot.ID || (job.Status != JobAssigned && job.Status != JobInProgress) {
			return fmt.Errorf("I5: %s does not own unfinished job %s", robot.ID, robot.JobID)
		}
	}
	switch robot.Status {
	case RobotIdle, RobotCharging:
		if robot.JobID != "" {
			return fmt.Errorf("I6: %s is %s with a job", robot.ID, robot.Status)
		}
	case RobotMoving:
		if robot.JobID == "" && !cfg.AutoCharge {
			return fmt.Errorf("I6: %s moving without a job", robot.ID)
		}
	case RobotFaulted:
		if robot.Battery != 0 {
			return fmt.Errorf("I6: %s faulted with battery %d", robot.ID, robot.Battery)
		}
	}
	if robot.WaitTicks < 0 || robot.Blocked != (robot.WaitTicks >= cfg.BlockedAfter) || robot.Status != RobotMoving && robot.WaitTicks != 0 {
		return fmt.Errorf("I14: %s inconsistent waiting state", robot.ID)
	}
	return nil
}

func checkReservationInvariants(s State, cfg Config, robots map[string]RobotState, navigation ...map[string]robotNavigation) error {
	relocating := make(map[string]bool)
	if len(navigation) > 0 {
		for id, nav := range navigation[0] {
			if !nav.relocation {
				continue
			}
			robot, exists := robots[id]
			arrived := len(nav.route) == 0 && nav.target == (Point{X: robot.X, Y: robot.Y})
			if !exists || !cfg.Recovery || robot.Status != RobotMoving || robot.JobID != "" || cfg.CellTypeAt(nav.target) != CellFree || !arrived && (len(nav.route) == 0 || nav.route[len(nav.route)-1] != nav.target) {
				return fmt.Errorf("I6: %s has invalid relocation trip", id)
			}
			previous := Point{X: robot.X, Y: robot.Y}
			for _, point := range nav.route {
				dx, dy := point.X-previous.X, point.Y-previous.Y
				if dx*dx+dy*dy != 1 || cfg.CellTypeAt(point) != CellFree && cfg.CellTypeAt(point) != CellAisle {
					return fmt.Errorf("I6: %s has invalid relocation route", id)
				}
				previous = point
			}
			relocating[id] = true
		}
	}
	reserved := make(map[string]bool)
	for _, station := range s.Stations {
		if station.ReservedBy == "" {
			continue
		}
		robot, exists := robots[station.ReservedBy]
		if station.OccupiedBy != "" || !exists || !cfg.AutoCharge || robot.Status != RobotMoving || robot.JobID != "" || reserved[robot.ID] || relocating[robot.ID] {
			return fmt.Errorf("I16: station %s has invalid or duplicate reservation %s", station.ID, station.ReservedBy)
		}
		reserved[robot.ID] = true
	}
	for _, robot := range s.Robots {
		if robot.Status == RobotMoving && robot.JobID == "" && !reserved[robot.ID] && !relocating[robot.ID] {
			return fmt.Errorf("I6: %s moving without a job or valid charging/relocation trip", robot.ID)
		}
	}
	return nil
}

func checkTrafficInvariants(robots []RobotState, cfg Config) error {
	cells := make(map[Point]string)
	aisles := make(map[int]string)
	for _, robot := range robots {
		position := Point{X: robot.X, Y: robot.Y}
		if other := cells[position]; other != "" {
			return fmt.Errorf("I12: %s and %s share %v", other, robot.ID, position)
		}
		cells[position] = robot.ID
		if cfg.onAisle(position) {
			if other := aisles[position.X]; other != "" {
				return fmt.Errorf("I13: %s and %s share aisle column %d", other, robot.ID, position.X)
			}
			aisles[position.X] = robot.ID
		}
	}
	return nil
}

func checkAisleInvariants(s State, cfg Config) error {
	if s.Aisles == nil || len(s.Aisles) != len(cfg.AisleColumns) {
		return fmt.Errorf("I15: aisle records do not match configured aisles")
	}
	for id, column := range cfg.AisleColumns {
		occupant := ""
		for _, robot := range s.Robots {
			if robot.X != column || robot.Y < cfg.AisleYMin || robot.Y > cfg.AisleYMax {
				continue
			}
			if occupant == "" || idNumber(robot.ID) < idNumber(occupant) {
				occupant = robot.ID
			}
		}
		if s.Aisles[id].ID != id || s.Aisles[id].OccupiedBy != occupant {
			return fmt.Errorf("I15: aisle %d does not match physical occupancy %q", id, occupant)
		}
	}
	return nil
}

func checkJobInvariants(job JobState, robots map[string]RobotState) error {
	if !slices.Contains([]JobStatus{JobPending, JobAssigned, JobInProgress, JobDone}, job.Status) {
		return fmt.Errorf("I3: %s invalid job status %q", job.ID, job.Status)
	}
	if (job.AssignedRobot == "") != (job.Status == JobPending) {
		return fmt.Errorf("I4: %s assignment does not match pending status", job.ID)
	}
	if job.Status != JobPending {
		robot, exists := robots[job.AssignedRobot]
		if !exists || (job.Status != JobDone && robot.JobID != job.ID) {
			return fmt.Errorf("I4: %s missing robot or reciprocal assignment", job.ID)
		}
	}
	return nil
}

func checkStationInvariants(stations []StationState, robots map[string]RobotState) error {
	occupancy := make(map[string]int)
	for _, station := range stations {
		if station.OccupiedBy == "" {
			continue
		}
		robot, exists := robots[station.OccupiedBy]
		if !exists || robot.X != station.X || robot.Y != station.Y || (robot.Status != RobotCharging && robot.Status != RobotOffline) {
			return fmt.Errorf("I7: station %s has invalid occupant %s", station.ID, station.OccupiedBy)
		}
		occupancy[robot.ID]++
		if occupancy[robot.ID] > 1 {
			return fmt.Errorf("I8: %s occupies multiple stations", robot.ID)
		}
	}
	for _, robot := range robots {
		if robot.Status == RobotCharging && occupancy[robot.ID] != 1 {
			return fmt.Errorf("I8: charging robot %s needs one station", robot.ID)
		}
	}
	return nil
}

func checkFaultInvariants(faults []FaultState, tick int, robots map[string]RobotState) error {
	seen := make(map[string]bool)
	for _, fault := range faults {
		if !slices.Contains([]FaultType{FaultLowBattery, FaultRobotUnresponsive, FaultAisleConflict}, fault.Type) {
			return fmt.Errorf("I3: %s invalid fault type %q", fault.ID, fault.Type)
		}
		if seen[fault.ID] || fault.Tick > tick {
			return fmt.Errorf("I11: duplicate fault %s or future tick %d", fault.ID, fault.Tick)
		}
		seen[fault.ID] = true
		for _, robotID := range fault.Robots {
			if _, exists := robots[robotID]; !exists {
				return fmt.Errorf("I11: %s references unknown robot %s", fault.ID, robotID)
			}
		}
	}
	return nil
}

func checkEntityOrder(s State) error {
	groups := map[string][]string{"R": {}, "J": {}, "S": {}, "F": {}}
	for _, robot := range s.Robots {
		groups["R"] = append(groups["R"], robot.ID)
	}
	for _, job := range s.Jobs {
		groups["J"] = append(groups["J"], job.ID)
	}
	for _, station := range s.Stations {
		groups["S"] = append(groups["S"], station.ID)
	}
	for _, fault := range s.Faults {
		groups["F"] = append(groups["F"], fault.ID)
	}
	for _, prefix := range []string{"R", "J", "S", "F"} {
		for i, id := range groups[prefix] {
			if want := fmt.Sprintf("%s%d", prefix, i+1); id != want {
				return fmt.Errorf("I9: ID %q at index %d, want %s", id, i, want)
			}
		}
	}
	return nil
}

func assertInvariants(t *testing.T, s *Sim, cfg Config) {
	t.Helper()
	if err := checkInvariants(s.Snapshot(), cfg, s.navigation); err != nil {
		t.Fatalf("tick %d: %v", s.state.Tick, err)
	}
	if err := checkRecoveryInvariants(s); err != nil {
		t.Fatal(err)
	}
}

func checkRecoveryInvariants(s *Sim) error {
	for _, robot := range s.state.Robots {
		recovery := s.recovery[robot.ID]
		progress := recovery.cycle
		if progress.age < 0 || (progress.key == "") != (progress.age == 0) || progress.key != "" && (!s.cfg.Recovery || progress.best < 0 || progress.tick != s.state.Tick || progress.job != robot.JobID || progress.target != s.recoveryTarget(robot) || robot.Status != RobotIdle && robot.Status != RobotMoving) {
			return fmt.Errorf("I14: %s invalid private cycle progress", robot.ID)
		}
		if recovery.hold < 0 || recovery.hold > s.cfg.HoldTicks || recovery.hold > 0 && (!recovery.resume || robot.WaitTicks != 0 || robot.Blocked) {
			return fmt.Errorf("I14: %s inconsistent recovery hold", robot.ID)
		}
		if recovery.resume {
			nav, exists := s.navigation[robot.ID]
			if !s.cfg.Recovery || robot.Status != RobotMoving || !exists || nav.target != s.recoveryTarget(robot) {
				return fmt.Errorf("I6: %s hold lost mission or navigation", robot.ID)
			}
			previous := Point{X: robot.X, Y: robot.Y}
			for _, point := range nav.route {
				dx, dy := point.X-previous.X, point.Y-previous.Y
				if dx*dx+dy*dy != 1 || s.cfg.CellTypeAt(point) == CellWall || s.cfg.CellTypeAt(point) == CellOutOfBounds || s.cfg.CellTypeAt(point) == CellStation && point != nav.target {
					return fmt.Errorf("I6: %s hold has invalid route", robot.ID)
				}
				previous = point
			}
			if previous != nav.target {
				return fmt.Errorf("I6: %s hold has incomplete route", robot.ID)
			}
		}
		previous, count := -1, 0
		for _, tick := range recovery.retreats {
			if tick < 0 || tick > s.state.Tick || tick <= previous {
				return fmt.Errorf("I14: %s invalid retreat history", robot.ID)
			}
			if tick > s.state.Tick-200 {
				count++
			}
			previous = tick
		}
		if count > 10 {
			return fmt.Errorf("I14: %s exceeded retreat limit", robot.ID)
		}
	}
	for id := range s.recovery {
		if s.faultRobot(id) == nil {
			return fmt.Errorf("I6: unknown recovery robot %s", id)
		}
	}
	return nil
}

func TestInvariantViolations(t *testing.T) {
	for _, tc := range []struct {
		name, invariant string
		corrupt         func(*State)
	}{
		{"battery below zero", "I1", func(s *State) { s.Robots[0].Battery = -1 }},
		{"battery above 100", "I1", func(s *State) { s.Robots[0].Battery = 101 }},
		{"negative X", "I2", func(s *State) { s.Robots[0].X = -1 }},
		{"default width boundary", "I2", func(s *State) { s.Robots[0].X = DefaultConfig().Width }},
		{"default height boundary", "I2", func(s *State) { s.Robots[0].Y = DefaultConfig().Height }},
		{"robot enum", "I3", func(s *State) { s.Robots[0].Status = "invalid" }},
		{"job enum", "I3", func(s *State) { s.Jobs[0].Status = "invalid" }},
		{"fault enum", "I3", func(s *State) { s.Faults[0].Type = "invalid" }},
		{"pending assignment", "I4", func(s *State) { s.Jobs[0].AssignedRobot = "R1" }},
		{"done missing history", "I4", func(s *State) { s.Jobs[0].Status = JobDone }},
		{"unknown assigned robot", "I4", func(s *State) {
			s.Jobs[0].Status, s.Jobs[0].AssignedRobot = JobAssigned, "R99"
		}},
		{"missing reciprocal job", "I4", func(s *State) {
			s.Jobs[0].Status, s.Jobs[0].AssignedRobot = JobInProgress, "R1"
		}},
		{"unknown job", "I5", func(s *State) {
			s.Robots[0].Status, s.Robots[0].JobID = RobotMoving, "J99"
		}},
		{"holding completed job", "I5", func(s *State) {
			s.Jobs[0].Status, s.Jobs[0].AssignedRobot = JobDone, "R1"
			s.Robots[0].Status, s.Robots[0].JobID = RobotMoving, "J1"
		}},
		{"two holders", "I5", func(s *State) {
			s.Jobs[0].Status, s.Jobs[0].AssignedRobot = JobAssigned, "R1"
			s.Robots[0].Status, s.Robots[0].JobID = RobotMoving, "J1"
			s.Robots[1].Status, s.Robots[1].JobID = RobotMoving, "J1"
		}},
		{"moving without job", "I6", func(s *State) { s.Robots[0].Status = RobotMoving }},
		{"faulted with battery", "I6", func(s *State) { s.Robots[0].Status = RobotFaulted }},
		{"idle with job", "I6", func(s *State) {
			s.Jobs[0].Status, s.Jobs[0].AssignedRobot = JobAssigned, "R1"
			s.Robots[0].JobID = "J1"
		}},
		{"unknown occupant", "I7", func(s *State) { s.Stations[0].OccupiedBy = "R99" }},
		{"occupant wrong status", "I7", func(s *State) { s.Stations[0].OccupiedBy = "R1" }},
		{"occupant wrong position", "I7", func(s *State) {
			s.Robots[0].Status = RobotOffline
			s.Stations[1].OccupiedBy = "R1"
		}},
		{"charging without station", "I8", func(s *State) { s.Robots[0].Status = RobotCharging }},
		{"multiple occupied stations", "I8", func(s *State) {
			s.Robots[0].Status = RobotOffline
			s.Stations[0].OccupiedBy = "R1"
			s.Stations[1].X, s.Stations[1].Y, s.Stations[1].OccupiedBy = 0, 0, "R1"
		}},
		{"robot order", "I9", func(s *State) { s.Robots[1], s.Robots[2] = s.Robots[2], s.Robots[1] }},
		{"job prefix", "I9", func(s *State) { s.Jobs[0].ID = "X1" }},
		{"station gap", "I9", func(s *State) { s.Stations[1].ID = "S3" }},
		{"fault padding", "I9", func(s *State) { s.Faults[0].ID = "F01" }},
		{"job limit", "I10", func(s *State) {
			s.Jobs = append(s.Jobs, JobState{ID: "J2", From: Point{1, 1}, To: Point{2, 1}, Status: JobPending})
		}},
		{"unknown fault robot", "I11", func(s *State) { s.Faults[0].Robots = []string{"R99"} }},
		{"future fault", "I11", func(s *State) { s.Faults[0].Tick = s.Tick + 1 }},
		{"duplicate fault", "I11", func(s *State) { s.Faults = append(s.Faults, s.Faults[0]) }},
		{"same cell", "I12", func(s *State) { s.Robots[1].X, s.Robots[1].Y = s.Robots[0].X, s.Robots[0].Y }},
		{"same aisle", "I13", func(s *State) {
			s.Robots[0].X, s.Robots[0].Y = 4, 1
			s.Robots[1].X, s.Robots[1].Y = 4, 2
		}},
		{"negative waiting", "I14", func(s *State) { s.Robots[0].WaitTicks = -1 }},
		{"blocked without waiting", "I14", func(s *State) { s.Robots[0].Blocked = true }},
		{"idle waiting", "I14", func(s *State) { s.Robots[0].WaitTicks = 1 }},
		{"nil aisles", "I15", func(s *State) { s.Aisles = nil }},
		{"missing aisle", "I15", func(s *State) { s.Aisles = s.Aisles[:2] }},
		{"extra aisle", "I15", func(s *State) { s.Aisles = append(s.Aisles, AisleState{ID: 3}) }},
		{"aisle order", "I15", func(s *State) { s.Aisles[0], s.Aisles[1] = s.Aisles[1], s.Aisles[0] }},
		{"incorrect aisle occupant", "I15", func(s *State) { s.Aisles[0].OccupiedBy = "R1" }},
		{"missing physical occupant", "I15", func(s *State) { s.Robots[0].X, s.Robots[0].Y = 4, 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := cfgRobotOnStation()
			cfg.InitialJobs, cfg.MaxJobs = 1, 1
			sim := newTestSim(t, cfg, 1)
			sim.recordFault(FaultLowBattery, []string{"R1"}, nil)
			assertInvariants(t, sim, cfg)
			state := sim.Snapshot()
			tc.corrupt(&state)
			if err := checkInvariants(state, cfg); err == nil || !strings.HasPrefix(err.Error(), tc.invariant+":") {
				t.Fatalf("got %v, want %s violation", err, tc.invariant)
			}
		})
	}
}

func TestInvariantsAcrossSeeds(t *testing.T) {
	for _, seed := range []int64{1, 2, 3} {
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			cfg := DefaultConfig()
			s := newTestSim(t, cfg, seed)
			assertInvariants(t, s, cfg)
			for tick := 1; tick <= 300; tick++ {
				s.Tick()
				assertInvariants(t, s, cfg)
			}
		})
	}
}

func TestInvariantsRetainedJobs(t *testing.T) {
	for _, status := range []RobotStatus{RobotFaulted, RobotOffline} {
		for _, jobStatus := range []JobStatus{JobAssigned, JobInProgress} {
			cfg := DefaultConfig()
			s := newTestSim(t, cfg, 1)
			s.state.Robots[0].Status, s.state.Robots[0].JobID = status, "J1"
			s.state.Robots[0].Battery = 0
			if status == RobotOffline {
				s.state.Robots[0].Battery = 73
			}
			s.state.Jobs[0].Status, s.state.Jobs[0].AssignedRobot = jobStatus, "R1"
			assertInvariants(t, s, cfg)
		}
	}
}

func TestInvariantsUseSuppliedGrid(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Width, cfg.Height = 25, 15
	cfg.RobotStarts[0] = Point{24, 14}
	s := newTestSim(t, cfg, 1)
	assertInvariants(t, s, cfg)
	if err := checkInvariants(s.Snapshot(), DefaultConfig()); err == nil || !strings.HasPrefix(err.Error(), "I2:") {
		t.Fatalf("default grid should reject position outside its bounds, got %v", err)
	}
}

func TestFinalInvariantMatrix(t *testing.T) {
	for _, seed := range []int64{1, 2, 3, 42, 99} {
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			t.Run("injections", func(t *testing.T) { runInjectionMatrix(t, seed) })
			t.Run("charging", func(t *testing.T) { runChargingMatrix(t, seed) })
		})
	}
}

func runInjectionMatrix(t *testing.T, seed int64) {
	t.Helper()
	cfg := DefaultConfig()
	s := newTestSim(t, cfg, seed)
	for tick := 1; tick <= 500; tick++ {
		s.Tick()
		assertInvariants(t, s, cfg)
		switch tick {
		case 20:
			requireInjection(t, s.InjectLowBattery("R1"))
		case 30:
			requireInjection(t, s.InjectUnresponsive("R2"))
		case 40:
			requireInjection(t, s.InjectAisleConflict("R3", "R4", 0))
		default:
			continue
		}
		assertInvariants(t, s, cfg)
	}
}

func runChargingMatrix(t *testing.T, seed int64) {
	t.Helper()
	cfg := cfgRobotOnStation()
	s := newTestSim(t, cfg, seed)
	requireInjection(t, s.InjectLowBattery("R1"))
	assertInvariants(t, s, cfg)
	wantFaults := []FaultState{{ID: "F1", Type: FaultLowBattery, Robots: []string{"R1"}, Tick: 0}}
	if s.state.Robots[0].Battery != 15 || !reflect.DeepEqual(s.state.Faults, wantFaults) {
		t.Fatal("incorrect initial low-battery injection")
	}
	if err := s.SetCharging("R1", "S1"); err != nil {
		t.Fatal(err)
	}
	assertInvariants(t, s, cfg)
	if s.state.Stations[0].OccupiedBy != "R1" {
		t.Fatal("S1 was not reserved")
	}
	for tick := 1; tick <= 500; tick++ {
		s.Tick()
		assertInvariants(t, s, cfg)
		want := RobotState{ID: "R1", Battery: 15 + 2*min(tick, 12), Status: RobotCharging}
		if tick >= 13 {
			want.Status = RobotOffline
		}
		if s.state.Robots[0] != want || s.state.Stations[0].OccupiedBy != "R1" {
			t.Fatalf("tick %d: robot %+v, station %+v", tick, s.state.Robots[0], s.state.Stations[0])
		}
		if tick == 10 {
			requireInjection(t, s.InjectUnresponsive("R1"))
			wantFaults = append(wantFaults, FaultState{ID: "F2", Type: FaultRobotUnresponsive, Robots: []string{"R1"}, Tick: 10})
			assertInvariants(t, s, cfg)
		}
		countdown := 0
		if tick >= 10 {
			countdown = max(0, 13-tick)
		}
		if s.unresponsiveTicks["R1"] != countdown || !reflect.DeepEqual(s.state.Faults, wantFaults) {
			t.Fatalf("tick %d: countdown or exact fault list changed", tick)
		}
	}
	assertChargingFailure(t, s, "R2", "S1", ErrStationOccupied)
	assertInvariants(t, s, cfg)
}
