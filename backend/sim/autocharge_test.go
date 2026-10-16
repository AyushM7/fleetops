package sim

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

var stabilizationDiagnostics = flag.Bool("stabilization-diag", false, "report Step 1 S1 station access without changing soak acceptance")
var stabilizationLegacy = flag.String("stabilization-legacy", "", "compare legacy runs with a saved pre-Step-1 reference")

func TestEnergySafeQueueSeed2(t *testing.T) {
	s := newTestSim(t, WarehouseConfig(), 2)
	arrivals := map[string]int{}
	queued := map[string]bool{}
	for tick := 1; tick <= 520; tick++ {
		before := s.Snapshot()
		s.Tick()
		assertInvariants(t, s, s.cfg)
		for i, r := range s.state.Robots {
			if r.ID != "R3" && r.ID != "R6" {
				continue
			}
			queued[r.ID] = queued[r.ID] || s.queuedForCharging(r)
			if r.Status == RobotFaulted {
				t.Fatalf("%s depleted at tick%d", r.ID, tick)
			}
			if r.Status == RobotCharging && before.Robots[i].Status != RobotCharging {
				arrivals[r.ID]++
				if r.Battery < 1 {
					t.Fatal("charging admission floor lost")
				}
			}
		}
	}
	for _, id := range []string{"R3", "R6"} {
		if !queued[id] || arrivals[id] == 0 {
			t.Fatalf("%s queued=%v arrivals=%d", id, queued[id], arrivals[id])
		}
	}
}

func TestStabilizationLegacyCompatibility(t *testing.T) {
	if *stabilizationLegacy == "" {
		t.Skip("requires -stabilization-legacy reference")
	}
	data, err := os.ReadFile(*stabilizationLegacy)
	if err != nil {
		t.Fatal(err)
	}
	var references []struct {
		Scenario string
		Seed     int64
		State    State
	}
	if err := json.Unmarshal(data, &references); err != nil {
		t.Fatal(err)
	}
	if len(references) != 10 {
		t.Fatalf("need ten legacy references, got %d", len(references))
	}
	seen := make(map[string]bool)
	for _, reference := range references {
		key := fmt.Sprintf("%s/%d", reference.Scenario, reference.Seed)
		if seen[key] || !slices.Contains([]int64{1, 2, 3, 42, 99}, reference.Seed) {
			t.Fatalf("invalid/duplicate reference %s", key)
		}
		seen[key] = true
		cfg, ticks := DefaultConfig(), 500
		if reference.Scenario == "S2" {
			cfg = WarehouseConfig()
			cfg.AutoCharge, cfg.Recovery = false, false
			ticks = 600
		} else if reference.Scenario != "S3" {
			t.Fatalf("unknown legacy scenario %s", reference.Scenario)
		}
		a, b := newTestSim(t, cfg, reference.Seed), newTestSim(t, cfg, reference.Seed)
		for tick := 0; tick < ticks; tick++ {
			a.Tick()
			b.Tick()
			assertInvariants(t, a, cfg)
		}
		got, _ := json.Marshal(a.Snapshot())
		repeat, _ := json.Marshal(b.Snapshot())
		want, _ := json.Marshal(reference.State)
		if !bytes.Equal(got, repeat) || !bytes.Equal(got, want) {
			t.Fatalf("legacy JSON changed: %s", key)
		}
	}
}

type stabilizationReport struct {
	Seed                                                int64
	MaxWait, Completed, MaxProtectedIdle, MaxChargeWait int
	BatteryFaults                                       []string
	Arrivals, StationEvacuations                        map[string]int
	FinalStations                                       []StationState
	FinalAccess                                         map[string][]string
}

