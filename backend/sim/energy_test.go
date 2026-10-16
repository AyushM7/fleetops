package sim

import (
	"flag"
	"fmt"
	"reflect"
	"slices"
	"testing"
)

var stabilizationSweep = flag.Bool("stabilization-sweep", false, "measure every Step 2 S1 gate for reserves 10,15,20,25")

type energySweepReport struct {
	Reserve                                                                      int
	Seed                                                                         int64
	MaxWait, MaxChargeWait, MaxProtectedIdle, MinArrival, Arrivals, ZeroArrivals int
	Completed, Retreats, Relocations, CycleTicks, FinalCycles                    int
	Faults, FinalBlocked                                                         []string
	Failures                                                                     map[string]string
	RetreatsByRobot, RelocationsByRobot, ChargeSessions                          map[string]int
	WaitClasses                                                                  map[string]int
	Final                                                                        diagFrame
}

func TestStabilizationReserveSweep(t *testing.T) {
	if !*stabilizationSweep {
		t.Skip("requires -stabilization-sweep")
	}
	passing := []int{}
	for _, reserve := range []int{10, 15, 20, 25} {
		passes := true
		for _, seed := range []int64{1, 2, 3, 42, 99} {
			report := measureEnergyS1(t, reserve, seed)
			t.Logf("RESERVE_SWEEP %s", diagJSON(report))
			passes = passes && len(report.Failures) == 0
		}
		if passes {
			passing = append(passing, reserve)
		}
	}
	if len(passing) == 0 {
		t.Fatal("no reserve passes every S1 gate; retain the accepted configuration and stop")
	}
	selected := min(passing[0]+5, 25)
	t.Logf("SELECTED_RESERVE %d", selected)
	for _, seed := range []int64{1, 2, 3, 42, 99} {
		report := measureEnergyS1(t, selected, seed)
		t.Logf("RESERVE_VERIFY %s", diagJSON(report))
		if len(report.Failures) > 0 {
			t.Errorf("selected reserve failed seed%d: %v", seed, report.Failures)
		}
	}
}

func measureEnergyS1(t *testing.T, reserve int, seed int64) energySweepReport {
	t.Helper()
	cfg := WarehouseConfig()
	cfg.ReserveBattery = reserve
	s := newTestSim(t, cfg, seed)
	r := energySweepReport{Reserve: reserve, Seed: seed, MinArrival: 101, Faults: []string{}, FinalBlocked: []string{}, Failures: map[string]string{}, RetreatsByRobot: map[string]int{}, RelocationsByRobot: map[string]int{}, ChargeSessions: map[string]int{}, WaitClasses: map[string]int{}}
	chargeWait, protectedWait := map[string]int{}, map[string]int{}
	fail := func(gate, detail string) {
		if _, seen := r.Failures[gate]; !seen {
			r.Failures[gate] = fmt.Sprintf("tick%d %s", s.state.Tick, detail)
			t.Logf("RESERVE_FAILURE reserve%d seed%d gate=%s detail=%s snapshot=%s", reserve, seed, gate, r.Failures[gate], diagJSON(diagSnapshot(s)))
		}
	}
	for tick := 1; tick <= 1500; tick++ {
		before := s.Snapshot()
		s.Tick()
		if err := checkInvariants(s.Snapshot(), cfg, s.navigation); err != nil {
			fail("invariants", err.Error())
		}
		if err := checkRecoveryInvariants(s); err != nil {
			fail("recovery invariants", err.Error())
		}
		order := s.recoveryOrder()
		cycles := recoveryCycles(order, s.recoveryBlockers(order))
		if len(cycles) > 0 {
			r.CycleTicks++
			if tick == 1500 {
				r.FinalCycles = len(cycles)
			}
		}
		for i, robot := range s.state.Robots {
			old := before.Robots[i]
			observeEnergyWait(t, s, &r, old, robot, fail)
			observeEnergyCharging(t, s, &r, old, robot, fail)
			observeEnergyRecovery(s, &r, old, robot, fail)
			observeEnergyIdle(s, &r, robot, chargeWait, protectedWait, fail)
		}
	}
	for _, robot := range s.state.Robots {
		done := 0
		for _, job := range s.state.Jobs {
			if job.Status == JobDone && job.AssignedRobot == robot.ID {
				done++
			}
		}
		r.Completed += done
		if r.RelocationsByRobot[robot.ID] > done+r.ChargeSessions[robot.ID]+1 {
			fail("relocation bound", robot.ID)
		}
		if robot.Blocked {
			r.FinalBlocked = append(r.FinalBlocked, robot.ID)
			fail("final blocked", robot.ID)
		}
	}
	if r.Completed != 20 {
		fail("job completion", fmt.Sprintf("done=%d/20", r.Completed))
	}
	r.Final = diagSnapshot(s)
	return r
}

