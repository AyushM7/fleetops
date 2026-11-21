package sim

import (
	"reflect"
	"slices"
	"testing"
)

func maintenanceTestConfig() Config {
	cfg := breakdownConfig()
	cfg.MaintenanceRepairTicks = 3
	return cfg
}

func TestWarehouseV2MaintenanceDeliveryIntegration(t *testing.T) {
	cfg := WarehouseV2Config()
	cfg.ScheduledBreakdowns = []ScheduledBreakdown{{Tick: 48, RobotID: "R4", Type: FaultSensorFailure}}
	s, replay := newTestSim(t, cfg, 42), newTestSim(t, cfg, 42)
	var previous State
	var assignment, arrival, verified, delivered, returned, steps, repairTicks int
	var serviceCell Point
	for range 240 {
		s.Tick()
		replay.Tick()
		state := s.Snapshot()
		assertInvariants(t, s, cfg)
		if !reflect.DeepEqual(state, replay.Snapshot()) {
			t.Fatalf("replay diverged at tick %d", state.Tick)
		}
		if state.Tick < 48 {
			continue
		}
		r4, m1 := state.Robots[3], state.Robots[10]
		job := jobByID(t, state, "J4")
		if state.Tick == 48 && (r4.ActiveFault != FaultSensorFailure || r4.X != 9 || r4.Y != 9 || job.CargoRobotID != "R4") {
			t.Fatalf("fixture did not fail during delivery: robot=%+v job=%+v", r4, job)
		}
		if state.Maintenance != nil {
			attempt := state.Maintenance.Active
			if attempt != nil {
				if attempt.RobotID != "R4" || attempt.FaultID != "F1" || m1.JobID != "" {
					t.Fatalf("maintenance not grounded in the R4 breakdown: %+v", attempt)
				}
				if assignment == 0 {
					assignment, serviceCell = state.Tick, attempt.ServiceCell
					if m1.X != 9 || m1.Y != 0 || state.Maintenance.Phase != MaintenanceAssigned {
						t.Fatalf("assignment moved M1: %+v", m1)
					}
				} else {
					prior := previous.Robots[10]
					dx, dy := m1.X-prior.X, m1.Y-prior.Y
					if delta := dx*dx + dy*dy; delta != 0 {
						steps++
						if delta != 1 || prior.Battery-m1.Battery != cfg.MovingDrain {
							t.Fatalf("nonphysical maintenance movement at tick %d: %+v → %+v", state.Tick, prior, m1)
						}
					}
				}
				if state.Maintenance.Phase == MaintenanceAtTarget {
					arrival = state.Tick
					if (Point{m1.X, m1.Y}) != (Point{9, 10}) || m1.Status != RobotServicing {
						t.Fatalf("unsafe service arrival: %+v", m1)
					}
				}
				if attempt.RepairTicks != repairTicks {
					prior := previous.Robots[10]
					if attempt.RepairTicks != repairTicks+1 || prior.Battery-m1.Battery != 1 ||
						(Point{m1.X, m1.Y}) != serviceCell || (Point{prior.X, prior.Y}) != serviceCell {
						t.Fatalf("repair tick moved M1 or skipped its energy debit: %+v", attempt)
					}
					repairTicks++
				}
			}
			if verified == 0 && len(state.Maintenance.History) != 0 {
				finished := state.Maintenance.History[0]
				verified = state.Tick
				if finished.Outcome != MaintenanceCompleted || finished.FinishedTick != verified || finished.RepairTicks != cfg.MaintenanceRepairTicks ||
					r4.ActiveFault != "" || r4.Status != RobotMoving || (Point{r4.X, r4.Y}) != (Point{9, 9}) {
					t.Fatalf("unexpected verification: %+v and %+v", finished, r4)
				}
			}
		}
		if verified == 0 || state.Tick == verified {
			if job.AssignedRobot != "R4" || job.CargoRobotID != "R4" || job.Status != JobInProgress ||
				(Point{r4.X, r4.Y}) != (Point{9, 9}) || state.Aisles[1].OccupiedBy != "R4" {
				t.Fatalf("tick %d interrupted cargo or occupancy: robot=%+v job=%+v", state.Tick, r4, job)
			}
		}
		if delivered == 0 && job.Status == JobDone {
			delivered = state.Tick
			prior := previous.Robots[3]
			dx, dy := r4.X-prior.X, r4.Y-prior.Y
			if verified == 0 || delivered <= verified || (Point{r4.X, r4.Y}) != job.To || dx*dx+dy*dy != 1 || job.AssignedRobot != "R4" {
				t.Fatalf("job did not finish with an ordinary physical arrival: %+v", job)
			}
		}
		if verified != 0 && state.Tick > verified && delivered == 0 {
			prior := previous.Robots[3]
			dx, dy := r4.X-prior.X, r4.Y-prior.Y
			if delta := dx*dx + dy*dy; delta > 1 || prior.Battery-r4.Battery != delta*cfg.MovingDrain ||
				job.Status != JobInProgress || job.CargoRobotID != "R4" {
				t.Fatalf("tick %d interrupted resumed delivery: robot=%+v job=%+v", state.Tick, r4, job)
			}
		}
		if verified != 0 && state.Tick > verified && returned == 0 {
			prior := previous.Robots[10]
			dx, dy := m1.X-prior.X, m1.Y-prior.Y
			if delta := dx*dx + dy*dy; delta > 1 || prior.Battery-m1.Battery != delta*cfg.MovingDrain {
				t.Fatalf("tick %d had a nonphysical return: %+v → %+v", state.Tick, prior, m1)
			}
		}
		if returned == 0 && verified != 0 && (Point{m1.X, m1.Y}) == *cfg.MaintenanceBay {
			returned = state.Tick
		}
		previous = state
		if delivered != 0 && returned != 0 {
			break
		}
	}
	if assignment != 49 || arrival != 102 || steps != 42 || repairTicks != 3 || verified != 108 ||
		delivered != 122 || returned != 139 || len(previous.Maintenance.History) != 1 || len(previous.Faults) != 1 {
		t.Fatalf("incomplete sequence: assigned=%d arrived=%d steps=%d repairs=%d verified=%d delivered=%d returned=%d maintenance=%+v",
			assignment, arrival, steps, repairTicks, verified, delivered, returned, previous.Maintenance)
	}
	t.Logf("R4 stopped with J4 at (9,9) on tick 48; M1 assigned %d, arrived at (9,10) on %d after %d moves, used %d repair units; verified %d, delivered %d, returned to bay %d",
		assignment, arrival, steps, repairTicks, verified, delivered, returned)
}

