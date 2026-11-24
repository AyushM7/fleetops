package sim

import (
	"flag"
	"fmt"
	"reflect"
	"slices"
	"testing"
)

var runPhase4Soak = flag.Bool("phase4-soak", false, "run the extended V2 maintenance soak")

func TestPhase4MaintenanceSoak(t *testing.T) {
	if !*runPhase4Soak {
		t.Skip("requires -phase4-soak")
	}
	const ticks = 3000
	cfg := WarehouseV2Config()
	cfg.ScheduledBreakdowns = []ScheduledBreakdown{
		{48, "R1", FaultMotorFailure},
		{48, "R4", FaultSensorFailure},
		{200, "R5", FaultRobotUnresponsive},
		{600, "R8", FaultSensorFailure},
		{1300, "R9", FaultCriticalBattery},
	}
	for _, seed := range []int64{1, 2, 3, 42, 99} {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			s, replay := newTestSim(t, cfg, seed), newTestSim(t, cfg, seed)
			previous := s.Snapshot()
			attempted := make(map[string]bool)
			charged, maxWait, attempts := 0, 0, 0
			var waitPeak string
			outcomes := make(map[MaintenanceOutcome]int)
			for range ticks {
				s.Tick()
				replay.Tick()
				state := s.Snapshot()
				assertInvariants(t, s, cfg)
				if !reflect.DeepEqual(state, replay.Snapshot()) {
					t.Fatalf("seed %d replay diverged at tick %d", seed, state.Tick)
				}
				checkMaintenanceSoakMovement(t, previous, state, cfg)
				for _, robot := range state.Robots {
					if robot.WaitTicks > maxWait {
						maxWait = robot.WaitTicks
						waitPeak = fmt.Sprintf("%s at tick %d (%s: %s)", robot.ID, state.Tick,
							classifySoakWait(s, robot), waitingCause(s, robot, make(map[string]bool)))
					}
					if robot.ID == MaintenanceRobotID && robot.Status == RobotCharging {
						charged++
					}
				}
				if work := state.Maintenance; work != nil {
					var oldWork MaintenanceState
					if previous.Maintenance != nil {
						oldWork = *previous.Maintenance
					}
					if len(work.History) < len(oldWork.History) || !slices.Equal(oldWork.History, work.History[:len(oldWork.History)]) {
						t.Fatalf("tick %d changed maintenance history", state.Tick)
					}
					for _, attempt := range work.History[len(oldWork.History):] {
						target := s.faultRobot(attempt.RobotID)
						if attempt.FinishedTick != state.Tick || attempt.Outcome == MaintenanceCompleted && target.ActiveFault != "" {
							t.Fatalf("tick %d invalid outcome: %+v for %+v", state.Tick, attempt, target)
						}
						outcomes[attempt.Outcome]++
					}
					if work.Active != nil {
						attempt := work.Active
						if oldWork.Active == nil {
							if attempted[attempt.FaultID] {
								t.Fatalf("tick %d duplicated attempt for %s", state.Tick, attempt.FaultID)
							}
							attempted[attempt.FaultID] = true
							attempts++
						} else {
							checkMaintenanceSoakRepair(t, previous, state, *oldWork.Active, *attempt, s)
						}
						maxAttemptTicks := 2*cfg.Width*cfg.Height + 2*s.maintenanceRetryTicks() + cfg.MaintenanceRepairTicks + 3
						if state.Tick-attempt.StartedTick > maxAttemptTicks || attempts > len(cfg.ScheduledBreakdowns) {
							t.Fatalf("tick %d exceeded dispatch bounds: %+v", state.Tick, attempt)
						}
					}
				}
				previous = state
			}
			t.Logf("seed=%d ticks=0–%d faults=%d attempts=%d outcomes=%v M1 charging ticks=%d deliveries=%d max wait=%d (%s)",
				seed, ticks, len(previous.Faults), attempts, outcomes, charged, previous.CompletedJobs, maxWait, waitPeak)
		})
	}
}

