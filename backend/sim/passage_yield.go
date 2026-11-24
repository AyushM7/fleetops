package sim

import "slices"

type passageYield struct {
	route               []Point
	peer                string
	mouth               Point
	expires             int
	reserveMouthForPeer bool
}

func (s *Sim) startPassageYield(index int, cycle []int) bool {
	robot := &s.state.Robots[index]
	if s.traffic == nil || len(cycle) != 2 ||
		robot.JobID == "" && !s.navigation[robot.ID].relocation && s.reservedStation(robot.ID) == nil {
		return false
	}
	other := cycle[0]
	if other == index {
		other = cycle[1]
	}
	peer := s.state.Robots[other]
	position, peerPosition := Point{robot.X, robot.Y}, Point{peer.X, peer.Y}
	next, moving := s.requiredCell(index)
	peerNext, peerMoving := s.requiredCell(other)
	if !moving || !peerMoving {
		return false
	}
	// Keep shared-passage recovery for two opposing job routes.
	if robot.JobID != "" && peer.JobID != "" && next == peerNext && s.cfg.narrowPassage(next) &&
		s.crossingBlocker(*robot, s.navigation[robot.ID].route) == other &&
		s.crossingBlocker(peer, s.navigation[peer.ID].route) == index {
		dx, dy := next.X-position.X, next.Y-position.Y
		for _, refuge := range []Point{{position.X - dy, position.Y + dx}, {position.X + dy, position.Y - dx}} {
			if s.admitPassageYield(robot, peer, []Point{refuge}, position) {
				return true
			}
		}
		return false
	}
	if next != peerPosition || peerNext != position {
		return false
	}
	for _, passage := range orthogonalNeighbors(position) {
		if !s.cfg.narrowPassage(passage) || s.exitBlocker(*robot, passage) >= 0 {
			continue
		}
		dx, dy := passage.X-position.X, passage.Y-position.Y
		path := []Point{passage}
		for len(path)+1 < s.cfg.RecoverAfter {
			last := path[len(path)-1]
			next := Point{last.X + dx, last.Y + dy}
			if s.cfg.CellTypeAt(next) != CellFree || s.exitBlocker(*robot, next) >= 0 {
				break
			}
			path = append(path, next)
			if s.cfg.narrowPassage(next) {
				continue
			}
			for _, side := range []Point{{next.X - dy, next.Y + dx}, {next.X + dy, next.Y - dx}} {
				if s.admitPassageYield(robot, peer, append(slices.Clone(path), side), path[1]) {
					return true
				}
			}
		}
	}
	return false
}

func (s *Sim) admitPassageYield(robot *RobotState, peer RobotState, path []Point, mouth Point) bool {
	refuge := path[len(path)-1]
	cell := s.cfg.CellTypeAt(refuge)
	if cell != CellFree && cell != CellAisle ||
		cell == CellFree && (s.cfg.narrowPassage(refuge) || s.protected[refuge] && !s.availableChargerPocket(refuge)) ||
		slices.Contains(s.navigation[peer.ID].route, refuge) ||
		s.navigationBlocked(*robot, refuge, true) || s.exitClaim(*robot, refuge) >= 0 {
		return false
	}
	after := *robot
	after.X, after.Y = refuge.X, refuge.Y
	target := s.recoveryTarget(*robot)
	returnRoute, ok := s.findNavigationRoute(after, target, false)
	if !ok || !s.routeAffordable(*robot, target, len(path)+len(returnRoute)) || !s.reserveMove(*robot, path[0]) {
		return false
	}
	robot.X, robot.Y = path[0].X, path[0].Y
	robot.Battery -= s.cfg.MovingDrain
	robot.WaitTicks, robot.Blocked = 0, false
	// Commit the turnout before the opposing robot can enter the vacated cell.
	nav := s.navigation[robot.ID]
	nav.route = nil
	s.navigation[robot.ID] = nav
	s.recordRetreat(robot.ID, false)
	recovery := s.recovery[robot.ID]
	recovery.turnout = &passageYield{route: path[1:], peer: peer.ID, mouth: mouth, expires: s.state.Tick + s.cfg.RecoverAfter, reserveMouthForPeer: len(path) == 1}
	s.recovery[robot.ID] = recovery
	return true
}

func (s *Sim) availableChargerPocket(point Point) bool {
	if !s.cfg.chargerPocket(point) {
		return false
	}
	neighbors := orthogonalNeighbors(point)
	return !slices.ContainsFunc(s.state.Stations, func(station StationState) bool {
		return slices.Contains(neighbors[:], Point{station.X, station.Y}) &&
			(station.OccupiedBy != "" || station.ReservedBy != "")
	})
}

func (s *Sim) stepPassageYield(robot *RobotState) bool {
	recovery := s.recovery[robot.ID]
	turnout := recovery.turnout
	if turnout == nil {
		return false
	}
	if s.state.Tick < turnout.expires {
		if len(turnout.route) != 0 {
			next := turnout.route[0]
			if robot.Battery > s.cfg.MovingDrain && !s.navigationBlocked(*robot, next, true) && s.reserveMove(*robot, next) {
				robot.X, robot.Y = next.X, next.Y
				robot.Battery -= s.cfg.MovingDrain
				robot.WaitTicks, robot.Blocked = 0, false
				turnout.route = turnout.route[1:]
				return true
			}
		} else if peer := s.faultRobot(turnout.peer); peer == nil || peer.Status != RobotMoving ||
			(Point{peer.X, peer.Y}) != turnout.mouth && !slices.Contains(s.navigation[peer.ID].route, turnout.mouth) {
			s.finishPassageYield(robot)
			return false
		}
		robot.WaitTicks++
		robot.Blocked = robot.WaitTicks >= s.cfg.BlockedAfter
		return true
	}
	s.finishPassageYield(robot)
	return false
}

func (s *Sim) finishPassageYield(robot *RobotState) {
	recovery := s.recovery[robot.ID]
	recovery.turnout = nil
	s.recovery[robot.ID] = recovery
	target := s.recoveryTarget(*robot)
	if !s.planNavigation(robot, target, true) {
		s.planNavigation(robot, target, false)
	}
}
