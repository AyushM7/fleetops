package sim

import (
	"reflect"
	"slices"
	"testing"
)

func seed99IdleMouthScene(t *testing.T) *Sim {
	t.Helper()
	s, repeat := newTestSim(t, WarehouseConfig(), 99), newTestSim(t, WarehouseConfig(), 99)
	o := newFinalSoakObserver(finalSoakScenario{name: "S3"}, 99)
	for tick := 1; tick <= 142; tick++ {
		s.Tick()
		repeat.Tick()
		injectNaturalSoakFault(t, s, repeat, o)
		diagAssertParity(t, s, repeat)
	}
	owner, waiter := s.faultRobot("R2"), s.faultRobot("R1")
	if owner.Status != RobotIdle || owner.Battery != 16 || owner.X != 4 || owner.Y != 8 || waiter.X != 4 || waiter.Y != 1 {
		t.Fatal("seed99 idle mouth obstruction did not reproduce")
	}
	if _, feasible := s.relocationRoute(*owner); feasible {
		t.Fatal("obstructed owner already has an affordable relocation")
	}
	waiter.WaitTicks, waiter.Blocked = s.cfg.RecoverAfter, true
	return s
}

func TestIdleAisleMouthYield(t *testing.T) {
	s := seed99IdleMouthScene(t)
	owner := *s.faultRobot("R2")
	job := s.faultRobot("R1").JobID
	events := recoverNow(s)
	waiter := s.faultRobot("R1")
	if len(events) != 1 || events[0].robot != waiter.ID || waiter.X != 5 || waiter.Y != 1 || waiter.Battery != 83 || waiter.JobID != job {
		t.Fatalf("yield did not preserve the mission and retreat budget: %v %+v", events, waiter)
	}
	if *s.faultRobot("R2") != owner || s.recovery[waiter.ID].hold != s.cfg.HoldTicks || !s.recovery[waiter.ID].resume {
		t.Fatal("yield moved owner or lost the existing hold/resume protocol")
	}
	nav, feasible := s.relocationRoute(owner)
	if !feasible || nav.target != (Point{X: 4, Y: 0}) || len(nav.route) != 8 || !s.relocationAffordable(owner, nav.target, len(nav.route)) {
		t.Fatalf("yield did not enable the energy-safe exit: %+v", nav)
	}
	assertInvariants(t, s, s.cfg)
	for tick := 0; tick < 30; tick++ {
		before := s.Snapshot()
		s.Tick()
		assertInvariants(t, s, s.cfg)
		for i, robot := range s.state.Robots {
			dx, dy := robot.X-before.Robots[i].X, robot.Y-before.Robots[i].Y
			if dx*dx+dy*dy > 1 || robot.Status == RobotFaulted {
				t.Fatalf("unsafe yield continuation: %+v", robot)
			}
		}
	}
	if s.cfg.onAisle(Point{X: s.faultRobot("R2").X, Y: s.faultRobot("R2").Y}) || s.faultRobot("R1").WaitTicks > 40 {
		t.Fatal("owner did not clear or waiter remained obstructed")
	}
}

func TestIdleAisleMouthYieldGuards(t *testing.T) {
	for _, name := range []string{"recovery disabled", "charging disabled", "short wait", "moved", "hold", "resuming", "retreat limit", "offline owner", "owner moving", "owner can relocate", "owner stranded", "waiter energy", "blocked retreat", "yield insufficient"} {
		t.Run(name, func(t *testing.T) {
			s := seed99IdleMouthScene(t)
			waiter, owner := s.faultRobot("R1"), s.faultRobot("R2")
			skip := make([]bool, len(s.state.Robots))
			recovery := s.recovery[waiter.ID]
			switch name {
			case "recovery disabled":
				s.cfg.Recovery = false
			case "charging disabled":
				s.cfg.AutoCharge = false
			case "short wait":
				waiter.WaitTicks--
			case "moved":
				skip[0] = true
			case "hold":
				recovery.hold = 1
			case "resuming":
				recovery.resume = true
			case "retreat limit":
				for tick := s.state.Tick - 10; tick < s.state.Tick; tick++ {
					recovery.retreats = append(recovery.retreats, tick)
				}
			case "offline owner":
				owner.Status = RobotOffline
			case "owner moving":
				owner.Status = RobotMoving
			case "owner can relocate":
				owner.Battery = 17
			case "owner stranded":
				owner.Battery = 12
			case "waiter energy":
				waiter.Battery = 1
			case "blocked retreat":
				s.faultRobot("R5").X, s.faultRobot("R5").Y = 5, 1
				s.faultRobot("R6").X, s.faultRobot("R6").Y = 3, 1
				s.faultRobot("R3").X, s.faultRobot("R3").Y = 4, 0
			case "yield insufficient":
				blockIdleMouthExit(s)
			}
			s.recovery[waiter.ID] = recovery
			before := diagJSON(diagSnapshot(s))
			var events []recoveryEvent
			if name == "recovery disabled" {
				events = s.recoverTraffic(skip)
			} else {
				events = s.recoverIdleAisleMouths(skip)
			}
			if len(events) != 0 || diagJSON(diagSnapshot(s)) != before {
				t.Fatalf("rejected yield changed state: %v", events)
			}
		})
	}
}

func TestIdleAisleMouthYieldProbeIsolation(t *testing.T) {
	s := seed99IdleMouthScene(t)
	entry := s.recovery["R1"]
	entry.retreats = make([]int, 1, 10)
	entry.retreats[0] = 1
	s.recovery["R1"] = entry
	beforeHistory := slices.Clone(entry.retreats[:cap(entry.retreats)])
	blockIdleMouthExit(s)
	before := diagJSON(diagSnapshot(s))
	if s.yieldIdleAisleMouth(0, 1) || diagJSON(diagSnapshot(s)) != before || !slices.Equal(entry.retreats[:cap(entry.retreats)], beforeHistory) {
		t.Fatal("failed probe leaked state or mutated shared retreat storage")
	}
}

func blockIdleMouthExit(s *Sim) {
	// After a rightward yield, these idle robots close the other two exits.
	// Idle occupancy affects movement, while the parked-obstacle energy
	// estimate still leaves the owner queued for charging.
	for id, point := range map[string]Point{"R5": {X: 4, Y: 0}, "R3": {X: 3, Y: 1}} {
		robot := s.faultRobot(id)
		robot.X, robot.Y, robot.Status, robot.JobID = point.X, point.Y, RobotIdle, ""
	}
}

func TestIdleAisleMouthYieldNumericOrder(t *testing.T) {
	a, b := seed99IdleMouthScene(t), seed99IdleMouthScene(t)
	slices.Reverse(b.state.Robots)
	if !reflect.DeepEqual(recoverNow(a), recoverNow(b)) {
		t.Fatal("yield selection depends on robot slice order")
	}
	for _, robot := range a.state.Robots {
		if robot != *b.faultRobot(robot.ID) || !reflect.DeepEqual(a.navigation[robot.ID], b.navigation[robot.ID]) || !reflect.DeepEqual(a.recovery[robot.ID], b.recovery[robot.ID]) {
			t.Fatalf("nondeterministic yield for %s", robot.ID)
		}
	}
}
