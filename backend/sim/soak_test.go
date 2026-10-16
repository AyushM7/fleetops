package sim

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/AyushM7/fleetops/backend/routing"
)

var exploreSoakTuning = flag.Bool("soak-tuning", false, "diagnose the approved integer warehouse tuning range")

func TestApprovedTuningDiagnostics(t *testing.T) {
	if !*exploreSoakTuning {
		t.Skip("explicit diagnostic parameter sweep")
	}
	passed := 0
	for replan := 1; replan <= 3; replan++ {
		for reserve := 5; reserve <= 15; reserve++ {
			for charge := 40; charge <= 60; charge++ {
				cfg := WarehouseConfig()
				cfg.ReplanAfter, cfg.BlockedAfter, cfg.ReserveBattery, cfg.ChargeBelow = replan, 15, reserve, charge
				failure := tuningFailure(t, cfg)
				if failure == "" {
					passed++
				}
				t.Logf("replan=%d reserve=%d charge=%d failure=%s", replan, reserve, charge, failure)
			}
		}
	}
	t.Logf("S1 tuning candidates passing all five seeds: %d/693; BlockedAfter only controls the flag, so changing it cannot change fault, completion, or wait outcomes", passed)
}

func tuningFailure(t *testing.T, cfg Config) string {
	t.Helper()
	for _, seed := range []int64{1, 2, 3, 42, 99} {
		s := newTestSim(t, cfg, seed)
		for tick := 1; tick <= 1500; tick++ {
			s.Tick()
			assertInvariants(t, s, cfg)
			for _, r := range s.state.Robots {
				if r.Status == RobotFaulted || r.WaitTicks > 40 {
					return fmt.Sprintf("seed=%d tick=%d robot=%s status=%s wait=%d snapshot=%s", seed, tick, r.ID, r.Status, r.WaitTicks, soakEvidence(s))
				}
			}
		}
		for _, j := range s.state.Jobs {
			if j.Status != JobDone {
				return fmt.Sprintf("seed=%d incomplete=%s snapshot=%s", seed, j.ID, soakEvidence(s))
			}
		}
	}
	return ""
}

func soakEvidence(s *Sim) string {
	data, _ := json.Marshal(struct {
		State  State
		Routes map[string][]Point
	}{s.Snapshot(), func() map[string][]Point {
		routes := make(map[string][]Point)
		for id, nav := range s.navigation {
			routes[id] = nav.route
		}
		return routes
	}()})
	return string(data)
}

func stationEvidence(s *Sim, robot RobotState) string {
	var result string
	for _, station := range s.state.Stations {
		route, err := routing.FindPath(routingGrid{cfg: s.cfg}, routing.Cell{X: robot.X, Y: robot.Y}, routing.Cell{X: station.X, Y: station.Y}, routing.Options{Blocked: func(c routing.Cell) bool {
			return s.navigationBlocked(robot, Point{X: c.X, Y: c.Y}, false)
		}})
		result += fmt.Sprintf(" %s free=%v occupied=%q reserved=%q distance=%d cost=%d error=%v;", station.ID, s.stationFreeFor(station, robot.ID), station.OccupiedBy, station.ReservedBy, len(route), len(route)*s.cfg.MovingDrain, err)
	}
	return result
}

func TestWarehouseDepletionDiagnostics(t *testing.T) {
	s, err := New(WarehouseConfig(), 42)
	if err != nil {
		t.Fatal(err)
	}
	for tick := 1; tick <= 300; tick++ {
		before := s.Snapshot()
		oldRoutes := make(map[string][]Point)
		for id, nav := range s.navigation {
			oldRoutes[id] = slices.Clone(nav.route)
		}
		s.Tick()
		for i, robot := range s.state.Robots {
			if robot.ID != "R2" && robot.ID != "R5" {
				continue
			}
			old := before.Robots[i]
			if old.JobID != "" && robot.JobID == "" || old.Status != robot.Status || old.Status == RobotMoving && robot.X == old.X && robot.Y == old.Y && !slices.Equal(oldRoutes[robot.ID], s.navigation[robot.ID].route) {
				t.Logf("tick=%d %s before=%+v after=%+v oldRoute=%v route=%v stations:%s", tick, robot.ID, old, robot, oldRoutes[robot.ID], s.navigation[robot.ID].route, stationEvidence(s, robot))
				if len(s.navigation[robot.ID].route) > len(oldRoutes[robot.ID]) && robot.X == old.X && robot.Y == old.Y {
					t.Logf("detour tick=%d robot=%s snapshot=%s", tick, robot.ID, soakEvidence(s))
				}
			}
			if robot.Status == RobotFaulted && old.Status != RobotFaulted {
				t.Logf("depletion tick=%d %s snapshot=%s", tick, robot.ID, soakEvidence(s))
			}
		}
	}
}

type soakScenario struct {
	name                     string
	ticks                    int
	warehouse, charge, fault bool
}

type soakResult struct {
	maxWait, maxTick      int
	maxCause, maxEvidence string
	faults                map[string]bool
}

func TestPhase2Soak(t *testing.T) {
	legacyWarehouse := WarehouseConfig()
	legacyWarehouse.AutoCharge, legacyWarehouse.Recovery = false, false
	for _, scenario := range []finalSoakScenario{
		{"S1", 1500, WarehouseConfig(), false, false},
		{"S2/default", 500, DefaultConfig(), false, true},
		{"S2/warehouse", 600, legacyWarehouse, false, true},
		{"S3", 600, WarehouseConfig(), true, false},
	} {
		for _, seed := range []int64{1, 2, 3, 42, 99} {
			t.Run(fmt.Sprintf("%s/seed%d", scenario.name, seed), func(t *testing.T) { runFinalSoak(t, scenario, seed) })
		}
	}
}

