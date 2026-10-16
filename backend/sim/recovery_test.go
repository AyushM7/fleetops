package sim

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func recoverySim(t *testing.T, cfg Config, positions, targets []Point, routes [][]Point) *Sim {
	t.Helper()
	cfg.AutoCharge, cfg.Recovery, cfg.AutoDispatch = true, true, false
	cfg.InitialJobs, cfg.NewJobEvery = 0, 10000
	s := newTestSim(t, cfg, 42)
	s.state.Tick = 100
	for i, position := range positions {
		r := &s.state.Robots[i]
		r.X, r.Y, r.Status, r.WaitTicks, r.Blocked = position.X, position.Y, RobotMoving, cfg.RecoverAfter, true
		r.JobID = "J" + strings.TrimPrefix(r.ID, "R")
		s.state.Jobs = append(s.state.Jobs, JobState{ID: r.JobID, From: position, To: targets[i], Status: JobInProgress, AssignedRobot: r.ID})
		s.navigation[r.ID] = robotNavigation{target: targets[i], route: slices.Clone(routes[i])}
	}
	assertInvariants(t, s, s.cfg)
	return s
}

func recoveryMouth(t *testing.T) *Sim {
	t.Helper()
	return recoverySim(t, WarehouseConfig(), []Point{{4, 2}, {4, 1}}, []Point{{3, 1}, {4, 3}}, [][]Point{{{4, 1}, {3, 1}}, {{4, 2}, {4, 3}}})
}

func recoveryThree(t *testing.T) *Sim {
	t.Helper()
	return recoverySim(t, DefaultConfig(), []Point{{4, 2}, {3, 2}, {3, 3}}, []Point{{2, 2}, {3, 4}, {5, 3}}, [][]Point{{{3, 2}, {2, 2}}, {{3, 3}, {3, 4}}, {{4, 3}, {5, 3}}})
}

func recoverNow(s *Sim) []recoveryEvent { return s.recoverTraffic(make([]bool, len(s.state.Robots))) }

func TestRecoveryGraphOrdering(t *testing.T) {
	s := &Sim{state: State{Robots: []RobotState{{ID: "R10"}, {ID: "R2"}, {ID: "R1"}}}}
	if got := s.recoveryOrder(); !slices.Equal(got, []int{2, 1, 0}) {
		t.Fatalf("not numeric order: %v", got)
	}
	// A lower-numbered incoming chain must not reorder separate cycles.
	cycles := recoveryCycles([]int{0, 1, 2, 3, 4}, []int{3, 2, 1, 4, 3})
	if !reflect.DeepEqual(cycles, [][]int{{1, 2}, {3, 4}}) {
		t.Fatalf("cycle ordering=%v", cycles)
	}
}

func TestRecoveryPrivateProgressReset(t *testing.T) {
	s := recoveryMouth(t)
	cycle := []int{0, 1}
	for tick := 100; tick < 104; tick++ {
		s.state.Tick = tick
		s.recordRecoveryCycle(cycle)
	}
	if s.recovery["R2"].cycle.age != 4 {
		t.Fatal("cycle did not age")
	}
	s.recordRecoveryCycle(cycle)
	if s.recovery["R2"].cycle.age != 4 {
		t.Fatal("aged twice in one tick")
	}
	s.state.Tick++
	s.state.Robots[1].Y = 2
	s.recordRecoveryCycle(cycle)
	if s.recovery["R2"].cycle.age != 1 {
		t.Fatal("genuine progress did not reset age")
	}
	s.navigation["R1"] = robotNavigation{}
	s.navigation["R2"] = robotNavigation{}
	for i := range s.state.Robots {
		s.state.Robots[i].WaitTicks = 0
	}
	recoverNow(s)
	if s.recovery["R2"].cycle.age != 0 {
		t.Fatal("resolved dependency retained age")
	}
}