func observeEnergyWait(t *testing.T, s *Sim, r *energySweepReport, old, robot RobotState, fail func(string, string)) {
	r.MaxWait = max(r.MaxWait, robot.WaitTicks)
	if robot.WaitTicks > 40 {
		fail("wait cap", fmt.Sprintf("%s wait=%d", robot.ID, robot.WaitTicks))
		if old.WaitTicks <= 40 {
			cause := diagClassify(diagDependencies(s), robot.ID)
			for _, class := range cause.Classes {
				r.WaitClasses[class]++
			}
			t.Logf("RESERVE_WAIT reserve%d seed%d tick%d %s cause=%s snapshot=%s", r.Reserve, r.Seed, s.state.Tick, robot.ID, diagJSON(cause), diagJSON(diagSnapshot(s)))
		}
	}

}
func observeEnergyCharging(t *testing.T, s *Sim, r *energySweepReport, old, robot RobotState, fail func(string, string)) {
	if robot.Status == RobotFaulted && old.Status != RobotFaulted {
		r.Faults = append(r.Faults, fmt.Sprintf("%s@%d battery%d from %s", robot.ID, s.state.Tick, robot.Battery, old.Status))
		t.Logf("RESERVE_FAULT reserve%d seed%d tick%d before=%s snapshot=%s", r.Reserve, r.Seed, s.state.Tick, diagJSON(old), diagJSON(diagSnapshot(s)))
		fail("fault", robot.ID)
	}
	if robot.Status == RobotCharging && old.Status != RobotCharging {
		r.Arrivals++
		r.MinArrival = min(r.MinArrival, robot.Battery)
		if robot.Battery == 0 {
			r.ZeroArrivals++
			fail("SA5 usage", robot.ID)
		}
		if robot.Battery < 1 {
			fail("arrival floor", robot.ID)
		}
	}
	if old.Status == RobotCharging && robot.Status != RobotCharging {
		r.ChargeSessions[robot.ID]++
	}

}
func observeEnergyRecovery(s *Sim, r *energySweepReport, old, robot RobotState, fail func(string, string)) {
	// A one-step relocation may start and finish between snapshots. It has
	// no job and changes position; idle drain never changes position.
	if old.Status == RobotIdle && old.JobID == "" && robot.JobID == "" &&
		(robot.Status == RobotMoving && s.navigation[robot.ID].relocation || robot.Status == RobotIdle && (robot.X != old.X || robot.Y != old.Y)) {
		r.Relocations++
		r.RelocationsByRobot[robot.ID]++
	}
	for _, at := range s.recovery[robot.ID].retreats {
		if at == s.state.Tick {
			r.Retreats++
			r.RetreatsByRobot[robot.ID]++
		}
	}
	if s.retreatLimited(robot.ID) {
		count := 0
		for _, at := range s.recovery[robot.ID].retreats {
			if at > s.state.Tick-200 {
				count++
			}
		}
		if count > 10 {
			fail("retreat bound", robot.ID)
		}
	}

}
func observeEnergyIdle(s *Sim, r *energySweepReport, robot RobotState, chargeWait, protectedWait map[string]int, fail func(string, string)) {
	if robot.Status == RobotIdle && robot.JobID == "" && s.protected[Point{X: robot.X, Y: robot.Y}] {
		protectedWait[robot.ID]++
	} else {
		protectedWait[robot.ID] = 0
	}
	r.MaxProtectedIdle = max(r.MaxProtectedIdle, protectedWait[robot.ID])
	if protectedWait[robot.ID] > 5 {
		fail("protected idle cap", robot.ID)
	}
	if robot.Status == RobotIdle && robot.JobID == "" && robot.Battery < s.cfg.ChargeBelow {
		if chargeClassForTest(s, robot) == "stranded" {
			fail("stranded", fmt.Sprintf("%s battery%d", robot.ID, robot.Battery))
			chargeWait[robot.ID] = 0
		} else {
			chargeWait[robot.ID]++
		}
	} else {
		chargeWait[robot.ID] = 0
	}
	r.MaxChargeWait = max(r.MaxChargeWait, chargeWait[robot.ID])
	if chargeWait[robot.ID] > 200 {
		fail("charge wait cap", robot.ID)
	}
}
func TestEnergyChargingAdmissionFloorAndSafetyNet(t *testing.T) {
	s := autochargeSim(t, DefaultConfig(), Point{3, 0}, 7)
	s.cfg.MovingDrain = 2
	r := &s.state.Robots[0]
	if !s.routeAffordable(*r, Point{0, 0}, 3) {
		t.Fatal("floor-one charging route rejected")
	}
	r.Battery = 6
	if s.routeAffordable(*r, Point{0, 0}, 3) {
		t.Fatal("zero-arrival replacement admitted")
	}
	r.Battery = 7
	s.seekCharging(make([]bool, len(s.state.Robots)))
	if s.reservedStation(r.ID) == nil {
		t.Fatal("charging trip missing")
	}
	r.Battery = 6 // Exercise the safety net on a pre-existing trip, not admission.
	uses := 0
	for tick := 0; tick < 3; tick++ {
		old := *r
		s.Tick()
		if r.Status == RobotCharging && old.Status != RobotCharging && r.Battery == 0 {
			uses++
		}
	}
	if uses != 1 || r.Status != RobotCharging || r.Battery != 0 || len(s.state.Faults) != 0 {
		t.Fatalf("SA5 uses=%d robot=%+v", uses, r)
	}
	s.Tick()
	if r.Battery != s.cfg.ChargeGain {
		t.Fatal("charging did not begin next tick")
	}
}

