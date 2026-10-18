package graphql

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/graphql/model"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

type agentRunService interface {
	StartAgentRun(context.Context, string, fleet.AgentMetadata) (*fleet.AgentRun, error)
	FinishAgentRun(context.Context, string, fleet.AgentFinish) (fleet.AgentReceipt, error)
	ReadAgentRuns(context.Context, string, int) ([]fleet.AgentRun, error)
}

func agentRunError() error {
	return &gqlerror.Error{Message: "agent provenance unavailable or denied", Extensions: map[string]any{"code": "AGENT_PROVENANCE_DENIED"}}
}
func agentRunModel(run *fleet.AgentRun) *model.AgentRun {
	data, _ := json.Marshal(run)
	result := &model.AgentRun{ID: run.ID, RuntimeID: run.RuntimeID, ObservationID: run.ObservationID, Data: string(data)}
	if run.Proposal != nil {
		result.Proposal = proposalModel(run.Proposal)
	}
	return result
}
func (r *mutationResolver) startAgentRun(ctx context.Context, observation, metadata string) (*model.AgentRun, error) {
	service, ok := r.Service.(agentRunService)
	if !ok {
		return nil, agentRunError()
	}
	var input fleet.AgentMetadata
	if fleet.DecodeAgentJSON(metadata, &input) != nil {
		return nil, agentRunError()
	}
	run, err := service.StartAgentRun(ctx, observation, input)
	if err != nil {
		return nil, agentRunError()
	}
	return agentRunModel(run), nil
}
func (r *mutationResolver) finishAgentRun(ctx context.Context, id, completion string) (*model.AgentRunReceipt, error) {
	service, ok := r.Service.(agentRunService)
	if !ok {
		return nil, agentRunError()
	}
	var input fleet.AgentFinish
	if fleet.DecodeAgentJSON(completion, &input) != nil {
		return nil, agentRunError()
	}
	receipt, err := service.FinishAgentRun(ctx, id, input)
	if err != nil {
		return nil, agentRunError()
	}
	result := &model.AgentRunReceipt{RunID: receipt.RunID, Recorded: receipt.Recorded, Denial: receipt.Denial}
	if receipt.Result.Proposal.ID != "" {
		result.ProposalResult = mutationResult(receipt.Result, nil)
		if receipt.Denial != "" {
			result.ProposalResult.Error = &receipt.Denial
		}
	}
	return result, nil
}
func (r *queryResolver) agentRuns(ctx context.Context, id string, limit int) ([]*model.AgentRun, error) {
	service, ok := r.Service.(agentRunService)
	if !ok {
		return nil, agentRunReadError(fleet.ErrPersistence)
	}
	runs, err := service.ReadAgentRuns(ctx, id, limit)
	if err != nil {
		return nil, agentRunReadError(err)
	}
	result := make([]*model.AgentRun, 0, len(runs))
	for i := range runs {
		result = append(result, agentRunModel(&runs[i]))
	}
	return result, nil
}

func agentRunReadError(err error) error {
	code, message := "UNAVAILABLE", "agent provenance unavailable"
	if errors.Is(err, fleet.ErrUnauthorized) {
		code, message = "FORBIDDEN", "agent provenance access required"
	}
	return &gqlerror.Error{Message: message, Extensions: map[string]any{"code": code}}
}
