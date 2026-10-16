package sim

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/AyushM7/fleetops/backend/routing"
	"github.com/AyushM7/fleetops/backend/scheduler"
)

var runReliabilityDiagnostics = flag.Bool("diag", false, "run opt-in R0 reliability diagnostics")

const (
	diagParked = "Parked-robot obstruction"
	diagIdle   = "Idle-robot blockage"
	diagCycle  = "Healthy-robot traffic cycle"
	diagOther  = "Other or unknown"
)

type diagRoute struct {
	Target     Point
	Cells      []Point
	Relocation bool
}
type diagFrame struct {
	State    State
	Routes   map[string]diagRoute
	Recovery map[string]diagRecovery
}

type diagRecovery struct {
	Hold     int
	Resume   bool
	Retreats []int
}
type diagNode struct {
	Status   RobotStatus
	Battery  int
	Next     *Point
	Blockers []string
}
type diagCause struct {
	Classes []string
	Chains  []string
	Graph   map[string]diagNode
}
type diagEvent struct {
	Tick                  int
	Robot, Kind, Activity string
	Waiting               bool
	Action                string `json:",omitempty"`
	Before, After         RobotState
	Stations              string
	Cause                 *diagCause      `json:",omitempty"`
	Clearance             []diagClearance `json:",omitempty"`
	Energy                *diagEnergy     `json:",omitempty"`
	PlannedRoute          *diagRoute      `json:",omitempty"`
	Frame                 diagFrame
}
type diagSummary struct {
	Scenario    string
	Seed        int64
	MaxWait     int
	WaitClasses map[string]int
	Depletions  []string
	Cycles      []string
	Relocations []string
	Recoveries  []string
}
type diagJournal struct {
	events          []diagEvent
	waitSignature   map[string]string
	chargeSignature map[string]string
	summary         diagSummary
}

func diagSnapshot(s *Sim) diagFrame {
	frame := diagFrame{State: s.Snapshot(), Routes: make(map[string]diagRoute), Recovery: make(map[string]diagRecovery)}
	for _, r := range s.state.Robots {
		if nav, ok := s.navigation[r.ID]; ok {
			frame.Routes[r.ID] = diagRoute{Target: nav.target, Cells: slices.Clone(nav.route), Relocation: nav.relocation}
		}
		if recovery, ok := s.recovery[r.ID]; ok {
			frame.Recovery[r.ID] = diagRecovery{Hold: recovery.hold, Resume: recovery.resume, Retreats: slices.Clone(recovery.retreats)}
		}
	}
	return frame
}

func diagJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func diagActivity(s *Sim, r RobotState) string {
	if r.JobID != "" {
		return "job " + r.JobID
	}
	if s.reservedStation(r.ID) != nil {
		return "charging trip"
	}
	if s.navigation[r.ID].relocation {
		return "relocation trip"
	}
	if r.Status == RobotMoving {
		return "relocation or unsupported jobless movement"
	}
	if r.Status == RobotIdle && s.cfg.AutoCharge && r.Battery < s.cfg.ChargeBelow {
		return "idle seeking charge"
	}
	return string(r.Status)
}

// Select the route stepMoving will actually attempt, including pickup already
// reached at tick start. This lookup never installs or changes a cached route.
func diagAttempt(s *Sim, r RobotState) []Point {
	target := s.navigation[r.ID].target
	for _, j := range s.state.Jobs {
		if j.ID != r.JobID || r.JobID == "" {
			continue
		}
		target = j.From
		if j.Status == JobInProgress || (Point{X: r.X, Y: r.Y}) == j.From {
			target = j.To
		}
	}
	if nav, ok := s.navigation[r.ID]; ok && nav.target == target {
		return nav.route
	}
	route, err := routing.FindPath(routingGrid{cfg: s.cfg}, routing.Cell{X: r.X, Y: r.Y}, routing.Cell{X: target.X, Y: target.Y}, routing.Options{Blocked: func(c routing.Cell) bool {
		return s.navigationBlocked(r, Point{X: c.X, Y: c.Y}, false)
	}})
	if err != nil {
		return nil
	}
	points := make([]Point, len(route))
	for i, c := range route {
		points[i] = Point{X: c.X, Y: c.Y}
	}
	return points
}

func diagDependencies(s *Sim) map[string]diagNode {
	graph := make(map[string]diagNode)
	for _, r := range s.state.Robots {
		node := diagNode{Status: r.Status, Battery: r.Battery, Blockers: []string{}}
		route := diagAttempt(s, r)
		if r.Status == RobotMoving && len(route) > 0 {
			next := route[0]
			node.Next = &next
			for _, other := range s.state.Robots {
				if other.ID == r.ID {
					continue
				}
				sameCell := next == (Point{X: other.X, Y: other.Y})
				heldAisle := s.cfg.onAisle(next) && next.X == other.X && s.cfg.onAisle(Point{X: other.X, Y: other.Y}) && (!s.cfg.onAisle(Point{X: r.X, Y: r.Y}) || r.X != next.X)
				if sameCell || heldAisle {
					node.Blockers = append(node.Blockers, other.ID)
				}
			}
		}
		graph[r.ID] = node
	}
	return graph
}