// The controlled fixture below remains for historical R0/R3 regression tests.
// Final acceptance uses unchanged layouts and natural aisle injection instead.

func newSoak(t *testing.T, scenario soakScenario, seed int64) *Sim {
	t.Helper()
	cfg := DefaultConfig()
	if scenario.warehouse {
		cfg = WarehouseConfig()
	}
	cfg.AutoCharge = scenario.charge
	cfg.Recovery = cfg.Recovery && scenario.charge
	if scenario.fault {
		// Controlled fixture: R2 stays inside aisle 0 until injection. Dispatch
		// begins after expiry; J1's pickup is deliberately obstructed by that aisle.
		cfg.RobotStarts[1] = Point{X: 4, Y: 2}
		cfg.AutoDispatch = false
		cfg.Recovery = false // Keep the controlled parked-aisle fixture until expiry.
	}
	s := newTestSim(t, cfg, seed)
	if scenario.fault {
		s.state.Jobs[0].From = Point{X: 4, Y: 3}
		s.state.Jobs[0].To = Point{X: 1, Y: 1}
	}
	return s
}

func runSoak(t *testing.T, scenario soakScenario, seed int64) {
	t.Helper()
	s := newSoak(t, scenario, seed)
	result := soakResult{faults: make(map[string]bool)}
	for tick := 1; tick <= scenario.ticks; tick++ {
		before := s.Snapshot()
		s.Tick()
		if scenario.fault {
			advanceFaultSoak(t, s, before)
		}
		if err := checkInvariants(s.Snapshot(), s.cfg, s.navigation); err != nil {
			t.Fatalf("tick=%d %v snapshot=%s", tick, err, soakEvidence(s))
		}
		if err := checkRecoveryInvariants(s); err != nil {
			t.Fatalf("tick=%d %v snapshot=%s", tick, err, soakEvidence(s))
		}
		result.observe(t, s, scenario)
	}
	result.check(t, s, scenario)
}

func advanceFaultSoak(t *testing.T, s *Sim, before State) {
	t.Helper()
	if s.state.Tick == 30 {
		r := s.state.Robots[1]
		if !s.cfg.onAisle(Point{X: r.X, Y: r.Y}) {
			t.Fatal("controlled R2 is outside aisle")
		}
		if err := s.InjectUnresponsive("R2"); err != nil {
			t.Fatal(err)
		}
	}
	if s.state.Tick == 33 {
		if s.state.Robots[1].Status != RobotOffline {
			t.Fatal("countdown did not expire at tick 33")
		}
		s.cfg.AutoDispatch = true
		s.cfg.Recovery = true
	}
	if s.state.Jobs[0].Status != JobPending {
		t.Fatalf("parked-aisle pickup assigned: %s", soakEvidence(s))
	}
	checkParkedAssignments(t, s, before)
}

func checkParkedAssignments(t *testing.T, s *Sim, before State) {
	t.Helper()
	for i, j := range s.state.Jobs {
		if j.AssignedRobot == "" || i < len(before.Jobs) && before.Jobs[i].AssignedRobot != "" {
			continue
		}
		for _, parked := range before.Robots {
			if parked.Status != RobotOffline && parked.Status != RobotFaulted && parked.Status != RobotCharging {
				continue
			}
			for _, goal := range []Point{j.From, j.To} {
				if goal == (Point{X: parked.X, Y: parked.Y}) || s.cfg.onAisle(goal) && goal.X == parked.X && s.cfg.onAisle(Point{X: parked.X, Y: parked.Y}) {
					t.Errorf("new assignment %s to parked %s goal at tick=%d: %s", j.ID, parked.ID, s.state.Tick, soakEvidence(s))
				}
			}
		}
	}
}

func (result *soakResult) observe(t *testing.T, s *Sim, scenario soakScenario) {
	t.Helper()
	for _, r := range s.state.Robots {
		if r.WaitTicks > result.maxWait {
			result.maxWait, result.maxTick = r.WaitTicks, s.state.Tick
			result.maxCause = waitingCause(s, r, make(map[string]bool))
			result.maxEvidence = soakEvidence(s)
		}
		if scenario.name == "S1" && r.Status == RobotFaulted && !result.faults[r.ID] {
			result.faults[r.ID] = true
			t.Errorf("battery fault tick=%d robot=%s snapshot=%s", s.state.Tick, r.ID, soakEvidence(s))
		}
	}
}

func waitingCause(s *Sim, r RobotState, seen map[string]bool) string {
	if seen[r.ID] {
		return "healthy traffic cycle at " + r.ID
	}
	seen[r.ID] = true
	nav := s.navigation[r.ID]
	if len(nav.route) == 0 {
		return "no cached route for " + r.ID
	}
	next := nav.route[0]
	for _, other := range s.state.Robots {
		if other.ID == r.ID {
			continue
		}
		sameCell := next == (Point{X: other.X, Y: other.Y})
		heldAisle := s.cfg.onAisle(next) && next.X == other.X && s.cfg.onAisle(Point{X: other.X, Y: other.Y}) && (!s.cfg.onAisle(Point{X: r.X, Y: r.Y}) || r.X != next.X)
		if !sameCell && !heldAisle {
			continue
		}
		if other.Status != RobotMoving {
			return fmt.Sprintf("%s %s", other.Status, other.ID)
		}
		return waitingCause(s, other, seen)
	}
	return "next step now free; awaiting numeric turn/replan"
}