func TestRecoveryFutureCrossingDoesNotStartCycle(t *testing.T) {
	s := recoverySim(t, DefaultConfig(), []Point{{X: 6, Y: 0}, {X: 7, Y: 0}}, []Point{{X: 9, Y: 1}, {X: 4, Y: 1}}, [][]Point{
		{{X: 6, Y: 1}, {X: 7, Y: 1}, {X: 8, Y: 1}, {X: 9, Y: 1}},
		{{X: 7, Y: 1}, {X: 6, Y: 1}, {X: 5, Y: 1}, {X: 4, Y: 1}},
	})
	for i := range s.state.Robots {
		s.state.Robots[i].WaitTicks = 0
		s.state.Robots[i].Blocked = false
	}
	for n := 0; n < s.cfg.RecoverAfter+1; n++ {
		s.state.Tick++
		if events := recoverNow(s); len(events) != 0 {
			t.Fatalf("unblocked future crossing recovered: %v", events)
		}
		for _, r := range s.state.Robots {
			if s.recovery[r.ID].cycle.age != 0 {
				t.Fatal("temporary direction seeded a cycle")
			}
		}
	}
}

func TestRecoveryPrivateProgressNegativeInvariants(t *testing.T) {
	for _, name := range []string{"negative", "empty key", "future", "distance", "mission", "disabled"} {
		t.Run(name, func(t *testing.T) {
			s := recoveryMouth(t)
			s.recordRecoveryCycle([]int{0, 1})
			entry := s.recovery["R2"]
			switch name {
			case "negative":
				entry.cycle.age = -1
			case "empty key":
				entry.cycle.key = ""
			case "future":
				entry.cycle.tick++
			case "distance":
				entry.cycle.best = -1
			case "mission":
				entry.cycle.job = "J99"
			case "disabled":
				s.cfg.Recovery = false
			}
			s.recovery["R2"] = entry
			if checkRecoveryInvariants(s) == nil {
				t.Fatal("invalid progress accepted")
			}
		})
	}
}

func TestRecoverySeed1BacktrackingCycle(t *testing.T) {
	cfg := WarehouseConfig()
	cfg.InitialJobs, cfg.NewJobEvery, cfg.AutoDispatch = 0, 10000, false
	s := newTestSim(t, cfg, 1)
	for i := range s.state.Robots {
		s.state.Robots[i].Status = RobotOffline
	}
	a, b := &s.state.Robots[4], &s.state.Robots[5]
	a.X, a.Y, a.Status, a.Battery = 16, 0, RobotMoving, 14
	b.X, b.Y, b.Status, b.Battery, b.JobID = 17, 0, RobotMoving, 98, "J1"
	s.state.Jobs = []JobState{{ID: "J1", From: Point{X: 4, Y: 0}, To: Point{X: 11, Y: 1}, Status: JobAssigned, AssignedRobot: "R6"}}
	s.state.Stations[1].ReservedBy = "R5"
	s.navigation["R5"] = robotNavigation{target: Point{X: 19, Y: 0}, route: []Point{{X: 17, Y: 0}, {X: 18, Y: 0}, {X: 19, Y: 0}}}
	if !s.planNavigation(b, Point{X: 4, Y: 0}, false) {
		t.Fatal("job route unavailable")
	}
	retreated, arrived := false, false
	for tick := 1; tick <= 35; tick++ {
		before := s.Snapshot()
		s.Tick()
		assertInvariants(t, s, s.cfg)
		for i, r := range s.state.Robots {
			dx, dy := r.X-before.Robots[i].X, r.Y-before.Robots[i].Y
			if dx*dx+dy*dy > 1 {
				t.Fatal("moved twice")
			}
		}
		if slices.Contains(s.recovery["R6"].retreats, tick) {
			retreated = true
			if tick < cfg.RecoverAfter || before.Robots[5].WaitTicks >= cfg.RecoverAfter {
				t.Fatal("did not recover recurring short waits")
			}
		}
		if a.Status == RobotCharging {
			arrived = true
			if a.Battery < 1 {
				t.Fatal("charging arrival underbudgeted")
			}
			break
		}
		if s.state.Stations[1].ReservedBy != "R5" {
			t.Fatal("lost charging reservation")
		}
	}
	if !retreated || !arrived {
		t.Fatalf("retreated=%v arrived=%v R5=%+v R6=%+v", retreated, arrived, *a, *b)
	}
}

func TestRecoveryAisleMouth(t *testing.T) {
	s := recoveryMouth(t)
	jobs, faults := slices.Clone(s.state.Jobs), slices.Clone(s.state.Faults)
	events := recoverNow(s)
	r := s.state.Robots[1]
	if len(events) != 1 || events[0].robot != "R2" || !slices.Equal(events[0].cycle, []string{"R1", "R2"}) || r.X != 5 || r.Y != 1 || r.Battery != 99 || r.WaitTicks != 0 || r.Blocked {
		t.Fatalf("retreat=%v robot=%+v", events, r)
	}
	if !reflect.DeepEqual(jobs, s.state.Jobs) || !reflect.DeepEqual(faults, s.state.Faults) {
		t.Fatal("retreat changed jobs/faults")
	}
	assertInvariants(t, s, s.cfg)
}