func diagClassify(graph map[string]diagNode, id string) diagCause {
	cause := diagCause{Classes: []string{}, Chains: []string{}, Graph: graph}
	diagVisit(graph, id, nil, &cause)
	slices.Sort(cause.Classes)
	slices.Sort(cause.Chains)
	cause.Classes = slices.Compact(cause.Classes)
	cause.Chains = slices.Compact(cause.Chains)
	return cause
}

func diagVisit(graph map[string]diagNode, id string, path []string, cause *diagCause) {
	if slices.Contains(path, id) {
		cause.Classes = append(cause.Classes, diagCycle)
		cause.Chains = append(cause.Chains, strings.Join(append(slices.Clone(path), id), " -> "))
		return
	}
	path = append(slices.Clone(path), id)
	node, exists := graph[id]
	category := ""
	switch {
	case !exists:
		category = diagOther
	case node.Status == RobotOffline || node.Status == RobotFaulted || node.Status == RobotCharging:
		category = diagParked
	case node.Status == RobotIdle:
		category = diagIdle
	case node.Status != RobotMoving || node.Battery <= 0 || len(node.Blockers) == 0:
		category = diagOther
	}
	if category != "" {
		cause.Classes = append(cause.Classes, category)
		cause.Chains = append(cause.Chains, strings.Join(path, " -> ")+" ["+category+"]")
		return
	}
	for _, blocker := range node.Blockers {
		diagVisit(graph, blocker, path, cause)
	}
}

func (journal *diagJournal) event(s *Sim, before RobotState, frame diagFrame, kind string, cause *diagCause) {
	var planned *diagRoute
	if nav, ok := s.navigation[before.ID]; ok {
		planned = &diagRoute{Target: nav.target, Cells: slices.Clone(nav.route), Relocation: nav.relocation}
	}
	journal.events = append(journal.events, diagEvent{Tick: s.state.Tick, Robot: before.ID, Kind: kind, Activity: diagActivityFromFrame(frame, before), Waiting: before.WaitTicks > 0, Before: before, After: *s.faultRobot(before.ID), Stations: stationEvidence(s, before), Cause: cause, PlannedRoute: planned, Frame: frame})
}

func (journal *diagJournal) longWait(s *Sim, r RobotState, frame diagFrame, cause diagCause) {
	if r.WaitTicks <= 40 {
		delete(journal.waitSignature, r.ID)
		return
	}
	signature := diagJSON(struct{ Classes, Chains []string }{cause.Classes, cause.Chains})
	if journal.waitSignature[r.ID] == signature {
		return
	}
	journal.waitSignature[r.ID] = signature
	for _, class := range cause.Classes {
		journal.summary.WaitClasses[class]++
	}
	for _, chain := range cause.Chains {
		if slices.Contains(cause.Classes, diagCycle) && !strings.Contains(chain, "[") {
			journal.summary.Cycles = append(journal.summary.Cycles, fmt.Sprintf("tick=%d %s", s.state.Tick, chain))
		}
	}
	clearance := []diagClearance{}
	for _, other := range s.state.Robots {
		if other.Status == RobotIdle {
			clearance = append(clearance, diagIdleClearance(s, other))
		}
	}
	journal.events = append(journal.events, diagEvent{Tick: s.state.Tick, Robot: r.ID, Kind: "wait exceeds 40; classification or dependency changed", Activity: diagActivity(s, r), Waiting: true, Before: frame.State.Robots[idNumber(r.ID)-1], After: r, Stations: stationEvidence(s, r), Cause: &cause, Clearance: clearance, Frame: frame})
}

func diagNewJournal(scenario soakScenario, seed int64) *diagJournal {
	return &diagJournal{waitSignature: make(map[string]string), chargeSignature: make(map[string]string), summary: diagSummary{Scenario: scenario.name, Seed: seed, WaitClasses: make(map[string]int), Depletions: []string{}, Cycles: []string{}}}
}

