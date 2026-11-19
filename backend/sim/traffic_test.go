package sim

import (
	"fmt"
	"slices"
	"testing"
)

func TestWarehouseV2ReservesCellsAndEdges(t *testing.T) {
	a := RobotState{ID: "R1", X: 1, Y: 1}
	b := RobotState{ID: "R2", X: 2, Y: 1}
	c := RobotState{ID: "R3", X: 3, Y: 1}
	faulted := RobotState{ID: "R4", X: 2, Y: 0, Status: RobotFaulted}
	s := &Sim{traffic: newTrafficReservations([]RobotState{a, b, c, faulted})}
	if s.reserveMove(a, Point{2, 1}) || s.reserveMove(b, Point{1, 1}) || s.reserveMove(b, Point{2, 0}) {
		t.Fatal("head-on or faulted-cell move was admitted")
	}
	if !s.reserveMove(b, Point{2, 2}) || !s.reserveMove(a, Point{2, 1}) {
		t.Fatal("following a robot into its vacated cell should be safe")
	}
	a.X = 2
	if s.reserveMove(a, Point{1, 1}) || s.reserveMove(c, Point{2, 1}) {
		t.Fatal("a second move or a destination already reserved this tick was admitted")
	}
}

func TestWarehouseV2TrafficPriorityAgesAndRotates(t *testing.T) {
	s := newTestSim(t, WarehouseV2Config(), 42)
	s.traffic = newTrafficReservations(s.state.Robots)
	for tick := 1; tick <= len(s.state.Robots); tick++ {
		s.state.Tick = tick
		if first := s.movementOrder()[0]; first != tick-1 {
			t.Fatalf("tick %d: expected rotation to %d, got %d", tick, tick-1, first)
		}
	}
	s.state.Robots[3].WaitTicks = 2
	if first := s.movementOrder()[0]; first != 3 {
		t.Fatalf("expected the longest waiting robot first, got %d", first)
	}
}

func TestWarehouseV2DetectsAndClearsAisleMouthDeadlock(t *testing.T) {
	s := recoverySim(t, WarehouseV2Config(), []Point{{4, 2}, {4, 1}}, []Point{{3, 1}, {4, 3}}, [][]Point{{{4, 1}, {3, 1}}, {{4, 2}, {4, 3}}})
	s.Tick()
	snapshot := s.Snapshot()
	if len(snapshot.TrafficDeadlocks) != 1 || !slices.Equal(snapshot.TrafficDeadlocks[0].Robots, []string{"R1", "R2"}) || snapshot.TrafficDeadlocks[0].YieldingRobot != "R2" {
		t.Fatalf("expected a detected cycle with a safe yielding robot: %+v", snapshot.TrafficDeadlocks)
	}
	snapshot.TrafficDeadlocks[0].Robots[0] = "mutated"
	if s.Snapshot().TrafficDeadlocks[0].Robots[0] != "R1" {
		t.Fatal("snapshot aliased live deadlock data")
	}
	for range 25 {
		before := s.Snapshot()
		s.Tick()
		assertInvariants(t, s, s.cfg)
		assertV2TrafficStep(t, before, s.Snapshot())
	}
	if s.Snapshot().CompletedJobs != 2 {
		t.Fatalf("cycle did not clear, robots: %+v", s.Snapshot().Robots)
	}
}

func TestWarehouseV2CongestionSafety(t *testing.T) {
	for _, seed := range []int64{1, 42, 99} {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			s := newTestSim(t, WarehouseV2Config(), seed)
			for range 1000 {
				before := s.Snapshot()
				s.Tick()
				assertInvariants(t, s, s.cfg)
				assertV2TrafficStep(t, before, s.Snapshot())
			}
			if s.Snapshot().CompletedJobs < 40 {
				t.Fatalf("fleet made insufficient progress under congestion: %d completions; dependencies %v; routes %v", s.Snapshot().CompletedJobs, s.recoveryBlockers(s.recoveryOrder()), s.navigation)
			}
		})
	}
}

func assertV2TrafficStep(t *testing.T, before, after State) {
	t.Helper()
	edges := make(map[trafficEdge]string, len(after.Robots))
	for i, old := range before.Robots {
		robot := after.Robots[i]
		from, to := Point{old.X, old.Y}, Point{robot.X, robot.Y}
		if from == to {
			continue
		}
		dx, dy := from.X-to.X, from.Y-to.Y
		if dx*dx+dy*dy != 1 {
			t.Fatalf("tick %d: %s moved more than one cell from %v to %v", after.Tick, robot.ID, from, to)
		}
		if other := edges[trafficEdge{to, from}]; other != "" {
			t.Fatalf("tick %d: %s and %s swapped cells", after.Tick, robot.ID, other)
		}
		edges[trafficEdge{from, to}] = robot.ID
	}
}