func TestStabilizationS1Diagnostics(t *testing.T) {
	if !*stabilizationDiagnostics {
		t.Skip("requires -stabilization-diag")
	}
	for _, seed := range []int64{1, 2, 3, 42, 99} {
		s := newTestSim(t, WarehouseConfig(), seed)
		report := stabilizationReport{Seed: seed, BatteryFaults: []string{}, Arrivals: map[string]int{}, StationEvacuations: map[string]int{}, FinalAccess: map[string][]string{}}
		protectedWait, chargeWait := map[string]int{}, map[string]int{}
		for tick := 1; tick <= 1500; tick++ {
			before := s.Snapshot()
			s.Tick()
			assertInvariants(t, s, s.cfg)
			for i, r := range s.state.Robots {
				old := before.Robots[i]
				report.MaxWait = max(report.MaxWait, r.WaitTicks)
				if r.Status == RobotFaulted && old.Status != RobotFaulted {
					report.BatteryFaults = append(report.BatteryFaults, fmt.Sprintf("%s@%d from %s", r.ID, tick, old.Status))
				}
				if r.Status == RobotIdle && r.JobID == "" && s.protected[Point{X: r.X, Y: r.Y}] {
					protectedWait[r.ID]++
				} else {
					protectedWait[r.ID] = 0
				}
				report.MaxProtectedIdle = max(report.MaxProtectedIdle, protectedWait[r.ID])
				if r.Status == RobotIdle && r.Battery < s.cfg.ChargeBelow && chargeClassForTest(s, r) != "stranded" {
					chargeWait[r.ID]++
				} else {
					chargeWait[r.ID] = 0
				}
				report.MaxChargeWait = max(report.MaxChargeWait, chargeWait[r.ID])
				for _, station := range s.state.Stations {
					if r.Status == RobotCharging && old.Status != RobotCharging && r.X == station.X && r.Y == station.Y {
						report.Arrivals[station.ID]++
					}
					if old.Status == RobotIdle && old.X == station.X && old.Y == station.Y && r.Status == RobotMoving && r.JobID == "" && s.navigation[r.ID].relocation {
						report.StationEvacuations[station.ID]++
					}
				}
			}
		}
		for _, job := range s.state.Jobs {
			if job.Status == JobDone {
				report.Completed++
			}
		}
		report.FinalStations = s.Snapshot().Stations
		for _, station := range s.state.Stations {
			report.FinalAccess[station.ID] = []string{}
			for _, robot := range s.state.Robots {
				for _, point := range orthogonalNeighbors(Point{X: station.X, Y: station.Y}) {
					if robot.X == point.X && robot.Y == point.Y {
						report.FinalAccess[station.ID] = append(report.FinalAccess[station.ID], robot.ID)
					}
				}
			}
		}
		data, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("STABILIZATION %s", data)
	}
}

func autochargeSim(t *testing.T, cfg Config, start Point, battery int) *Sim {
	t.Helper()
	cfg.AutoCharge, cfg.AutoDispatch, cfg.InitialJobs, cfg.NewJobEvery = true, false, 0, 1000
	s := newTestSim(t, cfg, 42)
	for i := range s.state.Robots {
		s.state.Robots[i].Status = RobotOffline
	}
	r := &s.state.Robots[0]
	r.X, r.Y, r.Battery, r.Status = start.X, start.Y, battery, RobotIdle
	return s
}

func TestAutoChargeCycle(t *testing.T) {
	s := autochargeSim(t, DefaultConfig(), Point{3, 0}, 35)
	for tick := 1; tick <= 37; tick++ {
		s.Tick()
		r, station := s.state.Robots[0], s.state.Stations[0]
		if tick < 3 {
			if r.Status != RobotMoving || r.X != 3-tick || r.Y != 0 || r.Battery != 35-tick || r.JobID != "" || station.ReservedBy != "R1" || station.OccupiedBy != "" {
				t.Fatalf("tick %d charge trip: %+v %+v", tick, r, station)
			}
		} else {
			wantStatus, wantOccupant := RobotCharging, "R1"
			if tick == 37 {
				wantStatus, wantOccupant = RobotIdle, ""
			}
			if r.Status != wantStatus || r.X != 0 || r.Y != 0 || r.Battery != min(100, 32+2*(tick-3)) || station.ReservedBy != "" || station.OccupiedBy != wantOccupant {
				t.Fatalf("tick %d charging cycle: %+v %+v", tick, r, station)
			}
		}
		if r.WaitTicks != 0 || r.Blocked || len(s.state.Faults) != 0 {
			t.Fatal("routine charge retained waiting or created faults")
		}
		assertInvariants(t, s, s.cfg)
	}
}

