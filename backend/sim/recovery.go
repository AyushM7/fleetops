package sim

import (
	"fmt"
	"slices"
	"strings"
)

type robotRecovery struct {
	hold     int
	resume   bool
	retreats []int
	cycle    recoveryCycleProgress
}

type recoveryCycleProgress struct {
	key, job        string
	target          Point
	age, best, tick int
}

type recoveryEvent struct {
	cycle  []string
	robot  string
	reason string
}

func (s *Sim) prepareRecovery(robot *RobotState) bool {
	if !s.cfg.Recovery || robot.Status != RobotMoving {
		return false
	}
	recovery := s.recovery[robot.ID]
	if recovery.hold > 0 {
		recovery.hold--
		s.recovery[robot.ID] = recovery
		return true
	}
	if !recovery.resume {
		return false
	}
	if robot.WaitTicks == 0 || (robot.WaitTicks+1)%s.cfg.ReplanAfter == 0 {
		if s.planNavigation(robot, s.recoveryTarget(*robot), true) {
			recovery.resume = false
			s.recovery[robot.ID] = recovery
			return false
		}
	}
	robot.WaitTicks++
	robot.Blocked = robot.WaitTicks >= s.cfg.BlockedAfter
	return true
}

func (s *Sim) recoveryTarget(robot RobotState) Point {
	for _, job := range s.state.Jobs {
		if job.ID == robot.JobID && job.AssignedRobot == robot.ID {
			if job.Status == JobAssigned {
				return job.From
			}
			return job.To
		}
	}
	return s.navigation[robot.ID].target
}

func (s *Sim) recoveryOrder() []int {
	order := make([]int, len(s.state.Robots))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(a, b int) int { return idNumber(s.state.Robots[a].ID) - idNumber(s.state.Robots[b].ID) })
	return order
}

func (s *Sim) requiredCell(index int) (Point, bool) {
	robot := s.state.Robots[index]
	recovery := s.recovery[robot.ID]
	// resume also covers the last held tick, whose counter has just reached zero.
	if recovery.hold > 0 || recovery.resume && robot.WaitTicks == 0 {
		return Point{}, false
	}
	nav := s.navigation[robot.ID]
	if len(nav.route) == 0 || nav.target != s.recoveryTarget(robot) {
		return Point{}, false
	}
	return nav.route[0], true
}

func (s *Sim) recoveryBlockers(order []int) []int {
	blockers := make([]int, len(order))
	for i := range blockers {
		blockers[i] = -1
		next, ok := s.requiredCell(i)
		if !ok {
			continue
		}
		// Cell occupancy takes precedence over ownership of the entered aisle.
		for _, j := range order {
			other := s.state.Robots[j]
			if j != i && next == (Point{X: other.X, Y: other.Y}) {
				blockers[i] = j
				break
			}
		}
		if blockers[i] >= 0 || !s.cfg.onAisle(next) {
			continue
		}
		robot := s.state.Robots[i]
		if robot.X == next.X && s.cfg.onAisle(Point{X: robot.X, Y: robot.Y}) {
			continue
		}
		for _, j := range order {
			other := s.state.Robots[j]
			if j != i && other.X == next.X && s.cfg.onAisle(Point{X: other.X, Y: other.Y}) {
				blockers[i] = j
				break
			}
		}
	}
	return blockers
}

func recoveryCycles(order, blockers []int) [][]int {
	visited := make([]bool, len(order))
	cycles := [][]int{}
	for _, start := range order {
		path := []int{}
		for at := start; at >= 0 && !visited[at]; at = blockers[at] {
			if offset := slices.Index(path, at); offset >= 0 {
				cycle := slices.Clone(path[offset:])
				slices.SortFunc(cycle, func(a, b int) int { return slices.Index(order, a) - slices.Index(order, b) })
				cycles = append(cycles, cycle)
				break
			}
			path = append(path, at)
		}
		for _, at := range path {
			visited[at] = true
		}
	}
	slices.SortFunc(cycles, func(a, b []int) int { return slices.Index(order, a[0]) - slices.Index(order, b[0]) })
	return cycles
}