// Replay the existing passes only to observe exact numeric movement turns.
// Every resulting state, private route, and countdown is checked against Tick.
func diagTick(s *Sim, journal *diagJournal) {
	s.state.Tick++
	if s.state.Tick%s.cfg.NewJobEvery == 0 && len(s.state.Jobs) < s.cfg.MaxJobs {
		s.addJob()
	}
	slices.SortFunc(s.state.Robots, func(a, b RobotState) int { return idNumber(a.ID) - idNumber(b.ID) })
	skip := make([]bool, len(s.state.Robots))
	for i := range s.state.Robots {
		before := s.state.Robots[i]
		frame := diagSnapshot(s)
		skip[i] = s.prepareRobot(&s.state.Robots[i])
		journal.depletion(s, before, frame)
		if before.Status == RobotIdle && s.state.Robots[i].Battery < before.Battery {
			journal.event(s, before, frame, "idle battery drain", nil)
		}
	}
	before := diagSnapshot(s)
	s.assignPending(skip)
	journal.jobBudgets(s, before)
	s.seekCharging(skip)
	s.clearIdleAisles(skip)
	journal.passTransitions(s, before)
	moved := make([]bool, len(s.state.Robots))
	for i := range s.state.Robots {
		if skip[i] {
			continue
		}
		if s.state.Robots[i].Status == RobotMoving {
			position := Point{X: s.state.Robots[i].X, Y: s.state.Robots[i].Y}
			journal.movement(s, i)
			moved[i] = position != (Point{X: s.state.Robots[i].X, Y: s.state.Robots[i].Y})
		}
		if s.state.Robots[i].Status != RobotMoving {
			s.finishNavigation(&s.state.Robots[i])
		}
	}
	recoveryBefore := diagSnapshot(s)
	for _, event := range s.recoverTraffic(moved) {
		journal.summary.Recoveries = append(journal.summary.Recoveries, fmt.Sprintf("tick=%d %s", s.state.Tick, event))
		if event.robot != "" {
			journal.event(s, recoveryBefore.State.Robots[idNumber(event.robot)-1], recoveryBefore, "cycle retreat and hold", nil)
		}
	}
	s.state.Aisles = s.snapshotAisles()
}

func (journal *diagJournal) passTransitions(s *Sim, before diagFrame) {
	for i, r := range s.state.Robots {
		if s.navigation[r.ID].relocation && !before.Routes[r.ID].Relocation {
			journal.summary.Relocations = append(journal.summary.Relocations, fmt.Sprintf("%s@%d (%d,%d) -> %v", r.ID, s.state.Tick, r.X, r.Y, s.navigation[r.ID].target))
		}
		if before.State.Robots[i].Status != r.Status || before.State.Robots[i].JobID != r.JobID {
			journal.event(s, before.State.Robots[i], before, "dispatch, charging, or relocation selection", nil)
		}
		journal.unavailableCharge(s, r)
	}
}

func (journal *diagJournal) depletion(s *Sim, before RobotState, frame diagFrame) {
	after := *s.faultRobot(before.ID)
	if before.Status == RobotFaulted || after.Status != RobotFaulted || after.Battery != 0 {
		return
	}
	activity := diagActivityFromFrame(frame, before)
	action := "other or unknown drain"
	if before.X != after.X || before.Y != after.Y {
		action = "successful movement; MovingDrain"
	} else if before.Status == RobotIdle {
		action = "stationary idle; IdleDrainEvery"
	}
	journal.summary.Depletions = append(journal.summary.Depletions, fmt.Sprintf("%s@%d %s waiting=%v", before.ID, s.state.Tick, activity, before.WaitTicks > 0))
	journal.events = append(journal.events, diagEvent{Tick: s.state.Tick, Robot: before.ID, Kind: "battery depletion", Activity: activity, Waiting: before.WaitTicks > 0, Action: action, Before: before, After: after, Stations: stationEvidence(s, before), Frame: frame})
}

func diagActivityFromFrame(frame diagFrame, r RobotState) string {
	if frame.Routes[r.ID].Relocation {
		return "relocation trip"
	}
	if r.JobID != "" {
		return "job " + r.JobID
	}
	for _, station := range frame.State.Stations {
		if station.ReservedBy == r.ID {
			return "charging trip"
		}
	}
	if r.Status == RobotMoving {
		return "relocation or unsupported jobless movement"
	}
	return string(r.Status)
}

type diagClearance struct {
	Robot       string
	Battery     int
	Destination *Point
	Route       []Point
	Cost        int
	Reason      string
}

type diagEnergy struct {
	Active                                     bool
	Job, ExitStation                           string
	Leg1, Leg2, ExitCost, Battery, RequiredRA1 int
	EligibleRA1                                bool
}

func (journal *diagJournal) jobBudgets(s *Sim, before diagFrame) {
	dist := s.dispatchDistance()
	for i, r := range s.state.Robots {
		if r.JobID == "" || r.JobID == before.State.Robots[i].JobID {
			continue
		}
		for _, j := range s.state.Jobs {
			if j.ID != r.JobID {
				continue
			}
			energy := diagJobEnergy(s, r, j, dist)
			journal.events = append(journal.events, diagEvent{Tick: s.state.Tick, Robot: r.ID, Kind: "job energy admission; RA1 counterfactual", Activity: "job " + j.ID, Before: before.State.Robots[i], After: r, Energy: &energy, Frame: diagSnapshot(s)})
		}
	}
}

