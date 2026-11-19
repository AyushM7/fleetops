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

func TestWarehouseV2KeepsAisleMouthClearForExit(t *testing.T) {
	s := newTestSim(t, WarehouseV2Config(), 42)
	r1, r2 := &s.state.Robots[0], &s.state.Robots[1]
	r1.X, r1.Y = 13, 1
	r2.X, r2.Y = 14, 2
	if blocker := s.crossingBlocker(*r1, []Point{{14, 1}, {14, 2}}); blocker != 1 {
		t.Fatalf("should wait outside an occupied aisle mouth, blocker = %d", blocker)
	}
	r1.X, r1.Y = 14, 10
	r2.X, r2.Y = 14, 1
	route := []Point{{14, 9}, {14, 8}, {14, 7}, {14, 6}, {14, 5}, {14, 4}, {14, 3}, {14, 2}, {14, 1}}
	if blocker := s.crossingBlocker(*r1, route); blocker != 1 {
		t.Fatalf("should wait before an aisle with an occupied planned exit, blocker = %d", blocker)
	}
}

func TestWarehouseV2NarrowPassageKeepsItsExitAndHasNoJobStops(t *testing.T) {
	cfg := WarehouseV2Config()
	s := newTestSim(t, cfg, 42)
	passage, exit := Point{10, 1}, Point{11, 1}
	if slices.Contains(s.jobCells, passage) || !slices.Contains(cfg.JobCells(), passage) {
		t.Fatal("narrow passage should be a transit cell but never a job stop")
	}
	r1, r2 := &s.state.Robots[0], &s.state.Robots[1]
	r1.X, r1.Y, r1.Status = passage.X, passage.Y, RobotMoving
	r2.X, r2.Y = 12, 1
	s.navigation[r1.ID] = robotNavigation{target: exit, route: []Point{exit}}
	s.traffic = newTrafficReservations(s.state.Robots)
	if s.reserveMove(*r2, exit) || !s.reserveMove(*r1, exit) {
		t.Fatal("another robot can take the exit from a robot inside the narrow passage")
	}
}

func TestWarehouseV2ProtectsChargerPocketExit(t *testing.T) {
	s := newTestSim(t, WarehouseV2Config(), 3)
	charger, pocket, exit := Point{12, 0}, Point{11, 0}, Point{11, 1}
	arriving, departing := &s.state.Robots[2], &s.state.Robots[4]
	arriving.X, arriving.Y, arriving.Status = exit.X, exit.Y, RobotMoving
	departing.X, departing.Y, departing.Status = charger.X, charger.Y, RobotMoving
	s.navigation[departing.ID] = robotNavigation{target: exit, route: []Point{pocket, exit}, relocation: true, undocking: true}
	if blocker := s.crossingBlocker(*departing, []Point{pocket, exit}); blocker != 2 {
		t.Errorf("undocking into a pocket with an occupied exit was admitted, blocker = %d", blocker)
	}
	departing.X, departing.Y = pocket.X, pocket.Y
	arriving.X, arriving.Y = 12, 1
	s.navigation[departing.ID] = robotNavigation{target: exit, route: []Point{exit}, relocation: true, undocking: true}
	s.traffic = newTrafficReservations(s.state.Robots)
	if s.reserveMove(*arriving, exit) {
		t.Error("arriving robot took the departing robot's only exit")
	}
	if !s.reserveMove(*departing, exit) {
		t.Error("departing robot could not take its reserved exit")
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

func TestWarehouseV2CanYieldWithoutCompletingAnUnaffordableParkingTrip(t *testing.T) {
	cfg := WarehouseV2Config()
	s := newTestSim(t, cfg, 42)
	robot := &s.state.Robots[0]
	robot.X, robot.Y, robot.Status, robot.Battery = 11, 1, RobotMoving, 4
	s.navigation[robot.ID] = robotNavigation{target: cfg.RobotStarts[0], relocation: true}
	s.traffic = newTrafficReservations(s.state.Robots)
	if !s.retreatRobot(robot, []int{0}) {
		t.Fatal("parking robot could not yield despite an affordable charger exit")
	}
	if robot.Status != RobotIdle || (Point{robot.X, robot.Y}) != (Point{12, 1}) || robot.Battery != 3 {
		t.Fatalf("parking should yield one cell and preserve energy for a charger: %+v", robot)
	}
	if _, exists := s.navigation[robot.ID]; exists {
		t.Fatal("yielding robot kept its abandoned parking route")
	}
}

func TestWarehouseV2CongestionSafety(t *testing.T) {
	for _, seed := range []int64{1, 42, 99} {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			s := newTestSim(t, WarehouseV2Config(), seed)
			lastCompleted := 0
			for tick := 1; tick <= 2500; tick++ {
				before := s.Snapshot()
				s.Tick()
				assertInvariants(t, s, s.cfg)
				assertV2TrafficStep(t, before, s.Snapshot())
				if tick%500 == 0 {
					if s.state.CompletedJobs-lastCompleted < 10 {
						t.Fatalf("only %d deliveries in ticks %d–%d; dependencies %v; routes %v", s.state.CompletedJobs-lastCompleted, tick-499, tick, s.recoveryBlockers(s.recoveryOrder()), s.navigation)
					}
					lastCompleted = s.state.CompletedJobs
				}
			}
		})
	}
}

func TestWarehouseV2SustainsDeliveriesAfterCongestionIntegration(t *testing.T) {
	for _, seed := range []int64{1, 2, 3, 42, 99} {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			s := newTestSim(t, WarehouseV2Config(), seed)
			lastCompleted := 0
			for tick := 1; tick <= 5000; tick++ {
				s.Tick()
				r5 := s.state.Robots[4]
				if seed == 3 && (Point{r5.X, r5.Y}) == (Point{11, 0}) && r5.WaitTicks > 4*s.cfg.RecoverAfter {
					t.Fatalf("tick %d: R5 trapped leaving C3 after %d ticks at %v, route %+v, cycles %+v", tick, r5.WaitTicks,
						Point{r5.X, r5.Y}, s.navigation[r5.ID], s.state.TrafficDeadlocks)
				}
				for _, robot := range s.state.Robots {
					if robot.Status == RobotFaulted {
						t.Fatalf("tick %d: %s depleted its battery", tick, robot.ID)
					}
				}
				if tick%500 != 0 {
					continue
				}
				if s.state.CompletedJobs == lastCompleted {
					t.Fatalf("no deliveries in ticks %d–%d; robots %+v; dependencies %v; routes %v", tick-499, tick, s.Snapshot().Robots, s.recoveryBlockers(s.recoveryOrder()), s.navigation)
				}
				lastCompleted = s.state.CompletedJobs
			}
			longestWait := 0
			for _, robot := range s.state.Robots {
				longestWait = max(longestWait, robot.WaitTicks)
			}
			t.Logf("%d ticks, %d completed deliveries, longest current wait %d ticks", s.state.Tick, s.state.CompletedJobs, longestWait)
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
