package sim

import (
	"reflect"
	"testing"
)

// Private fixture: the offline robot occupies the only exit from the source's
// pocket. Automatic navigation, charging, dispatch, and recovery remain enabled.
func reassignmentScenario(t *testing.T) *Sim {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Width, cfg.Height = 8, 8
	cfg.AisleColumns, cfg.AisleYMin, cfg.AisleYMax = []int{6}, 1, 2
	cfg.Stations = []Point{{7, 0}}
	cfg.RobotStarts = []Point{{0, 3}, {2, 3}, {4, 3}, {7, 7}, {5, 7}}
	cfg.InitialJobs, cfg.MaxJobs, cfg.NewJobEvery = 0, 1, 10000
	cfg.AutoDispatch, cfg.AutoCharge, cfg.Recovery = true, true, true
	for y := 0; y < cfg.Height; y++ {
		if y != 3 {
			cfg.Walls = append(cfg.Walls, Point{2, y})
		}
	}
	s := newTestSim(t, cfg, 42)
	s.state.Robots[0].Status, s.state.Robots[0].JobID = RobotMoving, "J1"
	s.state.Robots[1].Status = RobotOffline
	s.state.Jobs = []JobState{{ID: "J1", From: Point{4, 4}, To: Point{6, 4}, Status: JobAssigned, AssignedRobot: "R1"}}
	assertInvariants(t, s, cfg)
	return s
}

func advanceReassignmentScenario(t *testing.T, s *Sim, ticks int) {
	t.Helper()
	for range ticks {
		s.Tick()
		assertInvariants(t, s, s.cfg)
	}
}

func TestReassignmentRecoveryScenarioProof(t *testing.T) {
	baseline, recovered := reassignmentScenario(t), reassignmentScenario(t)
	advanceReassignmentScenario(t, baseline, 42)
	advanceReassignmentScenario(t, recovered, 42)
	if !reflect.DeepEqual(baseline.Snapshot(), recovered.Snapshot()) {
		t.Fatal("observation snapshots differ")
	}
	source := recovered.state.Robots[0]
	if !source.Blocked || source.WaitTicks <= 40 || source.Battery < 30 || source.Status != RobotMoving || recovered.state.Jobs[0].Status != JobAssigned {
		t.Fatalf("source is not healthy, blocked, and before pickup: %+v", source)
	}
	t.Logf("identical snapshot tick=%d; source=%+v; job=%+v", recovered.state.Tick, source, recovered.state.Jobs[0])
	// Stage 1 counterfactual only: imitate ownership transfer in this private
	// fixture, then use the unchanged production scheduler/navigation to prove
	// admission and delivery. No production command exists at this stage.
	recovered.clearNavigation(&recovered.state.Robots[0])
	recovered.state.Robots[0].Status, recovered.state.Robots[0].JobID = RobotIdle, ""
	recovered.state.Jobs[0].Status, recovered.state.Jobs[0].AssignedRobot = JobPending, ""
	recovered.assignPending(make([]bool, len(recovered.state.Robots)))
	if recovered.state.Jobs[0].AssignedRobot != "R3" {
		t.Fatalf("existing route/energy admission did not choose recipient: %+v", recovered.state.Jobs[0])
	}
	assertInvariants(t, recovered, recovered.cfg)
	completionTick := 0
	// 200 ticks cover 66 replanning opportunities and 13 recovery thresholds.
	// The parked offline obstruction cannot be moved by healthy-cycle recovery.
	for range 200 {
		advanceReassignmentScenario(t, baseline, 1)
		advanceReassignmentScenario(t, recovered, 1)
		if completionTick == 0 && recovered.state.Jobs[0].Status == JobDone {
			completionTick = recovered.state.Tick
		}
	}
	if baseline.state.Jobs[0].Status != JobAssigned || !baseline.state.Robots[0].Blocked {
		t.Fatalf("automatic baseline unexpectedly progressed: %+v", baseline.Snapshot())
	}
	if completionTick == 0 || recovered.state.Jobs[0].AssignedRobot != "R3" {
		t.Fatalf("recipient did not complete delivery: %+v", recovered.Snapshot())
	}
	repeat := reassignmentScenario(t)
	advanceReassignmentScenario(t, repeat, 242)
	if !reflect.DeepEqual(baseline.Snapshot(), repeat.Snapshot()) {
		t.Fatal("baseline is not deterministic")
	}
	t.Logf("baseline tick=%d remains assigned to R1, wait=%d battery=%d; R3 delivered at tick=%d; all per-tick invariants passed", baseline.state.Tick, baseline.state.Robots[0].WaitTicks, baseline.state.Robots[0].Battery, completionTick)
}
