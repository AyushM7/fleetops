package sim

import (
	"reflect"
	"testing"
)

func blockedReassignmentScenario(t *testing.T) *Sim {
	t.Helper()
	s := reassignmentScenario(t)
	advanceReassignmentScenario(t, s, 42)
	return s
}

func TestReassignmentCommandAtomicAndCompletes(t *testing.T) {
	s := blockedReassignmentScenario(t)
	before := s.Snapshot()
	plan, err := s.PreviewReassignment("J1", "R1", "R3")
	if err != nil || plan != (ReassignmentPlan{1, 2, 5, 13}) {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	if !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatal("preview mutated state")
	}
	if err := s.ReassignUnstartedJob("J1", "R1", "R3"); err != nil {
		t.Fatal(err)
	}
	after := s.Snapshot()
	if after.Tick != before.Tick || after.Robots[0].JobID != "" || after.Robots[0].Status != RobotIdle || after.Robots[0].Blocked || after.Robots[2].JobID != "J1" || after.Jobs[0].AssignedRobot != "R3" || after.Jobs[0].Status != JobAssigned {
		t.Fatalf("non-atomic transfer: %+v", after)
	}
	assertInvariants(t, s, s.cfg)
	advanceReassignmentScenario(t, s, 3)
	if s.state.Jobs[0].Status != JobDone {
		t.Fatal("transfer did not enable delivery")
	}
}

func TestReassignmentRejectsWithoutMutation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		alter func(*Sim)
	}{
		{"pickup reached", func(s *Sim) { s.state.Robots[0].X, s.state.Robots[0].Y = 4, 4 }},
		{"collected", func(s *Sim) { s.state.Jobs[0].Status = JobInProgress }},
		{"stale owner", func(s *Sim) { s.state.Jobs[0].AssignedRobot = "R4" }},
		{"no longer blocked", func(s *Sim) { s.state.Robots[0].Blocked = false }},
		{"source low battery", func(s *Sim) { s.state.Robots[0].Battery = 29 }},
		{"source offline", func(s *Sim) { s.state.Robots[0].Status = RobotOffline }},
		{"recipient low battery", func(s *Sim) { s.state.Robots[2].Battery = 29 }},
		{"recipient unavailable", func(s *Sim) { s.state.Robots[2].Status = RobotCharging }},
		{"recipient busy", func(s *Sim) { s.state.Robots[2].JobID = "J2" }},
		{"pending offline", func(s *Sim) { s.unresponsiveTicks["R3"] = 1 }},
		{"energy", func(s *Sim) { s.cfg.MovingDrain = 12 }},
		{"pickup blocked", func(s *Sim) { s.state.Robots[3].X, s.state.Robots[3].Y = 4, 4 }},
		{"delivery blocked", func(s *Sim) { s.state.Robots[3].X, s.state.Robots[3].Y = 6, 4 }},
		{"occupied aisle", func(s *Sim) { s.state.Jobs[0].To = Point{6, 1}; s.state.Robots[3].X, s.state.Robots[3].Y = 6, 2 }},
		{"charger blocked", func(s *Sim) { s.state.Robots[3].X, s.state.Robots[3].Y = 7, 0 }},
		{"charger reserved", func(s *Sim) { s.state.Stations[0].ReservedBy = "R4" }},
		{"duplicate assignment", func(s *Sim) { s.state.Robots[3].JobID = "J1" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := blockedReassignmentScenario(t)
			tc.alter(s)
			before := s.Snapshot()
			nav := s.navigation["R1"]
			if err := s.ReassignUnstartedJob("J1", "R1", "R3"); err == nil {
				t.Fatal("invalid transfer accepted")
			}
			if !reflect.DeepEqual(before, s.Snapshot()) || !reflect.DeepEqual(nav, s.navigation["R1"]) {
				t.Fatal("rejected command mutated state")
			}
		})
	}
}
