package safety_test

import (
	"errors"
	"testing"

	"github.com/AyushM7/fleetops/backend/safety"
	"github.com/AyushM7/fleetops/backend/sim"
)

type reassignmentReader struct {
	sim.Reader
	state        sim.State
	previewErr   error
	previewCalls int
	request      safety.ReassignUnstartedJobRequest
}

func (r *reassignmentReader) Snapshot() sim.State { return r.state }
func (r *reassignmentReader) PreviewReassignment(job, from, to string) (sim.ReassignmentPlan, error) {
	r.previewCalls++
	r.request = safety.ReassignUnstartedJobRequest{JobID: job, FromRobotID: from, ToRobotID: to}
	return sim.ReassignmentPlan{}, r.previewErr
}
func TestReassignmentPreflightBothRobotsAndPlanner(t *testing.T) {
	request := safety.ReassignUnstartedJobRequest{JobID: "J1", FromRobotID: "R1", ToRobotID: "R3"}
	for _, tc := range []struct {
		name    string
		robot   int
		battery int
		status  sim.RobotStatus
		want    error
	}{
		{"source minimum", 0, 30, sim.RobotMoving, nil},
		{"source low", 0, 29, sim.RobotMoving, safety.ErrBatteryBelowMinimum},
		{"recipient low", 1, 29, sim.RobotIdle, safety.ErrBatteryBelowMinimum},
		{"source offline", 0, 100, sim.RobotOffline, sim.ErrRobotUnavailable},
		{"recipient faulted", 1, 100, sim.RobotFaulted, sim.ErrRobotUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &reassignmentReader{state: sim.State{Robots: []sim.RobotState{{ID: "R1", Status: sim.RobotMoving, Battery: 100}, {ID: "R3", Status: sim.RobotIdle, Battery: 100}}}}
			r.state.Robots[tc.robot].Battery, r.state.Robots[tc.robot].Status = tc.battery, tc.status
			if err := safety.PreflightReassignment(r, request); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want=%v", err, tc.want)
			}
			if tc.want == nil && (r.previewCalls != 1 || r.request != request) {
				t.Fatal("planner not checked with exact request")
			}
			if tc.want != nil && r.previewCalls != 0 {
				t.Fatal("invalid robot reached planner")
			}
		})
	}
	r := &reassignmentReader{state: sim.State{Robots: []sim.RobotState{{ID: "R1", Battery: 100}, {ID: "R3", Battery: 100}}}, previewErr: sim.ErrReassignmentRoute}
	if err := safety.PreflightReassignment(r, request); !errors.Is(err, sim.ErrReassignmentRoute) {
		t.Fatal(err)
	}
	request.FromRobotID = "missing"
	if err := safety.PreflightReassignment(r, request); !errors.Is(err, sim.ErrUnknownRobot) {
		t.Fatal(err)
	}
}