func TestAutoChargeStationTie(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Stations = []Point{{0, 0}, {2, 0}}
	s := autochargeSim(t, cfg, Point{1, 1}, 15)
	before := s.Snapshot()
	for repeat := 0; repeat < 100; repeat++ {
		station, route := s.nearestChargingStation(s.state.Robots[0])
		if station == nil || station.ID != "S1" || len(route) != 2 {
			t.Fatalf("equal distance tie changed: %+v, route=%v", station, route)
		}
		route[0] = Point{-1, -1}
	}
	if !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatal("station selection mutated state")
	}
	slices.Reverse(s.state.Stations)
	station, _ := s.nearestChargingStation(s.state.Robots[0])
	if station.ID != "S1" {
		t.Fatal("tie depends on station storage order")
	}
	s.state.Stations[1].ID = "S10"
	station, _ = s.nearestChargingStation(s.state.Robots[0])
	if station.ID != "S2" {
		t.Fatal("station 2 must precede 10 numerically")
	}
}

func TestAutoChargeReservationsExclusive(t *testing.T) {
	s := autochargeSim(t, DefaultConfig(), Point{2, 0}, 10)
	s.state.Robots[1].X, s.state.Robots[1].Y, s.state.Robots[1].Status, s.state.Robots[1].Battery = 3, 0, RobotIdle, 10
	s.state.Robots[2].X, s.state.Robots[2].Y = 19, 0
	s.state.Stations[1].OccupiedBy = "R3"
	slices.Reverse(s.state.Robots)
	s.Tick()
	if station := s.state.Stations[0]; station.ReservedBy != "R1" || station.OccupiedBy != "" {
		t.Fatalf("lower robot did not reserve free station: %+v", station)
	}
	if r := s.state.Robots[1]; r.Status != RobotIdle || r.JobID != "" || r.X != 3 || r.Y != 0 || r.Battery != 10 {
		t.Fatalf("second robot claimed unavailable station: %+v", r)
	}
	assertInvariants(t, s, s.cfg)
	s.Tick()
	if s.state.Stations[0].ReservedBy != "" || s.state.Stations[0].OccupiedBy != "R1" || s.state.Robots[1].Status != RobotIdle {
		t.Fatal("reservation-to-occupancy handoff was not exclusive")
	}
	assertInvariants(t, s, s.cfg)
}

func TestAutoChargeTravelBatteryBoundary(t *testing.T) {
	for _, battery := range []int{5, 6, 7} {
		t.Run(fmt.Sprintf("battery_%d", battery), func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.MovingDrain = 2
			s := autochargeSim(t, cfg, Point{3, 0}, battery)
			s.Tick()
			if battery <= 6 {
				if r := s.state.Robots[0]; r.Status != RobotIdle || r.Battery != battery || r.X != 3 || s.state.Stations[0].ReservedBy != "" {
					t.Fatalf("insufficient energy started a trip: %+v", r)
				}
				return
			}
			s.Tick()
			s.Tick()
			if r := s.state.Robots[0]; r.Status != RobotCharging || r.Battery != 1 || r.X != 0 || r.Y != 0 || s.state.Stations[0].ReservedBy != "" || s.state.Stations[0].OccupiedBy != "R1" {
				t.Fatalf("strict admission must retain one battery unit: %+v", r)
			}
			s.Tick()
			if s.state.Robots[0].Battery != 1+cfg.ChargeGain {
				t.Fatal("arrival did not charge on following tick")
			}
			assertInvariants(t, s, s.cfg)
		})
	}
}

func TestAutoChargeZeroArrivalSafetyNet(t *testing.T) {
	s := autochargeSim(t, DefaultConfig(), Point{3, 0}, 7)
	s.cfg.MovingDrain = 2
	s.seekCharging(make([]bool, len(s.state.Robots)))
	if s.reservedStation("R1") == nil {
		t.Fatal("strictly feasible trip rejected")
	}
	s.state.Robots[0].Battery = 6 // A pre-existing trip can still reach its reserved station at zero.
	for tick := 0; tick < 3; tick++ {
		s.Tick()
	}
	r := s.state.Robots[0]
	if r.Status != RobotCharging || r.Battery != 0 || s.state.Stations[0].OccupiedBy != r.ID {
		t.Fatalf("zero-arrival safety net changed: %+v", r)
	}
	s.Tick()
	if s.state.Robots[0].Battery != s.cfg.ChargeGain {
		t.Fatal("zero arrival did not gain next tick")
	}
}

