package sim

import (
	"errors"
	"reflect"
	"testing"
)

func TestAssignJobBatchValidation(t *testing.T) {
	pair := JobAssignment{"J1", "R1"}
	for _, tc := range []struct {
		name  string
		pairs []JobAssignment
	}{
		{"empty", nil},
		{"over capacity", make([]JobAssignment, 11)},
		{"repeated job", []JobAssignment{pair, {"J1", "R2"}}},
		{"repeated robot", []JobAssignment{pair, {"J2", "R1"}}},
		{"leading zero", []JobAssignment{{"J01", "R1"}}},
		{"wrong prefix", []JobAssignment{{"R1", "R2"}}},
		{"overflow", []JobAssignment{{"J9999999999999999999", "R1"}}},
		{"maintenance robot", []JobAssignment{{"J1", "M1"}}},
		{"retired ID", []JobAssignment{{"J1", "R11"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := (AssignJobBatchRequest{tc.pairs}).Validate(); !errors.Is(err, ErrInvalidAssignmentBatch) {
				t.Fatalf("Validate = %v, want invalid batch", err)
			}
		})
	}
	if err := newTestSim(t, WarehouseConfig(), 1).AssignJobBatch(AssignJobBatchRequest{[]JobAssignment{pair}}); !errors.Is(err, ErrAssignmentLayout) {
		t.Fatalf("legacy assignment batch = %v, want V2-only", err)
	}
}

func newBatchTestSim(t *testing.T) *Sim {
	t.Helper()
	cfg := WarehouseV2Config()
	cfg.AutoDispatch, cfg.InitialJobs, cfg.NewJobEvery = false, 0, 10000
	s := newTestSim(t, cfg, 1)
	s.state.Jobs = []JobState{
		{ID: "J1", Type: JobNormal, From: Point{0, 11}, To: Point{1, 11}, Status: JobPending},
		{ID: "J2", Type: JobPriority, From: Point{4, 11}, To: Point{5, 11}, Status: JobPending, DeadlineTick: 35},
	}
	return s
}

func TestAssignJobBatchIsAtomicIntegration(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*Sim)
		want  error
	}{
		{"unknown job", func(s *Sim) { s.state.Jobs[1].ID = "J3" }, ErrAssignmentConflict},
		{"job already assigned", func(s *Sim) { s.state.Jobs[1].Status = JobAssigned }, ErrAssignmentConflict},
		{"job deferred", func(s *Sim) { s.state.Jobs[1].Deferred = true }, ErrAssignmentConflict},
		{"charger reserved", func(s *Sim) { s.state.Stations[0].ReservedBy = "R3" }, ErrAssignmentConflict},
		{"faulted at pickup", func(s *Sim) {
			r := &s.state.Robots[3]
			r.X, r.Y, r.Status = 4, 11, RobotFaulted
		}, ErrAssignmentInfeasible},
		{"mission beyond available energy", func(s *Sim) {
			s.state.Robots[2].Battery = s.cfg.ChargeBelow + 1
			s.state.Jobs[1].From, s.state.Jobs[1].To = Point{19, 9}, Point{0, 9}
		}, ErrAssignmentInfeasible},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newBatchTestSim(t)
			tc.setup(s)
			before := s.Snapshot()
			err := s.AssignJobBatch(AssignJobBatchRequest{[]JobAssignment{{"J1", "R1"}, {"J2", "R3"}}})
			if !errors.Is(err, tc.want) || !reflect.DeepEqual(before, s.Snapshot()) || len(s.navigation) > 0 {
				t.Fatalf("batch = %v, want %v and unchanged state", err, tc.want)
			}
		})
	}
}

func TestAssignJobBatchUsesNormalMovementAndCustodyIntegration(t *testing.T) {
	s := newBatchTestSim(t)
	before := s.Snapshot()
	request := AssignJobBatchRequest{[]JobAssignment{{"J1", "R1"}, {"J2", "R3"}}}
	if err := s.AssignJobBatch(request); err != nil {
		t.Fatal(err)
	}
	after := s.Snapshot()
	for i, idx := range []int{0, 2} {
		a, b := before.Robots[idx], after.Robots[idx]
		if a.X != b.X || a.Y != b.Y || a.Battery != b.Battery || after.Jobs[i].AssignedRobot != b.ID {
			t.Fatalf("assignment moved or discharged a robot: before=%+v after=%+v", a, b)
		}
	}
	if err := s.AssignJobBatch(request); !errors.Is(err, ErrAssignmentConflict) {
		t.Fatalf("repeat assignment = %v, want conflict", err)
	}
	s.Tick()
	s.Tick()
	if s.Snapshot().Jobs[0].CargoRobotID != "R1" {
		t.Fatal("R1 did not pick up its assigned job")
	}
	r1 := &s.state.Robots[0]
	r1.Blocked, r1.WaitTicks = true, s.cfg.BlockedAfter
	if err := s.ReassignUnstartedJob("J1", "R1", "R2"); !errors.Is(err, ErrReassignment) {
		t.Fatalf("reassign collected cargo = %v, want rejection", err)
	}
	for range 8 {
		before = s.Snapshot()
		s.Tick()
		assertInvariants(t, s, s.cfg)
		assertV2TrafficStep(t, before, s.Snapshot())
	}
	if state := s.Snapshot(); state.CompletedJobs != 2 || state.Jobs[1].Outcome != JobOnTime {
		t.Fatalf("assignments did not produce verified deliveries: %+v", state.Jobs)
	}
}

func TestOperationsDispatchHoldExpiresWithoutStoppingTicks(t *testing.T) {
	s := newTestSim(t, WarehouseV2Config(), 42)
	options := s.OperationsAssignments(s.Snapshot().Jobs)
	if len(options) == 0 {
		t.Fatal("fixture has no dispatch options")
	}
	s.SetOperationsDispatchHold(options)
	jobID := options[0].JobID
	for range operationsDispatchTicks {
		s.Tick()
		for _, job := range s.Snapshot().Jobs {
			if job.ID == jobID && job.AssignedRobot != "" {
				t.Fatal("deterministic dispatch claimed an offered job during the window")
			}
		}
	}
	s.Tick()
	for _, job := range s.Snapshot().Jobs {
		if job.ID == jobID && job.AssignedRobot != "" {
			return
		}
	}
	t.Fatal("offered job was still held after the final dispatch tick")
}