func jobByID(t *testing.T, state State, id string) JobState {
	t.Helper()
	for _, job := range state.Jobs {
		if job.ID == id {
			return job
		}
	}
	t.Fatalf("missing job %s at tick %d", id, state.Tick)
	return JobState{}
}

func TestMaintenanceAdmissionDenials(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fault FaultType
		cell  Point
		low   bool
		want  MaintenanceReason
	}{
		{"insufficient energy", FaultMotorFailure, Point{8, 10}, true, maintenanceInsufficient},
		{"inside aisle", FaultSensorFailure, Point{9, 5}, false, maintenanceUnreachable},
		{"external power", FaultCriticalBattery, Point{8, 10}, false, maintenanceExternalPower},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := maintenanceTestConfig()
			cfg.AutoCharge, cfg.Recovery = false, false
			s := newTestSim(t, cfg, 42)
			r1, m1 := s.faultRobot("R1"), s.faultRobot(MaintenanceRobotID)
			r1.X, r1.Y = tc.cell.X, tc.cell.Y
			if tc.low {
				m1.Battery = cfg.ReserveBattery - 1
			}
			requireInjection(t, s.injectBreakdown(r1.ID, tc.fault))
			before := *r1
			before.Activity = ActivityFaulted
			for range 15 {
				s.Tick()
				assertInvariants(t, s, cfg)
			}
			state := s.Snapshot()
			if state.Robots[0] != before || state.Maintenance.Active != nil ||
				len(state.Maintenance.History) != 0 || len(state.Maintenance.Issues) != 1 ||
				state.Maintenance.Issues[0].Reason != tc.want || (Point{m1.X, m1.Y}) != *cfg.MaintenanceBay {
				t.Fatalf("unsafe admission or lost issue: robot=%+v work=%+v", state.Robots[0], state.Maintenance)
			}
		})
	}
}

