package sim

import "errors"

var (
	ErrInvalidConfig    = errors.New("invalid configuration")
	ErrUnknownRobot     = errors.New("unknown robot")
	ErrUnknownStation   = errors.New("unknown station")
	ErrUnknownAisle     = errors.New("unknown aisle")
	ErrSameRobot        = errors.New("same robot")
	ErrRobotBusy        = errors.New("robot busy")
	ErrNotAtStation     = errors.New("robot not at station")
	ErrStationOccupied  = errors.New("station occupied")
	ErrRobotUnavailable = errors.New("robot unavailable")
)
