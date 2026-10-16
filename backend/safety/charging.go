package safety

import (
	"errors"

	"github.com/AyushM7/fleetops/backend/sim"
)

// SetChargingRequest proposes charging in place at the named station.
type SetChargingRequest struct {
	RobotID   string
	StationID string
}

// ErrBatteryBelowMinimum enforces frozen safety rule 2 for operator commands,
// independently of autonomous charging and simulator configuration thresholds.
var ErrBatteryBelowMinimum = errors.New("robot battery below 30%")

// PreflightSetCharging returns nil when the observed state permits charging.
// It takes exactly one snapshot and returns the first rejection in fixed order:
// robot existence, station existence, robot availability, battery, station
// eligibility, busy status, position. It never authorizes or executes a command.
// Callers must serialize access: sim.Reader is not goroutine-safe.
func PreflightSetCharging(reader sim.Reader, request SetChargingRequest) error {
	state := reader.Snapshot()
	robot, ok := findRobot(state.Robots, request.RobotID)
	if !ok {
		return sim.ErrUnknownRobot
	}
	station, ok := findStation(state.Stations, request.StationID)
	if !ok {
		return sim.ErrUnknownStation
	}
	if robot.Status == sim.RobotFaulted || robot.Status == sim.RobotOffline {
		return sim.ErrRobotUnavailable
	}
	if robot.Battery < 30 {
		return ErrBatteryBelowMinimum
	}
	if err := stationEligibility(state.Robots, robot.ID, station); err != nil {
		return err
	}
	if robot.Status != sim.RobotIdle || robot.JobID != "" {
		return sim.ErrRobotBusy
	}
	if robot.X != station.X || robot.Y != station.Y {
		return sim.ErrNotAtStation
	}
	return nil
}

func findRobot(robots []sim.RobotState, id string) (sim.RobotState, bool) {
	for _, robot := range robots {
		if robot.ID == id {
			return robot, true
		}
	}
	return sim.RobotState{}, false
}

func findStation(stations []sim.StationState, id string) (sim.StationState, bool) {
	for _, station := range stations {
		if station.ID == id {
			return station, true
		}
	}
	return sim.StationState{}, false
}

func stationEligibility(robots []sim.RobotState, robotID string, station sim.StationState) error {
	if station.OccupiedBy != "" {
		return sim.ErrStationOccupied
	}
	if station.ReservedBy != "" && station.ReservedBy != robotID {
		return sim.ErrStationReserved
	}
	// Physical occupancy must also be free of other robots, even when station
	// metadata has no charging occupant (for example, an idle parked robot).
	for _, other := range robots {
		if other.ID != robotID && other.X == station.X && other.Y == station.Y {
			return sim.ErrStationOccupied
		}
	}
	return nil
}