func (result soakResult) check(t *testing.T, s *Sim, scenario soakScenario) {
	t.Helper()
	t.Logf("maximum waitTicks=%d at tick=%d cause=%s", result.maxWait, result.maxTick, result.maxCause)
	if !scenario.fault && result.maxWait > 40 {
		t.Errorf("wait limit exceeded: tick=%d reason=%s snapshot=%s", result.maxTick, result.maxCause, result.maxEvidence)
	}
	if scenario.fault && result.maxWait > 40 {
		t.Logf("fault-scenario long wait: reason=%s snapshot=%s", result.maxCause, result.maxEvidence)
		if strings.HasPrefix(result.maxCause, "healthy traffic cycle") {
			t.Errorf("unrelated healthy-robot deadlock at tick=%d: %s", result.maxTick, result.maxEvidence)
		}
	}
	if scenario.name == "S1" || scenario.name == "S2" {
		for _, r := range s.state.Robots {
			if r.Blocked {
				t.Errorf("final blocked robot %s: %s", r.ID, soakEvidence(s))
			}
		}
	}
	if scenario.name == "S1" {
		done := 0
		for _, j := range s.state.Jobs {
			if j.Status == JobDone {
				done++
			}
		}
		if done != 20 {
			t.Errorf("completed %d/20 jobs: %s", done, soakEvidence(s))
		}
	}
}

type finalSoakScenario struct {
	name          string
	ticks         int
	cfg           Config
	fault, legacy bool
}

type finalSoakReport struct {
	Scenario                                             string
	Seed                                                 int64
	MaxWait, MaxChargingWait, MaxProtectedIdle           int
	Completed, Relocations, Retreats, MaxRollingRetreats int
	Arrivals, MinArrivalBattery, ZeroBatteryArrivals     int
	InjectedRobot                                        string
	InjectionTick, OfflineTick                           int
	BatteryFaults, FinalBlocked                          []string
	RelocationsByRobot, RetreatsByRobot, WaitClasses     map[string]int
	Failures, EnergyFailures                             map[string]string
	ReplaySHA256, HistoricalEvidence                     string
	LegacyExceptionTicks, BlockedIncidents               int
}

type finalBlockedSegment struct {
	Start, End, LastWait int
	Class                string
	OwnParked            bool
}

type finalSoakObserver struct {
	report                    finalSoakReport
	chargeWait, protectedWait map[string]int
	waitClass                 map[string]string
	blocked                   map[string]finalBlockedSegment
}

func newFinalSoakObserver(scenario finalSoakScenario, seed int64) *finalSoakObserver {
	return &finalSoakObserver{
		report: finalSoakReport{Scenario: scenario.name, Seed: seed, MinArrivalBattery: 101,
			BatteryFaults: []string{}, FinalBlocked: []string{}, RelocationsByRobot: map[string]int{}, RetreatsByRobot: map[string]int{},
			WaitClasses: map[string]int{}, Failures: map[string]string{}, EnergyFailures: map[string]string{}},
		chargeWait: map[string]int{}, protectedWait: map[string]int{}, waitClass: map[string]string{},
		blocked: map[string]finalBlockedSegment{},
	}
}

func (o *finalSoakObserver) fail(t *testing.T, s *Sim, energy bool, gate, detail string) {
	t.Helper()
	failures := o.report.Failures
	if energy {
		failures = o.report.EnergyFailures
	}
	if _, seen := failures[gate]; seen {
		return
	}
	failures[gate] = fmt.Sprintf("tick%d %s", s.state.Tick, detail)
	t.Logf("ACCEPTANCE_FAILURE scenario=%s seed%d energy=%v gate=%s detail=%s snapshot=%s", o.report.Scenario, o.report.Seed, energy, gate, failures[gate], diagJSON(diagSnapshot(s)))
}

func naturalAisleCandidate(s *Sim) string {
	for _, i := range s.recoveryOrder() {
		r := s.state.Robots[i]
		if s.cfg.onAisle(Point{X: r.X, Y: r.Y}) {
			return r.ID
		}
	}
	return ""
}

func injectNaturalSoakFault(t *testing.T, s, repeat *Sim, o *finalSoakObserver) {
	t.Helper()
	if o.report.InjectedRobot != "" || s.state.Tick < 30 {
		return
	}
	id := naturalAisleCandidate(s)
	if id != "" {
		if naturalAisleCandidate(repeat) != id {
			t.Fatal("natural injection selection is nondeterministic")
		}
		if err := s.InjectUnresponsive(id); err != nil {
			t.Fatal(err)
		}
		if err := repeat.InjectUnresponsive(id); err != nil {
			t.Fatal(err)
		}
		o.report.InjectedRobot, o.report.InjectionTick = id, s.state.Tick
		t.Logf("NATURAL_INJECTION seed%d tick%d robot=%s position=(%d,%d)", s.state.Seed, s.state.Tick, id, s.faultRobot(id).X, s.faultRobot(id).Y)
	} else if s.state.Tick == 300 {
		o.fail(t, s, false, "fault setup", "no robot reached an aisle between ticks30 and300")
	}
}