func diagJobEnergy(s *Sim, r RobotState, j JobState, dist scheduler.Distance) diagEnergy {
	energy := diagEnergy{Active: s.cfg.AutoCharge, Job: j.ID, Battery: r.Battery, ExitCost: -1, RequiredRA1: -1}
	energy.Leg1, _ = dist(scheduler.Cell{X: r.X, Y: r.Y}, scheduler.Cell{X: j.From.X, Y: j.From.Y})
	energy.Leg2, _ = dist(scheduler.Cell{X: j.From.X, Y: j.From.Y}, scheduler.Cell{X: j.To.X, Y: j.To.Y})
	for _, station := range s.state.Stations {
		if !s.stationFreeFor(station, r.ID) {
			continue
		}
		steps, ok := dist(scheduler.Cell{X: j.To.X, Y: j.To.Y}, scheduler.Cell{X: station.X, Y: station.Y})
		if ok && (energy.ExitCost < 0 || steps < energy.ExitCost) {
			energy.ExitCost, energy.ExitStation = steps, station.ID
		}
	}
	if energy.ExitCost >= 0 {
		energy.RequiredRA1 = (energy.Leg1+energy.Leg2+energy.ExitCost)*s.cfg.MovingDrain + s.cfg.ReserveBattery
	}
	energy.EligibleRA1 = energy.ExitCost >= 0 && r.Battery >= energy.RequiredRA1
	return energy
}

// Hypothetical RA3 reachability only; Recovery is not implemented or enabled.
// Other robots and occupied aisles remain obstacles. No robot is moved.
func diagIdleClearance(s *Sim, r RobotState) diagClearance {
	result := diagClearance{Robot: r.ID, Battery: r.Battery, Route: []Point{}, Reason: "no reachable clearance cell with current traffic"}
	if !s.cfg.onAisle(Point{X: r.X, Y: r.Y}) {
		result.Reason = "outside an aisle; RA3 does not apply"
		return result
	}
	for y := 0; y < s.cfg.Height; y++ {
		for x := 0; x < s.cfg.Width; x++ {
			p := Point{X: x, Y: y}
			if !diagClearCell(s, r, p) {
				continue
			}
			route, err := routing.FindPath(routingGrid{cfg: s.cfg}, routing.Cell{X: r.X, Y: r.Y}, routing.Cell{X: x, Y: y}, routing.Options{Blocked: func(c routing.Cell) bool { return s.navigationBlocked(r, Point{X: c.X, Y: c.Y}, true) }})
			if err != nil || result.Destination != nil && len(route)*s.cfg.MovingDrain >= result.Cost {
				continue
			}
			result.Destination = &p
			result.Cost = len(route) * s.cfg.MovingDrain
			result.Route = make([]Point, len(route))
			for i, c := range route {
				result.Route[i] = Point{X: c.X, Y: c.Y}
			}
		}
	}
	if result.Destination != nil {
		result.Reason = "reachable with RA2 margin >=1; Recovery gating unresolved"
		if r.Battery-result.Cost < 1 {
			result.Reason = "clearance route fails RA2 margin >=1"
		}
	}
	return result
}

func diagClearCell(s *Sim, r RobotState, p Point) bool {
	if p == (Point{X: r.X, Y: r.Y}) || s.cfg.CellTypeAt(p) != CellFree {
		return false
	}
	for y := s.cfg.AisleYMin; y <= s.cfg.AisleYMax; y++ {
		if diagAbs(p.X-r.X)+diagAbs(p.Y-y) == 1 {
			return true
		}
	}
	return false
}

func diagAbs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func (journal *diagJournal) unavailableCharge(s *Sim, r RobotState) {
	if !s.cfg.AutoCharge || r.Status != RobotIdle || r.Battery >= s.cfg.ChargeBelow {
		delete(journal.chargeSignature, r.ID)
		return
	}
	signature := stationEvidence(s, r)
	if journal.chargeSignature[r.ID] == signature {
		return
	}
	journal.chargeSignature[r.ID] = signature
	clearance := []diagClearance{}
	for _, station := range s.state.Stations {
		for _, other := range s.state.Robots {
			if other.ID != r.ID && other.Status == RobotIdle && other.X == station.X && other.Y == station.Y {
				clearance = append(clearance, diagIdleClearance(s, other))
			}
		}
	}
	journal.events = append(journal.events, diagEvent{Tick: s.state.Tick, Robot: r.ID, Kind: "idle seeking charge; station availability or travel cost changed", Activity: diagActivity(s, r), Before: r, After: r, Stations: signature, Clearance: clearance, Frame: diagSnapshot(s)})
}

func (journal *diagJournal) movement(s *Sim, index int) {
	before := s.state.Robots[index]
	frame := diagSnapshot(s)
	cause := diagClassify(diagDependencies(s), before.ID)
	oldRoute := slices.Clone(diagAttempt(s, before))
	s.stepMoving(&s.state.Robots[index])
	after := s.state.Robots[index]
	journal.summary.MaxWait = max(journal.summary.MaxWait, after.WaitTicks)
	journal.longWait(s, after, frame, cause)
	journal.depletion(s, before, frame)
	if before.JobID != after.JobID || before.Status != after.Status {
		journal.event(s, before, frame, "movement/status transition", nil)
	}
	if old, ok := frame.Routes[before.ID]; ok && after.Status == RobotMoving && old.Target != s.navigation[after.ID].target {
		journal.event(s, before, frame, "target or route phase changed", nil)
	}
	if before.X == after.X && before.Y == after.Y && !slices.Equal(oldRoute, s.navigation[after.ID].route) {
		journal.event(s, before, frame, fmt.Sprintf("replan remaining steps %d -> %d", len(oldRoute), len(s.navigation[after.ID].route)), &cause)
	}
}

