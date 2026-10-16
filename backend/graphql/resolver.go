package graphql

import (
	"context"
	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/safety"
	"time"
)

// Service exposes workflow operations and detached reads, never simulator capabilities.
type Service interface {
	Submit(context.Context, safety.SetChargingRequest, time.Time) (fleet.Result, error)
	Decide(context.Context, string, proposal.Decision, proposal.OperatorIdentity) (fleet.Result, error)
	ReadState(context.Context) (fleet.StateView, error)
	ReadProposal(context.Context, string) (*fleet.ProposalView, error)
}
type Resolver struct{ Service Service }