func TestRecoveryThreeRobotSelection(t *testing.T) {
	for _, skipHighest := range []bool{false, true} {
		s := recoveryThree(t)
		skip := make([]bool, len(s.state.Robots))
		skip[2] = skipHighest
		events := s.recoverTraffic(skip)
		want := "R3"
		if skipHighest {
			want = "R2"
		}
		if len(events) != 1 || events[0].robot != want || !slices.Equal(events[0].cycle, []string{"R1", "R2", "R3"}) {
			t.Fatalf("selection=%v want=%s", events, want)
		}
		assertInvariants(t, s, s.cfg)
	}
}

func TestRecoveryHighestEnergyEligible(t *testing.T) {
	s := recoveryThree(t)
	s.state.Robots[2].Battery = 1
	events := recoverNow(s)
	if len(events) != 1 || events[0].robot != "R2" || s.state.Robots[2].Battery != 1 {
		t.Fatalf("did not fall back to highest energy-eligible robot: %v", events)
	}
	assertInvariants(t, s, s.cfg)
}

func TestRecoveryTwentySixVersusTwentyFour(t *testing.T) {
	for _, tc := range []struct {
		high, low int
		want      string
	}{{24, 100, "R1"}, {26, 100, "R2"}, {24, 1, ""}} {
		s := recoverySim(t, DefaultConfig(), []Point{{3, 3}, {3, 2}}, []Point{{3, 1}, {6, 8}}, [][]Point{
			{{3, 2}, {3, 1}},
			{{3, 3}, {3, 4}, {3, 5}, {3, 6}, {3, 7}, {3, 8}, {4, 8}, {5, 8}, {6, 8}},
		})
		s.state.Robots[1].Battery = tc.high
		s.state.Robots[0].Battery = tc.low
		// Left retreat 1 + remaining delivery 10 + charging exit 14 + floor 1 = 26.
		before := diagSnapshot(s)
		events := s.recoverCycle([]int{1, 0}, make([]bool, len(s.state.Robots)))
		if events.robot != tc.want {
			t.Fatalf("battery=%d: %v, want %s", tc.high, events, tc.want)
		}
		if tc.high == 24 && s.state.Robots[1].Battery != 24 {
			t.Fatal("rejected candidate was mutated")
		}
		if tc.want == "" && !reflect.DeepEqual(before, diagSnapshot(s)) {
			t.Fatal("infeasible cycle recovery mutated state")
		}
		assertInvariants(t, s, s.cfg)
	}
}

func TestRecoveryNeighborOrder(t *testing.T) {
	for _, tc := range []struct {
		name      string
		positions []Point
		walls     []Point
		want      Point
	}{
		{name: "right", positions: []Point{{6, 6}, {6, 5}}, want: Point{7, 5}},
		{name: "left", positions: []Point{{6, 6}, {6, 5}}, walls: []Point{{7, 5}}, want: Point{5, 5}},
		{name: "down", positions: []Point{{5, 5}, {6, 5}}, walls: []Point{{7, 5}}, want: Point{6, 6}},
		{name: "up", positions: []Point{{6, 6}, {6, 5}}, walls: []Point{{7, 5}, {5, 5}}, want: Point{6, 4}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := tc.positions[0], tc.positions[1]
			s := recoverySim(t, DefaultConfig(), tc.positions, []Point{b, a}, [][]Point{{b}, {a}})
			s.cfg.Walls = tc.walls
			events := recoverNow(s)
			r := s.state.Robots[1]
			if len(events) != 1 || events[0].robot != "R2" || (Point{X: r.X, Y: r.Y}) != tc.want {
				t.Fatalf("events=%v robot=%+v want=%v", events, r, tc.want)
			}
			assertInvariants(t, s, s.cfg)
		})
	}
}

func TestRecoveryRejectsRequiredCell(t *testing.T) {
	s := recoverySim(t, DefaultConfig(), []Point{{6, 6}, {6, 5}, {8, 5}}, []Point{{6, 4}, {6, 7}, {7, 5}}, [][]Point{{{6, 5}, {6, 4}}, {{6, 6}, {6, 7}}, {{7, 5}}})
	if !s.retreatRobot(&s.state.Robots[1], []int{0, 1, 2}) || s.state.Robots[1].X != 5 {
		t.Fatal("retreat used another cycle member's required cell")
	}
	assertInvariants(t, s, s.cfg)
}

