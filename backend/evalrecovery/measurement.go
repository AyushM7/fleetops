//go:build fleetops_test

package main

import "github.com/AyushM7/fleetops/backend/sim"

type jobOutcome struct {
	ID                    string        `json:"id"`
	Status                sim.JobStatus `json:"status"`
	CompletionTick        *int          `json:"completionTick"`
	TimeToCompletion      *int          `json:"timeToCompletion"`
	Censored              bool          `json:"censored"`
	CompletedBeforeWindow bool          `json:"completedBeforeWindow"`
}
type outcome struct {
	Final                sim.State   `json:"final"`
	Target               *jobOutcome `json:"target"`
	JobsCompleted        int         `json:"jobsCompletedInWindow"`
	BlockedRobotTicks    int         `json:"blockedRobotTicks"`
	ChargingRobotTicks   int         `json:"chargingRobotTicks"`
	OccupiedStationTicks int         `json:"occupiedStationTicks"`
	TotalFinalBattery    int         `json:"totalFinalBattery"`
}
type measurement struct {
	start       int
	completed   map[string]int
	initialDone map[string]bool
	result      outcome
}

func newMeasurement(initial sim.State) *measurement {
	m := &measurement{start: initial.Tick, completed: map[string]int{}, initialDone: map[string]bool{}}
	for _, job := range initial.Jobs {
		if job.Status == sim.JobDone {
			m.initialDone[job.ID] = true
		}
	}
	return m
}

// Samples are taken after each ordinary Go tick, excluding the initial snapshot.
func (m *measurement) sample(state sim.State) {
	for _, job := range state.Jobs {
		if _, seen := m.completed[job.ID]; job.Status == sim.JobDone && !seen && !m.initialDone[job.ID] {
			m.completed[job.ID] = state.Tick
		}
	}
	for _, robot := range state.Robots {
		if robot.Blocked {
			m.result.BlockedRobotTicks++
		}
		if robot.Status == sim.RobotCharging {
			m.result.ChargingRobotTicks++
		}
	}
	for _, station := range state.Stations {
		if station.OccupiedBy != "" {
			m.result.OccupiedStationTicks++
		}
	}
}
func (m *measurement) finish(final sim.State, target string) outcome {
	m.result.Final, m.result.JobsCompleted = final, len(m.completed)
	for _, robot := range final.Robots {
		m.result.TotalFinalBattery += robot.Battery
	}
	if target == "" {
		return m.result
	}
	m.result.Target = &jobOutcome{ID: target, Status: "missing", Censored: true}
	if m.initialDone[target] {
		m.result.Target.Censored, m.result.Target.CompletedBeforeWindow = false, true
	}
	for _, job := range final.Jobs {
		if job.ID == target {
			m.result.Target.Status = job.Status
		}
	}
	if tick, done := m.completed[target]; done {
		elapsed := tick - m.start
		m.result.Target.CompletionTick, m.result.Target.TimeToCompletion, m.result.Target.Censored = &tick, &elapsed, false
	}
	return m.result
}
