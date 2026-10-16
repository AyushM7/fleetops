package sim

import (
	"flag"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

var idleBlockerDiagnostics = flag.Bool("idle-blocker-diag", false, "measure retained-reserve S1 without running a reserve sweep")

func TestIdleBlockerS1Diagnostics(t *testing.T) {
	if !*idleBlockerDiagnostics {
		t.Skip("requires -idle-blocker-diag")
	}
	for _, seed := range []int64{1, 2, 3, 42, 99} {
		report := measureEnergyS1(t, 5, seed)
		t.Logf("IDLE_BLOCKER_S1 %s", diagJSON(report))
	}
}

func TestIdleBlockerSeed42RelocationDiagnostics(t *testing.T) {
	if !*idleBlockerDiagnostics {
		t.Skip("requires -idle-blocker-diag")
	}
	s := newTestSim(t, WarehouseConfig(), 42)
	reference := newTestSim(t, WarehouseConfig(), 42)
	for tick := 1; tick <= 1500; tick++ {
		reference.Tick()
		s.state.Tick++
		if tick%s.cfg.NewJobEvery == 0 && len(s.state.Jobs) < s.cfg.MaxJobs {
			s.addJob()
		}
		skip := make([]bool, len(s.state.Robots))
		for i := range s.state.Robots {
			skip[i] = s.prepareRobot(&s.state.Robots[i])
		}
		s.assignPending(skip)
		s.seekCharging(skip)
		before := s.state.Robots[0]
		owner, witness, obstructs := s.idleObstruction(before)
		protected := s.protected[Point{X: before.X, Y: before.Y}]
		s.clearIdleAisles(skip)
		after := s.state.Robots[0]
		if before.Status == RobotIdle && after.Status == RobotMoving && s.navigation[after.ID].relocation {
			t.Logf("R1 relocation tick%d from=(%d,%d) target=%v protected=%v owner=%+v ownerRoute=%v obstructs=%v witness=%v battery=%d", tick, before.X, before.Y, s.navigation[after.ID].target, protected, owner, s.navigation[owner.ID], obstructs, witness, before.Battery)
		}
		moved := make([]bool, len(skip))
		for i := range s.state.Robots {
			if skip[i] {
				continue
			}
			r := &s.state.Robots[i]
			position := Point{X: r.X, Y: r.Y}
			if r.Status == RobotMoving {
				s.stepMoving(r)
				moved[i] = position != (Point{X: r.X, Y: r.Y})
			}
			if r.Status != RobotMoving {
				s.finishNavigation(r)
			}
		}
		s.recoverTraffic(moved)
		s.state.Aisles = s.snapshotAisles()
		diagAssertParity(t, reference, s)
		assertInvariants(t, s, s.cfg)
	}
	done := 0
	for _, job := range s.state.Jobs {
		if job.AssignedRobot == "R1" && job.Status == JobDone {
			done++
		}
	}
	t.Logf("R1 final completed=%d state=%+v", done, s.state.Robots[0])
}

func idleBlockerScene(t *testing.T, charging bool) *Sim {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Recovery = true
	s := autochargeSim(t, cfg, Point{2, 0}, 100)
	r := &s.state.Robots[1]
	r.X, r.Y, r.Status, r.Battery = 3, 0, RobotMoving, 100
	if charging {
		s.state.Stations[0].ReservedBy = r.ID
		s.navigation[r.ID] = robotNavigation{target: Point{0, 0}, route: []Point{{2, 0}, {1, 0}, {0, 0}}}
	} else {
		r.JobID = "J1"
		s.state.Jobs = []JobState{{ID: "J1", From: Point{3, 0}, To: Point{2, 0}, Status: JobInProgress, AssignedRobot: r.ID}}
		s.prepareNavigation(r, Point{2, 0})
	}
	assertInvariants(t, s, s.cfg)
	return s
}

func directBlockerRecordedScene(t *testing.T, tick int) *Sim {
	t.Helper()
	cfg := WarehouseConfig()
	cfg.InitialJobs, cfg.NewJobEvery, cfg.AutoDispatch = 0, 10000, false
	s := newTestSim(t, cfg, 42)
	s.state.Tick = tick - 1
	positions := []Point{{X: 18, Y: 1}, {X: 17, Y: 0}, {X: 0, Y: 2}, {X: 15, Y: 1}, {X: 16, Y: 0}, {X: 2, Y: 0}}
	batteries := []int{66, 72, 62, 62, 26, 69}
	owner := 4
	if tick == 1500 {
		positions = []Point{{X: 18, Y: 1}, {X: 17, Y: 1}, {X: 1, Y: 1}, {X: 17, Y: 0}, {X: 19, Y: 2}, {X: 2, Y: 0}}
		batteries = []int{93, 39, 90, 91, 64, 97}
		owner = 1
	}
	for i, p := range positions {
		r := &s.state.Robots[i]
		r.X, r.Y, r.Battery = p.X, p.Y, batteries[i]
	}
	r := &s.state.Robots[owner]
	r.Status = RobotMoving
	s.state.Stations[1].ReservedBy = r.ID
	s.navigation[r.ID] = robotNavigation{target: Point{X: 19, Y: 0}, route: []Point{{X: 17, Y: 0}, {X: 18, Y: 0}, {X: 19, Y: 0}}}
	assertInvariants(t, s, s.cfg)
	return s
}

func TestDirectBlockerRecordedPrecedence(t *testing.T) {
	for _, tick := range []int{873, 1500} {
		t.Run(fmt.Sprint(tick), func(t *testing.T) {
			var want diagFrame
			for repeat := 0; repeat < 2; repeat++ {
				s := directBlockerRecordedScene(t, tick)
				direct, owner, target := "R2", "R5", (Point{X: 17, Y: 1})
				if tick == 1500 {
					direct, owner, target = "R4", "R2", (Point{X: 16, Y: 0})
				}
				if repeat == 1 {
					slices.Reverse(s.state.Robots)
				}
				s.clearIdleAisles(make([]bool, len(s.state.Robots)))
				r1 := s.faultRobot("R1")
				if r1.Status != RobotIdle || (Point{X: r1.X, Y: r1.Y}) != (Point{X: 18, Y: 1}) {
					t.Fatal("indirect blocker moved unnecessarily")
				}
				if nav := s.navigation[direct]; !nav.relocation || nav.target != target {
					t.Fatalf("direct clearance=%+v", nav)
				}
				if s.reservedStation(owner) == nil {
					t.Fatal("lost charging reservation")
				}
				claimed := map[Point]bool{}
				for _, r := range s.state.Robots {
					if nav := s.navigation[r.ID]; nav.relocation {
						if claimed[nav.target] {
							t.Fatal("destination conflict")
						}
						claimed[nav.target] = true
					}
				}
				slices.SortFunc(s.state.Robots, func(a, b RobotState) int { return idNumber(a.ID) - idNumber(b.ID) })
				if repeat == 0 {
					want = diagSnapshot(s)
				} else if !reflect.DeepEqual(want, diagSnapshot(s)) {
					t.Fatal("claims depend on input order")
				}
				// Movement retains numeric priority: R2 must wait for higher-ID R4,
				// while R5 follows R2's already vacated cell in the same tick.
				s = directBlockerRecordedScene(t, tick)
				if repeat == 1 {
					slices.Reverse(s.state.Robots)
				}
				s.Tick()
				assertInvariants(t, s, s.cfg)
				if r := s.faultRobot(direct); (Point{X: r.X, Y: r.Y}) != target || r.Status != RobotIdle {
					t.Fatalf("direct clearance failed: %+v", r)
				}
				for step := 0; step < 4; step++ {
					s.Tick()
					assertInvariants(t, s, s.cfg)
				}
				if s.faultRobot(owner).Status != RobotCharging {
					t.Fatal("charging did not progress")
				}
				if r := s.faultRobot("R1"); r.X != 18 || r.Y != 1 {
					t.Fatalf("indirect relocation was merely delayed: %s", diagJSON(diagSnapshot(s)))
				}
			}
		})
	}
}

func TestDirectBlockerInfeasibleFallback(t *testing.T) {
	for _, reason := range []string{"energy", "skip", "claims", "unfinished clearance"} {
		t.Run(reason, func(t *testing.T) {
			s := directBlockerRecordedScene(t, 873)
			skip := make([]bool, len(s.state.Robots))
			claimed := s.tripDestinations()
			switch reason {
			case "energy":
				s.faultRobot("R2").Battery = 1
			case "skip":
				skip[1] = true
			case "unfinished clearance":
				// A prior tick's uncompleted relocation is not a fresh admission.
				s.faultRobot("R2").Status = RobotMoving
				s.navigation["R2"] = robotNavigation{target: Point{X: 17, Y: 1}, route: []Point{{X: 17, Y: 1}}, relocation: true}
			case "claims":
				for y := 0; y < s.cfg.Height; y++ {
					for x := 0; x < s.cfg.Width; x++ {
						claimed[Point{X: x, Y: y}] = "R6"
					}
				}
			}
			if s.directBlockerCanClear(4, claimed, skip, nil) {
				t.Fatal("infeasible/skipped direct blocker suppressed fallback")
			}
			owner, witness, ok := s.idleObstructionForPass(*s.faultRobot("R1"), claimed, skip, nil)
			if !ok || owner.ID != "R5" || len(witness) == 0 {
				t.Fatal("indirect eligibility lost")
			}
			if reason != "claims" {
				s.clearIdleAisles(skip)
				if !s.navigation["R1"].relocation {
					t.Fatal("indirect fallback was not admitted")
				}
				assertInvariants(t, s, s.cfg)
			}
		})
	}
}

func TestDirectBlockerAlreadyAdmittedThisPass(t *testing.T) {
	s := directBlockerRecordedScene(t, 873)
	// Swap positions/batteries so the direct blocker is processed first.
	a, b := &s.state.Robots[0], &s.state.Robots[1]
	a.X, b.X = b.X, a.X
	a.Y, b.Y = b.Y, a.Y
	a.Battery, b.Battery = b.Battery, a.Battery
	s.clearIdleAisles(make([]bool, len(s.state.Robots)))
	if nav := s.navigation["R1"]; !nav.relocation || nav.target != (Point{X: 17, Y: 1}) {
		t.Fatalf("direct clearance=%+v", nav)
	}
	if r := s.faultRobot("R2"); r.Status != RobotIdle || r.X != 18 || r.Y != 1 {
		t.Fatal("accepted clearance did not defer indirect analysis")
	}
	assertInvariants(t, s, s.cfg)
}

func TestIdleBlockerDeliveryAndCharging(t *testing.T) {
	for _, charging := range []bool{false, true} {
		s := idleBlockerScene(t, charging)
		if s.protected[Point{2, 0}] {
			t.Fatal("fixture must exercise outside-P clearing")
		}
		beforeJob := s.state.Robots[1].JobID
		s.Tick()
		if r := s.state.Robots[0]; r.Status != RobotIdle || r.X != 2 || r.Y != 1 || r.Battery != 99 {
			t.Fatalf("blocker did not clear: %+v", r)
		}
		if r := s.state.Robots[1]; r.X != 2 || r.Y != 0 || r.Battery != 99 {
			t.Fatalf("active mission did not advance: %+v", r)
		}
		if charging && (s.reservedStation("R2") == nil || beforeJob != "") {
			t.Fatal("charging reservation lost")
		}
		for tick := 0; tick < 80; tick++ {
			s.Tick()
			assertInvariants(t, s, s.cfg)
		}
		if r := s.state.Robots[0]; r.Status != RobotIdle || r.X != 2 || r.Y != 1 {
			t.Fatalf("unnecessary relocation loop: %+v", r)
		}
		if !charging && s.state.Jobs[0].Status != JobDone {
			t.Fatal("delivery did not finish")
		}
	}
}

func counterfactualBlockerScene(t *testing.T) *Sim {
	t.Helper()
	s := autochargeSim(t, WarehouseConfig(), Point{14, 10}, 44)
	r := &s.state.Robots[1]
	r.X, r.Y, r.Status, r.Battery, r.JobID = 15, 11, RobotMoving, 32, "J1"
	// Keep the left-hand bypass occupied, as in the recorded traffic scene.
	s.state.Robots[2].X, s.state.Robots[2].Y = 14, 11
	s.state.Jobs = []JobState{{ID: "J1", From: Point{14, 4}, To: Point{7, 0}, Status: JobInProgress, AssignedRobot: r.ID}}
	s.prepareNavigation(r, Point{7, 0})
	r.WaitTicks = 1
	s.recovery[r.ID] = robotRecovery{resume: true}
	return s
}

func TestIdleBlockerUsableRouteContinues(t *testing.T) {
	s := counterfactualBlockerScene(t)
	s.recovery["R2"] = robotRecovery{}
	s.state.Robots[1].WaitTicks = 0
	before := diagSnapshot(s)
	s.clearIdleAisles(make([]bool, len(s.state.Robots)))
	if !reflect.DeepEqual(before, diagSnapshot(s)) {
		t.Fatal("hypothetical cheaper route moved an idle robot")
	}
	s.Tick()
	if r := s.state.Robots[1]; r.X != 15 || r.Y != 10 || r.WaitTicks != 0 {
		t.Fatalf("usable next step did not continue: %+v", r)
	}
	s.Tick()
	if s.state.Robots[0].X == 14 && s.state.Robots[0].Y == 10 {
		t.Fatal("actual next-cell blockage was not cleared")
	}
	assertInvariants(t, s, s.cfg)
}

func TestIdleBlockerSeed3NoRepeatedBypass(t *testing.T) {
	s := newTestSim(t, WarehouseConfig(), 3)
	for tick := 1; tick <= 100; tick++ {
		before := s.state.Robots[3]
		s.Tick()
		assertInvariants(t, s, s.cfg)
		nav := s.navigation["R4"]
		if before.Status == RobotIdle && nav.relocation && ((Point{before.X, before.Y}) == (Point{15, 1}) && nav.target == (Point{17, 1}) || (Point{before.X, before.Y}) == (Point{17, 1}) && nav.target == (Point{15, 1})) {
			t.Fatalf("unnecessary R4 bypass at tick%d", tick)
		}
	}
}

func TestIdleBlockerCounterfactualRoute(t *testing.T) {
	s := counterfactualBlockerScene(t)
	r := &s.state.Robots[1]
	before := diagSnapshot(s)
	if _, ok := s.clearingAnalysisRoute(*r, Point{7, 0}, nil); ok {
		t.Fatal("blocked mission must be unaffordable")
	}
	witness, ok := s.clearingAnalysisRoute(*r, Point{7, 0}, &s.state.Robots[0])
	if !ok || len(witness) != 19 {
		t.Fatalf("counterfactual route=%v feasible=%v", witness, ok)
	}
	if !reflect.DeepEqual(before, diagSnapshot(s)) {
		t.Fatal("route analysis mutated simulator")
	}
	s.clearIdleAisles(make([]bool, len(s.state.Robots)))
	if nav := s.navigation["R1"]; !nav.relocation || nav.target != (Point{13, 10}) {
		t.Fatalf("wrong evacuation: %+v", nav)
	}
	for _, p := range witness {
		if p == s.navigation["R1"].target {
			t.Fatal("relocation blocks the witness route")
		}
	}
	for tick := 0; tick < 35; tick++ {
		s.Tick()
		assertInvariants(t, s, s.cfg)
	}
	if s.state.Jobs[0].Status != JobDone {
		t.Fatal("counterfactual mission never completed")
	}
}

func TestIdleBlockerInsufficientEvidence(t *testing.T) {
	s := counterfactualBlockerScene(t)
	s.state.Robots[1].Battery = 26 // Even the unobstructed mission needs 27.
	before := diagSnapshot(s)
	s.clearIdleAisles(make([]bool, len(s.state.Robots)))
	if !reflect.DeepEqual(before, diagSnapshot(s)) {
		t.Fatal("route-cost improvement without feasible admission triggered clearing")
	}
}

func TestIdleBlockerPassPriority(t *testing.T) {
	for _, dispatch := range []bool{false, true} {
		s := idleBlockerScene(t, false)
		skip := make([]bool, len(s.state.Robots))
		if dispatch {
			s.cfg.AutoDispatch = true
			s.state.Jobs = append(s.state.Jobs, JobState{ID: "J2", From: Point{2, 0}, To: Point{2, 1}, Status: JobPending})
			s.assignPending(skip)
		} else {
			s.state.Robots[0].Battery = 20
			s.seekCharging(skip)
		}
		before := diagSnapshot(s)
		s.clearIdleAisles(skip)
		if !reflect.DeepEqual(before, diagSnapshot(s)) || s.navigation["R1"].relocation {
			t.Fatal("clearing displaced a same-tick job/charging decision")
		}
		assertInvariants(t, s, s.cfg)
	}
}

func TestIdleBlockerAvoidsFutureDelivery(t *testing.T) {
	s := idleBlockerScene(t, false)
	s.state.Jobs[0].Status = JobAssigned
	s.state.Jobs[0].From, s.state.Jobs[0].To = Point{2, 0}, Point{2, 1}
	s.clearIdleAisles(make([]bool, len(s.state.Robots)))
	if s.navigation["R1"].target != (Point{1, 1}) {
		t.Fatalf("relocation obstructs future delivery: %+v", s.navigation["R1"])
	}
	assertInvariants(t, s, s.cfg)
}

func TestIdleBlockerEligibility(t *testing.T) {
	for _, name := range []string{"no mission", "feasible detour", "insufficient battery", "skipped", "disabled", "faulted", "offline", "charging", "assigned"} {
		t.Run(name, func(t *testing.T) {
			s := idleBlockerScene(t, false)
			skip := make([]bool, len(s.state.Robots))
			r := &s.state.Robots[0]
			switch name {
			case "no mission":
				s.state.Robots[1].Status = RobotIdle
			case "feasible detour":
				s.state.Robots[1].X, s.state.Robots[1].Y = 3, 1
				s.state.Jobs[0].To = Point{1, 1}
				s.navigation["R2"] = robotNavigation{target: Point{1, 1}, route: []Point{{3, 0}, {2, 0}, {1, 0}, {1, 1}}}
			case "insufficient battery":
				r.Battery = 4
			case "skipped":
				skip[0] = true
			case "disabled":
				s.cfg.Recovery = false
			case "faulted":
				r.Status, r.Battery = RobotFaulted, 0
			case "offline":
				r.Status = RobotOffline
			case "charging":
				r.Status = RobotCharging
			case "assigned":
				r.Status, r.JobID = RobotMoving, "J2"
			}
			before := diagSnapshot(s)
			s.clearIdleAisles(skip)
			if !reflect.DeepEqual(before, diagSnapshot(s)) {
				t.Fatal("ineligible blocker changed")
			}
		})
	}
}

func TestIdleBlockerMultipleClaims(t *testing.T) {
	var want diagFrame
	for repeat := 0; repeat < 20; repeat++ {
		s := idleBlockerScene(t, false)
		s.state.Robots[1].X, s.state.Robots[1].Y, s.state.Robots[1].Status, s.state.Robots[1].JobID = 2, 1, RobotIdle, ""
		delete(s.navigation, "R2")
		for i, position := range []Point{{3, 0}, {3, 1}} {
			r := &s.state.Robots[i+2]
			r.X, r.Y, r.Status, r.JobID = position.X, position.Y, RobotMoving, fmt.Sprintf("J%d", i+1)
			job := JobState{ID: r.JobID, From: position, To: Point{2, i}, Status: JobInProgress, AssignedRobot: r.ID}
			if i == 0 {
				s.state.Jobs[0] = job
			} else {
				s.state.Jobs = append(s.state.Jobs, job)
			}
			s.prepareNavigation(r, job.To)
		}
		if repeat%2 == 1 {
			slices.Reverse(s.state.Robots)
		}
		s.clearIdleAisles(make([]bool, len(s.state.Robots)))
		if s.navigation["R1"].target != (Point{1, 1}) || s.navigation["R2"].target != (Point{2, 2}) {
			t.Fatalf("claims=%v", s.navigation)
		}
		slices.SortFunc(s.state.Robots, func(a, b RobotState) int { return idNumber(a.ID) - idNumber(b.ID) })
		if repeat == 0 {
			want = diagSnapshot(s)
		} else if !reflect.DeepEqual(want, diagSnapshot(s)) {
			t.Fatal("selection depends on slice order")
		}
		for tick := 0; tick < 20; tick++ {
			s.Tick()
			assertInvariants(t, s, s.cfg)
		}
		if s.state.Jobs[0].Status != JobDone || s.state.Jobs[1].Status != JobDone {
			t.Fatal("blocked deliveries did not complete")
		}
	}
}

func TestProtectedCells(t *testing.T) {
	cfg := WarehouseConfig()
	s := newTestSim(t, cfg, 42)
	want := map[Point]bool{{0, 0}: true, {1, 0}: true, {0, 1}: true, {19, 0}: true, {18, 0}: true, {19, 1}: true}
	for _, x := range cfg.AisleColumns {
		for y := 2; y <= 9; y++ {
			want[Point{X: x, Y: y}] = true
		}
	}
	if len(s.protected) != 30 || !reflect.DeepEqual(s.protected, want) {
		t.Fatalf("protected set=%v", s.protected)
	}
	for _, point := range []Point{{4, 1}, {4, 10}, {2, 0}, {17, 0}, {-1, 0}, {20, 0}} {
		if s.protected[point] {
			t.Fatalf("unexpected protected cell %v", point)
		}
	}
	cfg.Stations[0] = Point{1, 1}
	if !s.protected[Point{0, 0}] || s.cfg.Stations[0] != (Point{0, 0}) {
		t.Fatal("cache aliases caller configuration")
	}
	other := newTestSim(t, WarehouseConfig(), 42)
	delete(s.protected, Point{0, 0})
	if !other.protected[Point{0, 0}] {
		t.Fatal("protected cache shared by simulators")
	}
	cfg = DefaultConfig()
	cfg.Walls = []Point{{1, 0}}
	if protectedCells(cfg)[Point{1, 0}] || protectedCells(cfg)[Point{-1, 0}] {
		t.Fatal("non-walkable access cell protected")
	}
}

func TestProtectedStationEvacuation(t *testing.T) {
	cfg := WarehouseConfig()
	s := autochargeSim(t, cfg, Point{0, 0}, 99)
	requireInjection(t, s.SetCharging("R1", "S1"))
	s.Tick()
	r := s.state.Robots[0]
	if r.Status != RobotIdle || r.X != 0 || r.Y != 0 || r.Battery != 100 || len(s.navigation) != 0 {
		t.Fatal("charging completion performed additional work")
	}
	s.Tick()
	r = s.state.Robots[0]
	if r.Status != RobotMoving || (Point{X: r.X, Y: r.Y}) != (Point{1, 0}) || s.navigation[r.ID].target != (Point{2, 0}) || r.Battery != 99 {
		t.Fatalf("evacuation/tie order: %+v nav=%+v", r, s.navigation[r.ID])
	}
	assertInvariants(t, s, s.cfg)
	s.Tick()
	r = s.state.Robots[0]
	if r.Status != RobotIdle || r.X != 2 || r.Y != 0 || r.Battery != 98 || len(s.navigation) != 0 || s.recovery[r.ID].hold != 0 {
		t.Fatalf("evacuation did not complete without hold: %+v", r)
	}
	if !s.stationFreeFor(s.state.Stations[0], "") {
		t.Fatal("evacuation did not free station")
	}
	assertInvariants(t, s, s.cfg)
}

func TestProtectedEvacuationClaims(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Recovery, cfg.AutoCharge = true, true
	cfg.Walls = []Point{{2, 0}}
	s := autochargeSim(t, cfg, Point{0, 0}, 100)
	r2 := &s.state.Robots[1]
	r2.X, r2.Y, r2.Status, r2.Battery = 1, 0, RobotIdle, 100
	slices.Reverse(s.state.Robots)
	s.clearIdleAisles(make([]bool, len(s.state.Robots)))
	if s.navigation["R1"].target != (Point{1, 1}) || s.navigation["R2"].target != (Point{2, 1}) {
		t.Fatalf("ID/claim order: R1=%+v R2=%+v", s.navigation["R1"], s.navigation["R2"])
	}
	slices.SortFunc(s.state.Robots, func(a, b RobotState) int { return idNumber(a.ID) - idNumber(b.ID) })
	assertInvariants(t, s, s.cfg)
}

func TestProtectedEvacuationAvoidsJobDestination(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Recovery, cfg.AutoCharge = true, true
	cfg.Walls = []Point{{2, 0}}
	s := autochargeSim(t, cfg, Point{0, 0}, 100)
	r := &s.state.Robots[1]
	r.X, r.Y, r.Status, r.JobID = 1, 0, RobotMoving, "J1"
	s.state.Jobs = []JobState{{ID: "J1", From: Point{1, 1}, To: Point{2, 2}, Status: JobAssigned, AssignedRobot: r.ID}}
	s.prepareNavigation(r, Point{1, 1})
	s.clearIdleAisles(make([]bool, len(s.state.Robots)))
	if s.navigation["R1"].target == (Point{1, 1}) || s.navigation["R1"].target != (Point{0, 2}) {
		t.Fatal("evacuation claimed a job's in-flight goal")
	}
	assertInvariants(t, s, s.cfg)
}

func TestProtectedEvacuationRetryAndExit(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Recovery, cfg.AutoCharge = true, true
	s := autochargeSim(t, cfg, Point{0, 0}, 100)
	s.state.Robots[1].X, s.state.Robots[1].Y = 1, 0
	s.state.Robots[2].X, s.state.Robots[2].Y = 0, 1
	s.clearIdleAisles(make([]bool, len(s.state.Robots)))
	if s.state.Robots[0].Status != RobotIdle || len(s.navigation) != 0 {
		t.Fatal("unreachable evacuation started")
	}
	s.state.Robots[1].X, s.state.Robots[1].Y = 3, 11
	s.clearIdleAisles(make([]bool, len(s.state.Robots)))
	if !s.navigation["R1"].relocation {
		t.Fatal("evacuation did not retry")
	}
	// Station occupancy/reservation is ignored only at the estimated exit goal.
	s = autochargeSim(t, cfg, Point{1, 0}, 100)
	s.state.Robots[1].X, s.state.Robots[1].Y = 0, 0
	s.state.Stations[0].OccupiedBy = "R2"
	s.state.Robots[2].X, s.state.Robots[2].Y = 19, 0
	s.state.Stations[1].OccupiedBy = "R3"
	if exit := s.relocationExit(s.state.Robots[0], Point{2, 0}); exit != 2 {
		t.Fatalf("occupied exit estimate=%d", exit)
	}
	s.state.Robots[3].X, s.state.Robots[3].Y = 1, 0
	s.state.Robots[0].X, s.state.Robots[0].Y = 2, 1
	if exit := s.relocationExit(s.state.Robots[0], Point{2, 0}); exit != 4 {
		t.Fatalf("parked transit obstacle ignored: exit=%d", exit)
	}
}

func TestProtectedTripInvariantViolations(t *testing.T) {
	for _, name := range []string{"missing record", "protected target", "duplicate target", "reserved station occupied", "charging target"} {
		t.Run(name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Recovery, cfg.AutoCharge = true, true
			s := autochargeSim(t, cfg, Point{2, 2}, 100)
			r := &s.state.Robots[0]
			r.Status = RobotMoving
			s.navigation[r.ID] = robotNavigation{target: Point{2, 1}, route: []Point{{2, 1}}, relocation: true}
			switch name {
			case "missing record":
				delete(s.navigation, r.ID)
			case "protected target":
				nav := s.navigation[r.ID]
				nav.target = Point{0, 1}
				s.navigation[r.ID] = nav
			case "duplicate target":
				s.state.Robots[1].Status = RobotMoving
				s.state.Robots[1].X, s.state.Robots[1].Y = 3, 1
				s.navigation["R2"] = s.navigation[r.ID]
			case "reserved station occupied":
				s.state.Stations[0].ReservedBy = r.ID
				s.state.Robots[1].X, s.state.Robots[1].Y = 0, 0
				s.navigation[r.ID] = robotNavigation{target: Point{0, 0}, route: []Point{{2, 1}, {1, 1}, {0, 1}, {0, 0}}}
			case "charging target":
				s.state.Stations[0].ReservedBy = r.ID
				nav := s.navigation[r.ID]
				nav.relocation = false
				s.navigation[r.ID] = nav
			}
			err := checkTripInvariants(s.Snapshot(), s.cfg, s.navigation)
			want := "I19:"
			if name == "reserved station occupied" {
				want = "I20:"
			}
			if err == nil || !strings.HasPrefix(err.Error(), want) {
				t.Fatalf("invariant=%v want=%s", err, want)
			}
		})
	}
}