func (s *Sim) recoverTraffic(skip []bool) []recoveryEvent {
	if !s.cfg.Recovery {
		return nil
	}
	trigger := false
	for _, robot := range s.state.Robots {
		trigger = trigger || robot.WaitTicks >= s.cfg.RecoverAfter
	}
	order := s.recoveryOrder()
	blockers := s.recoveryBlockers(order)
	s.recurringRecoveryBlockers(order, blockers)
	cycles := recoveryCycles(order, blockers)
	events := []recoveryEvent{}
	tracked := make(map[int]bool)
	for _, cycle := range cycles {
		healthy := true
		for _, i := range cycle {
			status := s.state.Robots[i].Status
			healthy = healthy && (status == RobotIdle || status == RobotMoving)
		}
		if healthy {
			for _, i := range cycle {
				tracked[i] = true
			}
			aged := s.recordRecoveryCycle(cycle)
			if trigger || aged {
				event := s.recoverCycle(cycle, skip)
				events = append(events, event)
				if event.robot != "" {
					for _, i := range cycle {
						s.clearRecoveryCycle(s.state.Robots[i].ID)
					}
				}
			}
		}
	}
	for _, i := range order {
		if !tracked[i] {
			s.clearRecoveryCycle(s.state.Robots[i].ID)
		}
	}
	return events
}

func (s *Sim) clearRecoveryCycle(id string) {
	entry := s.recovery[id]
	if entry.cycle.key != "" {
		entry.cycle = recoveryCycleProgress{}
		s.recovery[id] = entry
	}
}

func (s *Sim) recoveryDistance(robot RobotState) int {
	// Static shortest distance is a low-water mark: returning after a detour
	// is not new progress until the mission becomes closer than before.
	route, err := s.PlanRoute(Point{X: robot.X, Y: robot.Y}, s.recoveryTarget(robot))
	if err != nil {
		return -1
	}
	return len(route)
}

func (s *Sim) continuingRecoveryCycle(robot RobotState) bool {
	progress := s.recovery[robot.ID].cycle
	return robot.Status == RobotMoving && progress.key != "" && progress.job == robot.JobID && progress.target == s.recoveryTarget(robot) &&
		(progress.tick == s.state.Tick || progress.tick == s.state.Tick-1) && s.recoveryDistance(robot) >= progress.best
}

// A previously observed cycle is unresolved while its replacement routes still
// contain an opposed edge. This bridges the free first step of a backtrack;
// unrelated future crossings never initiate cycle tracking.
func (s *Sim) recurringRecoveryBlockers(order, blockers []int) {
	for _, i := range order {
		robot := s.state.Robots[i]
		if blockers[i] >= 0 || !s.continuingRecoveryCycle(robot) {
			continue
		}
		for _, j := range order {
			other := s.state.Robots[j]
			if i != j && s.recovery[robot.ID].cycle.key == s.recovery[other.ID].cycle.key && s.continuingRecoveryCycle(other) && s.opposedRecoveryRoutes(robot, other) {
				blockers[i] = j
				break
			}
		}
	}
}

func (s *Sim) opposedRecoveryRoutes(a, b RobotState) bool {
	an, bn := s.navigation[a.ID], s.navigation[b.ID]
	if an.target != s.recoveryTarget(a) || bn.target != s.recoveryTarget(b) {
		return false
	}
	previousA := Point{X: a.X, Y: a.Y}
	for _, nextA := range an.route {
		previousB := Point{X: b.X, Y: b.Y}
		for _, nextB := range bn.route {
			if previousA == nextB && nextA == previousB {
				return true
			}
			previousB = nextB
		}
		previousA = nextA
	}
	return false
}

