//go:build fleetops_test

package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/AyushM7/fleetops/backend/sim"
)

const horizon = 200
const seed int64 = 42

type action struct {
	Action      string `json:"action"`
	Purpose     string `json:"purpose"`
	RobotID     string `json:"robotId,omitempty"`
	StationID   string `json:"stationId,omitempty"`
	JobID       string `json:"jobId,omitempty"`
	FromRobotID string `json:"fromRobotId,omitempty"`
	ToRobotID   string `json:"toRobotId,omitempty"`
}
type fault struct {
	Tick    int    `json:"tick"`
	Kind    string `json:"kind"`
	RobotID string `json:"robotId"`
}
type scenario struct {
	ID             string  `json:"id"`
	Source         string  `json:"source"`
	Goal           string  `json:"goal"`
	TargetJob      string  `json:"targetJob"`
	ExpectedAction *action `json:"expectedAction"`
	SetupFaults    []fault `json:"setupFaults"`
}
type observation struct {
	scenario
	Seed        int64         `json:"seed"`
	Horizon     int           `json:"horizonTicks"`
	InitialHash string        `json:"initialHash"`
	Input       agentSnapshot `json:"input"`
}

func scenarios() []scenario {
	base := "sim.NewReassignmentTestFixture; Phase 4 tick-42 fixture; autonomous navigation/charging/recovery unchanged"
	return []scenario{
		{"blocked-reassignment", base, "target_delivery", "J1", &action{Action: "ReassignUnstartedJob", Purpose: "incident_recovery", JobID: "J1", FromRobotID: "R1", ToRobotID: "R3"}, []fault{}},
		{"blocked-no-candidate", base, "no_supported_intervention", "J1", nil, []fault{{42, "low_battery", "R3"}, {42, "low_battery", "R4"}, {42, "low_battery", "R5"}}},
		{"offline-unfinished-job", base, "no_supported_intervention", "J1", nil, []fault{{42, "unresponsive", "R1"}}},
		{"routine-charging", "sim.DefaultConfig; R1 starts on S1; no initial jobs; 1000 ordinary idle ticks; AutoCharge remains default false", "battery", "", &action{Action: "SetCharging", Purpose: "routine_charging", RobotID: "R1", StationID: "S1"}, []fault{}},
		{"healthy-control", "sim.DefaultConfig; same job-free station layout as charging; no injected faults", "no_op_control", "", nil, []fault{}},
	}
}
func newScenario(spec scenario) (*sim.Sim, error) {
	var s *sim.Sim
	var err error
	if spec.ID == "routine-charging" || spec.ID == "healthy-control" {
		cfg := sim.DefaultConfig()
		cfg.InitialJobs, cfg.MaxJobs, cfg.NewJobEvery = 0, 1, 10000
		cfg.RobotStarts[0] = cfg.Stations[0]
		s, err = sim.New(cfg, seed)
	} else {
		s, err = sim.NewReassignmentTestFixture()
	}
	if err != nil {
		return nil, err
	}
	if spec.ID == "routine-charging" {
		for range 1000 {
			s.Tick()
		}
	}
	for _, f := range spec.SetupFaults {
		if s.Snapshot().Tick != f.Tick {
			return nil, fmt.Errorf("fault schedule tick mismatch")
		}
		switch f.Kind {
		case "low_battery":
			err = s.InjectLowBattery(f.RobotID)
		case "unresponsive":
			err = s.InjectUnresponsive(f.RobotID)
		default:
			return nil, fmt.Errorf("unknown trusted fault")
		}
		if err != nil {
			return nil, err
		}
	}
	if spec.ID == "offline-unfinished-job" {
		for range 3 {
			s.Tick()
		}
	}
	return s, nil
}
func stateHash(state sim.State) string {
	raw, err := json.Marshal(state)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
func observeScenarios() ([]observation, error) {
	result := []observation{}
	for _, spec := range scenarios() {
		s, err := newScenario(spec)
		if err != nil {
			return nil, err
		}
		view, err := agentView(s)
		if err != nil {
			return nil, err
		}
		result = append(result, observation{spec, seed, horizon, stateHash(s.Snapshot()), view})
	}
	return result, nil
}