func TestProtectedEvacuationUnreachableExit(t *testing.T) {
	s := clearingSim(t, WarehouseConfig())
	for i, point := range []Point{{1, 0}, {0, 1}, {18, 0}, {19, 1}} {
		r := &s.state.Robots[i+1]
		r.X, r.Y, r.Status = point.X, point.Y, RobotOffline
	}
	if exit := s.relocationExit(s.state.Robots[0], Point{4, 1}); exit != -1 {
		t.Fatalf("parked access obstacles ignored: exit=%d", exit)
	}
	s.clearIdleAisles(make([]bool, len(s.state.Robots)))
	if s.state.Robots[0].Status != RobotIdle || len(s.navigation) != 0 {
		t.Fatal("relocation started without a reachable charging exit")
	}
	assertInvariants(t, s, s.cfg)
}

func clearingSim(t *testing.T, cfg Config) *Sim {
	t.Helper()
	cfg.AutoCharge, cfg.Recovery, cfg.AutoDispatch = true, true, false
	cfg.InitialJobs = 0
	cfg.RobotStarts[0] = Point{X: 4, Y: 5}
	return newTestSim(t, cfg, 42)
}

func TestIdleClearingLifecycle(t *testing.T) {
	s := clearingSim(t, WarehouseConfig())
	for tick := 1; tick <= 4; tick++ {
		s.Tick()
		r := s.state.Robots[0]
		if r.X != 4 || r.Y != 5-tick || r.Battery != 100-tick || r.JobID != "" || r.WaitTicks != 0 || r.Blocked {
			t.Fatalf("tick %d: %+v", tick, r)
		}
		_, cached := s.navigation[r.ID]
		if tick < 4 && (r.Status != RobotMoving || !s.navigation[r.ID].relocation || s.reservedStation(r.ID) != nil) {
			t.Fatalf("missing private relocation: %+v", r)
		}
		if tick == 4 && (r.Status != RobotIdle || cached) {
			t.Fatalf("relocation not released: %+v", r)
		}
		assertInvariants(t, s, s.cfg)
	}
	s.Tick()
	if s.state.Robots[0].Y != 1 || s.state.Robots[0].Status != RobotIdle {
		t.Fatal("completed relocation restarted")
	}
}