func diagAssertParity(t *testing.T, reference, observed *Sim) {
	t.Helper()
	if !reflect.DeepEqual(reference.Snapshot(), observed.Snapshot()) || !reflect.DeepEqual(reference.navigation, observed.navigation) || !reflect.DeepEqual(reference.unresponsiveTicks, observed.unresponsiveTicks) || !reflect.DeepEqual(reference.recovery, observed.recovery) {
		t.Fatalf("diagnostic replay differs from Tick at %d: reference=%s observed=%s", reference.state.Tick, diagJSON(diagSnapshot(reference)), diagJSON(diagSnapshot(observed)))
	}
}

func TestReliabilityDiagnosis(t *testing.T) {
	if !*runReliabilityDiagnostics {
		t.Skip("requires -diag")
	}
	for _, scenario := range []soakScenario{{"S1", 1500, true, true, false}, {"S2", 600, true, false, false}, {"S3", 500, false, false, false}, {"S4", 600, true, true, true}} {
		for _, seed := range []int64{1, 2, 3, 42, 99} {
			t.Run(fmt.Sprintf("%s/seed%d", scenario.name, seed), func(t *testing.T) { diagScenario(t, scenario, seed) })
		}
	}
}

func diagScenario(t *testing.T, scenario soakScenario, seed int64) {
	reference, observed := newSoak(t, scenario, seed), newSoak(t, scenario, seed)
	journal := diagNewJournal(scenario, seed)
	for tick := 1; tick <= scenario.ticks; tick++ {
		before := reference.Snapshot()
		reference.Tick()
		diagTick(observed, journal)
		if scenario.fault {
			advanceFaultSoak(t, reference, before)
			advanceFaultSoak(t, observed, before)
		}
		diagAssertParity(t, reference, observed)
		assertInvariants(t, reference, reference.cfg)
	}
	for _, event := range journal.events {
		t.Logf("EVENT %s", diagJSON(event))
	}
	t.Logf("FINAL %s", diagJSON(diagSnapshot(reference)))
	t.Logf("SUMMARY %s", diagJSON(journal.summary))
}

func TestReliabilityDiagnosisClassifier(t *testing.T) {
	if !*runReliabilityDiagnostics {
		t.Skip("requires -diag")
	}
	for _, tc := range []struct {
		name  string
		graph map[string]diagNode
		want  []string
	}{
		{"parked", map[string]diagNode{"R1": {Status: RobotMoving, Battery: 10, Blockers: []string{"R2"}}, "R2": {Status: RobotOffline}}, []string{diagParked}},
		{"idle", map[string]diagNode{"R1": {Status: RobotMoving, Battery: 10, Blockers: []string{"R2"}}, "R2": {Status: RobotIdle, Battery: 10}}, []string{diagIdle}},
		{"healthy cycle", map[string]diagNode{"R1": {Status: RobotMoving, Battery: 10, Blockers: []string{"R2"}}, "R2": {Status: RobotMoving, Battery: 10, Blockers: []string{"R1"}}}, []string{diagCycle}},
		{"unknown", map[string]diagNode{"R1": {Status: RobotMoving, Battery: 10}}, []string{diagOther}},
		{"branches", map[string]diagNode{"R1": {Status: RobotMoving, Battery: 10, Blockers: []string{"R2", "R3"}}, "R2": {Status: RobotIdle, Battery: 10}, "R3": {Status: RobotFaulted}}, []string{diagIdle, diagParked}},
		{"diamond is not cycle", map[string]diagNode{"R1": {Status: RobotMoving, Battery: 10, Blockers: []string{"R2", "R3"}}, "R2": {Status: RobotMoving, Battery: 10, Blockers: []string{"R4"}}, "R3": {Status: RobotMoving, Battery: 10, Blockers: []string{"R4"}}, "R4": {Status: RobotIdle, Battery: 10}}, []string{diagIdle}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := diagClassify(tc.graph, "R1")
			slices.Sort(tc.want)
			if !slices.Equal(first.Classes, tc.want) {
				t.Fatalf("classification %v, want %v", first.Classes, tc.want)
			}
			for repeat := 0; repeat < 100; repeat++ {
				if diagJSON(diagClassify(tc.graph, "R1")) != diagJSON(first) {
					t.Fatal("classifier is nondeterministic")
				}
			}
		})
	}
}

