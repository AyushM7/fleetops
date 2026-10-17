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

// External GraphQL tests receive only the production facade, never Driver or
// Commander. The trusted fixture and constructor exist only in test builds.
func NewReassignmentTestService(t *testing.T, clock func() time.Time) *Service {
	t.Helper()
	simulator, err := sim.NewReassignmentTestFixture()
	if err != nil {
		t.Fatal(err)
	}
	s, err := newService(simulator, simulator.Tick, integrationPool(t), clock, func(ctx context.Context, id proposal.OperatorIdentity) bool {
		p, ok := auth.FromContext(ctx)
		return ok && p.Role == auth.Operator && p.Subject == id.Subject
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