func TestClearingTargetOrder(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cfg    Config
		change func(*Sim)
		want   Point
	}{
		{name: "distance", cfg: WarehouseConfig(), change: func(s *Sim) { s.state.Robots[0].Y = 8 }, want: Point{X: 4, Y: 10}},
		{name: "occupied nearer exit", cfg: WarehouseConfig(), change: func(s *Sim) { s.state.Robots[1].X, s.state.Robots[1].Y = 4, 1 }, want: Point{X: 4, Y: 10}},
		{name: "unreachable nearer exit", cfg: WarehouseConfig(), change: func(s *Sim) { s.state.Robots[1].X, s.state.Robots[1].Y = 4, 3 }, want: Point{X: 4, Y: 10}},
		{name: "X tie", cfg: DefaultConfig(), change: func(*Sim) {}, want: Point{X: 3, Y: 5}},
		{name: "Y tie", cfg: DefaultConfig(), change: func(s *Sim) {
			s.cfg.AisleYMin, s.cfg.AisleYMax = 2, 8
			for y := 2; y <= 8; y++ {
				s.cfg.Walls = append(s.cfg.Walls, Point{X: 3, Y: y}, Point{X: 5, Y: y})
			}
		}, want: Point{X: 4, Y: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := clearingSim(t, tc.cfg)
			tc.change(s)
			s.protected = protectedCells(s.cfg)
			nav, ok := s.relocationRoute(s.state.Robots[0])
			if !ok || nav.target != tc.want {
				t.Fatalf("route=%+v found=%v want=%v", nav, ok, tc.want)
			}
		})
	}
}