func TestEnergySingleAssignmentContingency(t *testing.T) {
	for _, reserve := range []int{10, 15, 20, 25} {
		s := energyR2J19Scene(t)
		s.cfg.ReserveBattery = reserve
		for _, i := range []int{3, 4} {
			r := &s.state.Robots[i]
			r.Status = RobotOffline
			r.X, r.Y = s.cfg.RobotStarts[i].X, s.cfg.RobotStarts[i].Y
		}
		s.state.Stations[0].ReservedBy = ""
		s.state.Stations[1].OccupiedBy = ""
		r := &s.state.Robots[1]
		r.Battery = 40 + reserve - 1
		s.assignPending(make([]bool, len(s.state.Robots)))
		if r.JobID != "" {
			t.Fatalf("reserve%d admitted below assignment boundary", reserve)
		}
		r.Battery++
		s.assignPending(make([]bool, len(s.state.Robots)))
		if r.JobID != "J19" {
			t.Fatalf("reserve%d rejected exact assignment boundary", reserve)
		}
	}
}

func TestEnergyRelocationReplacementIncludesExit(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AutoCharge = true
	s := autochargeSim(t, cfg, Point{1, 0}, 4)
	r := &s.state.Robots[0]
	s.navigation[r.ID] = robotNavigation{target: Point{1, 1}, relocation: true}
	s.state.Stations[0].ReservedBy = "R2"
	s.state.Stations[1].ReservedBy = "R3"
	for _, reserve := range []int{5, 25} {
		s.cfg.ReserveBattery = reserve
		for _, battery := range []int{3, 4} {
			r.Battery = battery
			if s.routeAffordable(*r, Point{1, 1}, 1) != (battery == 4) {
				t.Fatal("relocation must budget step + exit + one, without reserve")
			}
		}
	}
	s.cfg.Walls = []Point{{0, 1}, {1, 0}, {18, 0}, {19, 1}}
	if s.routeAffordable(*r, Point{1, 1}, 1) {
		t.Fatal("relocation accepted without reachable charging exit")
	}
}

