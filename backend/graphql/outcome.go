package graphql

import (
	"context"
	"errors"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/graphql/model"
	"github.com/AyushM7/fleetops/backend/proposal"
)

var errInternal = errors.New("workflow unavailable")

func (r *mutationResolver) decide(ctx context.Context, id string, decision proposal.Decision) (*model.MutationResult, error) {
	principal, _ := auth.FromContext(ctx)
	result, err := r.Service.Decide(ctx, id, decision, proposal.OperatorIdentity{Subject: principal.Subject})
	return mutationResult(result, err), nil
}
func mutationResult(result fleet.Result, err error) *model.MutationResult {
	view := &model.MutationResult{ProposalID: result.Proposal.ID, State: string(result.Proposal.State), Execution: string(result.Execution), AuditCommitted: result.AuditCommitted, ExecutionAttempted: result.ExecutionAttempted}
	if err != nil {
		// Do not forward arbitrary dependency errors (or a database URL) to clients.
		message := "request denied"
		switch {
		case errors.Is(err, fleet.ErrPersistence), errors.Is(err, fleet.ErrPoisoned):
			message = "workflow unavailable; reconciliation may be required"
		case errors.Is(err, fleet.ErrUnauthorized):
			message = "unauthorized"
		case errors.Is(err, fleet.ErrSelfApproval):
			message = "self approval forbidden"
		case errors.Is(err, fleet.ErrForeignRuntime):
			message = "proposal belongs to a prior runtime"
		case errors.Is(err, proposal.ErrExpired):
			message = "proposal expired"
		case errors.Is(err, proposal.ErrInvalidTransition):
			message = "proposal already decided"
		}
		view.Error = &message
	}
	return view
}