func TestClearingObstaclesAndEnergy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Sim)
		want   bool
	}{
		{name: "one remaining after exit", change: func(s *Sim) { s.state.Robots[0].Battery = 10 }, want: true},
		{name: "exit energy missing", change: func(s *Sim) { s.state.Robots[0].Battery = 9 }},
		{name: "zero remaining rejected", change: func(s *Sim) { s.state.Robots[0].Battery = 4 }},
		{name: "movement cost", change: func(s *Sim) { s.cfg.MovingDrain = 2; s.state.Robots[0].Battery = 8 }},
		{name: "occupied exits", change: func(s *Sim) {
			s.state.Robots[1].X, s.state.Robots[1].Y = 4, 1
			s.state.Robots[2].X, s.state.Robots[2].Y = 4, 10
		}},
		{name: "station exits", change: func(s *Sim) { s.cfg.Stations = append(s.cfg.Stations, Point{X: 4, Y: 1}, Point{X: 4, Y: 10}) }},
		{name: "wall exits", change: func(s *Sim) { s.cfg.Walls = append(s.cfg.Walls, Point{X: 4, Y: 1}, Point{X: 4, Y: 10}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := clearingSim(t, WarehouseConfig())
			tc.change(s)
			s.protected = protectedCells(s.cfg)
			before := s.Snapshot()
			nav, ok := s.relocationRoute(s.state.Robots[0])
			if ok != tc.want {
				t.Fatalf("found=%v route=%+v", ok, nav)
			}
			if !reflect.DeepEqual(before, s.Snapshot()) || len(s.navigation) != 0 {
				t.Fatal("candidate planning mutated state")
			}
			s.clearIdleAisles(make([]bool, len(s.state.Robots)))
			if !tc.want && (s.state.Robots[0].Status != RobotIdle || len(s.navigation) != 0) {
				t.Fatal("unsafe relocation started")
			}
		})
	}
}

