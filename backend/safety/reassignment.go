package safety

import "github.com/AyushM7/fleetops/backend/sim"

type ReassignUnstartedJobRequest struct {
	JobID, FromRobotID, ToRobotID string
}

func PreflightReassignment(reader sim.Reader, request ReassignUnstartedJobRequest) error {
	state := reader.Snapshot()
	for _, id := range []string{request.FromRobotID, request.ToRobotID} {
		robot, ok := findRobot(state.Robots, id)
		if !ok {
			return sim.ErrUnknownRobot
		}
		if robot.Status == sim.RobotOffline || robot.Status == sim.RobotFaulted {
			return sim.ErrRobotUnavailable
		}
		if robot.Battery < 30 {
			return ErrBatteryBelowMinimum
		}
	}
	_, err := reader.PreviewReassignment(request.JobID, request.FromRobotID, request.ToRobotID)
	return err
}