func TestChargeQueueOrder(t *testing.T) {
	for _, tc := range []struct {
		name               string
		battery1, battery2 int
		want               string
		firstID            string
	}{
		{name: "lowest battery", battery1: 20, battery2: 10, want: "R2"},
		{name: "numeric tie", battery1: 20, battery2: 20, want: "R1"},
		{name: "numeric two versus ten", battery1: 20, battery2: 20, want: "R2", firstID: "R10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := autochargeSim(t, DefaultConfig(), Point{2, 0}, tc.battery1)
			if tc.firstID != "" {
				s.state.Robots[0].ID = tc.firstID
			}
			s.state.Robots[1].X, s.state.Robots[1].Y, s.state.Robots[1].Status, s.state.Robots[1].Battery = 3, 0, RobotIdle, tc.battery2
			s.state.Robots[2].X, s.state.Robots[2].Y = 19, 0
			s.state.Stations[1].OccupiedBy = "R3"
			slices.Reverse(s.state.Robots)
			s.seekCharging(make([]bool, len(s.state.Robots)))
			if s.state.Stations[0].ReservedBy != tc.want {
				t.Fatalf("queue owner=%q want=%q", s.state.Stations[0].ReservedBy, tc.want)
			}
		})
	}
}

func TestChargeQueueImmediateClaims(t *testing.T) {
	s := autochargeSim(t, DefaultConfig(), Point{1, 0}, 10)
	s.state.Robots[1].X, s.state.Robots[1].Y, s.state.Robots[1].Status, s.state.Robots[1].Battery = 2, 0, RobotIdle, 11
	s.state.Robots[2].X, s.state.Robots[2].Y, s.state.Robots[2].Status, s.state.Robots[2].Battery = 3, 0, RobotIdle, 12
	s.seekCharging(make([]bool, len(s.state.Robots)))
	if s.state.Stations[0].ReservedBy != "R1" || s.state.Stations[1].ReservedBy != "" || s.state.Robots[1].Status != RobotIdle || s.state.Robots[2].Status != RobotIdle {
		t.Fatal("queue reused station or accepted unaffordable far station")
	}
}

func TestChargeStationPhysicalFreedom(t *testing.T) {
	s := autochargeSim(t, DefaultConfig(), Point{0, 0}, 15)
	if s.stationFreeFor(s.state.Stations[0], "R1") {
		t.Fatal("station occupied by candidate was declared free")
	}
	s.seekCharging(make([]bool, len(s.state.Robots)))
	if s.state.Robots[0].Status != RobotCharging || s.state.Stations[0].OccupiedBy != "R1" {
		t.Fatal("existing already-on-station charging rule changed")
	}
}

func TestChargeQueueClaimsBothStations(t *testing.T) {
	s := autochargeSim(t, DefaultConfig(), Point{1, 0}, 30)
	for i := 1; i <= 2; i++ {
		r := &s.state.Robots[i]
		r.X, r.Y, r.Status, r.Battery = i+1, 0, RobotIdle, 30+i
	}
	s.seekCharging(make([]bool, len(s.state.Robots)))
	if s.state.Stations[0].ReservedBy != "R1" || s.state.Stations[1].ReservedBy != "R2" || s.state.Robots[2].Status != RobotIdle {
		t.Fatal("station claims were not immediately visible to later candidates")
	}
	assertInvariants(t, s, s.cfg)
}

func chargeClassForTest(s *Sim, robot RobotState) string {
	station, _ := s.nearestChargingStation(robot)
	if station != nil {
		return "available"
	}
	exit := s.relocationExit(robot, Point{X: robot.X, Y: robot.Y})
	if exit >= 0 && robot.Battery-exit*s.cfg.MovingDrain >= 1 {
		return "queued"
	}
	return "stranded"
}

