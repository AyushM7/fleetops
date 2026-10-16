package sim

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
)

func navigationSim(t *testing.T, cfg Config, start, pickup, delivery Point) *Sim {
	t.Helper()
	cfg.InitialJobs, cfg.MaxJobs, cfg.AutoDispatch = 1, 1, false
	s := newTestSim(t, cfg, 42)
	for i := range s.state.Robots {
		s.state.Robots[i].Status = RobotOffline
	}
	r := &s.state.Robots[0]
	r.X, r.Y, r.Status, r.JobID = start.X, start.Y, RobotMoving, "J1"
	j := &s.state.Jobs[0]
	j.From, j.To, j.Status, j.AssignedRobot = pickup, delivery, JobAssigned, r.ID
	return s
}

func TestNavigationAroundShelves(t *testing.T) {
	s := navigationSim(t, WarehouseConfig(), Point{1, 2}, Point{4, 2}, Point{4, 1})
	want := []Point{{1, 1}, {2, 1}, {3, 1}, {4, 1}, {4, 2}, {4, 1}}
	for i, point := range want {
		s.Tick()
		r := s.state.Robots[0]
		if (Point{r.X, r.Y}) != point || r.Battery != 99-i {
			t.Fatalf("step %d: %+v, want %v battery %d", i+1, r, point, 99-i)
		}
		assertInvariants(t, s, s.cfg)
	}
	if s.state.Jobs[0].Status != JobDone || s.state.Jobs[0].AssignedRobot != "R1" || s.state.Robots[0].JobID != "" {
		t.Fatal("arrival lost completion or assignment history")
	}
}

func TestNavigationStationRules(t *testing.T) {
	for _, tc := range []struct {
		name        string
		start, goal Point
		want        []Point
	}{
		{name: "station not transit", start: Point{0, 1}, goal: Point{1, 0}, want: []Point{{1, 1}, {1, 0}}},
		{name: "station destination", start: Point{1, 0}, goal: Point{0, 0}, want: []Point{{0, 0}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := navigationSim(t, DefaultConfig(), tc.start, tc.start, tc.goal)
			for _, point := range tc.want {
				s.Tick()
				r := s.state.Robots[0]
				if (Point{r.X, r.Y}) != point {
					t.Fatalf("position %+v, want %v", r, point)
				}
				assertInvariants(t, s, s.cfg)
			}
		})
	}
}

func TestNavigationNumericMovementPriority(t *testing.T) {
	s := navigationSim(t, DefaultConfig(), Point{1, 1}, Point{2, 1}, Point{2, 0})
	s.state.Robots[1].X, s.state.Robots[1].Y = 3, 1
	s.state.Robots[1].Status, s.state.Robots[1].JobID = RobotMoving, "J2"
	s.state.Jobs = append(s.state.Jobs, JobState{ID: "J2", From: Point{2, 1}, To: Point{3, 0}, Status: JobAssigned, AssignedRobot: "R2"})
	s.cfg.MaxJobs = 2
	slices.Reverse(s.state.Robots)
	s.Tick()
	first, second := s.state.Robots[0], s.state.Robots[1]
	if (Point{first.X, first.Y}) != (Point{2, 1}) || (Point{second.X, second.Y}) != (Point{3, 1}) || second.WaitTicks != 1 || second.Battery != 100 {
		t.Fatalf("numeric priority or waiting changed: %+v, %+v", first, second)
	}
	assertInvariants(t, s, s.cfg)
	s.Tick()
	if s.state.Robots[1].X != 2 || s.state.Robots[1].WaitTicks != 0 || s.state.Robots[1].Battery != 99 {
		t.Fatalf("waiting robot did not use freed cell: %+v", s.state.Robots[1])
	}
	assertInvariants(t, s, s.cfg)
}

