package sim

import (
	"fmt"
	"math/rand"
	"slices"
)

// Sim owns deterministic state and is not goroutine-safe.
type Sim struct {
	cfg               Config
	state             State
	rng               *rand.Rand
	jobCells          []Point
	jobSequence       int
	idleTicks         map[string]int
	unresponsiveTicks map[string]int
	navigation        map[string]robotNavigation
	recovery          map[string]robotRecovery
	protected         map[Point]bool
	traffic           *trafficReservations
	operationsHold    dispatchHold
}

// New constructs an isolated deterministic simulator from a configuration and seed.
func New(cfg Config, seed int64) (*Sim, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg = cfg.Clone()
	starts := cfg.robotStarts()
	s := &Sim{
		cfg:               cfg,
		rng:               rand.New(rand.NewSource(seed)),
		jobCells:          cfg.jobEndpoints(),
		unresponsiveTicks: make(map[string]int),
		navigation:        make(map[string]robotNavigation),
		recovery:          make(map[string]robotRecovery),
		protected:         protectedCells(cfg),
		state: State{
			Seed:     seed,
			Robots:   make([]RobotState, 0, len(starts)),
			Jobs:     make([]JobState, 0, cfg.MaxJobs),
			Stations: make([]StationState, 0, len(cfg.Stations)),
			Faults:   []FaultState{},
		},
	}
	for i, p := range starts {
		robot := RobotState{ID: fmt.Sprintf("R%d", i+1), X: p.X, Y: p.Y, Battery: 100, Status: RobotIdle}
		if cfg.MaintenanceBay != nil {
			robot.Role = RoleTransport
			if i == warehouseTransportCount {
				robot.ID, robot.Role = MaintenanceRobotID, RoleMaintenance
			}
		}
		s.state.Robots = append(s.state.Robots, robot)
		s.unresponsiveTicks[robot.ID] = 0
	}
	stationPrefix := "S"
	if cfg.MaintenanceBay != nil {
		stationPrefix = "C"
		s.idleTicks = make(map[string]int, len(starts))
	}
	for i, p := range cfg.Stations {
		s.state.Stations = append(s.state.Stations, StationState{
			ID: fmt.Sprintf("%s%d", stationPrefix, i+1), X: p.X, Y: p.Y,
		})
	}
	for i := 0; i < cfg.InitialJobs; i++ {
		s.addJob()
	}
	s.state.Aisles = s.snapshotAisles()
	return s, nil
}

// Tick is DYNAMICS: it advances the background simulation, not an operator command.
func (s *Sim) Tick() {
	s.state.Tick++
	if s.state.Tick%s.cfg.NewJobEvery == 0 {
		s.addJob()
	}
	s.stepRobots()
	if s.cfg.MaintenanceBay != nil {
		for i := range s.state.Jobs {
			if job := &s.state.Jobs[i]; job.Status != JobDone {
				s.trackJobDeadline(job)
			}
		}
		s.trimCompletedJobs()
	}
	s.state.Aisles = s.snapshotAisles()
}

// CellAt is READ: it looks up a cell without mutating simulation state.
func (s *Sim) CellAt(p Point) CellType {
	return s.cfg.CellTypeAt(p)
}

// Snapshot is READ: it returns owned copies of state, including nested fault data.
func (s *Sim) Snapshot() State {
	state := s.state
	state.Robots = append([]RobotState{}, s.state.Robots...)
	if s.cfg.MaintenanceBay != nil {
		for i := range state.Robots {
			robot := &state.Robots[i]
			robot.Maintenance = robot.Role == RoleMaintenance
			robot.Activity = s.robotActivity(*robot)
		}
	}
	state.Jobs = append([]JobState{}, s.state.Jobs...)
	if s.cfg.MaintenanceBay != nil {
		for i := range state.Jobs {
			job := &state.Jobs[i]
			job.QueueAgeTicks = job.AssignedTick - job.CreatedTick
			if job.Status == JobPending {
				job.QueueAgeTicks = state.Tick - job.CreatedTick
			}
			if job.Status == JobInProgress {
				job.CargoRobotID = job.AssignedRobot
			}
		}
	}
	state.Stations = append([]StationState{}, s.state.Stations...)
	state.TrafficDeadlocks = slices.Clone(s.state.TrafficDeadlocks)
	for i := range state.TrafficDeadlocks {
		state.TrafficDeadlocks[i].Robots = slices.Clone(state.TrafficDeadlocks[i].Robots)
	}
	state.Aisles = s.snapshotAisles()
	state.Faults = make([]FaultState, len(s.state.Faults))
	for i, fault := range s.state.Faults {
		state.Faults[i] = copyFault(fault)
	}
	return state
}

func copyFault(fault FaultState) FaultState {
	fault.Robots = append([]string{}, fault.Robots...)
	if fault.Aisle != nil {
		aisle := *fault.Aisle
		fault.Aisle = &aisle
	}
	return fault
}

func (s *Sim) recordFault(kind FaultType, robots []string, aisle *int) {
	fault := FaultState{
		ID: fmt.Sprintf("F%d", len(s.state.Faults)+1), Type: kind,
		Robots: robots, Aisle: aisle, Tick: s.state.Tick,
	}
	s.state.Faults = append(s.state.Faults, copyFault(fault))
}