func TestEnergyR2J19Admission(t *testing.T) {
	s := energyR2J19Scene(t)
	r := &s.state.Robots[1]
	cfg := s.cfg.Clone()
	skip := make([]bool, len(s.state.Robots))
	s.assignPending(skip)
	if s.state.Jobs[18].Status != JobPending || r.JobID != "" || r.Battery != 39 {
		t.Fatal("J19 assigned without an available charging exit")
	}
	for _, index := range []int{3, 4} {
		other := &s.state.Robots[index]
		other.Status = RobotOffline
		other.X, other.Y = cfg.RobotStarts[index].X, cfg.RobotStarts[index].Y
	}
	s.state.Stations[0].ReservedBy = ""
	s.state.Stations[1].OccupiedBy = ""
	if exit := s.chargingExitDistance(s.state.Jobs[18].To, "", s.dispatchDistance()); exit != 7 {
		t.Fatalf("exit=%d, want 7", exit)
	}
	s.assignPending(skip)
	if s.state.Jobs[18].Status != JobPending {
		t.Fatal("J19 ignored the charging-exit energy")
	}
	r.Battery = 45 // (17 pickup + 16 delivery + 7 exit) + 5 reserve.
	s.assignPending(skip)
	if s.state.Jobs[18].AssignedRobot != "R2" || r.JobID != "J19" {
		t.Fatal("exact complete-mission boundary was rejected")
	}
}

func TestEnergyR3J11RejectsCompleteMissionDetour(t *testing.T) {
	s := energyR3J11Scene(t)
	r := &s.state.Robots[2]
	job := s.state.Jobs[10]
	old := s.navigation[r.ID]
	// The observed replacement has 29 steps. Its current leg fits (34-29=5),
	// but delivery 4 + exit 3 + floor 1 require a complete budget of 37.
	if !s.routeAffordable(*r, job.From, 5) || s.routeAffordable(*r, job.From, 29) {
		t.Fatal("complete-mission budget did not distinguish the detour")
	}
	if s.planNavigation(r, job.From, true) || !reflect.DeepEqual(s.navigation[r.ID], old) {
		t.Fatal("underbudgeted J11 detour replaced the prior route")
	}
	s.state.Tick = 77
	s.Tick()
	if r.WaitTicks != 3 || r.Battery != 34 || r.JobID != "J11" || s.state.Jobs[10].Status != JobAssigned || !reflect.DeepEqual(s.navigation[r.ID], old) {
		t.Fatalf("rejected detour changed job/route/battery: %+v", r)
	}
	assertInvariants(t, s, s.cfg)
}

