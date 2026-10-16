package sim

import (
	"slices"
	"testing"
)

func TestQueuedChargingLowPower(t *testing.T) {
	for _, tc := range []struct {
		name           string
		battery, drain int
		change         func(*Sim)
		queued         bool
	}{
		{"queued boundary", 3, 1, func(*Sim) {}, true},
		{"stranded", 2, 1, func(*Sim) {}, false},
		{"movement multiplier", 5, 2, func(*Sim) {}, true},
		{"multiplier stranded", 4, 2, func(*Sim) {}, false},
		{"disabled", 10, 1, func(s *Sim) { s.cfg.AutoCharge = false }, false},
		{"above threshold", 40, 1, func(*Sim) {}, false},
		{"ready", 10, 1, func(s *Sim) { s.state.Stations[0].OccupiedBy = ""; s.state.Robots[1].X, s.state.Robots[1].Y = 3, 11 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := autochargeSim(t, DefaultConfig(), Point{2, 0}, tc.battery)
			for i, p := range []Point{{0, 0}, {19, 0}} {
				r := &s.state.Robots[i+1]
				r.X, r.Y = p.X, p.Y
				s.state.Stations[i].OccupiedBy = r.ID
			}
			s.cfg.MovingDrain = tc.drain
			tc.change(s)
			s.state.Tick = 20
			r := &s.state.Robots[0]
			if s.queuedForCharging(*r) != tc.queued {
				t.Fatal("wrong queue eligibility")
			}
			s.stepIdle(r)
			want := tc.battery
			if !tc.queued {
				want--
			}
			if r.Battery != want {
				t.Fatalf("battery=%d want%d", r.Battery, want)
			}
			assertInvariants(t, s, s.cfg)
		})
	}
}

func TestQueuedChargingReevaluation(t *testing.T) {
	s := autochargeSim(t, DefaultConfig(), Point{2, 0}, 3)
	for i, p := range []Point{{0, 0}, {19, 0}} {
		r := &s.state.Robots[i+1]
		r.X, r.Y = p.X, p.Y
		s.state.Stations[i].OccupiedBy = r.ID
	}
	r := &s.state.Robots[0]
	for tick := 20; tick <= 80; tick += 20 {
		s.state.Tick = tick
		s.stepIdle(r)
		if r.Battery != 3 {
			t.Fatal("genuine queue drained")
		}
	}
	s.state.Stations[0].OccupiedBy = ""
	s.state.Robots[1].X, s.state.Robots[1].Y = 3, 11
	s.stepIdle(r)
	if r.Battery != 2 || s.queuedForCharging(*r) {
		t.Fatal("ready/stranded robot retained an exemption")
	}
	for _, status := range []RobotStatus{RobotMoving, RobotFaulted, RobotOffline, RobotCharging} {
		copy := *r
		copy.Status = status
		if s.queuedForCharging(copy) {
			t.Fatalf("exempted %s", status)
		}
	}
	copy := *r
	copy.JobID = "J1"
	if s.queuedForCharging(copy) {
		t.Fatal("job owner exempted")
	}
	copy = *r
	copy.X, copy.Y = 0, 0
	copy.Battery = 1
	if s.queuedForCharging(copy) {
		t.Fatal("robot ready on its station classified queued")
	}
}

func setJobCells(t *testing.T, s *Sim, id string, from, to Point) {
	t.Helper()
	for i := range s.state.Jobs {
		job := &s.state.Jobs[i]
		if job.ID == id && job.Status == JobPending {
			job.From, job.To = from, to
			return
		}
	}
	t.Fatalf("pending job %s not found", id)
}

func movementSim(t *testing.T, cfg Config) *Sim {
	t.Helper()
	s := newTestSim(t, cfg, 1)
	for _, job := range s.state.Jobs {
		setJobCells(t, s, job.ID, Point{1, 5}, Point{19, 5})
	}
	return s
}

func TestNumericDispatch(t *testing.T) {
	cfg := DefaultConfig()
	cfg.InitialJobs = 10
	s := movementSim(t, cfg)
	for i := range s.state.Jobs {
		if !slices.Contains([]string{"J2", "J9", "J10"}, s.state.Jobs[i].ID) {
			s.state.Jobs[i].Status = JobDone
		}
	}
	slices.Reverse(s.state.Jobs)
	slices.Reverse(s.state.Robots)
	for tick := 1; tick <= 2; tick++ {
		s.Tick()
		for i, want := range []string{"J2", "J9", "J10", "", "", ""} {
			robot := s.state.Robots[i]
			if idNumber(robot.ID) != i+1 || robot.JobID != want {
				t.Fatalf("tick %d robot %+v, want job %s in numeric robot order", tick, robot, want)
			}
			for _, job := range s.state.Jobs {
				if job.ID == want && (job.AssignedRobot != robot.ID || job.Status != JobAssigned) {
					t.Fatalf("assignment changed or duplicated: %+v", job)
				}
			}
		}
	}
}