func TestReliabilityDiagnosisProbes(t *testing.T) {
	if !*runReliabilityDiagnostics {
		t.Skip("requires -diag")
	}
	for _, tc := range []struct {
		name      string
		seed      int64
		tick      int
		warehouse bool
		robot     string
		wantPoint *Point
		reason    string
	}{
		{"aisle exit reachable", 1, 163, true, "R6", &Point{X: 14, Y: 1}, "reachable with RA2"},
		{"both exits occupied", 99, 119, true, "R6", nil, "no reachable"},
		{"idle outside aisle", 1, 142, false, "R2", nil, "RA3 does not apply"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSoak(t, soakScenario{warehouse: tc.warehouse}, tc.seed)
			for tick := 0; tick < tc.tick; tick++ {
				s.Tick()
			}
			before := diagJSON(diagSnapshot(s))
			probe := diagIdleClearance(s, *s.faultRobot(tc.robot))
			if !reflect.DeepEqual(probe.Destination, tc.wantPoint) || !strings.Contains(probe.Reason, tc.reason) {
				t.Fatalf("unexpected clearance: %+v", probe)
			}
			if diagJSON(diagSnapshot(s)) != before {
				t.Fatal("clearance probe mutated simulator")
			}
		})
	}
	t.Run("RA1 unavailable exit", func(t *testing.T) {
		// Fixed R0 seed-42/J19 state: the R1 timeline no longer reaches this
		// unavailable-exit admission at tick 191.
		s := energyR2J19Scene(t)
		before := diagJSON(diagSnapshot(s))
		energy := diagJobEnergy(s, *s.faultRobot("R2"), s.state.Jobs[18], s.dispatchDistance())
		if energy.ExitCost != -1 || energy.EligibleRA1 {
			t.Fatalf("unavailable station admitted: %+v", energy)
		}
		if diagJSON(diagSnapshot(s)) != before {
			t.Fatal("energy probe mutated simulator")
		}
		s.assignPending(make([]bool, len(s.state.Robots)))
		if s.state.Jobs[18].Status != JobPending || s.faultRobot("R2").JobID != "" {
			t.Fatal("R1 accepted the recorded unavailable-exit J19 admission")
		}
		t.Log("Historical R0: J19 was admitted with battery 39, legs 17+16, no available charging exit, and R2 later depleted at tick 300. R1 now rejects that fixed admission.")
	})
}

func TestReliabilityDiagnosisAmendmentGap(t *testing.T) {
	if !*runReliabilityDiagnostics {
		t.Skip("requires -diag")
	}
	s := energyR3J11Scene(t)
	job := s.state.Jobs[10]
	historicalStart := RobotState{ID: "R3", X: 19, Y: 4, Battery: 49, Status: RobotMoving, JobID: "J11"}
	energy := diagJobEnergy(s, historicalStart, job, s.dispatchDistance())
	if !energy.EligibleRA1 || energy.RequiredRA1 != 32 || energy.Battery != 49 {
		t.Fatalf("unexpected historical admission: %+v", energy)
	}
	r := s.faultRobot("R3")
	old := s.navigation[r.ID]
	if r.Battery-29 != 5 {
		t.Fatal("recorded detour no longer demonstrates the per-leg budget gap")
	}
	if s.routeAffordable(*r, job.From, 29) || s.planNavigation(r, job.From, true) {
		t.Fatal("R1 accepted the underbudgeted complete-mission detour")
	}
	if !reflect.DeepEqual(old, s.navigation[r.ID]) || r.JobID != "J11" || r.Battery != 34 {
		t.Fatal("rejected replacement changed historical route, job, or battery")
	}
	s.state.Tick = 77
	s.Tick()
	if r.WaitTicks != 3 || r.Battery != 34 || r.Status != RobotMoving || !reflect.DeepEqual(old, s.navigation[r.ID]) {
		t.Fatal("rejected historical detour did not wait without drain")
	}
	assertInvariants(t, s, s.cfg)
	t.Log("Historical R0: RA1 admitted J11 (49 >= 32), and per-leg RA2 admitted the pickup detour (34-29=5), leaving only 1 after delivery before tick-120 depletion. R1 rejects that recorded route because pickup 29 + delivery 4 + exit 3 + reserve 5 requires 41, preserving the prior route and job.")
}