func TestChargeQueueStrandedVersusQueued(t *testing.T) {
	for _, battery := range []int{2, 3, 10} {
		s := autochargeSim(t, DefaultConfig(), Point{2, 0}, battery)
		s.state.Robots[1].X, s.state.Robots[1].Y = 0, 0
		s.state.Stations[0].OccupiedBy = "R2"
		s.state.Robots[2].X, s.state.Robots[2].Y = 19, 0
		s.state.Stations[1].OccupiedBy = "R3"
		before := s.Snapshot()
		want := "queued"
		if battery == 2 {
			want = "stranded"
		}
		if got := chargeClassForTest(s, s.state.Robots[0]); got != want {
			t.Fatalf("battery=%d class=%s want=%s", battery, got, want)
		}
		s.seekCharging(make([]bool, len(s.state.Robots)))
		if !reflect.DeepEqual(before, s.Snapshot()) {
			t.Fatal("refused charging request changed robot/station state")
		}
	}
}

func TestAutoChargeAlreadyOnStation(t *testing.T) {
	s := autochargeSim(t, DefaultConfig(), Point{0, 0}, 15)
	s.state.Robots[0].WaitTicks, s.state.Robots[0].Blocked = 12, true
	s.Tick()
	r, station := s.state.Robots[0], s.state.Stations[0]
	if r.Status != RobotCharging || r.Battery != 15 || r.WaitTicks != 0 || r.Blocked || station.ReservedBy != "" || station.OccupiedBy != "R1" {
		t.Fatalf("immediate charging used reservation, gain, or stale waiting: %+v %+v", r, station)
	}
	assertInvariants(t, s, s.cfg)
}

func TestAutoChargeThresholdAndDisabledDefaults(t *testing.T) {
	if DefaultConfig().AutoCharge || !WarehouseConfig().AutoCharge {
		t.Fatal("AutoCharge defaults changed incorrectly")
	}
	for _, enabled := range []bool{false, true} {
		for _, battery := range []int{39, 40} {
			s := autochargeSim(t, DefaultConfig(), Point{3, 0}, battery)
			s.cfg.AutoCharge = enabled
			s.Tick()
			want := RobotIdle
			if enabled && battery < s.cfg.ChargeBelow {
				want = RobotMoving
			}
			if s.state.Robots[0].Status != want {
				t.Fatalf("enabled=%v battery=%d: %+v", enabled, battery, s.state.Robots[0])
			}
			assertInvariants(t, s, s.cfg)
		}
	}
}

func TestAutoChargeWaitingAndReplanning(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ReplanAfter, cfg.BlockedAfter = 2, 3
	s := autochargeSim(t, cfg, Point{3, 0}, 15)
	s.seekCharging(make([]bool, len(s.state.Robots)))
	oldRoute := slices.Clone(s.navigation["R1"].route)
	s.state.Robots[1].X, s.state.Robots[1].Y = 2, 0
	for tick := 1; tick <= 2; tick++ {
		s.Tick()
		r := s.state.Robots[0]
		if r.X != 3 || r.Y != 0 || r.Battery != 15 || r.WaitTicks != tick || s.state.Stations[0].ReservedBy != "R1" {
			t.Fatalf("charging wait moved, drained, or lost reservation: %+v", r)
		}
		if tick == 1 && !slices.Equal(s.navigation["R1"].route, oldRoute) || tick == 2 && slices.Equal(s.navigation["R1"].route, oldRoute) {
			t.Fatal("charging replan did not follow threshold timing")
		}
		assertInvariants(t, s, s.cfg)
	}
	s.Tick()
	if r := s.state.Robots[0]; r.X != 3 || r.Y != 1 || r.Battery != 14 || r.WaitTicks != 0 || r.Blocked {
		t.Fatalf("replacement charging route not used on following tick: %+v", r)
	}
	assertInvariants(t, s, s.cfg)
}