func TestClearingOccupiedAisle(t *testing.T) {
	s := clearingSim(t, DefaultConfig())
	s.cfg.AisleColumns = []int{4, 5}
	s.state.Robots[1].X, s.state.Robots[1].Y = 5, 7
	s.cfg.Walls = []Point{{X: 3, Y: 5}}
	nav, ok := s.relocationRoute(s.state.Robots[0])
	if !ok {
		t.Fatal("expected other exit")
	}
	for _, p := range nav.route {
		if s.cfg.CellTypeAt(p) == CellStation || s.cfg.CellTypeAt(p) == CellWall || s.navigationBlocked(s.state.Robots[0], p, true) {
			t.Fatalf("illegal relocation cell %v", p)
		}
	}
}

func TestClearingEligibility(t *testing.T) {
	for _, name := range []string{"disabled", "skip", "job", "charging", "outside aisle", "assigned"} {
		t.Run(name, func(t *testing.T) {
			s := clearingSim(t, WarehouseConfig())
			skip := make([]bool, len(s.state.Robots))
			switch name {
			case "disabled":
				s.cfg.Recovery = false
			case "skip":
				skip[0] = true
			case "job":
				s.state.Robots[0].JobID = "J1"
			case "charging":
				s.state.Robots[0].Status = RobotCharging
			case "outside aisle":
				s.state.Robots[0].Y = 1
			case "assigned":
				s.state.Robots[0].Status = RobotMoving
			}
			before := s.Snapshot()
			s.clearIdleAisles(skip)
			if !reflect.DeepEqual(s.Snapshot(), before) || len(s.navigation) != 0 {
				t.Fatal("ineligible robot relocated")
			}
		})
	}
}