// Focused final-acceptance diagnosis; it neither changes the scenarios nor relaxes gates.
func TestFinalAcceptanceResolutionDiagnostics(t *testing.T) {
	if !*runReliabilityDiagnostics {
		t.Skip("requires -diag")
	}
	legacy := DefaultConfig()
	for _, tc := range []struct {
		name  string
		seed  int64
		cfg   Config
		ticks int
		fault bool
	}{
		{"legacy-default", 1, legacy, 500, false},
		{"natural-S3", 1, WarehouseConfig(), 600, true},
		{"natural-S3", 99, WarehouseConfig(), 600, true},
	} {
		t.Run(fmt.Sprintf("%s/seed%d", tc.name, tc.seed), func(t *testing.T) {
			s, repeat := newTestSim(t, tc.cfg, tc.seed), newTestSim(t, tc.cfg, tc.seed)
			o := newFinalSoakObserver(finalSoakScenario{name: tc.name}, tc.seed)
			type interval struct {
				Start, End, FirstWait, LastWait int
				Robot, Class, Chain             string
				OwnParkedObstruction            bool
			}
			active := map[string]interval{}
			closeInterval := func(id string) {
				if episode, ok := active[id]; ok {
					t.Logf("WAIT_INTERVAL %s", diagJSON(episode))
					delete(active, id)
				}
			}
			for tick := 1; tick <= tc.ticks; tick++ {
				before := s.Snapshot()
				s.Tick()
				repeat.Tick()
				if tc.fault {
					injectNaturalSoakFault(t, s, repeat, o)
				}
				diagAssertParity(t, s, repeat)
				assertInvariants(t, s, s.cfg)
				for i, r := range s.state.Robots {
					if tc.fault && tc.seed == 99 && r.ID == "R2" && (tick == 178 || tick == 240) {
						required, target := resolutionRelocationBudget(s, r)
						_, admitted := s.relocationRoute(r)
						t.Logf("IDLE_ROOT tick%d robot=%s battery=%d chargingClass=%s chargerExit=%d relocationAdmitted=%v minimumRelocationBattery=%d minimumTarget=%v", tick, r.ID, r.Battery, chargeClassForTest(s, r), s.relocationExit(r, Point{X: r.X, Y: r.Y}), admitted, required, target)
						if r.Battery != 16 || required != 17 || admitted {
							t.Fatal("energy-constrained idle blocker does not reproduce")
						}
					}
					if r.WaitTicks <= 40 {
						closeInterval(r.ID)
					} else {
						class := classifySoakWait(s, r)
						chain := strings.Join(diagClassify(diagDependencies(s), r.ID).Chains, "; ")
						parked := soakParkedObstruction(s, r)
						old, exists := active[r.ID]
						if !exists || old.Class != class || old.Chain != chain || old.OwnParkedObstruction != parked {
							closeInterval(r.ID)
							old = interval{Start: tick, FirstWait: r.WaitTicks, Robot: r.ID, Class: class, Chain: chain, OwnParkedObstruction: parked}
						}
						old.End, old.LastWait = tick, r.WaitTicks
						active[r.ID] = old
					}
					focus := tc.fault && tc.seed == 1 && tick == 170 && r.ID == "R6" || tc.fault && tc.seed == 99 && r.ID == "R1" && (tick == 137 || tick == 138 || tick == 147 || tick == 178 || tick == 240 || tick == 241 || tick == 245 || tick == 246 || tick == 250)
					transition := tc.fault && tc.seed == 99 && tick >= 130 && tick <= 250 && (r.ID == "R1" || r.ID == "R2") && (r.Status != before.Robots[i].Status || r.JobID != before.Robots[i].JobID || slices.Contains(s.recovery[r.ID].retreats, tick))
					if focus || transition {
						t.Logf("FOCUS tick%d robot=%s activity=%s class=%s ownParked=%v chain=%s before=%s after=%s route=%s recovery=%s stations=%s", tick, r.ID, diagActivity(s, r), classifySoakWait(s, r), soakParkedObstruction(s, r), strings.Join(diagClassify(diagDependencies(s), r.ID).Chains, "; "), diagJSON(before.Robots[i]), diagJSON(r), diagJSON(s.navigation[r.ID].route), diagJSON(diagSnapshot(s).Recovery[r.ID]), stationEvidence(s, r))
					}
					if tc.fault && tc.seed == 1 && tick == 170 && r.ID == "R6" {
						if r.WaitTicks != 10 || !r.Blocked || soakParkedObstruction(s, r) || classifySoakWait(s, r) != "PARKED" {
							t.Fatal("reported transitive obstruction does not reproduce")
						}
						for _, dynamic := range []bool{false, true} {
							target := s.recoveryTarget(r)
							route, err := routing.FindPath(routingGrid{cfg: s.cfg}, routing.Cell{X: r.X, Y: r.Y}, routing.Cell{X: target.X, Y: target.Y}, routing.Options{Blocked: func(c routing.Cell) bool { return s.navigationBlocked(r, Point{X: c.X, Y: c.Y}, dynamic) }})
							t.Logf("R6_GOAL_PROBE dynamic=%v goal=%v steps=%d error=%v", dynamic, target, len(route), err)
							if !dynamic && (err != nil || len(route) != 1) || dynamic && err != routing.ErrGoalBlocked {
								t.Fatal("own-goal reachability differs from reported evidence")
							}
						}
					}
				}
			}
			for _, r := range s.state.Robots {
				closeInterval(r.ID)
			}
			if tc.name == "legacy-default" && *stabilizationLegacy != "" {
				data, err := os.ReadFile(*stabilizationLegacy)
				if err != nil {
					t.Fatal(err)
				}
				var refs []struct {
					Scenario string
					Seed     int64
					State    State
				}
				if err := json.Unmarshal(data, &refs); err != nil {
					t.Fatal(err)
				}
				found := false
				for _, ref := range refs {
					if ref.Scenario == "S3" && ref.Seed == 1 {
						got, _ := json.Marshal(s.Snapshot())
						want, _ := json.Marshal(ref.State)
						if string(got) != string(want) {
							t.Fatal("default seed1 differs from saved pre-Step-1 R3 legacy reference")
						}
						found = true
					}
				}
				if !found {
					t.Fatal("saved later legacy reference lacks default seed1")
				}
				t.Log("LEGACY_MATCH default seed1 tick500 source=pre-Step-1-R3; not authenticated P2-07 evidence")
			}
		})
	}
}

