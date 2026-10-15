package sim

import (
	"fmt"
	"math/rand"
)

// Sim owns deterministic state and is not goroutine-safe.
type Sim struct {
	cfg               Config
	state             State
	rng               *rand.Rand
	jobCells          []Point
	unresponsiveTicks map[string]int
}

// New constructs an isolated deterministic simulator from a configuration and seed.
func New(cfg Config, seed int64) (*Sim, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg = cfg.Clone()
	s := &Sim{
		cfg:               cfg,
		rng:               rand.New(rand.NewSource(seed)),
		jobCells:          cfg.JobCells(),
		unresponsiveTicks: make(map[string]int),
		state: State{
			Seed:     seed,
			Robots:   make([]RobotState, 0, len(cfg.RobotStarts)),
			Jobs:     make([]JobState, 0, cfg.MaxJobs),
			Stations: make([]StationState, 0, len(cfg.Stations)),
			Faults:   []FaultState{},
		},
	}
	for i, p := range cfg.RobotStarts {
		id := fmt.Sprintf("R%d", i+1)
		s.state.Robots = append(s.state.Robots, RobotState{
			ID: id, X: p.X, Y: p.Y, Battery: 100, Status: RobotIdle,
		})
		s.unresponsiveTicks[id] = 0
	}
	for i, p := range cfg.Stations {
		s.state.Stations = append(s.state.Stations, StationState{
			ID: fmt.Sprintf("S%d", i+1), X: p.X, Y: p.Y,
		})
	}
	for i := 0; i < cfg.InitialJobs; i++ {
		s.addJob()
	}
	return s, nil
}

// Tick is DYNAMICS: it advances the background simulation, not an operator command.
func (s *Sim) Tick() {
	s.state.Tick++
	if s.state.Tick%s.cfg.NewJobEvery == 0 && len(s.state.Jobs) < s.cfg.MaxJobs {
		s.addJob()
	}
	s.stepRobots()
}

// CellAt is READ: it looks up a cell without mutating simulation state.
func (s *Sim) CellAt(p Point) CellType {
	return s.cfg.CellTypeAt(p)
}

// Snapshot is READ: it returns owned copies of state, including nested fault data.
func (s *Sim) Snapshot() State {
	state := s.state
	state.Robots = append([]RobotState{}, s.state.Robots...)
	state.Jobs = append([]JobState{}, s.state.Jobs...)
	state.Stations = append([]StationState{}, s.state.Stations...)
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
