package sim

type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type CellType string

const (
	CellFree        CellType = "free"
	CellAisle       CellType = "aisle"
	CellStation     CellType = "station"
	CellWall        CellType = "wall"
	CellOutOfBounds CellType = "out_of_bounds"
)

type RobotStatus string

const (
	RobotIdle     RobotStatus = "idle"
	RobotMoving   RobotStatus = "moving"
	RobotCharging RobotStatus = "charging"
	RobotFaulted  RobotStatus = "faulted"
	RobotOffline  RobotStatus = "offline"
)

type JobStatus string

const (
	JobPending    JobStatus = "pending"
	JobAssigned   JobStatus = "assigned"
	JobInProgress JobStatus = "in_progress"
	JobDone       JobStatus = "done"
)

type FaultType string

const (
	FaultLowBattery        FaultType = "low_battery"
	FaultRobotUnresponsive FaultType = "robot_unresponsive"
	FaultAisleConflict     FaultType = "aisle_conflict"
)

type RobotState struct {
	ID      string      `json:"id"`
	X       int         `json:"x"`
	Y       int         `json:"y"`
	Battery int         `json:"battery"` // Integer percentage, 0..100.
	Status  RobotStatus `json:"status"`
	JobID   string      `json:"jobId"` // Empty means no assignment.
}

type JobState struct {
	ID            string    `json:"id"`
	From          Point     `json:"from"`
	To            Point     `json:"to"`
	Status        JobStatus `json:"status"`
	AssignedRobot string    `json:"assignedRobot"` // Empty if unassigned; retained when done.
}

type StationState struct {
	ID         string `json:"id"`
	X          int    `json:"x"`
	Y          int    `json:"y"`
	OccupiedBy string `json:"occupiedBy"`
}

type FaultState struct {
	ID     string    `json:"id"`
	Type   FaultType `json:"type"`
	Robots []string  `json:"robots"`
	Aisle  *int      `json:"aisle,omitempty"`
	Tick   int       `json:"tick"`
}

type State struct {
	Tick     int            `json:"tick"`
	Seed     int64          `json:"seed"`
	Robots   []RobotState   `json:"robots"`
	Jobs     []JobState     `json:"jobs"`
	Stations []StationState `json:"stations"`
	Faults   []FaultState   `json:"faults"`
}
