package incident

import (
	"cmp"
	"errors"
	"slices"
	"strconv"

	"github.com/AyushM7/fleetops/backend/sim"
)

type Type string

const (
	LowBattery        Type = "low_battery"
	RobotUnresponsive Type = "robot_unresponsive"
	TrafficBlocked    Type = "traffic_blocked"
	MotorFailure      Type = Type(sim.FaultMotorFailure)
	SensorFailure     Type = Type(sim.FaultSensorFailure)
	CriticalBattery   Type = Type(sim.FaultCriticalBattery)
)

type Status string

const (
	Open     Status = "open"
	Resolved Status = "resolved"
)

type Severity string

const (
	Medium Severity = "medium"
	High   Severity = "high"
)

type Incident struct {
	ID       string
	RobotID  string
	Type     Type
	Status   Status
	Severity Severity
}

var (
	ErrInvalidSnapshot  = errors.New("snapshot robot IDs must be unique canonical robot identifiers")
	ErrInvalidHistory   = errors.New("invalid incident history or duplicate open robot/type")
	ErrInvalidCounter   = errors.New("counter is below an existing incident ID")
	ErrCounterExhausted = errors.New("incident counter exhausted")
)

type key struct {
	robotID string
	kind    Type
}

// Reconcile returns detached, ordered incident records and the last allocated
// counter. It never mutates previous or state. Invalid inputs or ID exhaustion
// return no records and the original counter, so errors consume no IDs.
func Reconcile(previous []Incident, state sim.State, counter uint64) ([]Incident, uint64, error) {
	if err := validateRobots(state.Robots); err != nil {
		return nil, counter, err
	}
	open, err := indexHistory(previous, counter)
	if err != nil {
		return nil, counter, err
	}
	result := append([]Incident{}, previous...)
	// Resolve copied open episodes first; each currently active condition below
	// restores its matching episode. No map iteration determines output or IDs.
	for i := range result {
		if result[i].Status == Open {
			result[i].Status = Resolved
		}
	}
	robots := slices.Clone(state.Robots)
	slices.SortFunc(robots, func(a, b sim.RobotState) int { return sim.CompareRobotIDs(a.ID, b.ID) })
	next := counter
	for _, robot := range robots {
		for _, condition := range detect(robot) {
			if index, exists := open[key{robotID: robot.ID, kind: condition.Type}]; exists {
				result[index].Status, result[index].Severity = Open, condition.Severity
				continue
			}
			if next == ^uint64(0) {
				return nil, counter, ErrCounterExhausted
			}
			next++
			condition.ID = "I" + strconv.FormatUint(next, 10)
			result = append(result, condition)
		}
	}
	slices.SortFunc(result, compareIncidents)
	return result, next, nil
}

func detect(robot sim.RobotState) []Incident {
	var conditions []Incident
	kind := Type(robot.ActiveFault)
	if kind == "" && robot.Status == sim.RobotOffline {
		kind = RobotUnresponsive
	}
	if robot.Battery < 30 && robot.JobID != "" && kind != CriticalBattery {
		severity := Medium
		if robot.Battery < 15 {
			severity = High
		}
		conditions = append(conditions, Incident{RobotID: robot.ID, Type: LowBattery, Status: Open, Severity: severity})
	}
	if breakdownIncident(kind) {
		conditions = append(conditions, Incident{RobotID: robot.ID, Type: kind, Status: Open, Severity: High})
	}
	if robot.Blocked {
		severity := Medium
		if robot.WaitTicks > 40 {
			severity = High
		}
		conditions = append(conditions, Incident{RobotID: robot.ID, Type: TrafficBlocked, Status: Open, Severity: severity})
	}
	return conditions
}

func validateRobots(robots []sim.RobotState) error {
	seen := make(map[string]bool, len(robots))
	for _, robot := range robots {
		if !validRobotID(robot.ID) || seen[robot.ID] {
			return ErrInvalidSnapshot
		}
		seen[robot.ID] = true
	}
	return nil
}

func indexHistory(previous []Incident, counter uint64) (map[key]int, error) {
	open := make(map[key]int)
	seen := make(map[string]bool, len(previous))
	var highest uint64
	for i, record := range previous {
		number, validID := idNumber(record.ID, 'I')
		if !validID || !validRobotID(record.RobotID) || seen[record.ID] || !validRecord(record) {
			return nil, ErrInvalidHistory
		}
		seen[record.ID], highest = true, max(highest, number)
		if record.Status == Open {
			k := key{robotID: record.RobotID, kind: record.Type}
			if _, exists := open[k]; exists {
				return nil, ErrInvalidHistory
			}
			open[k] = i
		}
	}
	if counter < highest {
		return nil, ErrInvalidCounter
	}
	return open, nil
}

func validRecord(record Incident) bool {
	return (record.Type == LowBattery || record.Type == TrafficBlocked || breakdownIncident(record.Type)) &&
		(record.Status == Open || record.Status == Resolved) &&
		(record.Severity == Medium || record.Severity == High)
}

func breakdownIncident(kind Type) bool {
	return kind == RobotUnresponsive || kind == MotorFailure || kind == SensorFailure || kind == CriticalBattery
}

func validRobotID(id string) bool {
	_, validTransport := idNumber(id, 'R')
	return validTransport || id == sim.MaintenanceRobotID
}

func idNumber(id string, prefix byte) (uint64, bool) {
	if len(id) < 2 || id[0] != prefix {
		return 0, false
	}
	number, err := strconv.ParseUint(id[1:], 10, 64)
	return number, err == nil && number > 0 && id[1:] == strconv.FormatUint(number, 10)
}

func compareIncidents(a, b Incident) int {
	if order := sim.CompareRobotIDs(a.RobotID, b.RobotID); order != 0 {
		return order
	}
	if order := cmp.Compare(a.Type, b.Type); order != 0 {
		return order
	}
	aNumber, _ := idNumber(a.ID, 'I')
	bNumber, _ := idNumber(b.ID, 'I')
	return cmp.Compare(aNumber, bNumber)
}
