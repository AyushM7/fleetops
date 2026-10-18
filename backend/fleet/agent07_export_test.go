//go:build fleetops_test

package fleet

import (
	"context"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/sim"
)

func NewAgent07TestService(t *testing.T, variant string, clock func() time.Time) *Service {
	t.Helper()
	simulator, err := sim.NewAgent07TestFixture(variant)
	if err != nil {
		t.Fatal(err)
	}
	s, err := newService(simulator, simulator.Tick, integrationPool(t), clock, func(ctx context.Context, op proposal.OperatorIdentity) bool {
		p, ok := auth.FromContext(ctx)
		return ok && p.Role == auth.Operator && p.Subject == op.Subject
	})
	if err != nil {
		t.Fatal(err)
	}
	s.poisoned = variant == "poisoned"
	return s
}
