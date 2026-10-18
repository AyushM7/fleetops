//go:build fleetops_test

package fleet

import (
	"context"
	"fmt"
	"time"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/sim"
)

// DemoRuntimeID records ownership even when no proposal is necessary.
func DemoRuntimeID(s *Service) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runtimeID
}

// NewDemoReassignmentService keeps the trusted fixture test-only while allowing
// the demo owner to reuse one pool without accumulating per-run test cleanups.
func NewDemoReassignmentService(pool *db.Pool, clock func() time.Time) (*Service, error) {
	return NewDemoScenarioService("blocked-reassignment", pool, clock)
}

// NewDemoScenarioService accepts only trusted demo presets, never visitor config.
func NewDemoScenarioService(id string, pool *db.Pool, clock func() time.Time) (*Service, error) {
	simulator, width, height, err := demoSimulator(id)
	if err != nil {
		return nil, err
	}
	return demoService(simulator, width, height, pool, clock)
}

func demoSimulator(id string) (*sim.Sim, int, int, error) {
	if id == "healthy-operations" || id == "routine-charging" {
		cfg := sim.WarehouseConfig()
		if id == "routine-charging" {
			cfg = sim.DefaultConfig()
			cfg.InitialJobs, cfg.MaxJobs, cfg.NewJobEvery = 0, 1, 10000
			cfg.RobotStarts[0] = cfg.Stations[0]
		}
		s, err := sim.New(cfg, 42)
		if err != nil {
			return nil, 0, 0, err
		}
		if id == "routine-charging" {
			for range 1000 {
				s.Tick()
			}
		}
		return s, cfg.Width, cfg.Height, nil
	}
	if id != "blocked-reassignment" && id != "blocked-no-candidate" {
		return nil, 0, 0, fmt.Errorf("unsupported demo scenario")
	}
	simulator, err := sim.NewReassignmentTestFixture()
	if err == nil && id == "blocked-no-candidate" {
		for _, robot := range []string{"R3", "R4", "R5"} {
			if err = simulator.InjectLowBattery(robot); err != nil {
				break
			}
		}
	}
	return simulator, 8, 8, err
}

func demoService(simulator *sim.Sim, width, height int, pool *db.Pool, clock func() time.Time) (*Service, error) {
	s, err := newService(simulator, simulator.Tick, pool, clock, func(ctx context.Context, id proposal.OperatorIdentity) bool {
		p, ok := auth.FromContext(ctx)
		return ok && p.Role == auth.Operator && p.Subject == id.Subject
	})
	if err != nil {
		return nil, err
	}
	s.layout = LayoutView{Width: width, Height: height}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			s.layout.Cells = append(s.layout.Cells, CellView{X: x, Y: y, Kind: string(simulator.CellAt(sim.Point{X: x, Y: y}))})
		}
	}
	return s, nil
}
