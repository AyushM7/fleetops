package sim

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
)

func dispatchSim(t *testing.T, cfg Config, start, pickup, delivery Point) *Sim {
	t.Helper()
	cfg.InitialJobs, cfg.MaxJobs = 1, 1
	s := newTestSim(t, cfg, 42)
	for i := range s.state.Robots {
		s.state.Robots[i].Status = RobotOffline
	}
	r := &s.state.Robots[0]
	r.X, r.Y, r.Status = start.X, start.Y, RobotIdle
	setJobCells(t, s, "J1", pickup, delivery)
	return s
}

func TestDispatchNearestAndSameTickMovement(t *testing.T) {
	s := dispatchSim(t, DefaultConfig(), Point{1, 1}, Point{5, 1}, Point{6, 1})
	s.state.Robots[1].X, s.state.Robots[1].Y, s.state.Robots[1].Status = 5, 0, RobotIdle
	s.Tick()
	first, nearest := s.state.Robots[0], s.state.Robots[1]
	if first.JobID != "" || s.state.Jobs[0].AssignedRobot != "R2" || nearest.JobID != "J1" || nearest.X != 5 || nearest.Y != 1 || nearest.Battery != 99 || s.state.Jobs[0].Status != JobInProgress {
		t.Fatalf("nearest assignment/first movement failed: first=%+v nearest=%+v job=%+v", first, nearest, s.state.Jobs[0])
	}
	assertInvariants(t, s, s.cfg)
}

func TestDispatchUsesShelfRouteDistances(t *testing.T) {
	s := dispatchSim(t, WarehouseConfig(), Point{1, 2}, Point{4, 2}, Point{4, 1})
	// R1 is nearer by Manhattan distance (3 vs 4) but needs a five-step shelf detour.
	s.state.Robots[1].X, s.state.Robots[1].Y, s.state.Robots[1].Status = 4, 6, RobotIdle
	s.Tick()
	if r := s.state.Robots[1]; r.JobID != "J1" || r.X != 4 || r.Y != 5 || r.Battery != 99 || s.state.Jobs[0].AssignedRobot != "R2" {
		t.Fatalf("scheduler did not use actual A* distance: %+v", r)
	}
	assertInvariants(t, s, s.cfg)
}

func TestDispatchParkedAisleBlocksPickup(t *testing.T) {
	s := dispatchSim(t, WarehouseConfig(), Point{1, 1}, Point{4, 2}, Point{4, 1})
	s.state.Robots[1].X, s.state.Robots[1].Y = 4, 6
	s.Tick()
	if s.state.Jobs[0].Status != JobPending || s.state.Robots[0].JobID != "" {
		t.Fatal("pickup inside a parked robot's aisle was assigned")
	}
	assertInvariants(t, s, s.cfg)
}

func TestDispatchNumericRobotTie(t *testing.T) {
	s := dispatchSim(t, DefaultConfig(), Point{1, 1}, Point{2, 1}, Point{2, 0})
	s.state.Robots[0].ID = "R10"
	s.state.Robots[1].X, s.state.Robots[1].Y, s.state.Robots[1].Status = 3, 1, RobotIdle
	s.Tick()
	if job := s.state.Jobs[0]; job.AssignedRobot != "R2" || job.Status != JobInProgress {
		t.Fatalf("numeric 2 must beat 10 on equal distance: %+v", job)
	}
}

func TestDispatchBatteryBoundary(t *testing.T) {
	for _, tc := range []struct {
		name             string
		battery, reserve int
		want             bool
	}{
		{name: "below full trip plus reserve", battery: 16, reserve: 5},
		{name: "exact full trip plus reserve", battery: 17, reserve: 5, want: true},
		{name: "higher reserve", battery: 17, reserve: 6},
		{name: "lower reserve", battery: 16, reserve: 4, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.MinAssignBattery, cfg.LowBatteryInjectLevel, cfg.MovingDrain, cfg.ReserveBattery = 1, 0, 3, tc.reserve
			s := dispatchSim(t, cfg, Point{1, 1}, Point{3, 1}, Point{5, 1})
			s.state.Robots[0].Battery = tc.battery
			s.Tick()
			robot, job := s.state.Robots[0], s.state.Jobs[0]
			if tc.want {
				if robot.JobID != "J1" || robot.X != 2 || robot.Y != 1 || robot.Battery != tc.battery-3 || job.Status != JobAssigned {
					t.Fatalf("feasible job did not move immediately: %+v %+v", robot, job)
				}
			} else if robot.JobID != "" || robot.X != 1 || robot.Y != 1 || robot.Battery != tc.battery || job.Status != JobPending {
				t.Fatalf("infeasible job changed robot or assignment: %+v %+v", robot, job)
			}
			assertInvariants(t, s, s.cfg)
		})
	}
}

func TestDispatchParkedPickupAndDelivery(t *testing.T) {
	for _, blockPickup := range []bool{true, false} {
		t.Run(fmt.Sprintf("pickup_%v", blockPickup), func(t *testing.T) {
			s := dispatchSim(t, DefaultConfig(), Point{1, 1}, Point{2, 1}, Point{3, 1})
			position := Point{3, 1}
			if blockPickup {
				position = Point{2, 1}
			}
			s.state.Robots[1].X, s.state.Robots[1].Y = position.X, position.Y
			s.Tick()
			if s.state.Jobs[0].Status != JobPending || s.state.Robots[0].JobID != "" || s.state.Robots[0].Battery != 100 {
				t.Fatal("parked goal was assigned")
			}
			assertInvariants(t, s, s.cfg)
		})
	}
}