func TestRecoveryRejectsUnavailable(t *testing.T) {
	for _, name := range []string{"disabled", "threshold", "parked", "skipped", "no neighbor", "energy"} {
		t.Run(name, func(t *testing.T) {
			s := recoveryMouth(t)
			skip := make([]bool, len(s.state.Robots))
			switch name {
			case "disabled":
				s.cfg.Recovery = false
			case "threshold":
				for i := 0; i < 2; i++ {
					s.state.Robots[i].WaitTicks = s.cfg.RecoverAfter - 1
				}
			case "parked":
				s.state.Robots[0].Status = RobotOffline
			case "skipped":
				skip[1] = true
			case "no neighbor":
				s.cfg.Walls = append(s.cfg.Walls, Point{5, 1}, Point{3, 1})
				s.cfg.Stations = append(s.cfg.Stations, Point{4, 0})
			case "energy":
				s.state.Robots[1].Battery = 11
			}
			before := diagSnapshot(s)
			events := s.recoverTraffic(skip)
			if len(events) > 0 && (events[0].robot != "" || events[0].reason == "") {
				t.Fatalf("unexpected recovery: %v", events)
			}
			after := diagSnapshot(s)
			// Cycle observation may create private progress, but rejected retreats
			// cannot change routes, holds, history, or public state.
			for id, entry := range after.Recovery {
				if _, existed := before.Recovery[id]; !existed && entry.Hold == 0 && !entry.Resume && len(entry.Retreats) == 0 {
					delete(after.Recovery, id)
				}
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("rejected recovery mutated state/trips")
			}
		})
	}
	// Retreat plus remaining delivery/exit/floor costs exactly 12.
	s := recoveryMouth(t)
	s.state.Robots[1].Battery = 12
	if events := recoverNow(s); len(events) != 1 || events[0].robot != "R2" || s.state.Robots[1].Battery != 11 {
		t.Fatalf("exact mission budget rejected: %v", events)
	}
}

func TestRecoveryHoldAndResume(t *testing.T) {
	s := recoveryMouth(t)
	recoverNow(s)
	before := s.state.Robots[1]
	job := s.state.Jobs[1]
	for tick := 1; tick <= s.cfg.HoldTicks; tick++ {
		s.Tick()
		r := s.state.Robots[1]
		if r != before || s.recovery[r.ID].hold != s.cfg.HoldTicks-tick || s.state.Jobs[1] != job {
			t.Fatalf("hold tick %d changed mission: %+v", tick, r)
		}
		assertInvariants(t, s, s.cfg)
	}
	s.Tick()
	r := s.state.Robots[1]
	if r.Battery != before.Battery-1 || r.X != 4 || r.Y != 1 || s.recovery[r.ID].resume {
		t.Fatalf("did not resume after hold: %+v recovery=%+v", r, s.recovery[r.ID])
	}
	for tick := 0; tick < 2; tick++ {
		s.Tick()
		assertInvariants(t, s, s.cfg)
	}
	if s.state.Jobs[1].Status != JobDone || s.state.Robots[1].Status != RobotIdle {
		t.Fatal("recovered mission did not complete")
	}
}

func TestRecoveryPickupMissionBudget(t *testing.T) {
	for _, battery := range []int{13, 14} {
		s := recoveryMouth(t)
		job := &s.state.Jobs[1]
		job.Status, job.From, job.To = JobAssigned, Point{4, 3}, Point{4, 4}
		s.state.Robots[1].Battery = battery
		events := recoverNow(s)
		if len(events) != 1 || (events[0].robot == "R2") != (battery == 14) {
			t.Fatalf("battery=%d complete pickup/delivery/exit budget: %v", battery, events)
		}
	}
}

func TestRecoveryResumeFailure(t *testing.T) {
	s := recoveryMouth(t)
	recoverNow(s)
	r := &s.state.Robots[1]
	entry := s.recovery[r.ID]
	entry.hold = 0
	s.recovery[r.ID] = entry
	r.Battery = 1
	prior := s.navigation[r.ID]
	for tick := 1; tick <= 4; tick++ {
		if !s.prepareRecovery(r) || r.WaitTicks != tick || r.Battery != 1 || !reflect.DeepEqual(prior, s.navigation[r.ID]) {
			t.Fatal("unaffordable resumption lost route or waiting state")
		}
	}
}