func TestEnergyR5ChargingDetourPreservesReservation(t *testing.T) {
	for _, battery := range []int{7, 5} {
		cfg := WarehouseConfig()
		cfg.InitialJobs, cfg.AutoDispatch, cfg.NewJobEvery = 0, false, 1000
		s := newTestSim(t, cfg, 42)
		positions := []Point{{X: 11, Y: 10}, {X: 2, Y: 10}, {X: 4, Y: 1}, {X: 19, Y: 0}, {X: 5, Y: 0}, {X: 4, Y: 0}}
		for i, p := range positions {
			s.state.Robots[i].X, s.state.Robots[i].Y = p.X, p.Y
		}
		r := &s.state.Robots[4]
		r.Status, r.Battery, r.WaitTicks = RobotMoving, battery, 2
		s.state.Robots[3].Status = RobotCharging
		s.state.Stations[0].ReservedBy = "R5"
		s.state.Stations[1].OccupiedBy = "R4"
		old := robotNavigation{target: Point{X: 0, Y: 0}, route: []Point{{X: 4, Y: 0}, {X: 3, Y: 0}, {X: 2, Y: 0}, {X: 1, Y: 0}, {X: 0, Y: 0}}}
		s.navigation[r.ID] = old
		s.state.Tick = 191
		if s.planNavigation(r, old.target, true) || !reflect.DeepEqual(s.navigation[r.ID], old) {
			t.Fatal("unaffordable R5 detour was installed")
		}
		s.waitForNavigation(r, old.target)
		if r.Battery != battery || r.WaitTicks != 3 || s.state.Stations[0].ReservedBy != "R5" {
			t.Fatal("failed replan drained battery or lost reservation")
		}
		s.state.Robots[2].X, s.state.Robots[2].Y = 5, 11
		s.state.Robots[5].X, s.state.Robots[5].Y = 11, 11
		for step := 0; step < 5; step++ {
			s.Tick()
			assertInvariants(t, s, s.cfg)
		}
		if r.Status != RobotCharging || r.Battery != battery-5 || s.state.Stations[0].OccupiedBy != "R5" || s.state.Stations[0].ReservedBy != "" {
			t.Fatalf("charging arrival failed: %+v", r)
		}
		s.Tick()
		if r.Battery != battery-5+cfg.ChargeGain {
			t.Fatal("charging gain did not begin next tick")
		}
	}
}

func TestEnergyPickupStepBoundary(t *testing.T) {
	for _, battery := range []int{5, 4, 1} {
		cfg := DefaultConfig()
		cfg.AutoCharge = true
		s := navigationSim(t, cfg, Point{X: 2, Y: 0}, Point{X: 1, Y: 0}, Point{X: 1, Y: 1})
		r := &s.state.Robots[0]
		s.prepareNavigation(r, s.state.Jobs[0].From)
		s.cfg.LowBatteryInjectLevel = battery
		if err := s.InjectLowBattery(r.ID); err != nil {
			t.Fatal(err)
		}
		s.Tick()
		if r.Battery != battery-1 || r.X != 1 || r.Y != 0 || s.state.Jobs[0].Status != JobInProgress {
			t.Fatalf("pickup was not charged once: %+v", r)
		}
		if battery == 1 {
			if r.Status != RobotFaulted || len(s.state.Faults) != 1 {
				t.Fatal("ordinary movement depletion or fault records changed")
			}
		} else if battery == 5 {
			if s.navigation[r.ID].target != s.state.Jobs[0].To || len(s.navigation[r.ID].route) != 1 {
				t.Fatal("exact delivery budget rejected")
			}
			s.Tick()
			if r.Battery != 3 || s.state.Jobs[0].Status != JobDone {
				t.Fatal("delivery arrival cost/completion changed")
			}
		} else {
			if len(s.navigation[r.ID].route) != 0 || s.navigation[r.ID].target != s.state.Jobs[0].From {
				t.Fatal("delivery route used pre-pickup battery")
			}
			s.Tick()
			if r.Battery != 3 || r.WaitTicks != 1 || r.JobID != "J1" {
				t.Fatal("underfunded delivery did not wait without drain")
			}
		}
		assertInvariants(t, s, s.cfg)
	}
	s := navigationSim(t, func() Config { cfg := DefaultConfig(); cfg.AutoCharge = true; return cfg }(), Point{X: 1, Y: 0}, Point{X: 1, Y: 0}, Point{X: 1, Y: 1})
	s.state.Robots[0].Battery = 4
	s.Tick()
	if s.state.Jobs[0].Status != JobDone || s.state.Robots[0].Battery != 3 {
		t.Fatal("start-at-pickup charged an extra movement step")
	}
}