func TestNavigationExclusiveAisleResumes(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ReplanAfter, cfg.BlockedAfter = 50, 100
	s := navigationSim(t, cfg, Point{4, 0}, Point{4, 2}, Point{5, 2})
	s.prepareNavigation(&s.state.Robots[0], Point{4, 2})
	s.state.Robots[1].X, s.state.Robots[1].Y = 4, 5
	for tick := 1; tick <= 2; tick++ {
		s.Tick()
		r := s.state.Robots[0]
		if r.Y != 0 || r.WaitTicks != tick || r.Battery != 100 {
			t.Fatalf("occupied aisle entered or wait drained battery: %+v", r)
		}
		assertInvariants(t, s, s.cfg)
	}
	s.state.Robots[1].X = 6
	s.Tick()
	r := s.state.Robots[0]
	if r.Y != 1 || r.WaitTicks != 0 || r.Blocked || r.Battery != 99 {
		t.Fatalf("free aisle did not resume: %+v", r)
	}
	s.Tick()
	if s.state.Robots[0].Y != 2 || s.state.Robots[0].Battery != 98 {
		t.Fatal("occupant could not continue through its own aisle")
	}
	assertInvariants(t, s, s.cfg)
}

func TestNavigationReplanWaitsUntilNextTick(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ReplanAfter, cfg.BlockedAfter = 2, 3
	s := navigationSim(t, cfg, Point{1, 1}, Point{3, 1}, Point{3, 0})
	s.prepareNavigation(&s.state.Robots[0], Point{3, 1})
	original := slices.Clone(s.navigation["R1"].route)
	s.state.Robots[1].X, s.state.Robots[1].Y = 2, 1
	s.Tick()
	if !slices.Equal(s.navigation["R1"].route, original) {
		t.Fatal("replanned before threshold")
	}
	s.Tick()
	r := s.state.Robots[0]
	if r.X != 1 || r.Y != 1 || r.WaitTicks != 2 || r.Battery != 100 || slices.Equal(s.navigation["R1"].route, original) {
		t.Fatalf("successful replan moved or reset waiting: %+v, route=%v", r, s.navigation["R1"].route)
	}
	s.Tick()
	r = s.state.Robots[0]
	if r.X != 1 || r.Y != 0 || r.WaitTicks != 0 || r.Blocked || r.Battery != 99 {
		t.Fatalf("replacement route not used next tick: %+v", r)
	}
	assertInvariants(t, s, s.cfg)
}

func TestNavigationFailedReplanRetainsRoute(t *testing.T) {
	s := navigationSim(t, DefaultConfig(), Point{1, 1}, Point{3, 1}, Point{3, 0})
	s.prepareNavigation(&s.state.Robots[0], Point{3, 1})
	original := slices.Clone(s.navigation["R1"].route)
	s.state.Robots[1].X, s.state.Robots[1].Y = 2, 1
	s.state.Robots[2].X, s.state.Robots[2].Y = 3, 1
	for tick := 1; tick <= 25; tick++ {
		s.Tick()
		r := s.state.Robots[0]
		if r.X != 1 || r.Y != 1 || r.Battery != 100 || r.WaitTicks != tick || r.Blocked != (tick >= s.cfg.BlockedAfter) || r.JobID != "J1" || !slices.Equal(s.navigation["R1"].route, original) {
			t.Fatalf("failed replan lost route, job, or wait state at tick %d: %+v", tick, r)
		}
		assertInvariants(t, s, s.cfg)
	}
	if len(s.state.Faults) != 0 || s.state.Jobs[0].Status != JobAssigned {
		t.Fatal("traffic generated faults or dropped unfinished work")
	}
}

func TestNavigationOfflineGoalAndCountdown(t *testing.T) {
	s := navigationSim(t, DefaultConfig(), Point{1, 1}, Point{2, 1}, Point{3, 1})
	s.state.Robots[1].X, s.state.Robots[1].Y = 2, 1
	for tick := 1; tick <= s.cfg.BlockedAfter; tick++ {
		s.Tick()
	}
	if r := s.state.Robots[0]; !r.Blocked || r.WaitTicks != s.cfg.BlockedAfter || r.Battery != 100 || r.JobID != "J1" {
		t.Fatalf("offline goal did not block retained job: %+v", r)
	}
	s.unresponsiveTicks["R1"] = 1
	s.Tick()
	r := s.state.Robots[0]
	if r.Status != RobotOffline || r.WaitTicks != 0 || r.Blocked || r.JobID != "J1" || r.Battery != 100 || r.X != 1 || r.Y != 1 {
		t.Fatalf("countdown did not override waiting: %+v", r)
	}
	if _, exists := s.navigation["R1"]; exists {
		t.Fatal("offline transition retained obsolete navigation")
	}
	assertInvariants(t, s, s.cfg)
}