func TestMaintenanceSchedulesAndRepairsOneFaultAtATime(t *testing.T) {
	cfg := maintenanceTestConfig()
	s := newTestSim(t, cfg, 42)
	r3, r2 := s.faultRobot("R3"), s.faultRobot("R2")
	r3.X, r3.Y, r2.X, r2.Y = 12, 10, 7, 10
	requireInjection(t, s.injectBreakdown("R3", FaultSensorFailure))
	requireInjection(t, s.injectBreakdown("R2", FaultMotorFailure))
	s.Tick()
	if active := s.maintenance.Active; active == nil || active.RobotID != "R2" || active.FaultID != "F2" ||
		len(s.maintenance.Issues) != 1 || s.maintenance.Issues[0].Reason != maintenanceBusy {
		t.Fatalf("invalid stable tie break or overlapping admission: %+v", s.maintenance)
	}
	if issue := s.assignMaintenance(MaintenanceRequest{RobotID: "R2", FaultID: "F2"}); issue != maintenanceBusy {
		t.Fatalf("duplicate dispatch was not denied: %s", issue)
	}
	for range 210 {
		s.Tick()
		assertInvariants(t, s, cfg)
		if len(s.maintenance.History) >= 2 {
			break
		}
	}
	history := s.maintenance.History
	if len(history) != 2 || history[0].RobotID != "R2" || history[1].RobotID != "R3" ||
		history[1].StartedTick <= history[0].FinishedTick || history[1].Fault != FaultSensorFailure ||
		history[0].Outcome != MaintenanceCompleted || history[1].Outcome != MaintenanceCompleted || r3.ActiveFault != "" {
		t.Fatalf("attempts overlapped or recovery failed: %+v, R3=%+v", history, r3)
	}
}

func TestMaintenanceConfirmsOfflineBeforeRepairAndResumesPickup(t *testing.T) {
	cfg := maintenanceTestConfig()
	s := newTestSim(t, cfg, 42)
	r1 := s.faultRobot("R1")
	r1.X, r1.Y, r1.Status, r1.JobID = 8, 10, RobotMoving, "J1"
	job := JobState{ID: "J1", Type: JobNormal, From: Point{4, 10}, To: Point{2, 10}, Status: JobAssigned, AssignedRobot: "R1"}
	s.state.Jobs = []JobState{job}
	requireInjection(t, s.injectBreakdown("R1", FaultRobotUnresponsive))
	for range cfg.UnresponsiveDelay - 1 {
		s.Tick()
		if s.maintenance.Active != nil || len(s.maintenance.Issues) != 1 || s.maintenance.Issues[0].Reason != maintenanceAwaitingOffline {
			t.Fatalf("maintenance acted before offline confirmation: %+v", s.maintenance)
		}
	}
	s.Tick()
	if s.maintenance.Active == nil || r1.Status != RobotOffline || s.maintenance.Active.StartedTick != cfg.UnresponsiveDelay {
		t.Fatalf("offline robot was not admitted: %+v", s.maintenance)
	}
	for range 105 {
		s.Tick()
		assertInvariants(t, s, cfg)
		if s.state.Jobs[0].Status != JobInProgress {
			continue
		}
		if len(s.maintenance.History) != 1 || s.maintenance.History[0].Outcome != MaintenanceCompleted ||
			s.maintenance.History[0].Fault != FaultRobotUnresponsive || r1.ActiveFault != "" ||
			s.unresponsiveTicks["R1"] != 0 || (Point{r1.X, r1.Y}) != job.From ||
			s.Snapshot().Jobs[0].CargoRobotID != "R1" {
			t.Fatalf("verified communication repair did not resume pickup: %+v, %+v", r1, s.maintenance)
		}
		return
	}
	t.Fatal("R1 never performed its original pickup")
}