// A read-only lower bound over the existing relocation candidates, including exit.
func resolutionRelocationBudget(s *Sim, robot RobotState) (int, Point) {
	claimed := s.tripDestinations()
	minimum := -1
	var target Point
	for y := 0; y < s.cfg.Height; y++ {
		for x := 0; x < s.cfg.Width; x++ {
			point := Point{X: x, Y: y}
			if point == (Point{X: robot.X, Y: robot.Y}) || s.cfg.CellTypeAt(point) != CellFree || s.protected[point] || claimed[point] != "" && claimed[point] != robot.ID || s.navigationBlocked(robot, point, true) {
				continue
			}
			route, err := routing.FindPath(routingGrid{cfg: s.cfg}, routing.Cell{X: robot.X, Y: robot.Y}, routing.Cell{X: x, Y: y}, routing.Options{Blocked: func(c routing.Cell) bool { return s.navigationBlocked(robot, Point{X: c.X, Y: c.Y}, true) }})
			exit := s.relocationExit(robot, point)
			if err != nil || exit < 0 {
				continue
			}
			required := (len(route)+exit)*s.cfg.MovingDrain + 1
			if minimum < 0 || required < minimum {
				minimum, target = required, point
			}
		}
	}
	return minimum, target
}

// Counterfactual route admission only: no production movement rule is changed.
func TestFinalSeed99PreventiveClearanceProbe(t *testing.T) {
	if !*runReliabilityDiagnostics {
		t.Skip("requires -diag")
	}
	s, repeat := newTestSim(t, WarehouseConfig(), 99), newTestSim(t, WarehouseConfig(), 99)
	o := newFinalSoakObserver(finalSoakScenario{name: "S3"}, 99)
	for tick := 1; tick <= 142; tick++ {
		before := s.Snapshot()
		s.Tick()
		repeat.Tick()
		injectNaturalSoakFault(t, s, repeat, o)
		diagAssertParity(t, s, repeat)
		if tick == 137 {
			r := *s.faultRobot("R1")
			t.Logf("MOUTH_ENTRY tick%d before=%s after=%s remainingRoute=%s aisleOwner=%s", tick, diagJSON(before.Robots[0]), diagJSON(r), diagJSON(s.navigation[r.ID].route), diagJSON(*s.faultRobot("R2")))
			if before.Robots[0].X != 5 || before.Robots[0].Y != 1 || r.X != 4 || r.Y != 1 || s.navigation[r.ID].route[0] != (Point{X: 4, Y: 2}) {
				t.Fatal("reported aisle-mouth entry does not reproduce")
			}
		}
	}
	r := *s.faultRobot("R2")
	before := diagJSON(diagSnapshot(s))
	need, target := resolutionRelocationBudget(s, r)
	_, admitted := s.relocationRoute(r)
	t.Logf("ACTUAL tick142 R2=%s minimumBattery=%d target=%v admitted=%v", diagJSON(r), need, target, admitted)
	if r.Status != RobotIdle || r.Battery != 16 || admitted || need != 17 {
		t.Fatal("recorded energy-constrained blocker does not reproduce")
	}
	// Test only the causal occupancy distinction, retaining R1's mission and
	// claimed corridor. This is not a forecast of the altered tick timeline.
	for _, other := range repeat.state.Robots {
		if other.ID != "R1" && other.X == 5 && other.Y == 1 {
			t.Fatal("counterfactual holding cell is occupied")
		}
	}
	repeat.faultRobot("R1").X = 5
	nav := repeat.navigation["R1"]
	nav.route = append([]Point{{X: 4, Y: 1}}, nav.route...)
	repeat.navigation["R1"] = nav
	need, target = resolutionRelocationBudget(repeat, r)
	clearance, admitted := repeat.relocationRoute(r)
	exit := repeat.relocationExit(r, clearance.target)
	t.Logf("COUNTERFACTUAL R1held=(5,1) minimumBattery=%d minimumTarget=%v admitted=%v chosenTarget=%v steps=%d exit=%d battery=%d route=%s", need, target, admitted, clearance.target, len(clearance.route), exit, r.Battery, diagJSON(clearance.route))
	if diagJSON(diagSnapshot(s)) != before {
		t.Fatal("counterfactual probe mutated actual replay")
	}
	if !admitted || (len(clearance.route)+exit)*repeat.cfg.MovingDrain+1 > r.Battery {
		t.Fatal("holding the aisle mouth does not enable energy-safe existing clearing")
	}
}