func TestEnergyChecksAfterPickupAndLegacyGate(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AutoCharge = true
	s := navigationSim(t, cfg, Point{X: 1, Y: 0}, Point{X: 1, Y: 0}, Point{X: 1, Y: 1})
	r := &s.state.Robots[0]
	s.state.Jobs[0].Status = JobInProgress
	for _, battery := range []int{3, 4} {
		r.Battery = battery
		if got := s.routeAffordable(*r, s.state.Jobs[0].To, 1); got != (battery == 4) {
			t.Fatal("delivery budget omitted exit or floor")
		}
	}
	s.state.Stations[0].ReservedBy = "R2"
	s.state.Stations[1].ReservedBy = "R3"
	if !s.routeAffordable(*r, s.state.Jobs[0].To, 1) {
		t.Fatal("post-admission exit incorrectly requires a free station")
	}
	before := s.Snapshot()
	s.cfg.AutoCharge = false
	if !s.routeAffordable(*r, s.state.Jobs[0].To, 1000) || !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatal("legacy gate changed behavior or mutated state")
	}
	s.cfg.AutoCharge = true
	r.JobID = "missing"
	if s.routeAffordable(*r, Point{X: 1, Y: 1}, 1) {
		t.Fatal("unowned mission accepted")
	}
	r.JobID = "J1"
	if s.routeAffordable(*r, Point{X: 5, Y: 5}, 1) {
		t.Fatal("unrelated route target accepted")
	}
	if !slices.Equal(s.navigation[r.ID].route, []Point(nil)) {
		t.Fatal("energy query installed navigation")
	}
}

func TestEnergyMissionCostsAndBlockedFutureLegs(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AutoCharge = true
	cfg.MovingDrain = 3
	s := navigationSim(t, cfg, Point{X: 2, Y: 0}, Point{X: 1, Y: 0}, Point{X: 1, Y: 1})
	r := &s.state.Robots[0]
	job := s.state.Jobs[0]
	for _, battery := range []int{12, 13} {
		r.Battery = battery
		if got := s.routeAffordable(*r, job.From, 1); got != (battery == 13) {
			t.Fatal("movement multiplier/exit/floor omitted")
		}
	}
	other := &s.state.Robots[1]
	other.X, other.Y = job.To.X, job.To.Y
	if s.routeAffordable(*r, job.From, 1) {
		t.Fatal("unreachable future delivery accepted")
	}
	other.X, other.Y = 0, 1
	s.state.Robots[2].X, s.state.Robots[2].Y = 1, 0
	s.state.Stations[1].ReservedBy = "R4"
	if exit := s.chargingExitDistance(job.To, r.ID, s.dispatchDistance()); exit != -1 {
		t.Fatalf("parked robots around S1 ignored: exit=%d", exit)
	}
}

func TestEnergyExitStationAvailability(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AutoCharge = true
	s := dispatchSim(t, cfg, Point{X: 2, Y: 1}, Point{X: 2, Y: 2}, Point{X: 1, Y: 1})
	point := Point{X: 1, Y: 1}
	if got := s.chargingExitDistance(point, "", s.dispatchDistance()); got != 2 {
		t.Fatalf("nearest exit=%d", got)
	}
	s.state.Stations[0].ReservedBy = "R2"
	if got := s.chargingExitDistance(point, "", s.dispatchDistance()); got != 19 {
		t.Fatalf("reserved station selected: %d", got)
	}
	s.state.Stations[0].ReservedBy = ""
	s.state.Stations[0].OccupiedBy = "R2"
	if got := s.chargingExitDistance(point, "", s.dispatchDistance()); got != 19 {
		t.Fatalf("occupied station selected: %d", got)
	}
	s.state.Stations[0].OccupiedBy = ""
	s.state.Robots[1].X, s.state.Robots[1].Y, s.state.Robots[1].Status = 0, 0, RobotIdle
	if got := s.chargingExitDistance(point, "", s.dispatchDistance()); got != 19 {
		t.Fatalf("physical idle station occupant ignored: %d", got)
	}
}

func installEnergyJob(s *Sim, job JobState) {
	count := idNumber(job.ID)
	s.cfg.MaxJobs = max(s.cfg.MaxJobs, count)
	s.state.Jobs = make([]JobState, count)
	for i := range s.state.Jobs {
		s.state.Jobs[i] = JobState{ID: fmt.Sprintf("J%d", i+1), From: Point{X: 1, Y: 0}, To: Point{X: 2, Y: 0}, Status: JobDone, AssignedRobot: "R1"}
	}
	s.state.Jobs[count-1] = job
}