func TestClearingPassPriority(t *testing.T) {
	t.Run("dispatch first", func(t *testing.T) {
		s := clearingSim(t, WarehouseConfig())
		s.cfg.AutoDispatch = true
		s.state.Jobs = []JobState{{ID: "J1", From: Point{X: 4, Y: 4}, To: Point{X: 1, Y: 1}, Status: JobPending}}
		s.Tick()
		r := s.state.Robots[0]
		if r.JobID != "J1" || r.Y != 4 || s.navigation[r.ID].relocation {
			t.Fatalf("clearing displaced job: %+v", r)
		}
		assertInvariants(t, s, s.cfg)
	})
	t.Run("charging first", func(t *testing.T) {
		s := clearingSim(t, WarehouseConfig())
		s.state.Robots[0].Battery = 30
		s.Tick()
		r := s.state.Robots[0]
		if r.Status != RobotMoving || s.navigation[r.ID].relocation || s.reservedStation(r.ID) == nil {
			t.Fatalf("clearing displaced charging: %+v", r)
		}
		assertInvariants(t, s, s.cfg)
	})
}

func TestRecoveryDisabledCompatibility(t *testing.T) {
	cfg := WarehouseConfig()
	cfg.Recovery = false
	a := newTestSim(t, cfg, 42)
	cfg.RecoverAfter, cfg.HoldTicks = 99, 8
	b := newTestSim(t, cfg, 42)
	for tick := 1; tick <= 300; tick++ {
		a.Tick()
		b.Tick()
		if !reflect.DeepEqual(a.Snapshot(), b.Snapshot()) || !reflect.DeepEqual(a.navigation, b.navigation) {
			t.Fatalf("reserved settings affected tick %d", tick)
		}
	}
}

