//go:build fleetops_test

package sim

import (
	"fmt"
	"strconv"
	"strings"
)

// NewPhase4V3Prelude constructs a full-fleet, credential-free acceptance fixture.
func NewPhase4V3Prelude(profile string, seed int64) (*Sim, error) {
	cfg := WarehouseV2Config()
	switch profile {
	case "regular":
	case "busy":
		cfg.NewJobEvery = 3
	case "charging":
		cfg.ChargeGain, cfg.MovingDrain = 1, 2
	case "unresponsive":
		cfg.ScheduledBreakdowns = []ScheduledBreakdown{{48, "R1", FaultRobotUnresponsive}, {80, "R4", FaultRobotUnresponsive}}
	case "physical-fault":
		cfg.ScheduledBreakdowns = []ScheduledBreakdown{{48, "R1", FaultMotorFailure}}
	case "two-faults":
		cfg.ScheduledBreakdowns = []ScheduledBreakdown{{48, "R1", FaultMotorFailure}, {48, "R4", FaultSensorFailure}}
	case "critical-battery":
		cfg.ScheduledBreakdowns = []ScheduledBreakdown{{48, "R4", FaultCriticalBattery}}
	default:
		return nil, fmt.Errorf("unknown Phase 4 V3 fixture profile %q", profile)
	}
	return New(cfg, seed)
}

func newPhase4V3Fixture(variant string) (*Sim, error) {
	profile, suffix, ok := strings.Cut(strings.TrimPrefix(variant, "phase4/"), "/")
	seedText, tickText, split := strings.Cut(suffix, "/")
	seed, seedErr := strconv.ParseInt(seedText, 10, 64)
	tick, tickErr := strconv.Atoi(tickText)
	if !ok || !split || seedErr != nil || tickErr != nil || seed <= 0 || tick < 0 || tick > 1500 {
		return nil, fmt.Errorf("invalid Phase 4 V3 fixture variant")
	}
	if strings.HasPrefix(profile, "lab-") && seed == 42 && tick == 42 {
		return phase4V3Lab(strings.TrimPrefix(profile, "lab-"))
	}
	s, err := NewPhase4V3Prelude(profile, seed)
	if err != nil {
		return nil, err
	}
	for range tick {
		s.Tick()
	}
	return s, nil
}

func phase4V3Lab(profile string) (*Sim, error) {
	s, err := NewReassignmentTestFixture()
	if err != nil {
		return nil, err
	}
	switch profile {
	case "single-eligible":
		s.state.Robots[3].Battery, s.state.Robots[4].Battery = 29, 29
	case "recipient-threshold":
		s.state.Robots[2].Battery, s.state.Robots[3].Battery, s.state.Robots[4].Battery = 30, 29, 90
	case "exact-mission-energy":
		plan, err := s.PreviewReassignment("J1", "R1", "R4")
		if err != nil {
			return nil, err
		}
		s.state.Robots[3].Battery = max(plan.RequiredBattery, s.cfg.MinAssignBattery)
	case "headroom-tie":
		for i := 2; i <= 3; i++ {
			robot := &s.state.Robots[i]
			plan, err := s.PreviewReassignment("J1", "R1", robot.ID)
			if err != nil {
				return nil, err
			}
			robot.Battery = max(30, plan.RequiredBattery) + 12
		}
		s.state.Robots[4].Battery = 29
	case "charging-recipient":
		docked := &s.state.Robots[2]
		station := &s.state.Stations[0]
		docked.X, docked.Y, docked.Status, docked.Battery = station.X, station.Y, RobotCharging, 65
		station.OccupiedBy = docked.ID
	case "cargo-in-transit":
		job := &s.state.Jobs[0]
		job.Status, job.CargoRobotID = JobInProgress, "R1"
	case "source-energy-denial":
		s.state.Robots[0].Battery = 29
	case "route-altered":
		s.state.Jobs[0].From, s.state.Jobs[0].To = Point{6, 3}, Point{4, 5}
	case "pickup-and-energy-conflict":
		s.state.Robots[2].Battery, s.state.Robots[3].Battery, s.state.Robots[4].Battery = 59, 98, 29
	case "battery-scarcity":
		s.state.Robots[2].Battery, s.state.Robots[3].Battery, s.state.Robots[4].Battery = 32, 30, 29
	default:
		return nil, fmt.Errorf("unknown Phase 4 V3 lab profile %q", profile)
	}
	return s, nil
}