func TestDispatchEligibility(t *testing.T) {
	for _, tc := range []struct {
		name    string
		auto    bool
		battery int
		status  RobotStatus
		wantJob bool
	}{
		{"disabled", false, 100, RobotIdle, false},
		{"below threshold", true, 29, RobotIdle, false},
		{"at threshold", true, 30, RobotIdle, true},
		{"faulted", true, 100, RobotFaulted, false},
		{"offline", true, 100, RobotOffline, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.AutoDispatch, cfg.InitialJobs = tc.auto, 1
			s := movementSim(t, cfg)
			for i := range s.state.Robots {
				s.state.Robots[i].Status = RobotFaulted
			}
			robot := &s.state.Robots[0]
			robot.Battery, robot.Status = tc.battery, tc.status
			before := *robot
			if tc.status == RobotFaulted || tc.status == RobotOffline {
				s.state.Tick = 19 // Neither status may drain on an idle-drain tick.
			}
			s.Tick()
			if tc.wantJob {
				if robot.JobID != "J1" || robot.Status != RobotMoving || robot.Y != 10 || robot.Battery != 29 {
					t.Fatalf("assignment must move immediately: %+v", robot)
				}
			} else if *robot != before || s.state.Jobs[0].Status != JobPending || s.state.Jobs[0].AssignedRobot != "" {
				t.Fatalf("ineligible robot or job changed: %+v, %+v", robot, s.state.Jobs[0])
			}
		})
	}
}

func TestIdleDrainBeforeDispatch(t *testing.T) {
	for _, battery := range []int{30, 31} {
		s := movementSim(t, DefaultConfig())
		s.state.Tick, s.state.Robots[0].Battery = 19, battery
		s.Tick()
		want := RobotState{ID: "R1", X: 1, Y: 11, Battery: 29, Status: RobotIdle, JobID: ""}
		if battery == 31 {
			want.Y, want.Status, want.JobID = 10, RobotMoving, "J1"
		}
		if s.state.Robots[0] != want {
			t.Fatalf("starting battery %d: %+v, want %+v", battery, s.state.Robots[0], want)
		}
	}
}

func TestMovementRoute(t *testing.T) {
	cfg := DefaultConfig()
	cfg.InitialJobs = 1
	s := movementSim(t, cfg)
	setJobCells(t, s, "J1", Point{3, 10}, Point{2, 9})
	for i, want := range []struct {
		position Point
		battery  int
		status   JobStatus
	}{
		{Point{3, 10}, 99, JobInProgress},
		{Point{3, 9}, 98, JobInProgress},
		{Point{2, 9}, 97, JobDone},
		{Point{2, 9}, 97, JobDone},
		{Point{2, 9}, 97, JobDone},
		{Point{2, 9}, 97, JobDone},
	} {
		s.Tick()
		robot, job := s.state.Robots[1], s.state.Jobs[0]
		wantRobot := RobotState{ID: "R2", X: want.position.X, Y: want.position.Y, Battery: want.battery, Status: RobotMoving, JobID: "J1"}
		if want.status == JobDone {
			wantRobot.Status, wantRobot.JobID = RobotIdle, ""
		}
		if robot != wantRobot || job.Status != want.status || job.AssignedRobot != "R2" {
			t.Fatalf("tick %d: robot %+v job %+v; want %+v, %s", i+1, robot, job, wantRobot, want.status)
		}
		s.cfg.AutoDispatch = false // Existing work must continue with new dispatch disabled.
	}
}

func TestMovementBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name        string
		from, to    Point
		battery     int
		alreadyAtTo bool
		want        RobotState
		jobStatus   JobStatus
	}{
		{"zero first leg", Point{1, 11}, Point{2, 11}, 100, false, RobotState{ID: "R1", X: 2, Y: 11, Battery: 97, Status: RobotIdle, JobID: ""}, JobDone},
		{"active depletion", Point{1, 5}, Point{19, 5}, 1, false, RobotState{ID: "R1", X: 1, Y: 10, Battery: 0, Status: RobotFaulted, JobID: "J1"}, JobAssigned},
		{"completed depletion", Point{1, 11}, Point{2, 11}, 1, false, RobotState{ID: "R1", X: 2, Y: 11, Battery: 0, Status: RobotFaulted, JobID: ""}, JobDone},
		{"already at destination", Point{1, 5}, Point{1, 11}, 1, true, RobotState{ID: "R1", X: 1, Y: 11, Battery: 1, Status: RobotIdle, JobID: ""}, JobDone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.InitialJobs, cfg.MovingDrain = 1, 3
			cfg.MinAssignBattery, cfg.LowBatteryInjectLevel = 1, 0
			s := movementSim(t, cfg)
			setJobCells(t, s, "J1", tc.from, tc.to)
			s.state.Robots[0].Battery = tc.battery
			// Existing work may deplete; the scheduler now rejects infeasible new work.
			if tc.battery == 1 && !tc.alreadyAtTo {
				s.state.Robots[0].Status, s.state.Robots[0].JobID = RobotMoving, "J1"
				s.state.Jobs[0].Status, s.state.Jobs[0].AssignedRobot = JobAssigned, "R1"
			}
			if tc.alreadyAtTo {
				s.state.Robots[0].Status, s.state.Robots[0].JobID = RobotMoving, "J1"
				s.state.Jobs[0].Status, s.state.Jobs[0].AssignedRobot = JobInProgress, "R1"
			}
			s.Tick()
			if s.state.Robots[0] != tc.want || s.state.Jobs[0].Status != tc.jobStatus || s.state.Jobs[0].AssignedRobot != "R1" || len(s.state.Faults) != 0 {
				t.Fatalf("unexpected transition: robot %+v job %+v faults %v", s.state.Robots[0], s.state.Jobs[0], s.state.Faults)
			}
		})
	}
	for _, tc := range []struct{ target, want Point }{
		{Point{3, 3}, Point{3, 2}},
		{Point{1, 3}, Point{1, 2}},
		{Point{2, 3}, Point{2, 3}},
		{Point{2, 1}, Point{2, 1}},
		{Point{2, 2}, Point{2, 2}},
	} {
		route, err := newTestSim(t, DefaultConfig(), 1).PlanRoute(Point{2, 2}, tc.target)
		if err != nil {
			t.Fatal(err)
		}
		got := Point{2, 2}
		if len(route) > 0 {
			got = route[0]
		}
		if got != tc.want {
			t.Errorf("step toward %v = %v, want %v", tc.target, got, tc.want)
		}
	}
}

func TestIdleDrainAndCountdown(t *testing.T) {
	for _, countdown := range []bool{false, true} {
		cfg := DefaultConfig()
		cfg.AutoDispatch, cfg.IdleDrainEvery = false, 2
		s := movementSim(t, cfg)
		robot := &s.state.Robots[0]
		robot.Battery = 1
		if countdown {
			s.unresponsiveTicks["R1"] = 4
		}
		for tick := 1; tick <= 6; tick++ {
			s.Tick()
			want := RobotState{ID: "R1", X: 1, Y: 11, Battery: 0, Status: RobotFaulted}
			if tick == 1 {
				want.Battery, want.Status = 1, RobotIdle
			}
			if countdown && tick >= 4 {
				want.Status = RobotOffline
			}
			if *robot != want || len(s.state.Faults) != 0 || s.state.Jobs[0].Status != JobPending {
				t.Fatalf("countdown %v tick %d: %+v, want %+v", countdown, tick, robot, want)
			}
			if countdown && s.unresponsiveTicks["R1"] != max(0, 4-tick) {
				t.Fatal("countdown stopped while faulted")
			}
		}
	}
}

func TestUnresponsiveCountdown(t *testing.T) {
	for _, tc := range []struct {
		name            string
		battery, delay  int
		lastTick        int
		actingBatteries []int
	}{
		{"A", 2, 3, 20, []int{1, 0}},
		{"B", 1, 1, 10, []int{}},
		{"normal", 99, 3, 20, []int{98, 97}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := movementSim(t, DefaultConfig())
			s.Tick()
			robot := &s.state.Robots[0]
			want := RobotState{ID: "R1", X: 1, Y: 10, Battery: 99, Status: RobotMoving, JobID: "J1"}
			if *robot != want {
				t.Fatalf("tick 1 = %+v, want %+v", robot, want)
			}
			robot.Battery, want.Battery = tc.battery, tc.battery
			s.unresponsiveTicks["R1"] = tc.delay
			stations := slices.Clone(s.state.Stations)
			for tick := 2; tick <= tc.lastTick; tick++ {
				offset := tick - 2
				if offset < len(tc.actingBatteries) {
					want.Y--
					want.Battery = tc.actingBatteries[offset]
					if want.Battery == 0 {
						want.Status = RobotFaulted
					}
				} else {
					want.Status = RobotOffline
				}
				s.Tick()
				if *robot != want || s.unresponsiveTicks["R1"] != max(0, tc.delay-(tick-1)) {
					t.Fatalf("tick %d = %+v countdown %d, want %+v", tick, robot, s.unresponsiveTicks["R1"], want)
				}
				if s.state.Jobs[0].Status != JobAssigned || s.state.Jobs[0].AssignedRobot != "R1" || len(s.state.Faults) != 0 || !slices.Equal(stations, s.state.Stations) {
					t.Fatalf("tick %d changed assignment, faults, or stations", tick)
				}
				for _, job := range s.state.Jobs[1:] {
					if job.AssignedRobot == "R1" {
						t.Fatalf("offline or busy R1 took another job: %+v", job)
					}
				}
			}
		})
	}
}