func TestMaintenanceCancelsChangedTargetAndBoundsTravel(t *testing.T) {
	for _, tc := range []struct {
		name    string
		blocked bool
		outcome MaintenanceOutcome
		reason  MaintenanceReason
	}{
		{"target changed", false, MaintenanceCancelled, maintenanceTargetChanged},
		{"service cell blocked", true, MaintenanceUnresolved, maintenanceBlocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := maintenanceTestConfig()
			s := newTestSim(t, cfg, 42)
			r1 := s.faultRobot("R1")
			r1.X, r1.Y = 8, 10
			requireInjection(t, s.injectBreakdown("R1", FaultSensorFailure))
			if issue := s.assignMaintenance(MaintenanceRequest{RobotID: "R1", FaultID: "F99"}); issue != maintenanceTargetChanged {
				t.Fatalf("stale fault identity admitted: %s", issue)
			}
			s.Tick()
			active := s.maintenance.Active
			if active == nil {
				t.Fatal("expected an assigned attempt")
			}
			if tc.blocked {
				r2 := s.faultRobot("R2")
				r2.X, r2.Y = active.ServiceCell.X, active.ServiceCell.Y
				requireInjection(t, s.injectBreakdown("R2", FaultCriticalBattery))
			} else {
				// Exercise invalidation defensively by changing the trusted fixture after admission.
				r1.X, r1.Y = 7, 10
				m1 := s.faultRobot(MaintenanceRobotID)
				before := Point{m1.X, m1.Y}
				s.Tick()
				if (Point{m1.X, m1.Y}) != before {
					t.Fatal("M1 moved toward a stale work target")
				}
			}
			for range 150 {
				s.Tick()
				assertInvariants(t, s, cfg)
			}
			history := s.maintenance.History
			if len(history) != 1 || history[0].Outcome != tc.outcome || history[0].Reason != tc.reason ||
				s.maintenance.Active != nil || s.faultRobot("R1").ActiveFault != FaultSensorFailure {
				t.Fatalf("lost interrupted physical attempt: %+v", s.maintenance)
			}
			if issue := s.assignMaintenance(history[0].MaintenanceRequest); issue != maintenanceAlreadyTried {
				t.Fatalf("terminal attempt admitted again: %s", issue)
			}
		})
	}
}

func TestMaintenanceWaitsForTrafficWithinTheSameAttempt(t *testing.T) {
	cfg := maintenanceTestConfig()
	cfg.AutoCharge, cfg.Recovery = false, false
	s := newTestSim(t, cfg, 42)
	r1 := s.faultRobot("R1")
	r1.X, r1.Y = 8, 10
	requireInjection(t, s.injectBreakdown(r1.ID, FaultMotorFailure))
	s.Tick()
	m1, blocker := s.faultRobot(MaintenanceRobotID), s.faultRobot("R2")
	attempt := s.maintenance.Active
	if attempt == nil {
		t.Fatal("expected an assigned attempt")
	}
	bay, origin := *cfg.MaintenanceBay, (Point{blocker.X, blocker.Y})
	first := s.navigation[m1.ID].route[0]
	blocker.X, blocker.Y = first.X, first.Y
	battery := m1.Battery
	for range cfg.ReplanAfter + 1 {
		s.Tick()
		assertInvariants(t, s, cfg)
		if s.maintenance.Active != attempt || attempt.RepairTicks != 0 || (Point{m1.X, m1.Y}) != bay ||
			(Point{blocker.X, blocker.Y}) != first || m1.Battery != battery {
			t.Fatalf("traffic blockage changed assignment, occupancy, or energy: %+v, M1=%+v, blocker=%+v", s.maintenance, m1, blocker)
		}
	}
	if m1.WaitTicks <= cfg.ReplanAfter {
		t.Fatalf("traffic never exercised replanning: %+v", m1)
	}
	// Clear the temporary fixture blockage and let normal movement resume.
	blocker.X, blocker.Y = origin.X, origin.Y
	for range 100 {
		s.Tick()
		assertInvariants(t, s, cfg)
		if len(s.maintenance.History) != 0 {
			break
		}
	}
	if history := s.maintenance.History; len(history) != 1 || history[0].ID != attempt.ID ||
		history[0].StartedTick != attempt.StartedTick || history[0].Outcome != MaintenanceCompleted || r1.ActiveFault != "" {
		t.Fatalf("clearing traffic failed to resume the original attempt: %+v", s.maintenance)
	}
}