func TestNavigationPlanningObstacleModels(t *testing.T) {
	s := navigationSim(t, DefaultConfig(), Point{4, 1}, Point{4, 2}, Point{5, 2})
	r := s.state.Robots[0]
	other := &s.state.Robots[1]
	other.X, other.Y = 4, 2
	for _, status := range []RobotStatus{RobotOffline, RobotFaulted, RobotCharging, RobotMoving, RobotIdle} {
		other.Status = status
		parked := status == RobotOffline || status == RobotFaulted || status == RobotCharging
		if got := s.navigationBlocked(r, Point{4, 2}, false); got != parked {
			t.Fatalf("status %s parked=%v, want %v", status, got, parked)
		}
		if !s.navigationBlocked(r, Point{4, 2}, true) || s.navigationBlocked(r, Point{4, 1}, true) || s.navigationBlocked(r, Point{4, 3}, true) {
			t.Fatal("dynamic model failed to block other cell or exempt own cell/aisle")
		}
	}
	other.Status = RobotOffline
	r.X, r.Y = 1, 1
	if !s.navigationBlocked(r, Point{4, 5}, false) || s.navigationBlocked(r, Point{4, 0}, false) {
		t.Fatal("parked aisle restriction extended outside aisle or omitted aisle")
	}
}

func TestNavigationInvariantsAcrossLayouts(t *testing.T) {
	for _, layout := range []struct {
		name string
		cfg  Config
	}{{name: "default", cfg: DefaultConfig()}, {name: "warehouse", cfg: WarehouseConfig()}} {
		for _, seed := range []int64{1, 2, 3} {
			t.Run(fmt.Sprintf("%s/seed_%d", layout.name, seed), func(t *testing.T) {
				s, repeat := newTestSim(t, layout.cfg, seed), newTestSim(t, layout.cfg, seed)
				for tick := 0; tick <= 300; tick++ {
					assertInvariants(t, s, layout.cfg)
					if !reflect.DeepEqual(s.Snapshot(), repeat.Snapshot()) {
						t.Fatalf("nondeterministic state at tick %d", tick)
					}
					if tick < 300 {
						before := s.Snapshot()
						s.Tick()
						repeat.Tick()
						for i, robot := range s.state.Robots {
							dx, dy := robot.X-before.Robots[i].X, robot.Y-before.Robots[i].Y
							if dx*dx+dy*dy > 1 {
								t.Fatalf("%s moved more than one cell at tick %d", robot.ID, tick+1)
							}
						}
					}
				}
			})
		}
	}
}

func TestNavigationReplansAtSubsequentMultiples(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ReplanAfter, cfg.BlockedAfter = 3, 4
	s := navigationSim(t, cfg, Point{1, 1}, Point{2, 1}, Point{3, 1})
	s.state.Robots[1].X, s.state.Robots[1].Y = 2, 1
	for tick := 1; tick <= 4; tick++ {
		s.Tick()
	}
	s.state.Robots[1].Y = 2
	s.Tick()
	if len(s.navigation["R1"].route) != 0 || s.state.Robots[0].WaitTicks != 5 {
		t.Fatal("retry ran between threshold multiples")
	}
	s.Tick()
	r := s.state.Robots[0]
	if len(s.navigation["R1"].route) == 0 || r.X != 1 || r.Y != 1 || r.Battery != 100 || r.WaitTicks != 6 || !r.Blocked {
		t.Fatalf("retry did not preserve blocked/wait state for its whole tick: %+v", r)
	}
	s.Tick()
	r = s.state.Robots[0]
	if r.X != 2 || r.Y != 1 || r.WaitTicks != 0 || r.Blocked || r.Battery != 99 {
		t.Fatalf("successful movement failed to reset waiting: %+v", r)
	}
	assertInvariants(t, s, s.cfg)
}