func TestDispatchSkipsBlockedEarlierJob(t *testing.T) {
	s := dispatchSim(t, DefaultConfig(), Point{1, 1}, Point{2, 1}, Point{3, 1})
	s.state.Robots[1].X, s.state.Robots[1].Y = 2, 1
	s.cfg.MaxJobs = 2
	s.state.Jobs = append(s.state.Jobs, JobState{ID: "J2", From: Point{1, 2}, To: Point{1, 3}, Status: JobPending})
	slices.Reverse(s.state.Jobs)
	s.assignPending(make([]bool, len(s.state.Robots)))
	if s.state.Jobs[0].ID != "J2" || s.state.Jobs[0].AssignedRobot != "R1" || s.state.Jobs[1].Status != JobPending {
		t.Fatalf("blocked early job prevented later assignment: %+v", s.state.Jobs)
	}
}

func TestDispatchNumericJobsAndSingleUse(t *testing.T) {
	cfg := DefaultConfig()
	cfg.InitialJobs = 10
	s := newTestSim(t, cfg, 42)
	for i := range s.state.Robots {
		s.state.Robots[i].Status = RobotOffline
	}
	s.state.Robots[0].X, s.state.Robots[0].Y, s.state.Robots[0].Status = 1, 1, RobotIdle
	s.state.Robots[1].X, s.state.Robots[1].Y, s.state.Robots[1].Status = 3, 1, RobotIdle
	for i := range s.state.Jobs {
		job := &s.state.Jobs[i]
		if job.ID == "J2" || job.ID == "J10" {
			job.From, job.To = Point{2, 1}, Point{2, 0}
		} else {
			job.Status, job.AssignedRobot = JobDone, "R1"
		}
	}
	slices.Reverse(s.state.Jobs)
	s.assignPending(make([]bool, len(s.state.Robots)))
	if s.state.Robots[0].JobID != "J2" || s.state.Robots[1].JobID != "J10" {
		t.Fatalf("numeric job priority or single-use changed: %+v", s.state.Robots[:2])
	}
}

func TestDispatchChargingCompletionSkipsTick(t *testing.T) {
	cfg := cfgRobotOnStation()
	s := dispatchSim(t, cfg, Point{0, 0}, Point{1, 0}, Point{2, 0})
	if err := s.SetCharging("R1", "S1"); err != nil {
		t.Fatal(err)
	}
	s.state.Robots[0].Battery = 99
	s.state.Tick = 19
	s.Tick()
	if r := s.state.Robots[0]; r.Status != RobotIdle || r.Battery != 100 || r.JobID != "" || r.X != 0 || r.Y != 0 || s.state.Jobs[0].Status != JobPending || s.state.Stations[0].OccupiedBy != "" {
		t.Fatalf("charging completion dispatched, moved, or drained: %+v", r)
	}
	s.Tick()
	if r := s.state.Robots[0]; r.JobID != "J1" || r.X != 1 || r.Y != 0 || r.Battery != 99 {
		t.Fatalf("completed charger did not dispatch next tick: %+v", r)
	}
	assertInvariants(t, s, s.cfg)
}

func TestDispatchAutoDisabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AutoDispatch = false
	s := dispatchSim(t, cfg, Point{1, 1}, Point{2, 1}, Point{3, 1})
	for tick := 0; tick < 5; tick++ {
		s.Tick()
	}
	if s.state.Robots[0].JobID != "" || s.state.Jobs[0].Status != JobPending || s.state.Robots[0].Battery != 100 {
		t.Fatal("disabled dispatch assigned or moved robot")
	}
}

func TestDispatchPassACompletesBeforeMovement(t *testing.T) {
	s := navigationSim(t, DefaultConfig(), Point{1, 1}, Point{3, 1}, Point{3, 0})
	s.cfg.AutoDispatch = true
	s.state.Robots[1].X, s.state.Robots[1].Y, s.state.Robots[1].Status = 2, 1, RobotMoving
	s.state.Robots[1].JobID = "J2"
	s.state.Jobs = append(s.state.Jobs, JobState{ID: "J2", From: Point{2, 0}, To: Point{3, 0}, Status: JobAssigned, AssignedRobot: "R2"})
	s.cfg.MaxJobs = 2
	s.unresponsiveTicks["R2"] = 1
	s.Tick()
	if r := s.state.Robots[0]; r.X != 1 || r.Y != 0 || r.WaitTicks != 0 {
		t.Fatalf("lower robot planned before higher robot became parked: %+v", r)
	}
	if r := s.state.Robots[1]; r.Status != RobotOffline || r.Battery != 100 || r.JobID != "J2" || r.X != 2 || r.Y != 1 {
		t.Fatalf("countdown precedence changed: %+v", r)
	}
	assertInvariants(t, s, s.cfg)
}

func TestDispatchFiveSeedInvariantMatrix(t *testing.T) {
	for _, layout := range []struct {
		name string
		cfg  Config
	}{{name: "default", cfg: DefaultConfig()}, {name: "warehouse", cfg: WarehouseConfig()}} {
		for _, seed := range []int64{1, 2, 3, 42, 99} {
			t.Run(fmt.Sprintf("%s/seed_%d", layout.name, seed), func(t *testing.T) {
				s, repeat := newTestSim(t, layout.cfg, seed), newTestSim(t, layout.cfg, seed)
				for tick := 0; tick <= 500; tick++ {
					assertInvariants(t, s, layout.cfg)
					if !reflect.DeepEqual(s.Snapshot(), repeat.Snapshot()) || len(s.state.Faults) != 0 {
						t.Fatalf("non-determinism or routine scheduling fault at tick %d", tick)
					}
					if tick < 500 {
						s.Tick()
						repeat.Tick()
					}
				}
			})
		}
	}
}
