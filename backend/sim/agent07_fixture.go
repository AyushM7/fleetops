//go:build fleetops_test

package sim

import "fmt"

// NewAgent07TestFixture constructs initial conditions only in explicitly tagged
// evaluation builds. These edits precede fleet.Service runtime/identity creation.
func NewAgent07TestFixture(variant string) (*Sim, error) {
	if variant == "healthy" {
		cfg := DefaultConfig()
		cfg.InitialJobs, cfg.MaxJobs, cfg.NewJobEvery = 0, 1, 10000
		cfg.AutoDispatch = false
		return New(cfg, 42)
	}
	s, err := NewReassignmentTestFixture()
	if err != nil {
		return nil, err
	}
	switch variant {
	case "blocked", "poisoned":
	case "non-first":
		s.state.Robots[2].X, s.state.Robots[2].Y = 7, 7
		s.state.Robots[3].X, s.state.Robots[3].Y = 4, 3
	case "tradeoff":
		for i, battery := range []int{48, 96, 65} {
			s.state.Robots[i+2].Battery = battery
		}
	case "tie":
		s.state.Robots[3].X, s.state.Robots[3].Y = 5, 4
	case "none":
		for i := 2; i < len(s.state.Robots); i++ {
			s.state.Robots[i].Battery = 29
		}
	default:
		return nil, fmt.Errorf("unknown agent07 fixture variant")
	}
	return s, nil
}
