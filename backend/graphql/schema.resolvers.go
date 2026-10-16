package graphql

import (
	"context"
	"errors"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/graphql/model"
	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/safety"
)

var errInternal = errors.New("workflow unavailable")

func (r *mutationResolver) SubmitProposal(ctx context.Context, input model.SetChargingInput) (*model.MutationResult, error) {
	result, err := r.Service.Submit(ctx, safety.SetChargingRequest{RobotID: input.RobotID, StationID: input.StationID}, input.ExpiresAt)
	return mutationResult(result, err), nil
}
func (r *mutationResolver) ApproveProposal(ctx context.Context, id string) (*model.MutationResult, error) {
	return r.decide(ctx, id, proposal.Approve)
}
func (r *mutationResolver) RejectProposal(ctx context.Context, id string) (*model.MutationResult, error) {
	return r.decide(ctx, id, proposal.Reject)
}
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
func (r *queryResolver) FleetState(ctx context.Context) (*model.FleetState, error) {
	state, err := r.Service.ReadState(ctx)
	if err != nil {
		return nil, errInternal
	}
	view := &model.FleetState{Tick: state.Tick, Poisoned: state.Poisoned, Robots: []*model.Robot{}, Stations: []*model.Station{}}
	for _, v := range state.Robots {
		view.Robots = append(view.Robots, &model.Robot{ID: v.ID, X: v.X, Y: v.Y, Battery: v.Battery, Status: v.Status, JobID: v.JobID, WaitTicks: v.WaitTicks, Blocked: v.Blocked})
	}
	for _, v := range state.Stations {
		view.Stations = append(view.Stations, &model.Station{ID: v.ID, X: v.X, Y: v.Y, OccupiedBy: v.OccupiedBy, ReservedBy: v.ReservedBy})
	}
	return view, nil
}
func (r *queryResolver) Proposal(ctx context.Context, id string) (*model.Proposal, error) {
	row, err := r.Service.ReadProposal(ctx, id)
	if err != nil {
		return nil, errInternal
	}
	if row == nil {
		return nil, nil
	}
	p := row.Proposal
	return &model.Proposal{ID: p.ID, Action: string(p.Action), RobotID: p.Request.RobotID, StationID: p.Request.StationID, CreatedAt: p.CreatedAt, ExpiresAt: p.ExpiresAt, State: string(p.State), Execution: string(row.Execution), SubmittedBy: row.SubmittedBy, CurrentRuntime: row.CurrentRuntime}, nil
}
func (r *Resolver) Mutation() MutationResolver { return &mutationResolver{r} }
func (r *Resolver) Query() QueryResolver       { return &queryResolver{r} }

type mutationResolver struct{ *Resolver }
type queryResolver struct{ *Resolver }