func checkMaintenanceSoakRepair(t *testing.T, previous, state State, prior, attempt MaintenanceAttempt, s *Sim) {
	t.Helper()
	if attempt.ID != prior.ID || attempt.FaultID != prior.FaultID || attempt.JobID != prior.JobID {
		t.Fatalf("tick %d rewrote the active attempt: %+v -> %+v", state.Tick, prior, attempt)
	}
	if attempt.RepairTicks == prior.RepairTicks {
		return
	}
	m1 := state.Robots[len(state.Robots)-1]
	oldM1 := previous.Robots[len(previous.Robots)-1]
	site := attempt.ServiceCell
	neighbors := orthogonalNeighbors(attempt.TargetCell)
	if attempt.RepairTicks != prior.RepairTicks+1 || oldM1.Battery-m1.Battery != 1 ||
		(Point{m1.X, m1.Y}) != site || (Point{oldM1.X, oldM1.Y}) != site ||
		!slices.Contains(neighbors[:], site) ||
		s.cfg.CellTypeAt(site) != CellFree || s.protected[site] || s.cfg.narrowPassage(site) {
		t.Fatalf("tick %d performed nonphysical or unsafe repair: %+v, M1 %+v -> %+v", state.Tick, attempt, oldM1, m1)
	}
}

func checkMaintenanceSoakMovement(t *testing.T, previous, state State, cfg Config) {
	t.Helper()
	robots := make(map[string]RobotState, len(state.Robots))
	for index, robot := range state.Robots {
		robots[robot.ID] = robot
		prior := previous.Robots[index]
		dx, dy := robot.X-prior.X, robot.Y-prior.Y
		if dx*dx+dy*dy > 1 || prior.ActiveFault != "" && (dx != 0 || dy != 0 || robot.Battery != prior.Battery || robot.JobID != prior.JobID) {
			t.Fatalf("tick %d nonphysical motion or fault state: %+v -> %+v", state.Tick, prior, robot)
		}
		if robot.Battery > prior.Battery && (prior.Status != RobotCharging || robot.Battery-prior.Battery > cfg.ChargeGain) {
			t.Fatalf("tick %d nonphysical energy gain: %+v -> %+v", state.Tick, prior, robot)
		}
		if robot.ID == MaintenanceRobotID && robot.JobID != "" {
			t.Fatalf("tick %d assigned a job to M1: %+v", state.Tick, robot)
		}
	}
	oldJobs := make(map[string]JobState, len(previous.Jobs))
	for _, job := range previous.Jobs {
		oldJobs[job.ID] = job
	}
	for _, job := range state.Jobs {
		prior := oldJobs[job.ID]
		robot := robots[job.AssignedRobot]
		position := Point{robot.X, robot.Y}
		switch {
		case job.Status == JobInProgress:
			if job.CargoRobotID != job.AssignedRobot || prior.CargoRobotID != "" && prior.CargoRobotID != job.CargoRobotID ||
				prior.Status != JobInProgress && position != job.From {
				t.Fatalf("tick %d nonphysical pickup or custody: %+v -> %+v", state.Tick, prior, job)
			}
		case job.Status == JobDone && job.CompletedTick == state.Tick:
			if prior.CargoRobotID != job.AssignedRobot || position != job.To || job.CargoRobotID != "" {
				t.Fatalf("tick %d nonphysical delivery: %+v -> %+v", state.Tick, prior, job)
			}
		}
	}
}

func TestPhase4RecoveryKeepsTheRobotAtPhysicalPickup(t *testing.T) {
	cfg := WarehouseV2Config()
	s := newTestSim(t, cfg, 2)
	for range 1200 {
		before := s.Snapshot()
		s.Tick()
		after := s.Snapshot()
		checkMaintenanceSoakMovement(t, before, after, cfg)
	}
}