func runFinalSoak(t *testing.T, scenario finalSoakScenario, seed int64) {
	t.Helper()
	s, repeat := newTestSim(t, scenario.cfg, seed), newTestSim(t, scenario.cfg, seed)
	o := newFinalSoakObserver(scenario, seed)
	for _, robot := range s.state.Robots {
		o.report.RelocationsByRobot[robot.ID], o.report.RetreatsByRobot[robot.ID] = 0, 0
	}
	for tick := 1; tick <= scenario.ticks; tick++ {
		before := s.Snapshot()
		s.Tick()
		repeat.Tick()
		if scenario.fault {
			injectNaturalSoakFault(t, s, repeat, o)
		}
		diagAssertParity(t, s, repeat)
		if err := checkInvariants(s.Snapshot(), s.cfg, s.navigation); err != nil {
			o.fail(t, s, false, "invariants", err.Error())
		}
		if err := checkRecoveryInvariants(s); err != nil {
			o.fail(t, s, false, "recovery invariants", err.Error())
		}
		o.observe(t, s, before, scenario)
		if scenario.fault {
			o.checkFaultAssignments(t, s, before)
		}
	}
	o.finish(t, s, repeat, scenario)
	t.Logf("FINAL_ACCEPTANCE %s", diagJSON(o.report))
	keys := make([]string, 0, len(o.report.Failures))
	for gate := range o.report.Failures {
		keys = append(keys, gate)
	}
	slices.Sort(keys)
	for _, gate := range keys {
		t.Errorf("%s: %s", gate, o.report.Failures[gate])
	}
	if !scenario.legacy {
		t.Run("S4", func(t *testing.T) {
			keys := make([]string, 0, len(o.report.EnergyFailures))
			for gate := range o.report.EnergyFailures {
				keys = append(keys, gate)
			}
			slices.Sort(keys)
			for _, gate := range keys {
				t.Errorf("%s: %s", gate, o.report.EnergyFailures[gate])
			}
			t.Logf("S4 source=%s seed%d faults=%v arrivals=%d minimumBattery=%d zeroArrivalUses=%d", scenario.name, seed, o.report.BatteryFaults, o.report.Arrivals, o.report.MinArrivalBattery, o.report.ZeroBatteryArrivals)
		})
	}
}

// Use the R0 route/chain model, with Section4's parked-goal and legacy-energy
// exceptions. Cell occupancy precedes aisle ownership; no map iteration selects a root.
func classifySoakWait(s *Sim, robot RobotState) string {
	if soakParkedObstruction(s, robot) {
		return "PARKED"
	}
	seen := map[string]bool{}
	for {
		if seen[robot.ID] {
			return "CYCLE"
		}
		seen[robot.ID] = true
		switch robot.Status {
		case RobotOffline, RobotFaulted, RobotCharging:
			return "PARKED"
		case RobotIdle:
			if !s.cfg.AutoCharge && robot.Battery < s.cfg.MinAssignBattery {
				return "PARKED"
			}
			return "IDLE-BLOCKER"
		case RobotMoving:
		default:
			return "OTHER"
		}
		route := diagAttempt(s, robot)
		if len(route) == 0 {
			return "OTHER"
		}
		next := route[0]
		var blocker *RobotState
		for _, i := range s.recoveryOrder() {
			other := &s.state.Robots[i]
			if other.ID != robot.ID && next == (Point{X: other.X, Y: other.Y}) {
				blocker = other
				break
			}
		}
		if blocker == nil && s.cfg.onAisle(next) && (!s.cfg.onAisle(Point{X: robot.X, Y: robot.Y}) || robot.X != next.X) {
			for _, i := range s.recoveryOrder() {
				other := &s.state.Robots[i]
				if other.ID != robot.ID && other.X == next.X && s.cfg.onAisle(Point{X: other.X, Y: other.Y}) {
					blocker = other
					break
				}
			}
		}
		if blocker == nil {
			return "OTHER"
		}
		robot = *blocker
	}
}

func soakParkedObstruction(s *Sim, robot RobotState) bool {
	if robot.Status != RobotMoving {
		return false
	}
	if _, exists := s.navigation[robot.ID]; !exists && robot.JobID == "" {
		return false
	}
	target := s.recoveryTarget(robot)
	_, err := routing.FindPath(routingGrid{cfg: s.cfg}, routing.Cell{X: robot.X, Y: robot.Y}, routing.Cell{X: target.X, Y: target.Y}, routing.Options{
		Blocked: func(c routing.Cell) bool { return s.navigationBlocked(robot, Point{X: c.X, Y: c.Y}, false) },
	})
	return errors.Is(err, routing.ErrNoPath) || errors.Is(err, routing.ErrGoalBlocked)
}

