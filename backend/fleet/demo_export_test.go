//go:build fleetops_test

package fleet

import (
	"context"
	"time"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/sim"
)

// NewDemoReassignmentService keeps the trusted fixture test-only while allowing
// the demo owner to reuse one pool without accumulating per-run test cleanups.
func NewDemoReassignmentService(pool *db.Pool, clock func() time.Time) (*Service, error) {
	simulator, err := sim.NewReassignmentTestFixture()
	if err != nil {
		return nil, err
	}
	return newDemoService(simulator, pool, clock)
}

func NewDemoGuidedService(pool *db.Pool, clock func() time.Time) (*Service, error) {
	simulator, err := sim.NewReassignmentInitialFixture()
	if err != nil {
		return nil, err
	}
	return newDemoService(simulator, pool, clock)
}

// DemoRuntimeID identifies preproposal evidence without exposing capabilities.
func (s *Service) DemoRuntimeID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runtimeID
}

func newDemoService(simulator *sim.Sim, pool *db.Pool, clock func() time.Time) (*Service, error) {
	s, err := newService(simulator, simulator.Tick, pool, clock, func(ctx context.Context, id proposal.OperatorIdentity) bool {
		p, ok := auth.FromContext(ctx)
		return ok && p.Role == auth.Operator && p.Subject == id.Subject
	})
	if err != nil {
		return nil, err
	}
	s.layout = LayoutView{Width: 8, Height: 8}
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			s.layout.Cells = append(s.layout.Cells, CellView{X: x, Y: y, Kind: string(simulator.CellAt(sim.Point{X: x, Y: y}))})
		}
	}
	return s, nil
}
