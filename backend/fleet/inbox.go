package fleet

import (
	"context"
	"encoding/json"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/proposal"
)

const PendingProposalLimit = 50

// ReadPendingProposals is an operator-only, read-only view. Expiration remains
// governed by the existing decision gate; reading never changes proposal state.
func (s *Service) ReadPendingProposals(ctx context.Context) ([]ProposalView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	principal, ok := auth.FromContext(ctx)
	if !ok || principal.Role != auth.Operator || !s.authorize(ctx, proposal.OperatorIdentity{Subject: principal.Subject}) {
		return nil, ErrUnauthorized
	}
	var records []struct {
		Payload     proposal.Proposal
		Execution   Execution
		SubmittedBy string `json:"submitted_by"`
	}
	err := s.transact(ctx, func(tx *db.Tx) error {
		var payload []byte
		err := tx.QueryRow(`SELECT COALESCE(jsonb_agg(to_jsonb(p) ORDER BY p.created_at, p.id COLLATE "C"), '[]'::jsonb)
FROM (
 SELECT id, payload, execution, submitted_by, (payload->>'CreatedAt')::timestamptz AS created_at
 FROM fleet_proposals WHERE runtime_id=$1 AND state='pending'
 ORDER BY created_at, id COLLATE "C" LIMIT $2
) p`, s.runtimeID, PendingProposalLimit).Scan(&payload)
		if err != nil {
			return err
		}
		return json.Unmarshal(payload, &records)
	})
	if err != nil {
		return nil, ErrPersistence
	}
	views := make([]ProposalView, 0, len(records))
	for _, row := range records {
		views = append(views, ProposalView{Proposal: row.Payload, Execution: row.Execution, SubmittedBy: row.SubmittedBy, CurrentRuntime: true})
	}
	return views, nil
}