func (o *finalSoakObserver) observe(t *testing.T, s *Sim, before State, scenario finalSoakScenario) {
	t.Helper()
	if scenario.name == "S1" && len(s.state.Faults) != 0 {
		o.fail(t, s, false, "fault records", "uninjected healthy fleet acquired a fault record")
	}
	if scenario.fault && o.report.InjectedRobot != "" {
		faults := s.state.Faults
		if len(faults) != 1 || faults[0].Type != FaultRobotUnresponsive || faults[0].Tick != o.report.InjectionTick || !slices.Equal(faults[0].Robots, []string{o.report.InjectedRobot}) {
			o.fail(t, s, false, "injection fault records", "expected exactly the natural unresponsive injection")
		}
	}
	for i, robot := range s.state.Robots {
		old := before.Robots[i]
		o.report.MaxWait = max(o.report.MaxWait, robot.WaitTicks)
		if robot.WaitTicks > 40 {
			class := classifySoakWait(s, robot)
			if o.waitClass[robot.ID] != class {
				o.report.WaitClasses[class]++
				t.Logf("WAIT_CLASS scenario=%s seed%d tick%d robot=%s class=%s", scenario.name, s.state.Seed, s.state.Tick, robot.ID, class)
			}
			o.waitClass[robot.ID] = class
			if verifiedLegacyWaitException(s, robot, scenario, class) {
				o.report.LegacyExceptionTicks++
			} else if scenario.name == "S1" || class != "PARKED" {
				o.fail(t, s, false, "wait "+class+" "+robot.ID, fmt.Sprintf("wait=%d", robot.WaitTicks))
			}
		} else {
			delete(o.waitClass, robot.ID)
		}
		if scenario.fault {
			obstructed := soakParkedObstruction(s, robot)
			if robot.Blocked != (robot.WaitTicks >= s.cfg.BlockedAfter) || robot.Status != RobotMoving && robot.WaitTicks != 0 {
				o.fail(t, s, false, "invalid blocked state "+robot.ID, fmt.Sprintf("wait=%d blocked=%v status=%s", robot.WaitTicks, robot.Blocked, robot.Status))
			}
			o.observeBlocked(t, s, robot, obstructed)
			if obstructed && robot.WaitTicks >= s.cfg.BlockedAfter && !robot.Blocked {
				o.fail(t, s, false, "hidden obstruction "+robot.ID, "blocked flag is false")
			}
		}
		o.observeEnergy(t, s, old, robot, !scenario.legacy)
		o.observeRecovery(s, old, robot)
		if !scenario.legacy {
			o.observeHealthyIdle(t, s, robot, scenario.name == "S1")
		}
	}
	if scenario.fault && o.report.InjectedRobot != "" {
		r := s.faultRobot(o.report.InjectedRobot)
		expiry := o.report.InjectionTick + s.cfg.UnresponsiveDelay
		if s.state.Tick < expiry && r.Status == RobotOffline || s.state.Tick >= expiry && r.Status != RobotOffline {
			o.fail(t, s, false, "injection countdown", "offline expiry timing changed")
		}
		if r.Status == RobotOffline && o.report.OfflineTick == 0 {
			o.report.OfflineTick = s.state.Tick
		}
	}
}

// This fingerprint admits only the owner-accepted, reproduced legacy episode.
// Its later low-battery PARKED interval already follows the general legacy rule.
func verifiedLegacyWaitException(s *Sim, robot RobotState, scenario finalSoakScenario, class string) bool {
	if scenario.name != "S2/default" || !scenario.legacy || s.state.Seed != 1 || !reflect.DeepEqual(s.cfg, DefaultConfig()) || class != "IDLE-BLOCKER" {
		return false
	}
	tick := s.state.Tick
	if tick < 142 || tick > 179 || robot.ID != "R6" || robot.Status != RobotMoving || robot.JobID != "J17" || robot.X != 7 || robot.Y != 7 || robot.WaitTicks != tick-101 {
		return false
	}
	blocker := s.faultRobot("R2")
	if blocker == nil || blocker.Status != RobotIdle || blocker.JobID != "" || blocker.X != 7 || blocker.Y != 8 || blocker.Battery != 38-tick/20 {
		return false
	}
	for _, job := range s.state.Jobs {
		if job.ID == "J17" {
			return job.AssignedRobot == "R6" && job.Status == JobInProgress && job.To == (Point{X: 7, Y: 8})
		}
	}
	return false
}

func (o *finalSoakObserver) closeBlocked(t *testing.T, id string) {
	t.Helper()
	if segment, exists := o.blocked[id]; exists {
		t.Logf("BLOCKED_INCIDENT scenario=%s seed%d robot=%s segment=%s", o.report.Scenario, o.report.Seed, id, diagJSON(segment))
		delete(o.blocked, id)
	}
}

func (o *finalSoakObserver) observeBlocked(t *testing.T, s *Sim, robot RobotState, parked bool) {
	t.Helper()
	if !robot.Blocked {
		o.closeBlocked(t, robot.ID)
		return
	}
	class := classifySoakWait(s, robot)
	segment, exists := o.blocked[robot.ID]
	if !exists {
		o.report.BlockedIncidents++
	}
	if !exists || segment.Class != class || segment.OwnParked != parked {
		o.closeBlocked(t, robot.ID)
		segment = finalBlockedSegment{Start: s.state.Tick, Class: class, OwnParked: parked}
	}
	segment.End, segment.LastWait = s.state.Tick, robot.WaitTicks
	o.blocked[robot.ID] = segment
}

func (o *finalSoakObserver) observeEnergy(t *testing.T, s *Sim, old, robot RobotState, enforce bool) {
	t.Helper()
	if robot.Status == RobotFaulted && old.Status != RobotFaulted {
		o.report.BatteryFaults = append(o.report.BatteryFaults, fmt.Sprintf("%s@%d", robot.ID, s.state.Tick))
		if enforce {
			o.fail(t, s, true, "battery depletion "+robot.ID, "unresponsive injection does not authorize battery depletion")
		}
		if o.report.Scenario == "S1" {
			o.fail(t, s, false, "fault "+robot.ID, "healthy fleet robot faulted")
		}
	}
	if robot.Status == RobotCharging && old.Status != RobotCharging {
		o.report.Arrivals++
		o.report.MinArrivalBattery = min(o.report.MinArrivalBattery, robot.Battery)
		if robot.Battery == 0 {
			o.report.ZeroBatteryArrivals++
		}
		if enforce && robot.Battery < 1 {
			o.fail(t, s, true, "charging arrival floor "+robot.ID, fmt.Sprintf("battery=%d", robot.Battery))
		}
	}
}

