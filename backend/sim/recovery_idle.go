package sim

import (
	"maps"
	"slices"
)

func (s *Sim) recoverIdleAisleMouths(skip []bool) []recoveryEvent {
	if !s.cfg.AutoCharge {
		return nil
	}
	order := s.recoveryOrder()
	blockers := s.recoveryBlockers(order)
	var events []recoveryEvent
	for _, i := range order {
		robot := s.state.Robots[i]
		recovery := s.recovery[robot.ID]
		if skip[i] || robot.Status != RobotMoving || robot.WaitTicks < s.cfg.RecoverAfter || recovery.hold > 0 || recovery.resume || s.retreatLimited(robot.ID) {
			continue
		}
		j := blockers[i]
		if j < 0 {
			continue
		}
		owner := s.state.Robots[j]
		next, required := s.requiredCell(i)
		if !required || !s.cfg.onAisle(next) || robot.X != next.X || s.cfg.onAisle(Point{X: robot.X, Y: robot.Y}) || owner.X != next.X || !s.cfg.onAisle(Point{X: owner.X, Y: owner.Y}) || !s.queuedForCharging(owner) {
			continue
		}
		if _, feasible := s.relocationRoute(owner); feasible {
			continue
		}
		if s.yieldIdleAisleMouth(i, j) {
			events = append(events, recoveryEvent{robot: robot.ID, reason: "yield mouth for queued idle " + owner.ID})
		}
	}
	return events
}

func (s *Sim) yieldIdleAisleMouth(waiter, owner int) bool {
	// Test the existing retreat and full relocation budget together before
	// changing live state. A failed probe must not consume retreat history.
	probe := *s
	probe.state.Robots = slices.Clone(s.state.Robots)
	probe.navigation = maps.Clone(s.navigation)
	probe.recovery = maps.Clone(s.recovery)
	id := s.state.Robots[waiter].ID
	recovery := probe.recovery[id]
	recovery.retreats = slices.Clone(recovery.retreats)
	probe.recovery[id] = recovery
	if !probe.retreatRobot(&probe.state.Robots[waiter], []int{waiter, owner}) {
		return false
	}
	if _, feasible := probe.relocationRoute(probe.state.Robots[owner]); !feasible {
		return false
	}
	s.state.Robots[waiter] = probe.state.Robots[waiter]
	s.navigation[id] = probe.navigation[id]
	s.recovery[id] = probe.recovery[id]
	return true
}