func TestAutoChargeBlockedTripRetainsReservation(t *testing.T) {
	s := autochargeSim(t, DefaultConfig(), Point{3, 0}, 15)
	s.seekCharging(make([]bool, len(s.state.Robots)))
	s.state.Robots[1].X, s.state.Robots[1].Y = 2, 0
	// Park on the access cells, preserving an unreachable reserved station
	// without placing a different robot on the reservation itself (I20).
	s.state.Robots[2].X, s.state.Robots[2].Y = 1, 0
	s.state.Robots[3].X, s.state.Robots[3].Y = 0, 1
	for tick := 1; tick <= s.cfg.BlockedAfter; tick++ {
		s.Tick()
		r := s.state.Robots[0]
		if r.Battery != 15 || r.WaitTicks != tick || r.Blocked != (tick >= s.cfg.BlockedAfter) || s.state.Stations[0].ReservedBy != "R1" {
			t.Fatalf("blocked charge trip corrupted state: %+v", r)
		}
		assertInvariants(t, s, s.cfg)
	}
	s.unresponsiveTicks["R1"] = 1
	s.Tick()
	if r := s.state.Robots[0]; r.Status != RobotOffline || r.WaitTicks != 0 || r.Blocked || r.Battery != 15 || s.state.Stations[0].ReservedBy != "" {
		t.Fatalf("offline transition did not clear blocked reservation: %+v", r)
	}
	assertInvariants(t, s, s.cfg)
}

func TestAutoChargePhysicalAndUnreachableStations(t *testing.T) {
	for _, unreachable := range []bool{false, true} {
		t.Run(fmt.Sprintf("unreachable_%v", unreachable), func(t *testing.T) {
			s := autochargeSim(t, DefaultConfig(), Point{2, 1}, 25)
			s.state.Robots[3].X, s.state.Robots[3].Y = 19, 0
			s.state.Stations[1].OccupiedBy = "R4"
			if unreachable {
				s.state.Robots[1].X, s.state.Robots[1].Y = 1, 0
				s.state.Robots[2].X, s.state.Robots[2].Y = 0, 1
			} else {
				s.state.Robots[1].X, s.state.Robots[1].Y, s.state.Robots[1].Status = 0, 0, RobotIdle
			}
			s.Tick()
			if r := s.state.Robots[0]; r.Status != RobotIdle || r.Battery != 25 || r.X != 2 || r.Y != 1 || s.state.Stations[0].ReservedBy != "" {
				t.Fatalf("unavailable station started a trip: %+v", r)
			}
			assertInvariants(t, s, s.cfg)
		})
	}
}

func TestAutoChargeOfflineReleasesReservation(t *testing.T) {
	s := autochargeSim(t, DefaultConfig(), Point{8, 0}, 15)
	s.Tick()
	requireInjection(t, s.InjectUnresponsive("R1"))
	for tick := 2; tick <= 4; tick++ {
		s.Tick()
		assertInvariants(t, s, s.cfg)
	}
	r := s.state.Robots[0]
	if r.Status != RobotOffline || r.X != 5 || r.Y != 0 || r.Battery != 12 || r.WaitTicks != 0 || r.Blocked || s.state.Stations[0].ReservedBy != "" || s.state.Stations[0].OccupiedBy != "" || len(s.state.Faults) != 1 {
		t.Fatalf("offline trip retained reservation or moved on expiry: %+v", r)
	}
}

func TestAutoChargeFaultReleasesReservation(t *testing.T) {
	for _, level := range []int{0, 1} {
		t.Run(fmt.Sprintf("level_%d", level), func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.LowBatteryInjectLevel = level
			s := autochargeSim(t, cfg, Point{8, 0}, 15)
			s.Tick()
			requireInjection(t, s.InjectLowBattery("R1"))
			s.Tick()
			r := s.state.Robots[0]
			if r.Status != RobotFaulted || r.Battery != 0 || r.X != 7-level || r.Y != 0 || r.WaitTicks != 0 || r.Blocked || s.state.Stations[0].ReservedBy != "" || s.state.Stations[0].OccupiedBy != "" || len(s.state.Faults) != 1 {
				t.Fatalf("faulted trip retained reservation: %+v", r)
			}
			assertInvariants(t, s, s.cfg)
		})
	}
}

