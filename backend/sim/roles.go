package sim

// Reader exposes read-only simulator operations.
type Reader interface {
	Snapshot() State
	CellAt(Point) CellType
	PlanRoute(from, to Point) ([]Point, error)
	AisleOccupant(aisleID int) (string, error)
	OccupiedAislesOnRoute(route []Point, forRobot string) ([]int, error)
}

// Commander exposes commands that require operator validation and approval.
type Commander interface {
	Reader
	SetCharging(robotID, stationID string) error
}

// Driver exposes trusted scenario injections, never agent tools.
type Driver interface {
	Reader
	InjectLowBattery(robotID string) error
	InjectUnresponsive(robotID string) error
	InjectAisleConflict(robotA, robotB string, aisleID int) error
}

var (
	_ Reader    = (*Sim)(nil)
	_ Commander = (*Sim)(nil)
	_ Driver    = (*Sim)(nil)
)
