package fleet

import (
	"encoding/json"
	"errors"

	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/jackc/pgx/v5"
)

type stored struct {
	runtimeID   string
	proposal    proposal.Proposal
	execution   Execution
	submittedBy string
}

func (s *Service) insert(tx *db.Tx, p proposal.Proposal, submittedBy string) error {
	payload, err := json.Marshal(p)
	if err != nil {
		return ErrPersistence
	}
	_, err = tx.Exec("INSERT INTO fleet_proposals (id, runtime_id, state, execution, payload, submitted_by) VALUES ($1,$2,$3,'none',$4::jsonb,$5)", p.ID, s.runtimeID, p.State, payload, submittedBy)
	return err
}

func (s *Service) load(tx *db.Tx, id string) (*stored, error) {
	var row stored
	var payload []byte
	err := tx.QueryRow("SELECT runtime_id, execution, payload, submitted_by FROM fleet_proposals WHERE id=$1 FOR UPDATE", id).Scan(&row.runtimeID, &row.execution, &payload, &row.submittedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(payload, &row.proposal); err != nil {
		return nil, ErrPersistence
	}
	return &row, nil
}

func (s *Service) save(tx *db.Tx, p proposal.Proposal, execution Execution) error {
	payload, err := json.Marshal(p)
	if err != nil {
		return ErrPersistence
	}
	tag, err := tx.Exec("UPDATE fleet_proposals SET state=$2, execution=$3, payload=$4::jsonb WHERE id=$1 AND runtime_id=$5", p.ID, p.State, execution, payload, s.runtimeID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrPersistence
	}
	return nil
}

func (s *Service) audit(tx *db.Tx, event proposal.DecisionEvent) error {
	_, err := s.insertAudit(tx, event)
	return err
}

func (s *Service) insertAudit(tx *db.Tx, event proposal.DecisionEvent) (int64, error) {
	payload, err := json.Marshal(event)
	if err != nil {
		return 0, ErrPersistence
	}
	var id int64
	err = tx.QueryRow(`INSERT INTO fleet_audit (runtime_id,proposal_id,attempt,at,operator_subject,accepted,reason,payload)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb) RETURNING id`, s.runtimeID, event.Proposal.ID, event.Attempt, event.At, event.Operator.Subject, event.Accepted, event.Reason, payload).Scan(&id)
	return id, err
}

func (s *Service) updateAudit(tx *db.Tx, id int64, event proposal.DecisionEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return ErrPersistence
	}
	tag, err := tx.Exec("UPDATE fleet_audit SET at=$2,accepted=$3,reason=$4,payload=$5::jsonb WHERE id=$1", id, event.At, event.Accepted, event.Reason, payload)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrPersistence
	}
	return nil
}
