package sim

import "fmt"

// ScheduledBreakdown applies at the end of Tick, or during construction for tick zero.
type ScheduledBreakdown struct {
	Tick    int
	RobotID string
	Type    FaultType
}

func (kind FaultType) isBreakdown() bool {
	switch kind {
	case FaultMotorFailure, FaultSensorFailure, FaultRobotUnresponsive, FaultCriticalBattery:
		return true
	default:
		return false
	}
}

func (c Config) validateBreakdowns() error {
	seen := make(map[string]bool)
	previousTick := 0
	for _, entry := range c.ScheduledBreakdowns {
		if c.MaintenanceBay == nil || !entry.Type.isBreakdown() ||
			!(IsV2TransportID(entry.RobotID) || entry.RobotID == MaintenanceRobotID) ||
			entry.Tick < previousTick || seen[entry.RobotID] {
			return fmt.Errorf("%w: invalid scheduled breakdown at tick %d for %q (%q)", ErrInvalidConfig, entry.Tick, entry.RobotID, entry.Type)
		}
		seen[entry.RobotID], previousTick = true, entry.Tick
	}
	return nil
}

func (s *Sim) applyScheduledBreakdowns() {
	for s.nextBreakdown < len(s.cfg.ScheduledBreakdowns) {
		entry := s.cfg.ScheduledBreakdowns[s.nextBreakdown]
		if entry.Tick != s.state.Tick {
			return
		}
		// A prior failure keeps its original cause and occurrence.
		if err := s.injectBreakdown(entry.RobotID, entry.Type); err != nil && err != ErrRobotUnavailable {
			panic(err)
		}
		s.nextBreakdown++
	}
}

func (s *Sim) injectBreakdown(robotID string, kind FaultType) error {
	robot := s.faultRobot(robotID)
	if robot == nil {
		return ErrUnknownRobot
	}
	if faultRobotUnavailable(robot) {
		return ErrRobotUnavailable
	}
	switch kind {
	case FaultCriticalBattery:
		s.depleteBattery(robot)
	case FaultRobotUnresponsive:
		s.unresponsiveTicks[robotID] = s.cfg.UnresponsiveDelay
		s.stopWithFault(robot, kind)
	default:
		s.stopWithFault(robot, kind)
	}
	return nil
}

func (s *Sim) depleteBattery(robot *RobotState) {
	robot.Battery = 0
	if s.cfg.MaintenanceBay == nil {
		robot.Status = RobotFaulted
		return
	}
	s.stopWithFault(robot, FaultCriticalBattery)
}

func (s *Sim) stopWithFault(robot *RobotState, kind FaultType) {
	robot.Status, robot.ActiveFault = RobotFaulted, kind
	s.recordFault(kind, []string{robot.ID}, nil)
	// Invalidate executable motion and release unused charger reservations.
	// Job ownership and physical dock occupancy survive the stop.
	s.finishNavigation(robot)
	s.clearRecoveryCycle(robot.ID)
}

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
	if s.cfg.MaintenanceBay != nil && robot.Battery == 0 && robot.Status != RobotCharging {
		s.depleteBattery(robot)
	}
	return nil
}

// InjectUnresponsive is DRIVER: trusted scenarios only, never an agent tool
// or operator command. V2 stops on detection. V1 uses its legacy countdown.
func (s *Sim) InjectUnresponsive(robotID string) error {
	if s.cfg.MaintenanceBay != nil {
		return s.injectBreakdown(robotID, FaultRobotUnresponsive)
	}
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