func TestAutoChargeOfflineOccupantRetainsStation(t *testing.T) {
	s := autochargeSim(t, DefaultConfig(), Point{0, 0}, 15)
	s.Tick()
	requireInjection(t, s.InjectUnresponsive("R1"))
	for tick := 2; tick <= 20; tick++ {
		s.Tick()
		if tick >= 4 {
			r := s.state.Robots[0]
			if r.Status != RobotOffline || r.Battery != 19 || s.state.Stations[0].OccupiedBy != "R1" || s.state.Stations[0].ReservedBy != "" || len(s.state.Faults) != 1 {
				t.Fatalf("offline charging occupant lost its station: %+v", r)
			}
		}
		assertInvariants(t, s, s.cfg)
	}
}

func TestAutoChargeLowBatteryInjection(t *testing.T) {
	for _, holdingJob := range []bool{false, true} {
		t.Run(fmt.Sprintf("holding_job_%v", holdingJob), func(t *testing.T) {
			s := autochargeSim(t, DefaultConfig(), Point{3, 0}, 100)
			if holdingJob {
				s.state.Robots[0].Status, s.state.Robots[0].JobID = RobotMoving, "J1"
				s.state.Jobs = []JobState{{ID: "J1", From: Point{3, 1}, To: Point{4, 1}, Status: JobAssigned, AssignedRobot: "R1"}}
			}
			requireInjection(t, s.InjectLowBattery("R1"))
			s.Tick()
			r := s.state.Robots[0]
			if holdingJob {
				if r.JobID != "J1" || r.X != 3 || r.Y != 1 || r.Battery != 14 || s.state.Stations[0].ReservedBy != "" {
					t.Fatalf("holding job was redirected to charging: %+v", r)
				}
			} else if r.JobID != "" || r.X != 2 || r.Y != 0 || r.Battery != 14 || s.state.Stations[0].ReservedBy != "R1" {
				t.Fatalf("idle injection did not start a trip: %+v", r)
			}
			if len(s.state.Faults) != 1 {
				t.Fatal("automatic transition created extra fault history")
			}
			assertInvariants(t, s, s.cfg)
		})
	}
}

func TestAutoChargeJobPriorityAndCompletionSkip(t *testing.T) {
	s := autochargeSim(t, DefaultConfig(), Point{0, 0}, 35)
	s.cfg.AutoDispatch = true
	s.state.Jobs = []JobState{{ID: "J1", From: Point{1, 0}, To: Point{2, 0}, Status: JobPending}}
	s.Tick()
	if r := s.state.Robots[0]; r.JobID != "J1" || r.X != 1 || r.Battery != 34 || s.state.Stations[0].OccupiedBy != "" || s.state.Stations[0].ReservedBy != "" {
		t.Fatalf("charging displaced a feasible job: %+v", r)
	}
	s = autochargeSim(t, DefaultConfig(), Point{0, 0}, 99)
	s.cfg.AutoDispatch = true
	s.state.Jobs = []JobState{{ID: "J1", From: Point{1, 0}, To: Point{2, 0}, Status: JobPending}}
	if err := s.SetCharging("R1", "S1"); err != nil {
		t.Fatal(err)
	}
	s.state.Tick = 19
	s.Tick()
	if r := s.state.Robots[0]; r.Status != RobotIdle || r.JobID != "" || r.Battery != 100 || r.X != 0 || s.state.Jobs[0].Status != JobPending {
		t.Fatalf("charging completion did more than release: %+v", r)
	}
	s.Tick()
	if r := s.state.Robots[0]; r.JobID != "J1" || r.Battery != 99 || r.X != 1 {
		t.Fatalf("released robot did not dispatch next tick: %+v", r)
	}
	assertInvariants(t, s, s.cfg)
}

func TestAutoChargeActiveTripRejectsJobs(t *testing.T) {
	s := autochargeSim(t, DefaultConfig(), Point{8, 0}, 35)
	s.Tick()
	s.cfg.AutoDispatch = true
	s.state.Jobs = []JobState{{ID: "J1", From: Point{7, 0}, To: Point{6, 0}, Status: JobPending}}
	s.Tick()
	if r := s.state.Robots[0]; r.JobID != "" || r.X != 6 || r.Battery != 33 || s.state.Jobs[0].Status != JobPending || s.state.Stations[0].ReservedBy != "R1" {
		t.Fatalf("active charging trip accepted delivery work: %+v", r)
	}
	assertInvariants(t, s, s.cfg)
}