func (o *finalSoakObserver) observeRecovery(s *Sim, old, robot RobotState) {
	if old.Status == RobotIdle && old.JobID == "" && robot.JobID == "" && (robot.Status == RobotMoving && s.navigation[robot.ID].relocation || robot.Status == RobotIdle && (robot.X != old.X || robot.Y != old.Y)) {
		o.report.Relocations++
		o.report.RelocationsByRobot[robot.ID]++
	}
	rolling := 0
	for _, tick := range s.recovery[robot.ID].retreats {
		if tick > s.state.Tick-200 {
			rolling++
		}
		if tick == s.state.Tick {
			o.report.Retreats++
			o.report.RetreatsByRobot[robot.ID]++
		}
	}
	o.report.MaxRollingRetreats = max(o.report.MaxRollingRetreats, rolling)
}

func (o *finalSoakObserver) observeHealthyIdle(t *testing.T, s *Sim, robot RobotState, enforce bool) {
	t.Helper()
	if robot.Status == RobotIdle && robot.JobID == "" && s.protected[Point{X: robot.X, Y: robot.Y}] {
		o.protectedWait[robot.ID]++
	} else {
		o.protectedWait[robot.ID] = 0
	}
	o.report.MaxProtectedIdle = max(o.report.MaxProtectedIdle, o.protectedWait[robot.ID])
	if enforce && o.protectedWait[robot.ID] > 5 {
		o.fail(t, s, false, "protected idle "+robot.ID, fmt.Sprintf("ticks=%d", o.protectedWait[robot.ID]))
	}
	if robot.Status == RobotIdle && robot.JobID == "" && robot.Battery < s.cfg.ChargeBelow {
		if chargeClassForTest(s, robot) == "stranded" {
			if enforce {
				o.fail(t, s, true, "stranded "+robot.ID, fmt.Sprintf("battery=%d", robot.Battery))
			}
			o.chargeWait[robot.ID] = 0
		} else {
			o.chargeWait[robot.ID]++
		}
	} else {
		o.chargeWait[robot.ID] = 0
	}
	o.report.MaxChargingWait = max(o.report.MaxChargingWait, o.chargeWait[robot.ID])
	if enforce && o.chargeWait[robot.ID] > 200 {
		o.fail(t, s, false, "charging wait "+robot.ID, fmt.Sprintf("ticks=%d", o.chargeWait[robot.ID]))
	}
}

func (o *finalSoakObserver) checkFaultAssignments(t *testing.T, s *Sim, before State) {
	t.Helper()
	for i, job := range s.state.Jobs {
		if job.AssignedRobot == "" || i < len(before.Jobs) && before.Jobs[i].AssignedRobot != "" {
			continue
		}
		for _, parked := range before.Robots {
			if parked.Status != RobotOffline && parked.Status != RobotFaulted && parked.Status != RobotCharging {
				continue
			}
			for _, goal := range []Point{job.From, job.To} {
				if goal == (Point{X: parked.X, Y: parked.Y}) || s.cfg.onAisle(goal) && goal.X == parked.X && s.cfg.onAisle(Point{X: parked.X, Y: parked.Y}) {
					o.fail(t, s, false, "assignment to parked goal "+job.ID, parked.ID)
				}
			}
		}
	}
}

