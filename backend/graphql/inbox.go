package graphql

import (
	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/graphql/model"
)

func proposalModel(row *fleet.ProposalView) *model.Proposal {
	p := row.Proposal
	return &model.Proposal{ID: p.ID, Action: string(p.Action), RobotID: p.Request.RobotID, StationID: p.Request.StationID, CreatedAt: p.CreatedAt, ExpiresAt: p.ExpiresAt, State: string(p.State), Execution: string(row.Execution), SubmittedBy: row.SubmittedBy, CurrentRuntime: row.CurrentRuntime}
}