func TestAutoChargeWarehouseSeedMatrix(t *testing.T) {
	for _, seed := range []int64{1, 2, 3, 42, 99} {
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			cfg := WarehouseConfig()
			s, repeat := newTestSim(t, cfg, seed), newTestSim(t, cfg, seed)
			for tick := 0; tick <= 500; tick++ {
				assertInvariants(t, s, cfg)
				if !reflect.DeepEqual(s.Snapshot(), repeat.Snapshot()) || len(s.state.Faults) != 0 {
					t.Fatalf("non-deterministic charging or routine fault at tick %d", tick)
				}
				if tick < 500 {
					s.Tick()
					repeat.Tick()
				}
			}
		})
	}
}

func TestAutoChargeWarehouseLifecycleOutcomes(t *testing.T) {
	cfg := WarehouseConfig()
	s := newTestSim(t, cfg, 42)
	arrivals, completions := 0, 0
	for tick := 1; tick <= 300; tick++ {
		before := s.Snapshot()
		s.Tick()
		for i, robot := range s.state.Robots {
			old := before.Robots[i]
			if robot.Status == RobotCharging && old.Status != RobotCharging {
				arrivals++
			}
			if old.Status == RobotCharging && robot.Status == RobotIdle {
				completions++
				if robot.Battery != 100 {
					t.Fatal("charge cycle completed below 100")
				}
			}
			if robot.Status == RobotFaulted && old.Status != RobotFaulted {
				t.Logf("tick %d: %s faulted from %s, position=(%d,%d), job=%q", tick, robot.ID, old.Status, robot.X, robot.Y, old.JobID)
			}
		}
		assertInvariants(t, s, cfg)
	}
	charging := 0
	for _, robot := range s.state.Robots {
		if robot.Status == RobotCharging {
			charging++
		}
	}
	if arrivals == 0 || arrivals != completions+charging || len(s.state.Faults) != 0 {
		t.Fatalf("warehouse cycles: arrivals=%d completions=%d faults=%v", arrivals, completions, s.state.Faults)
	}
	t.Logf("warehouse seed 42 / 300 ticks: %d charging arrivals, %d completions", arrivals, completions)
}

func TestAutoChargeReservationInvariantViolations(t *testing.T) {
	for _, tc := range []struct {
		name, invariant string
		corrupt         func(*State, *Config)
	}{
		{name: "missing trip", invariant: "I6:", corrupt: func(s *State, _ *Config) { s.Stations[0].ReservedBy = "" }},
		{name: "unknown owner", invariant: "I16:", corrupt: func(s *State, _ *Config) { s.Stations[0].ReservedBy = "R99" }},
		{name: "idle owner", invariant: "I16:", corrupt: func(s *State, _ *Config) { s.Robots[0].Status = RobotIdle }},
		{name: "duplicate owner", invariant: "I16:", corrupt: func(s *State, _ *Config) { s.Stations[1].ReservedBy = "R1" }},
		{name: "occupied and reserved", invariant: "I16:", corrupt: func(s *State, _ *Config) { s.Stations[0].OccupiedBy = "R2" }},
		{name: "disabled charging trip", invariant: "I6:", corrupt: func(_ *State, cfg *Config) { cfg.AutoCharge = false }},
		{name: "job holder owns reservation", invariant: "I16:", corrupt: func(s *State, _ *Config) {
			s.Robots[0].JobID = "J1"
			s.Jobs = []JobState{{ID: "J1", From: Point{3, 1}, To: Point{4, 1}, Status: JobAssigned, AssignedRobot: "R1"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := autochargeSim(t, DefaultConfig(), Point{3, 0}, 15)
			s.Tick()
			state, cfg := s.Snapshot(), s.cfg.Clone()
			tc.corrupt(&state, &cfg)
			if err := checkInvariants(state, cfg); err == nil || !strings.HasPrefix(err.Error(), tc.invariant) {
				t.Fatalf("invariant = %v, want %s", err, tc.invariant)
			}
		})
	}
}