func TestEnergyRejectedDeliveryKeepsReplanTiming(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AutoCharge = true
	s := navigationSim(t, cfg, Point{X: 2, Y: 0}, Point{X: 1, Y: 0}, Point{X: 1, Y: 1})
	r := &s.state.Robots[0]
	s.prepareNavigation(r, s.state.Jobs[0].From)
	other := &s.state.Robots[1]
	other.X, other.Y, other.Status, other.Battery = 1, 1, RobotOffline, 50

	s.cfg.LowBatteryInjectLevel = 10
	if err := s.InjectLowBattery(r.ID); err != nil {
		t.Fatal(err)
	}
	s.Tick() // Pickup consumes one unit; a parked robot makes the future delivery unreachable.
	s.Tick() // First blocked delivery attempt.
	if r.WaitTicks != 1 || r.Battery != 9 {
		t.Fatalf("unexpected initial wait: %+v", r)
	}
	other.X, other.Y, other.Status = 3, 11, RobotOffline
	s.state.Stations[0].OccupiedBy = ""
	for _, wantWait := range []int{2, 3} {
		s.Tick()
		if r.WaitTicks != wantWait || r.Battery != 9 || r.Y != 0 {
			t.Fatal("replacement bypassed replanning threshold or moved on replanning tick")
		}
	}
	if s.navigation[r.ID].target != s.state.Jobs[0].To {
		t.Fatal("affordable replacement was not installed at threshold")
	}
	s.Tick()
	if r.WaitTicks != 0 || r.Battery != 8 || s.state.Jobs[0].Status != JobDone {
		t.Fatal("replacement was not followed on next tick")
	}
	assertInvariants(t, s, s.cfg)
}

func energyR2J19Scene(t *testing.T) *Sim {
	t.Helper()
	cfg := WarehouseConfig()
	cfg.InitialJobs, cfg.MaxJobs, cfg.AutoDispatch = 1, 1, false
	s := newTestSim(t, cfg, 42)
	for i := range s.state.Robots {
		s.state.Robots[i].Status = RobotOffline
	}
	r := &s.state.Robots[1]
	r.X, r.Y, r.Battery, r.Status = 1, 10, 39, RobotIdle
	installEnergyJob(s, JobState{ID: "J19", From: Point{X: 18, Y: 10}, To: Point{X: 12, Y: 0}, Status: JobPending})
	s.state.Robots[4].Status, s.state.Robots[4].X, s.state.Robots[4].Y = RobotMoving, 5, 0
	s.state.Robots[3].Status, s.state.Robots[3].X, s.state.Robots[3].Y = RobotCharging, 19, 0
	s.state.Stations[0].ReservedBy = "R5"
	s.state.Stations[1].OccupiedBy = "R4"
	s.cfg.AutoDispatch = true
	return s
}

func energyR3J11Scene(t *testing.T) *Sim {
	t.Helper()
	cfg := WarehouseConfig()
	cfg.InitialJobs, cfg.MaxJobs, cfg.AutoDispatch = 1, 1, false
	s := newTestSim(t, cfg, 1)
	positions := []Point{{X: 9, Y: 3}, {X: 3, Y: 10}, {X: 7, Y: 1}, {X: 4, Y: 1}, {X: 6, Y: 1}, {X: 3, Y: 1}}
	for i, p := range positions {
		s.state.Robots[i].X, s.state.Robots[i].Y = p.X, p.Y
	}
	r := &s.state.Robots[2]
	r.Battery, r.Status, r.JobID, r.WaitTicks = 34, RobotMoving, "J11", 2
	job := JobState{ID: "J11", From: Point{X: 4, Y: 3}, To: Point{X: 3, Y: 0}, Status: JobAssigned, AssignedRobot: "R3"}
	installEnergyJob(s, job)
	old := robotNavigation{target: job.From, route: []Point{{X: 6, Y: 1}, {X: 5, Y: 1}, {X: 4, Y: 1}, {X: 4, Y: 2}, {X: 4, Y: 3}}}
	s.navigation[r.ID] = old
	return s
}
