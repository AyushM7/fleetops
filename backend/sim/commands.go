package sim

// SetCharging is COMMAND: Phase 3 must validate and obtain operator approval
// before execution. It reserves a station without moving the robot.
func (s *Sim) SetCharging(robotID, stationID string) error {
	var robot *RobotState
	for i := range s.state.Robots {
		if s.state.Robots[i].ID == robotID {
			robot = &s.state.Robots[i]
			break
		}
	}
	if robot == nil {
		return ErrUnknownRobot
	}
	var station *StationState
	for i := range s.state.Stations {
		if s.state.Stations[i].ID == stationID {
			station = &s.state.Stations[i]
			break
		}
	}
	if station == nil {
		return ErrUnknownStation
	}
	if station.OccupiedBy != "" {
		return ErrStationOccupied
	}
	if robot.Status != RobotIdle || robot.JobID != "" {
		return ErrRobotBusy
	}
	if robot.X != station.X || robot.Y != station.Y {
		return ErrNotAtStation
	}
	robot.Status = RobotCharging
	station.OccupiedBy = robot.ID
	return nil
}