func TestRecoveryChargingReservation(t *testing.T) {
	s := recoveryMouth(t)
	r := &s.state.Robots[1]
	r.JobID = ""
	s.state.Jobs = s.state.Jobs[:1]
	s.state.Stations[0].ReservedBy = r.ID
	s.cfg.Stations[0] = Point{4, 11}
	s.state.Stations[0].X, s.state.Stations[0].Y = 4, 11
	r.Battery = 13 // Retreat plus replacement costs twelve; admission retains one.
	route := []Point{}
	for y := 2; y <= 11; y++ {
		route = append(route, Point{X: 4, Y: y})
	}
	s.navigation[r.ID] = robotNavigation{target: Point{4, 11}, route: route}
	assertInvariants(t, s, s.cfg)
	if events := recoverNow(s); len(events) != 1 || events[0].robot != r.ID {
		t.Fatalf("charging retreat failed: %v", events)
	}
	if s.reservedStation(r.ID) == nil || r.JobID != "" || s.navigation[r.ID].relocation {
		t.Fatal("charging reservation/trip lost")
	}
	for tick := 0; tick < 30; tick++ {
		s.Tick()
		assertInvariants(t, s, s.cfg)
		if r.Status == RobotCharging {
			if s.state.Stations[0].OccupiedBy != r.ID || s.state.Stations[0].ReservedBy != "" || r.Battery != 1 {
				t.Fatal("bad charging arrival")
			}
			s.Tick()
			if r.Battery != 1+s.cfg.ChargeGain {
				t.Fatal("floor-one arrival did not charge next tick")
			}
			return
		}
		if s.reservedStation(r.ID) == nil {
			t.Fatal("reservation lost before arrival")
		}
	}
	t.Fatal("recovered charging trip did not arrive")
}

func TestRecoveryRelocationTrip(t *testing.T) {
	s := recoveryMouth(t)
	r := &s.state.Robots[1]
	r.JobID = ""
	r.Battery = 26
	s.state.Jobs = s.state.Jobs[:1]
	route := []Point{}
	for y := 2; y <= 10; y++ {
		route = append(route, Point{X: 4, Y: y})
	}
	s.navigation[r.ID] = robotNavigation{target: Point{4, 10}, route: route, relocation: true}
	if events := recoverNow(s); len(events) != 1 || events[0].robot != r.ID {
		t.Fatalf("relocation retreat failed: %v", events)
	}
	if !s.navigation[r.ID].relocation || s.reservedStation(r.ID) != nil {
		t.Fatal("relocation became charging")
	}
	for tick := 0; tick < 25; tick++ {
		s.Tick()
		assertInvariants(t, s, s.cfg)
		if r.Status == RobotIdle {
			if r.Battery != 15 || r.Y != 10 {
				t.Fatalf("incorrect relocation completion: %+v", r)
			}
			return
		}
	}
	t.Fatal("relocation did not resume")
}

func TestRecoveryRollingLimit(t *testing.T) {
	s := recoveryMouth(t)
	robots := slices.Clone(s.state.Robots)
	nav := s.navigation["R2"]
	reset := func(tick int) {
		s.state.Tick = tick
		s.state.Robots = slices.Clone(robots)
		s.navigation["R2"] = nav
		entry := s.recovery["R2"]
		entry.hold, entry.resume = 0, false
		s.recovery["R2"] = entry
	}
	for n := 0; n < 10; n++ {
		reset(100 + n)
		if events := recoverNow(s); len(events) != 1 || events[0].robot != "R2" {
			t.Fatalf("retreat %d rejected: %v", n, events)
		}
	}
	reset(299)
	if events := recoverNow(s); len(events) != 1 || events[0].robot != "" || !strings.Contains(events[0].reason, "limit") {
		t.Fatalf("rolling limit bypassed: %v", events)
	}
	reset(300)
	if events := recoverNow(s); len(events) != 1 || events[0].robot != "R2" || len(s.recovery["R2"].retreats) != 10 || s.recovery["R2"].retreats[0] != 101 {
		t.Fatalf("window failed to expire tick100: %v", events)
	}
	assertInvariants(t, s, s.cfg)
}