func TestNavigationArrivalBatteryAndCleanup(t *testing.T) {
	for _, delivery := range []bool{false, true} {
		t.Run(fmt.Sprintf("delivery_%v", delivery), func(t *testing.T) {
			pickup, goal := Point{2, 1}, Point{3, 1}
			if delivery {
				pickup, goal = Point{1, 1}, Point{2, 1}
			}
			s := navigationSim(t, DefaultConfig(), Point{1, 1}, pickup, goal)
			s.state.Robots[0].Battery = 1
			s.state.Robots[0].WaitTicks = 2
			s.Tick()
			r, job := s.state.Robots[0], s.state.Jobs[0]
			wantJob := JobInProgress
			wantID := "J1"
			if delivery {
				wantJob, wantID = JobDone, ""
			}
			if r.Battery != 0 || r.Status != RobotFaulted || r.JobID != wantID || r.WaitTicks != 0 || r.Blocked || job.Status != wantJob || job.AssignedRobot != "R1" {
				t.Fatalf("arrival/depletion precedence: robot=%+v job=%+v", r, job)
			}
			if _, exists := s.navigation["R1"]; exists {
				t.Fatal("fault transition retained route")
			}
			position := Point{r.X, r.Y}
			s.Tick()
			if (Point{s.state.Robots[0].X, s.state.Robots[0].Y}) != position || s.state.Robots[0].JobID != wantID {
				t.Fatal("faulted robot moved or dropped unfinished work")
			}
			assertInvariants(t, s, s.cfg)
		})
	}
}

func TestNavigationParkedObstacleAndOwnership(t *testing.T) {
	s := navigationSim(t, DefaultConfig(), Point{1, 1}, Point{3, 1}, Point{3, 0})
	s.state.Robots[1].X, s.state.Robots[1].Y = 2, 1
	s.Tick()
	if r := s.state.Robots[0]; r.Y != 0 || r.X != 1 || r.WaitTicks != 0 || r.Battery != 99 {
		t.Fatalf("initial parked-obstacle planning failed: %+v", r)
	}
	before := slices.Clone(s.navigation["R1"].route)
	route, err := s.PlanRoute(Point{1, 0}, Point{3, 1})
	if err != nil {
		t.Fatal(err)
	}
	route[0] = Point{-1, -1}
	snapshot := s.Snapshot()
	snapshot.Robots[0].X = 99
	if !slices.Equal(before, s.navigation["R1"].route) {
		t.Fatal("public reads exposed private routes")
	}
	assertInvariants(t, s, s.cfg)
}

func TestNavigationWallInvariant(t *testing.T) {
	cfg := WarehouseConfig()
	s := newTestSim(t, cfg, 42)
	s.state.Robots[0].X, s.state.Robots[0].Y = 2, 2
	if err := checkInvariants(s.Snapshot(), cfg); err == nil || err.Error()[:3] != "I2:" {
		t.Fatalf("robot in wall accepted: %v", err)
	}
}

func TestNavigationStatusTransitionsClearWaiting(t *testing.T) {
	for _, status := range []RobotStatus{RobotIdle, RobotFaulted, RobotOffline, RobotCharging} {
		t.Run(string(status), func(t *testing.T) {
			s := navigationSim(t, DefaultConfig(), Point{1, 1}, Point{3, 1}, Point{3, 0})
			r := &s.state.Robots[0]
			s.prepareNavigation(r, Point{3, 1})
			r.WaitTicks, r.Blocked, r.Status = 10, true, status
			if status == RobotFaulted {
				r.Battery = 0
			}
			if status == RobotIdle || status == RobotCharging {
				r.JobID, s.state.Jobs[0].Status = "", JobDone
			}
			if status == RobotCharging {
				r.X, r.Y, r.Battery = 0, 0, 80
				s.state.Stations[0].OccupiedBy = r.ID
			}
			before := Point{r.X, r.Y}
			s.Tick()
			if r.Status != status || r.WaitTicks != 0 || r.Blocked || (Point{r.X, r.Y}) != before {
				t.Fatalf("non-moving state did not clear waiting: %+v", r)
			}
			if _, exists := s.navigation[r.ID]; exists {
				t.Fatal("non-moving robot retained obsolete route")
			}
			if (status == RobotFaulted || status == RobotOffline) && r.JobID != "J1" {
				t.Fatal("status transition dropped unfinished work")
			}
			assertInvariants(t, s, s.cfg)
		})
	}
}