func TestMaintenanceVerificationDoesNotCompleteIfM1BreaksOnThatTick(t *testing.T) {
	cfg := WarehouseV2Config()
	cfg.ScheduledBreakdowns = []ScheduledBreakdown{
		{Tick: 48, RobotID: "R4", Type: FaultSensorFailure},
		{Tick: 108, RobotID: MaintenanceRobotID, Type: FaultMotorFailure},
	}
	s := newTestSim(t, cfg, 42)
	for range 107 {
		s.Tick()
	}
	if work := s.maintenance; work.Phase != MaintenanceVerifying || work.Active == nil ||
		work.Active.RepairTicks != cfg.MaintenanceRepairTicks {
		t.Fatalf("expected M1 to be verifying just before its breakdown: %+v", work)
	}
	for range 15 {
		s.Tick()
		assertInvariants(t, s, cfg)
	}
	state := s.Snapshot()
	history := state.Maintenance.History
	r4, m1, job := state.Robots[3], state.Robots[10], jobByID(t, state, "J4")
	if len(history) != 1 || history[0].Outcome != MaintenanceFailed || history[0].FinishedTick != 108 ||
		state.Maintenance.Active != nil || state.Maintenance.Phase != MaintenanceUnavailable ||
		m1.ActiveFault != FaultMotorFailure || r4.ActiveFault != FaultSensorFailure ||
		(Point{r4.X, r4.Y}) != (Point{9, 9}) || job.Status != JobInProgress || job.CargoRobotID != "R4" {
		t.Fatalf("failed M1 cleared R4 or changed its cargo: %+v, %+v, %+v, %+v", state.Maintenance, m1, r4, job)
	}
}

func TestMaintenanceFaultedM1CannotRepairItselfOrCompleteAnAttempt(t *testing.T) {
	cfg := maintenanceTestConfig()
	s := newTestSim(t, cfg, 42)
	r1 := s.faultRobot("R1")
	r1.X, r1.Y = 8, 10
	requireInjection(t, s.injectBreakdown("R1", FaultMotorFailure))
	for range 90 {
		s.Tick()
		if s.maintenance.Phase == MaintenanceRepairing {
			break
		}
	}
	if s.maintenance.Phase != MaintenanceRepairing {
		t.Fatalf("expected repair in progress: %+v", s.maintenance)
	}
	requireInjection(t, s.injectBreakdown(MaintenanceRobotID, FaultSensorFailure))
	for range 15 {
		s.Tick()
		assertInvariants(t, s, cfg)
	}
	state := s.Snapshot()
	if state.Maintenance.Active != nil || state.Maintenance.Phase != MaintenanceUnavailable ||
		len(state.Maintenance.History) != 1 || state.Maintenance.History[0].Outcome != MaintenanceFailed ||
		state.Robots[0].ActiveFault != FaultMotorFailure || state.Robots[10].ActiveFault != FaultSensorFailure ||
		!slices.Contains(state.Maintenance.Issues, MaintenanceIssue{MaintenanceRequest{"M1", "F2"}, maintenanceSelfRepair}) {
		t.Fatalf("unexpected interrupted attempt: %+v", state.Maintenance)
	}
}

func TestMaintenanceLeavesFaultOpenWhenDeliveryRecoveryCannotBeVerified(t *testing.T) {
	cfg := maintenanceTestConfig()
	s := newTestSim(t, cfg, 42)
	r1 := s.faultRobot("R1")
	r1.X, r1.Y, r1.Battery, r1.Status, r1.JobID = 9, 9, 2, RobotMoving, "J1"
	s.state.Jobs = []JobState{{ID: "J1", Type: JobNormal, From: Point{9, 8}, To: Point{14, 0}, Status: JobInProgress, AssignedRobot: "R1"}}
	requireInjection(t, s.injectBreakdown("R1", FaultSensorFailure))
	for range 135 {
		s.Tick()
		assertInvariants(t, s, cfg)
	}
	state := s.Snapshot()
	if got := state.Maintenance.History; len(got) != 1 || got[0].Outcome != MaintenanceUnresolved ||
		got[0].Reason != maintenanceRecoveryBlocked || got[0].RepairTicks != cfg.MaintenanceRepairTicks {
		t.Fatalf("failed readiness was not retained as unresolved: %+v", got)
	}
	if job, robot := state.Jobs[0], state.Robots[0]; job.Status != JobInProgress || job.CargoRobotID != "R1" ||
		robot.ActiveFault != FaultSensorFailure || robot.X != 9 || robot.Y != 9 || robot.Battery != 2 {
		t.Fatalf("unverified recovery mutated cargo ownership or robot: %+v, %+v", job, robot)
	}
}