func (o *finalSoakObserver) finish(t *testing.T, s, repeat *Sim, scenario finalSoakScenario) {
	for _, robot := range s.state.Robots {
		o.closeBlocked(t, robot.ID)
	}
	t.Helper()
	for _, job := range s.state.Jobs {
		if job.Status == JobDone {
			o.report.Completed++
		}
	}
	if scenario.name == "S1" {
		if len(s.state.Jobs) != 20 || o.report.Completed != 20 {
			o.fail(t, s, false, "job completion", fmt.Sprintf("done=%d/20", o.report.Completed))
		}
	}
	for _, robot := range s.state.Robots {
		if robot.Blocked {
			o.report.FinalBlocked = append(o.report.FinalBlocked, robot.ID)
			if scenario.name == "S1" {
				o.fail(t, s, false, "final blocked "+robot.ID, "still blocked")
			}
		}
	}
	if o.report.Arrivals == 0 {
		o.report.MinArrivalBattery = 0
	}
	if scenario.fault && o.report.InjectedRobot == "" {
		o.fail(t, s, false, "fault setup", "no natural aisle injection")
	}
	got, err := json.Marshal(s.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	again, err := json.Marshal(repeat.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, again) {
		o.fail(t, s, false, "deterministic replay", "final JSON differs")
	}
	o.report.ReplaySHA256 = fmt.Sprintf("%x", sha256.Sum256(got))
	if scenario.legacy {
		o.report.HistoricalEvidence = "P2-07 full-run baseline unavailable; pre-Step-1 R3 references are a separate later compatibility check"
	}
}

func finalWaitFixture(t *testing.T) *Sim {
	t.Helper()
	cfg := DefaultConfig()
	cfg.AutoDispatch, cfg.AutoCharge = false, true
	cfg.InitialJobs, cfg.NewJobEvery = 0, 10000
	s := newTestSim(t, cfg, 1)
	s.state.Robots[0].X, s.state.Robots[0].Y, s.state.Robots[0].Status, s.state.Robots[0].JobID = 2, 0, RobotMoving, "J1"
	s.state.Robots[1].X, s.state.Robots[1].Y = 3, 0
	s.state.Jobs = []JobState{{ID: "J1", From: Point{X: 2, Y: 0}, To: Point{X: 4, Y: 0}, Status: JobInProgress, AssignedRobot: "R1"}}
	s.navigation["R1"] = robotNavigation{target: Point{X: 4, Y: 0}, route: []Point{{X: 3, Y: 0}, {X: 4, Y: 0}}}
	return s
}

func TestFinalWaitClassification(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"idle", "IDLE-BLOCKER"}, {"legacy weak idle", "PARKED"}, {"legacy exact minimum", "IDLE-BLOCKER"},
		{"offline", "PARKED"}, {"faulted", "PARKED"}, {"cycle", "CYCLE"}, {"free next step", "OTHER"}, {"missing route", "OTHER"}, {"parked goal", "PARKED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := finalWaitFixture(t)
			switch tc.name {
			case "legacy weak idle":
				s.cfg.AutoCharge = false
				s.state.Robots[1].Battery = s.cfg.MinAssignBattery - 1
			case "legacy exact minimum":
				s.cfg.AutoCharge = false
				s.state.Robots[1].Battery = s.cfg.MinAssignBattery
			case "offline":
				s.state.Robots[1].Status = RobotOffline
			case "faulted":
				s.state.Robots[1].Status, s.state.Robots[1].Battery = RobotFaulted, 0
			case "cycle":
				r := &s.state.Robots[1]
				r.Status, r.JobID = RobotMoving, "J2"
				s.state.Jobs = append(s.state.Jobs, JobState{ID: "J2", From: Point{X: 3, Y: 0}, To: Point{X: 2, Y: 0}, Status: JobInProgress, AssignedRobot: r.ID})
				s.navigation[r.ID] = robotNavigation{target: Point{X: 2, Y: 0}, route: []Point{{X: 2, Y: 0}}}
			case "free next step":
				s.state.Robots[1].Y = 1
			case "missing route":
				s.navigation["R1"] = robotNavigation{target: Point{X: 4, Y: 0}}
			case "parked goal":
				r := &s.state.Robots[2]
				r.X, r.Y, r.Status = 4, 0, RobotOffline
			}
			before := diagJSON(diagSnapshot(s))
			if got := classifySoakWait(s, s.state.Robots[0]); got != tc.want {
				t.Fatalf("class=%s want=%s", got, tc.want)
			}
			if before != diagJSON(diagSnapshot(s)) {
				t.Fatal("classification mutated state")
			}
		})
	}
	s := finalWaitFixture(t)
	s.cfg = WarehouseConfig()
	s.state.Robots[0].X, s.state.Robots[0].Y = 4, 1
	s.state.Robots[1].X, s.state.Robots[1].Y = 4, 4
	s.state.Jobs[0].To = Point{X: 4, Y: 3}
	s.navigation["R1"] = robotNavigation{target: Point{X: 4, Y: 3}, route: []Point{{X: 4, Y: 2}, {X: 4, Y: 3}}}
	if classifySoakWait(s, s.state.Robots[0]) != "IDLE-BLOCKER" {
		t.Fatal("aisle-owner chain not followed")
	}
	s.state.Robots[1].Status = RobotOffline
	if classifySoakWait(s, s.state.Robots[0]) != "PARKED" {
		t.Fatal("parked aisle obstruction not classified")
	}
}

func TestFinalAcceptanceNegativeGates(t *testing.T) {
	for _, name := range []string{"wait40", "wait41", "charging wait", "evacuation", "stranded", "zero arrival", "depletion", "invalid blocked", "hidden parked", "parked assignment", "fault record"} {
		t.Run(name, func(t *testing.T) {
			s := finalWaitFixture(t)
			scenario := finalSoakScenario{name: "S1", cfg: s.cfg}
			o := newFinalSoakObserver(scenario, 1)
			before := s.Snapshot()
			r := &s.state.Robots[0]
			switch name {
			case "wait40", "wait41":
				r.WaitTicks = 40
				if name == "wait41" {
					r.WaitTicks++
				}
				o.observe(t, s, before, scenario)
			case "charging wait", "evacuation", "stranded":
				r.Status, r.JobID = RobotIdle, ""
				if name == "charging wait" {
					r.Battery = 39
					o.chargeWait[r.ID] = 200
				}
				if name == "evacuation" {
					r.X = 1
					o.protectedWait[r.ID] = 5
				}
				if name == "stranded" {
					r.Battery = 1
				}
				o.observeHealthyIdle(t, s, *r, true)
			case "zero arrival":
				r.Status, r.Battery = RobotCharging, 0
				o.observeEnergy(t, s, before.Robots[0], *r, true)
			case "depletion":
				r.Status, r.Battery = RobotFaulted, 0
				o.observeEnergy(t, s, before.Robots[0], *r, true)
			case "invalid blocked", "hidden parked":
				scenario.name, scenario.fault = "S3", true
				r.WaitTicks = s.cfg.BlockedAfter
				r.Blocked = name == "invalid blocked"
				if name == "invalid blocked" {
					r.WaitTicks--
				}
				if name == "hidden parked" {
					s.state.Robots[2].X, s.state.Robots[2].Y, s.state.Robots[2].Status = 4, 0, RobotOffline
				}
				o.observe(t, s, before, scenario)
			case "parked assignment":
				before.Jobs[0].AssignedRobot = ""
				before.Robots[2].X, before.Robots[2].Y, before.Robots[2].Status = 4, 0, RobotOffline
				o.checkFaultAssignments(t, s, before)
			case "fault record":
				s.state.Faults = []FaultState{{ID: "F1", Type: FaultRobotUnresponsive, Robots: []string{"R1"}, Tick: s.state.Tick}}
				o.observe(t, s, before, scenario)
			}
			failed := len(o.report.Failures)+len(o.report.EnergyFailures) > 0
			if failed != (name != "wait40") {
				t.Fatalf("gate result main=%v energy=%v", o.report.Failures, o.report.EnergyFailures)
			}
		})
	}
}