func (s *Sim) recordRecoveryCycle(cycle []int) bool {
	ids := make([]string, len(cycle))
	distances := make([]int, len(cycle))
	for n, i := range cycle {
		robot := s.state.Robots[i]
		ids[n] = robot.ID
		distances[n] = s.recoveryDistance(robot)
		if distances[n] < 0 {
			for _, j := range cycle {
				s.clearRecoveryCycle(s.state.Robots[j].ID)
			}
			return false
		}
	}
	key := strings.Join(ids, ",")
	reset := false
	age := 1
	for n, i := range cycle {
		robot := s.state.Robots[i]
		old := s.recovery[robot.ID].cycle
		reset = reset || old.key != key || old.job != robot.JobID || old.target != s.recoveryTarget(robot) || distances[n] < old.best || old.tick < s.state.Tick-1 || old.tick > s.state.Tick
		if old.tick == s.state.Tick {
			age = max(age, old.age)
		} else {
			age = max(age, old.age+1)
		}
	}
	if reset {
		age = 1
	}
	for n, i := range cycle {
		robot := s.state.Robots[i]
		entry := s.recovery[robot.ID]
		best := distances[n]
		if !reset {
			best = min(best, entry.cycle.best)
		}
		entry.cycle = recoveryCycleProgress{key: key, job: robot.JobID, target: s.recoveryTarget(robot), age: age, best: best, tick: s.state.Tick}
		s.recovery[robot.ID] = entry
	}
	return age >= s.cfg.RecoverAfter
}

func (s *Sim) recoverCycle(cycle []int, skip []bool) recoveryEvent {
	event := recoveryEvent{}
	for _, i := range cycle {
		event.cycle = append(event.cycle, s.state.Robots[i].ID)
	}
	reasons := []string{}
	candidates := slices.Clone(cycle)
	slices.SortFunc(candidates, func(a, b int) int {
		return idNumber(s.state.Robots[b].ID) - idNumber(s.state.Robots[a].ID)
	})
	// A legal but unaffordable higher-ID retreat must not suppress a feasible mover.
	for _, i := range candidates {
		robot := &s.state.Robots[i]
		recovery := s.recovery[robot.ID]
		if skip[i] || recovery.hold > 0 || recovery.resume && robot.WaitTicks == 0 || s.cfg.onAisle(Point{X: robot.X, Y: robot.Y}) {
			reasons = append(reasons, robot.ID+": moved/skipped, holding, or inside aisle")
			continue
		}
		if s.retreatLimited(robot.ID) {
			reasons = append(reasons, robot.ID+": rolling retreat limit")
			continue
		}
		if s.retreatRobot(robot, cycle) {
			event.robot = robot.ID
			return event
		}
		reasons = append(reasons, robot.ID+": no legal energy-feasible retreat")
	}
	event.reason = strings.Join(reasons, "; ")
	return event
}

func (s *Sim) retreatLimited(id string) bool {
	count := 0
	for _, tick := range s.recovery[id].retreats {
		if tick > s.state.Tick-200 {
			count++
		}
	}
	return count >= 10
}

func (s *Sim) retreatRobot(robot *RobotState, cycle []int) bool {
	position := Point{X: robot.X, Y: robot.Y}
	for _, delta := range [...]Point{{X: 1}, {X: -1}, {Y: 1}, {Y: -1}} {
		point := Point{X: position.X + delta.X, Y: position.Y + delta.Y}
		if s.cfg.CellTypeAt(point) != CellFree || s.navigationBlocked(*robot, point, true) {
			continue
		}
		required := false
		for _, i := range cycle {
			next, ok := s.requiredCell(i)
			required = required || ok && s.state.Robots[i].ID != robot.ID && point == next
		}
		if required {
			continue
		}
		proposed := *robot
		proposed.X, proposed.Y, proposed.Battery = point.X, point.Y, robot.Battery-s.cfg.MovingDrain
		if proposed.Battery < 1 {
			continue
		}
		target := s.recoveryTarget(proposed)
		// Budget the remaining mission using R1's parked-obstacle estimates;
		// dynamic replanning follows the hold after cycle members can move away.
		route, ok := s.navigationRoute(proposed, target, false)
		if !ok {
			continue
		}
		*robot = proposed
		robot.WaitTicks, robot.Blocked = 0, false
		nav := s.navigation[robot.ID]
		nav.target, nav.route = target, route
		s.navigation[robot.ID] = nav
		recovery := s.recovery[robot.ID]
		recovery.retreats = slices.DeleteFunc(recovery.retreats, func(tick int) bool { return tick <= s.state.Tick-200 })
		recovery.retreats = append(recovery.retreats, s.state.Tick)
		recovery.hold, recovery.resume = s.cfg.HoldTicks, true
		s.recovery[robot.ID] = recovery
		return true
	}
	return false
}

func (event recoveryEvent) String() string {
	return fmt.Sprintf("cycle=%v retreat=%s reason=%s", event.cycle, event.robot, event.reason)
}
