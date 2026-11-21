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
	RobotIdle      RobotStatus = "idle"
	RobotMoving    RobotStatus = "moving"
	RobotCharging  RobotStatus = "charging"
	RobotFaulted   RobotStatus = "faulted"
	RobotOffline   RobotStatus = "offline"
	RobotServicing RobotStatus = "servicing"
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
	ActivityGoingToRepair     RobotActivity = "GOING_TO_REPAIR"
	ActivityAtRepairSite      RobotActivity = "AT_REPAIR_SITE"
	ActivityRepairing         RobotActivity = "REPAIRING"
)

type JobStatus string

const (
	JobPending    JobStatus = "pending"
	JobAssigned   JobStatus = "assigned"
	JobInProgress JobStatus = "in_progress"
	JobDone       JobStatus = "done"
)

type JobType string

const (
	JobNormal   JobType = "NORMAL"
	JobPriority JobType = "PRIORITY"
)

type JobOutcome string

const (
	JobDelivered JobOutcome = "DELIVERED"
	JobOnTime    JobOutcome = "ON_TIME"
	JobLate      JobOutcome = "LATE"
)

type FaultType string

const (
	FaultLowBattery        FaultType = "low_battery"
	FaultRobotUnresponsive FaultType = "robot_unresponsive"
	FaultAisleConflict     FaultType = "aisle_conflict"
	FaultMotorFailure      FaultType = "motor_failure"
	FaultSensorFailure     FaultType = "sensor_failure"
	FaultCriticalBattery   FaultType = "critical_battery"
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
	ActiveFault FaultType     `json:"activeFault,omitempty"`
}

type JobState struct {
	ID            string    `json:"id"`
	From          Point     `json:"from"`
	To            Point     `json:"to"`
	Status        JobStatus `json:"status"`
	AssignedRobot string    `json:"assignedRobot"` // Empty if unassigned; retained when done.

	Type           JobType    `json:"type,omitempty"`
	CreatedTick    int        `json:"createdTick,omitempty"` // Initial V2 jobs are created at tick zero.
	AssignedTick   int        `json:"assignedTick,omitempty"`
	DeadlineTick   int        `json:"deadlineTick,omitempty"`
	DeadlineMissed bool       `json:"deadlineMissed,omitempty"`
	CompletedTick  int        `json:"completedTick,omitempty"`
	QueueAgeTicks  int        `json:"queueAgeTicks,omitempty"`
	CargoRobotID   string     `json:"cargoRobotId,omitempty"`
	Outcome        JobOutcome `json:"outcome,omitempty"`
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
	Maintenance      *MaintenanceState `json:"maintenance,omitempty"`
	TrafficDeadlocks []TrafficDeadlock `json:"trafficDeadlocks,omitempty"`
	CompletedJobs    int               `json:"completedJobs,omitempty"`
	Tick             int               `json:"tick"`
	Seed             int64             `json:"seed"`
	Robots           []RobotState      `json:"robots"`
	Jobs             []JobState        `json:"jobs"`
	Stations         []StationState    `json:"stations"`
	Faults           []FaultState      `json:"faults"`
	Aisles           []AisleState      `json:"aisles"`

	PriorityDeadlinesMet    int `json:"priorityDeadlinesMet,omitempty"`
	PriorityDeadlinesMissed int `json:"priorityDeadlinesMissed,omitempty"`
}
