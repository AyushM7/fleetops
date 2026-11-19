package sim

type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type CellType string

const (
	CellFree           CellType = "free"
	CellAisle          CellType = "aisle"
	CellStation        CellType = "station"
	CellParking        CellType = "parking"
	CellMaintenanceBay CellType = "maintenance_bay"
	CellWall           CellType = "wall"
	CellOutOfBounds    CellType = "out_of_bounds"
)

type RobotStatus string

const (
	RobotIdle     RobotStatus = "idle"
	RobotMoving   RobotStatus = "moving"
	RobotCharging RobotStatus = "charging"
	RobotFaulted  RobotStatus = "faulted"
	RobotOffline  RobotStatus = "offline"
)

type RobotRole string

const (
	RoleTransport   RobotRole = "transport"
	RoleMaintenance RobotRole = "maintenance"
)

type RobotActivity string

const (
	ActivityAvailable         RobotActivity = "AVAILABLE"
	ActivityGoingToPickup     RobotActivity = "GOING_TO_PICKUP"
	ActivityPickingUp         RobotActivity = "PICKING_UP"
	ActivityDelivering        RobotActivity = "DELIVERING"
	ActivityWaitingForTraffic RobotActivity = "WAITING_FOR_TRAFFIC"
	ActivityGoingToCharge     RobotActivity = "GOING_TO_CHARGE"
	ActivityCharging          RobotActivity = "CHARGING"
	ActivityUndocking         RobotActivity = "UNDOCKING"
	ActivityGoingToPark       RobotActivity = "GOING_TO_PARK"
	ActivityRelocating        RobotActivity = "RELOCATING"
	ActivityParked            RobotActivity = "PARKED"
	ActivityFaulted           RobotActivity = "FAULTED"
	ActivityOutOfService      RobotActivity = "OUT_OF_SERVICE"
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
	ID          string        `json:"id"`
	X           int           `json:"x"`
	Y           int           `json:"y"`
	Battery     int           `json:"battery"` // Integer percentage, 0..100.
	Status      RobotStatus   `json:"status"`
	JobID       string        `json:"jobId"` // Empty means no assignment.
	WaitTicks   int           `json:"waitTicks"`
	Blocked     bool          `json:"blocked"`
	Maintenance bool          `json:"maintenance,omitempty"`
	Role        RobotRole     `json:"role,omitempty"`
	Activity    RobotActivity `json:"activity,omitempty"`
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
	ReservedBy string `json:"reservedBy"`
}

type AisleState struct {
	ID         int    `json:"id"`
	OccupiedBy string `json:"occupiedBy"`
}

type FaultState struct {
	ID     string    `json:"id"`
	Type   FaultType `json:"type"`
	Robots []string  `json:"robots"`
	Aisle  *int      `json:"aisle,omitempty"`
	Tick   int       `json:"tick"`
}

type TrafficDeadlock struct {
	Robots        []string `json:"robots"`
	YieldingRobot string   `json:"yieldingRobot,omitempty"`
}

type State struct {
	TrafficDeadlocks []TrafficDeadlock `json:"trafficDeadlocks,omitempty"`
	CompletedJobs    int               `json:"completedJobs,omitempty"`
	Tick             int               `json:"tick"`
	Seed             int64             `json:"seed"`
	Robots           []RobotState      `json:"robots"`
	Jobs             []JobState        `json:"jobs"`
	Stations         []StationState    `json:"stations"`
	Faults           []FaultState      `json:"faults"`
	Aisles           []AisleState      `json:"aisles"`
}