func TestRecordedIdleAisleClearing(t *testing.T) {
	// R0 S2/seed1: R6 at (14,5), battery 14, with a four-step aisle exit.
	// Recovery requires AutoCharge, so this is an enabled counterfactual;
	// the original S2 scenario remains explicitly recovery-disabled.
	s := clearingSim(t, WarehouseConfig())
	s.state.Robots[0].X, s.state.Robots[0].Y = 1, 11
	r := &s.state.Robots[5]
	r.X, r.Y, r.Battery = 14, 5, 14
	for i := range s.state.Stations {
		station := &s.state.Stations[i]
		blocker := &s.state.Robots[i+1]
		blocker.X, blocker.Y, blocker.Status = station.X, station.Y, RobotOffline
		station.OccupiedBy = blocker.ID
	}
	for tick := 1; tick <= 4; tick++ {
		s.Tick()
		assertInvariants(t, s, s.cfg)
	}
	if r.Status != RobotIdle || r.X != 14 || r.Y != 1 || r.Battery != 10 {
		t.Fatalf("recorded affordable exit not cleared: %+v", r)
	}
}

func TestClearingReplanAndFault(t *testing.T) {
	s := clearingSim(t, WarehouseConfig())
	s.clearIdleAisles(make([]bool, len(s.state.Robots)))
	r := &s.state.Robots[0]
	prior := s.navigation[r.ID]
	r.Battery = 4
	if s.planNavigation(r, prior.target, true) || !reflect.DeepEqual(prior, s.navigation[r.ID]) {
		t.Fatal("unaffordable replan replaced trip")
	}
	s.state.Robots[1].X, s.state.Robots[1].Y = 4, 4
	s.stepMoving(r)
	if r.Y != 5 || r.Battery != 4 || r.WaitTicks != 1 || !s.navigation[r.ID].relocation || s.reservedStation(r.ID) != nil {
		t.Fatalf("failed wait: %+v", r)
	}
	s.stepMoving(r)
	s.stepMoving(r)
	if r.WaitTicks != 3 || r.Battery != 4 || !reflect.DeepEqual(prior, s.navigation[r.ID]) {
		t.Fatal("failed scheduled replan changed trip, battery, or wait count")
	}
	s.state.Robots[1].X, s.state.Robots[1].Y = 3, 11
	r.Battery = 1 // External depletion must still fault before reaching the exit.
	s.stepMoving(r)
	if r.Status != RobotFaulted || r.Battery != 0 || r.Y != 4 {
		t.Fatalf("depletion hidden: %+v", r)
	}
	s.finishNavigation(r)
	if _, ok := s.navigation[r.ID]; ok || r.WaitTicks != 0 || r.Blocked {
		t.Fatal("fault retained trip or wait flags")
	}
}

