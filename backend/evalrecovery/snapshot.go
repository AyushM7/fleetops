//go:build fleetops_test

package main

import (
	"github.com/AyushM7/fleetops/backend/incident"
	"github.com/AyushM7/fleetops/backend/sim"
)

type incidentView struct {
	RobotID   string            `json:"robotId"`
	Type      incident.Type     `json:"type"`
	Severity  incident.Severity `json:"severity"`
	Telemetry sim.RobotState    `json:"telemetry"`
}
type jobView struct {
	ID            string        `json:"id"`
	Status        sim.JobStatus `json:"status"`
	AssignedRobot string        `json:"assignedRobot"`
	FromX         int           `json:"fromX"`
	FromY         int           `json:"fromY"`
	ToX           int           `json:"toX"`
	ToY           int           `json:"toY"`
}
type candidateView struct {
	JobID             string `json:"jobId"`
	FromRobotID       string `json:"fromRobotId"`
	ToRobotID         string `json:"toRobotId"`
	PickupSteps       int    `json:"pickupSteps"`
	DeliverySteps     int    `json:"deliverySteps"`
	ChargingExitSteps int    `json:"chargingExitSteps"`
	RequiredBattery   int    `json:"requiredBattery"`
}
type agentSnapshot struct {
	Tick            int                `json:"tick"`
	Poisoned        bool               `json:"poisoned"`
	Robots          []sim.RobotState   `json:"robots"`
	Stations        []sim.StationState `json:"stations"`
	ActiveIncidents []incidentView     `json:"activeIncidents"`
	Jobs            []jobView          `json:"jobs"`
	Candidates      []candidateView    `json:"reassignmentCandidates"`
}

// Same detector and preview admissions as fleet.ReadState; only Reader capabilities.
func agentView(reader sim.Reader) (agentSnapshot, error) {
	state := reader.Snapshot()
	view := agentSnapshot{Tick: state.Tick, Robots: state.Robots, Stations: state.Stations,
		ActiveIncidents: []incidentView{}, Jobs: []jobView{}, Candidates: []candidateView{}}
	conditions, _, err := incident.Reconcile(nil, state, 0)
	if err != nil {
		return view, err
	}
	for _, condition := range conditions {
		for _, robot := range state.Robots {
			if robot.ID == condition.RobotID {
				view.ActiveIncidents = append(view.ActiveIncidents, incidentView{robot.ID, condition.Type, condition.Severity, robot})
			}
		}
	}
	for _, job := range state.Jobs {
		view.Jobs = append(view.Jobs, jobView{job.ID, job.Status, job.AssignedRobot, job.From.X, job.From.Y, job.To.X, job.To.Y})
		if job.Status != sim.JobAssigned {
			continue
		}
		for _, robot := range state.Robots {
			if robot.Status != sim.RobotIdle || robot.JobID != "" {
				continue
			}
			plan, err := reader.PreviewReassignment(job.ID, job.AssignedRobot, robot.ID)
			if err == nil {
				view.Candidates = append(view.Candidates, candidateView{job.ID, job.AssignedRobot, robot.ID, plan.PickupSteps, plan.DeliverySteps, plan.ChargingExitSteps, plan.RequiredBattery})
			}
		}
	}
	return view, nil
}