func TestRecoveryDeterminism(t *testing.T) {
	var want diagFrame
	for repeat := 0; repeat < 100; repeat++ {
		s := recoveryThree(t)
		// Slice order must not choose the mover.
		slices.Reverse(s.state.Robots)
		recoverNow(s)
		slices.SortFunc(s.state.Robots, func(a, b RobotState) int { return idNumber(a.ID) - idNumber(b.ID) })
		for tick := 0; tick < 12; tick++ {
			s.Tick()
			assertInvariants(t, s, s.cfg)
		}
		got := diagSnapshot(s)
		if repeat == 0 {
			want = got
		} else if !reflect.DeepEqual(want, got) {
			t.Fatal("recovery is nondeterministic")
		}
	}
}

func TestRecoveryCountdownPrecedence(t *testing.T) {
	s := recoveryMouth(t)
	recoverNow(s)
	requireInjection(t, s.InjectUnresponsive("R2"))
	before := s.state.Robots[1]
	for tick := 0; tick < 3; tick++ {
		s.Tick()
		assertInvariants(t, s, s.cfg)
	}
	r := s.state.Robots[1]
	if r.Status != RobotOffline || r.X != before.X || r.Y != before.Y || r.Battery != before.Battery || r.JobID != before.JobID || s.recovery[r.ID].resume || s.recovery[r.ID].hold != 0 {
		t.Fatalf("hold changed countdown precedence: %+v", r)
	}
}

func TestRecoveryNegativeInvariants(t *testing.T) {
	for _, name := range []string{"negative hold", "excess hold", "wait during hold", "no resume", "lost nav", "lost job", "invalid route", "future history", "duplicate history", "limit", "disabled", "unknown robot"} {
		t.Run(name, func(t *testing.T) {
			s := recoveryMouth(t)
			recoverNow(s)
			entry := s.recovery["R2"]
			switch name {
			case "negative hold":
				entry.hold = -1
			case "excess hold":
				entry.hold = s.cfg.HoldTicks + 1
			case "wait during hold":
				s.state.Robots[1].WaitTicks = 1
			case "no resume":
				entry.resume = false
			case "lost nav":
				delete(s.navigation, "R2")
			case "lost job":
				s.state.Robots[1].JobID = ""
			case "invalid route":
				nav := s.navigation["R2"]
				nav.route[0] = Point{19, 11}
			case "future history":
				entry.retreats = []int{s.state.Tick + 1}
			case "duplicate history":
				entry.retreats = []int{100, 100}
			case "limit":
				entry.retreats = []int{90, 91, 92, 93, 94, 95, 96, 97, 98, 99, 100}
			case "disabled":
				s.cfg.Recovery = false
			case "unknown robot":
				s.recovery["R99"] = robotRecovery{}
			}
			s.recovery["R2"] = entry
			err := checkInvariants(s.Snapshot(), s.cfg, s.navigation)
			if err == nil {
				err = checkRecoveryInvariants(s)
			}
			if err == nil {
				t.Fatal("corrupt recovery accepted")
			}
		})
	}
}

func TestRecoveryRecordedS4(t *testing.T) {
	for _, seed := range []int64{1, 2, 42} {
		s := newSoak(t, soakScenario{"S4", 600, true, true, true}, seed)
		retreats := 0
		for tick := 1; tick <= 600; tick++ {
			before := s.Snapshot()
			s.Tick()
			advanceFaultSoak(t, s, before)
			assertInvariants(t, s, s.cfg)
			for i, r := range s.state.Robots {
				dx, dy := r.X-before.Robots[i].X, r.Y-before.Robots[i].Y
				if dx*dx+dy*dy > 1 {
					t.Fatalf("seed%d tick%d %s moved twice", seed, tick, r.ID)
				}
				for _, at := range s.recovery[r.ID].retreats {
					if at == tick {
						retreats++
					}
				}
			}
		}
		if retreats == 0 && seed != 2 {
			t.Fatalf("seed%d original healthy cycle never recovered", seed)
		}
		if seed == 2 && retreats == 0 {
			order := s.recoveryOrder()
			if cycles := recoveryCycles(order, s.recoveryBlockers(order)); len(cycles) != 0 {
				t.Fatalf("changed seed2 timeline left cycles without recovery: %v", cycles)
			}
		}
		t.Logf("S4 seed%d: %d retreats", seed, retreats)
	}
}
