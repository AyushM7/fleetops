package sim

import (
	"cmp"
	"slices"

	"github.com/AyushM7/fleetops/backend/routing"
)

func (s *Sim) seekCharging(skip []bool) {
	if !s.cfg.AutoCharge {
		return
	}
	candidates := []int{}
	for i, robot := range s.state.Robots {
		if !skip[i] && robot.Status == RobotIdle && robot.JobID == "" && robot.Battery < s.cfg.ChargeBelow {
			candidates = append(candidates, i)
		}
	}
	slices.SortFunc(candidates, func(a, b int) int {
		left, right := s.state.Robots[a], s.state.Robots[b]
		if order := cmp.Compare(left.Battery, right.Battery); order != 0 {
			return order
		}
		return cmp.Compare(idNumber(left.ID), idNumber(right.ID))
	})
	for _, i := range candidates {
		robot := &s.state.Robots[i]
		if s.chargeAtCurrentStation(robot) {
			continue
		}
		station, route := s.nearestChargingStation(*robot)
		if station == nil {
			continue
		}
		station.ReservedBy, robot.Status = robot.ID, RobotMoving
		s.navigation[robot.ID] = robotNavigation{target: Point{X: station.X, Y: station.Y}, route: route}
	}
}

func (s *Sim) chargeAtCurrentStation(robot *RobotState) bool {
	for i := range s.state.Stations {
		station := &s.state.Stations[i]
		if robot.X == station.X && robot.Y == station.Y && station.OccupiedBy == "" && station.ReservedBy == "" {
			s.beginCharging(robot, station)
			return true
		}
	}
	return false
}

func (s *Sim) nearestChargingStation(robot RobotState) (*StationState, []Point) {
	var best *StationState
	var bestRoute []Point
	for i := range s.state.Stations {
		station := &s.state.Stations[i]
		if !s.stationFreeFor(*station, robot.ID) {
			continue
		}
		route, ok := s.chargingRoute(robot, *station)
		if !ok {
			continue
		}
		if best == nil || len(route) < len(bestRoute) || len(route) == len(bestRoute) && idNumber(station.ID) < idNumber(best.ID) {
			best, bestRoute = station, route
		}
	}
	return best, bestRoute
}

func (s *Sim) stationFreeFor(station StationState, _ string) bool {
	if station.OccupiedBy != "" || station.ReservedBy != "" {
		return false
	}
	for _, other := range s.state.Robots {
		if other.X == station.X && other.Y == station.Y {
			return false
		}
	}
	return true
}

func (s *Sim) chargingRoute(robot RobotState, station StationState) ([]Point, bool) {
	route, err := routing.FindPath(routingGrid{cfg: s.cfg}, routing.Cell{X: robot.X, Y: robot.Y}, routing.Cell{X: station.X, Y: station.Y}, routing.Options{
		Blocked: func(cell routing.Cell) bool {
			return s.navigationBlocked(robot, Point{X: cell.X, Y: cell.Y}, false)
		},
	})
	if err != nil || robot.Battery-len(route)*s.cfg.MovingDrain < 1 {
		return nil, false
	}
	points := make([]Point, len(route))
	for i, cell := range route {
		points[i] = Point{X: cell.X, Y: cell.Y}
	}
	return points, true
}

func (s *Sim) reservedStation(robotID string) *StationState {
	for i := range s.state.Stations {
		if s.state.Stations[i].ReservedBy == robotID {
			return &s.state.Stations[i]
		}
	}
	return nil
}

func (s *Sim) beginCharging(robot *RobotState, station *StationState) {
	station.ReservedBy, station.OccupiedBy = "", robot.ID
	robot.Status = RobotCharging
	s.clearNavigation(robot)
}

func (s *Sim) stepChargeTrip(robot *RobotState) {
	if !s.cfg.AutoCharge {
		return
	}
	station := s.reservedStation(robot.ID)
	if station == nil {
		return
	}
	target := Point{X: station.X, Y: station.Y}
	if (Point{X: robot.X, Y: robot.Y}) == target {
		s.beginCharging(robot, station)
		return
	}
	if robot.Battery == 0 {
		robot.Status = RobotFaulted
		return
	}
	if !s.advanceNavigation(robot, target) {
		return
	}
	robot.Battery = max(0, robot.Battery-s.cfg.MovingDrain)
	if (Point{X: robot.X, Y: robot.Y}) == target {
		// Successful arrival permits charging at zero; gain starts next tick.
		s.beginCharging(robot, station)
	} else if robot.Battery == 0 {
		robot.Status = RobotFaulted
	}
}

func (s *Sim) finishNavigation(robot *RobotState) {
	s.clearNavigation(robot)
	if robot.Status == RobotOffline || robot.Status == RobotFaulted {
		for i := range s.state.Stations {
			if s.state.Stations[i].ReservedBy == robot.ID {
				s.state.Stations[i].ReservedBy = ""
			}
		}
	}
}
