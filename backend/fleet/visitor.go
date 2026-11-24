package fleet

import (
	"context"
	"time"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/sim"
)

// NewVisitor owns a fresh simulator with a fixed preset and the supplied private
// database pool. Approval still requires independently authenticated operators.
func NewVisitor(id string, pool *db.Pool) (*Service, error) {
	if pool == nil {
		return nil, ErrConfig
	}
	simulator, width, height, err := sim.NewVisitorPreset(id)
	if err != nil {
		return nil, err
	}
	s, err := newService(simulator, simulator.Tick, pool, time.Now, visitorOperator)
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

// NewVisitorWarehouse constructs a seeded V2 simulator owned by one visitor.
func NewVisitorWarehouse(seed int64, pool *db.Pool) (*Service, error) {
	return NewVisitorWarehouseWithChargeTarget(seed, pool, 0)
}

// NewVisitorWarehouseWithChargeTarget uses the layout default when target is zero.
func NewVisitorWarehouseWithChargeTarget(seed int64, pool *db.Pool, target int) (*Service, error) {
	cfg := sim.WarehouseV2Config()
	if target != 0 {
		cfg.ChargeRelease = target
	}
	return New(cfg, seed, pool, time.Now, visitorOperator)
}

func visitorOperator(ctx context.Context, id proposal.OperatorIdentity) bool {
	p, ok := auth.FromContext(ctx)
	return ok && p.Role == auth.Operator && p.Subject == id.Subject
}

// RuntimeID is read-only provenance and never an authorization capability.
func (s *Service) RuntimeID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runtimeID
}