func TestRelocationInvariants(t *testing.T) {
	for _, name := range []string{"valid", "reservation", "missing", "bad target", "bad step", "idle trip", "disabled"} {
		t.Run(name, func(t *testing.T) {
			s := clearingSim(t, WarehouseConfig())
			s.clearIdleAisles(make([]bool, len(s.state.Robots)))
			nav := s.navigation["R1"]
			switch name {
			case "reservation":
				s.state.Stations[0].ReservedBy = "R1"
			case "missing":
				delete(s.navigation, "R1")
			case "bad target":
				nav.target = Point{X: 4, Y: 3}
				s.navigation["R1"] = nav
			case "bad step":
				nav.route[0] = Point{X: 18, Y: 11}
			case "idle trip":
				s.state.Robots[0].Status = RobotIdle
			case "disabled":
				s.cfg.Recovery = false
			}
			err := checkInvariants(s.Snapshot(), s.cfg, s.navigation)
			if name == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.HasPrefix(err.Error(), "I6:") && !strings.HasPrefix(err.Error(), "I16:") {
				t.Fatalf("invalid trip accepted: %v", err)
			}
		})
	}
}

func TestRelocationCountdownCleanup(t *testing.T) {
	s := clearingSim(t, WarehouseConfig())
	s.Tick()
	if !s.navigation["R1"].relocation {
		t.Fatal("expected relocation")
	}
	requireInjection(t, s.InjectUnresponsive("R1"))
	for tick := 0; tick < s.cfg.UnresponsiveDelay-1; tick++ {
		s.Tick()
	}
	before := s.state.Robots[0]
	s.Tick()
	r := s.state.Robots[0]
	if r.Status != RobotOffline || r.X != before.X || r.Y != before.Y || r.Battery != before.Battery || r.WaitTicks != 0 || r.Blocked {
		t.Fatalf("relocation changed countdown expiry: before=%+v after=%+v", before, r)
	}
	if _, ok := s.navigation[r.ID]; ok || s.reservedStation(r.ID) != nil {
		t.Fatal("offline robot retained relocation")
	}
	assertInvariants(t, s, s.cfg)
}
