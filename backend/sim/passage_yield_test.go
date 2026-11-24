package sim

import "testing"

func TestWarehouseV2PassageYieldReservesOnlyAFeasibleExit(t *testing.T) {
	for _, name := range []string{"clear", "occupied refuge", "insufficient energy"} {
		t.Run(name, func(t *testing.T) {
			s := recoverySim(t, WarehouseV2Config(), []Point{{11, 1}, {12, 1}}, []Point{{12, 1}, {1, 7}},
				[][]Point{{{12, 1}}, {{11, 1}, {10, 1}, {9, 1}, {8, 1}}})
			robot, peer := &s.state.Robots[0], &s.state.Robots[1]
			s.state.Robots[2].X, s.state.Robots[2].Y = 11, 0
			s.state.Robots[3].X, s.state.Robots[3].Y = 13, 1
			passage, mouth, refuge := Point{10, 1}, Point{9, 1}, Point{9, 2}
			if name == "occupied refuge" {
				s.state.Robots[4].X, s.state.Robots[4].Y = refuge.X, refuge.Y
				s.state.Robots[5].X, s.state.Robots[5].Y = 8, 1
			}
			if name == "insufficient energy" {
				robot.Battery = 3
			}
			before := *robot
			s.traffic = newTrafficReservations(s.state.Robots)
			event := s.recoverCycle([]int{0, 1}, make([]bool, len(s.state.Robots)))
			if name != "clear" {
				if event.robot != "" || *robot != before || s.exitClaim(*peer, mouth) >= 0 {
					t.Fatalf("unfeasible retreat changed state: %+v, %+v", event, robot)
				}
				return
			}
			if event.robot != robot.ID || (Point{robot.X, robot.Y}) != passage ||
				robot.JobID != before.JobID || robot.Battery != before.Battery-s.cfg.MovingDrain {
				t.Fatalf("expected one funded physical step with the original job: %+v, %+v", event, robot)
			}
			if s.exitClaim(*peer, mouth) != 0 || s.exitClaim(*peer, refuge) != 0 {
				t.Fatal("retreat failed to claim its exit and refuge")
			}
			if s.exitClaim(*peer, Point{9, 9}) != 0 {
				t.Fatal("opposing traffic could enter the refuge aisle")
			}
			s.clearNavigation(robot)
			if s.exitClaim(*peer, mouth) >= 0 || s.exitClaim(*peer, refuge) >= 0 {
				t.Fatal("clearing navigation held the cancelled retreat's reservations")
			}
		})
	}
}

func TestPhase4Seed2OpposingDeliveriesMakeProgress(t *testing.T) {
	cfg := WarehouseV2Config()
	cfg.ChargeRelease = *phase4StressTarget
	cfg.ScheduledBreakdowns = []ScheduledBreakdown{
		{48, "R1", FaultMotorFailure}, {48, "R4", FaultSensorFailure},
		{200, "R5", FaultRobotUnresponsive}, {600, "R8", FaultSensorFailure}, {1300, "R9", FaultCriticalBattery},
	}
	s := newTestSim(t, cfg, 2)
	var lastMove [2]int
	completed := map[string]int{}
	for range 3000 {
		before := s.Snapshot()
		s.Tick()
		after := s.Snapshot()
		assertInvariants(t, s, cfg)
		assertV2TrafficStep(t, before, after)
		checkMaintenanceSoakMovement(t, before, after, cfg)
		for k, i := range [2]int{2, 6} {
			old, robot := before.Robots[i], after.Robots[i]
			if old.X != robot.X || old.Y != robot.Y {
				lastMove[k] = after.Tick
			}
		}
		for _, job := range after.Jobs {
			if job.Status == JobDone && job.CompletedTick == after.Tick && (job.ID == "J130" || job.ID == "J151") {
				completed[job.ID] = after.Tick
			}
		}
	}
	if lastMove[0] <= 1983 || lastMove[1] <= 1983 || len(completed) != 2 || len(s.state.TrafficDeadlocks) != 0 {
		t.Fatalf("head-on encounter did not clear: last R3/R7 moves %v, delivered jobs %v, active cycles %+v",
			lastMove, completed, s.state.TrafficDeadlocks)
	}
	t.Logf("last R3/R7 moves %v, delivered jobs %v, aggregate deliveries %d", lastMove, completed, s.state.CompletedJobs)
}

func TestPhase4ChargingSharedPassageDelivers(t *testing.T) {
	cfg := WarehouseV2Config()
	for _, workload := range phase4StressWorkloads() {
		if workload.name == "charging-parking" {
			workload.configure(&cfg)
		}
	}
	s := newTestSim(t, cfg, 2)
	delivered := map[string]int{}
	for range 400 {
		before := s.Snapshot()
		s.Tick()
		after := s.Snapshot()
		assertInvariants(t, s, cfg)
		assertV2TrafficStep(t, before, after)
		for _, job := range after.Jobs {
			if job.Status == JobDone && (job.ID == "J1" || job.ID == "J3") {
				delivered[job.ID] = job.CompletedTick
			}
		}
	}
	if len(delivered) != 2 {
		t.Fatalf("opposing charging-workload deliveries did not clear: %v", delivered)
	}
	t.Logf("opposing charging workload completed J1 and J3: %v", delivered)
}