func TestNaturalAisleInjectionOrdering(t *testing.T) {
	cfg := WarehouseConfig()
	cfg.InitialJobs, cfg.AutoDispatch = 0, false
	s, repeat := newTestSim(t, cfg, 1), newTestSim(t, cfg, 1)
	for _, sim := range []*Sim{s, repeat} {
		sim.state.Tick = 29
		sim.state.Robots[1].X, sim.state.Robots[1].Y = 4, 2
		sim.state.Robots[4].X, sim.state.Robots[4].Y = 9, 2
		slices.Reverse(sim.state.Robots)
	}
	o := newFinalSoakObserver(finalSoakScenario{name: "S3"}, 1)
	injectNaturalSoakFault(t, s, repeat, o)
	if o.report.InjectedRobot != "" {
		t.Fatal("injected before tick30")
	}
	s.state.Tick, repeat.state.Tick = 30, 30
	injectNaturalSoakFault(t, s, repeat, o)
	if o.report.InjectedRobot != "R2" || o.report.InjectionTick != 30 {
		t.Fatal("did not select lowest numeric aisle robot")
	}
	faults := len(s.state.Faults)
	injectNaturalSoakFault(t, s, repeat, o)
	if len(s.state.Faults) != faults {
		t.Fatal("injected more than once")
	}
	diagAssertParity(t, s, repeat)
	missing, missingRepeat := newTestSim(t, cfg, 1), newTestSim(t, cfg, 1)
	missing.state.Tick, missingRepeat.state.Tick = 300, 300
	setup := newFinalSoakObserver(finalSoakScenario{name: "S3"}, 1)
	injectNaturalSoakFault(t, missing, missingRepeat, setup)
	if setup.report.Failures["fault setup"] == "" || len(missing.state.Faults) != 0 {
		t.Fatal("missing aisle setup was hidden or fabricated")
	}
}

func TestFinalIdleObstructionEvidenceOwnership(t *testing.T) {
	s := finalWaitFixture(t)
	before := diagJSON(diagSnapshot(s))
	owner, _, obstructs := s.idleObstruction(s.state.Robots[1])
	if !obstructs || owner.ID != "R1" {
		t.Fatal("idle-blocker evidence lost its active mission")
	}
	owner.X++
	if diagJSON(diagSnapshot(s)) != before {
		t.Fatal("diagnostic obstruction analysis or returned evidence aliases state")
	}
}

func TestFinalBlockedReportingUsesI14(t *testing.T) {
	s := finalWaitFixture(t)
	scenario := finalSoakScenario{name: "S3", fault: true}
	o := newFinalSoakObserver(scenario, 1)
	r := &s.state.Robots[0]
	r.WaitTicks, r.Blocked = s.cfg.BlockedAfter, true
	o.observe(t, s, s.Snapshot(), scenario)
	if len(o.report.Failures) != 0 || o.report.BlockedIncidents != 1 || o.blocked[r.ID].Class != "IDLE-BLOCKER" || o.blocked[r.ID].OwnParked {
		t.Fatalf("valid wait-based blocked incident lost: %+v", o.report)
	}
	// A classification change stays visible within the same blocked episode.
	s.state.Tick++
	s.state.Robots[1].Status = RobotOffline
	r.WaitTicks++
	o.observe(t, s, s.Snapshot(), scenario)
	if len(o.report.Failures) != 0 || o.report.BlockedIncidents != 1 || o.blocked[r.ID].Class != "PARKED" {
		t.Fatal("transitive obstruction classification or incident count lost")
	}
	r.WaitTicks, r.Blocked = 0, false
	o.observe(t, s, s.Snapshot(), scenario)
	if len(o.blocked) != 0 {
		t.Fatal("ended incident remains open")
	}
}

func TestFinalLegacyExceptionScope(t *testing.T) {
	s := newTestSim(t, DefaultConfig(), 1)
	for tick := 1; tick <= 180; tick++ {
		s.Tick()
		if tick < 142 {
			continue
		}
		scenario := finalSoakScenario{name: "S2/default", legacy: true}
		r := *s.faultRobot("R6")
		class := classifySoakWait(s, r)
		if got := verifiedLegacyWaitException(s, r, scenario, class); got != (tick <= 179) {
			t.Fatalf("exception boundary tick%d accepted=%v class=%s", tick, got, class)
		}
		if tick != 142 {
			continue
		}
		for _, change := range []string{"seed", "tick", "robot", "position", "wait", "battery", "goal", "config", "scenario", "class"} {
			t.Run(change, func(t *testing.T) {
				oldState, oldCfg := s.Snapshot(), s.cfg
				changed, sc, cl := r, scenario, class
				switch change {
				case "seed":
					s.state.Seed = 2
				case "tick":
					s.state.Tick = 141
				case "robot":
					changed.ID = "R5"
				case "position":
					changed.X++
				case "wait":
					changed.WaitTicks++
				case "battery":
					s.faultRobot("R2").Battery--
				case "goal":
					s.state.Jobs[16].To.X++
				case "config":
					s.cfg.AutoCharge = true
				case "scenario":
					sc.name = "S3"
				case "class":
					cl = "CYCLE"
				}
				accepted := verifiedLegacyWaitException(s, changed, sc, cl)
				s.state, s.cfg = oldState, oldCfg
				if accepted {
					t.Fatal("exception accepted an unverified episode")
				}
			})
		}
	}
}
