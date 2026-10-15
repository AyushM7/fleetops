package sim

// InjectLowBattery is DRIVER: trusted scenarios only, never an agent tool
// or operator command. Normal tick processing handles subsequent depletion.
func (s *Sim) InjectLowBattery(robotID string) error {
	robot := s.faultRobot(robotID)
	if robot == nil {
		return ErrUnknownRobot
	}
	if faultRobotUnavailable(robot) {
		return ErrRobotUnavailable
	}
	robot.Battery = min(robot.Battery, s.cfg.LowBatteryInjectLevel)
	s.recordFault(FaultLowBattery, []string{robotID}, nil)
	return nil
}

// InjectUnresponsive is DRIVER: trusted scenarios only, never an agent tool
// or operator command. It starts the existing countdown without changing status.
func (s *Sim) InjectUnresponsive(robotID string) error {
	robot := s.faultRobot(robotID)
	if robot == nil {
		return ErrUnknownRobot
	}
	if faultRobotUnavailable(robot) || s.unresponsiveTicks[robotID] > 0 {
		return ErrRobotUnavailable
	}
	s.unresponsiveTicks[robotID] = s.cfg.UnresponsiveDelay
	s.recordFault(FaultRobotUnresponsive, []string{robotID}, nil)
	return nil
}

// InjectAisleConflict is DRIVER: trusted scenarios only, never an agent tool
// or operator command. It records a conflict without changing physical state.
func (s *Sim) InjectAisleConflict(robotA, robotB string, aisleID int) error {
	a, b := s.faultRobot(robotA), s.faultRobot(robotB)
	if a == nil || b == nil {
		return ErrUnknownRobot
	}
	if robotA == robotB {
		return ErrSameRobot
	}
	if _, ok := s.cfg.AisleColumn(aisleID); !ok {
		return ErrUnknownAisle
	}
	if faultRobotUnavailable(a) || faultRobotUnavailable(b) {
		return ErrRobotUnavailable
	}
	s.recordFault(FaultAisleConflict, []string{robotA, robotB}, &aisleID)
	return nil
}

func (s *Sim) faultRobot(id string) *RobotState {
	for i := range s.state.Robots {
		if s.state.Robots[i].ID == id {
			return &s.state.Robots[i]
		}
	}
	return nil
}

func faultRobotUnavailable(robot *RobotState) bool {
	return robot.Status == RobotOffline || robot.Status == RobotFaulted
}
