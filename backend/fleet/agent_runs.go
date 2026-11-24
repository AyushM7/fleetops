package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/safety"
	"github.com/jackc/pgx/v5"
	"reflect"
	"time"
)

// DecideRecoveryProposal requires the persisted V3 evidence to still match Go
// at the execution boundary. Idle ticks alone do not invalidate the evidence.
func (s *Service) DecideRecoveryProposal(ctx context.Context, id, runID string, decision proposal.Decision, operator proposal.OperatorIdentity) (Result, error) {
	return s.decideProposal(ctx, id, decision, operator, func(tx *db.Tx) (error, error) {
		run, _, _, _, err := s.loadAgentRun(tx, runID)
		if err != nil {
			return nil, err
		}
		if run == nil || run.RuntimeID != s.runtimeID || run.Metadata.Contract != "recovery-decision-v3" ||
			!run.DecisionVerified || run.ProposalID == nil || *run.ProposalID != id {
			return ErrAgentRecord, nil
		}
		if !s.stillGroundedRecovery(run) {
			return ErrObservation, nil
		}
		return nil, nil
	})
}

func (s *Service) stillGroundedRecovery(run *AgentRun) bool {
	view, err := s.readStateLocked()
	if err != nil {
		return false
	}
	facts, err := agentFacts(view, run.Metadata.Contract)
	if err != nil || facts.Poisoned || facts.Tick < run.Facts.Tick {
		return false
	}
	facts.Tick = run.Facts.Tick
	if !reflect.DeepEqual(facts, run.Facts) {
		return false
	}
	// A new breakdown on the same robot is a separate review, even if
	// its current incident label and numerical facts happen to match.
	for _, fault := range s.commander.Snapshot().Faults {
		if fault.Tick <= run.Facts.Tick || len(fault.Robots) != 1 {
			continue
		}
		for _, incident := range facts.Incidents {
			if fault.Robots[0] == incident.RobotID && string(fault.Type) == incident.Type {
				return false
			}
		}
	}
	return true
}

func (s *Service) StartAgentRun(ctx context.Context, observation string, metadata AgentMetadata) (*AgentRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	principal, ok := auth.FromContext(ctx)
	if !ok || principal.Role != auth.Proposer {
		return nil, ErrUnauthorized
	}
	if !metadata.valid() {
		return nil, ErrAgentRecord
	}
	if err := s.checkObservation([]string{observation}, true); err != nil {
		return nil, err
	}
	view, err := s.readStateLocked()
	if err != nil {
		return nil, err
	}
	facts, err := agentFacts(view, metadata.Contract)
	if err != nil {
		return nil, err
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	id = "A" + id[1:]
	run := &AgentRun{ID: id, RuntimeID: s.runtimeID, ObservationID: observation, StartedAt: s.clock().UTC(), Metadata: metadata, Facts: facts, CostStatus: "unavailable"}
	if run.StartedAt.IsZero() {
		return nil, ErrAgentRecord
	}
	err = s.transact(ctx, func(tx *db.Tx) error {
		_, err := tx.Exec(`INSERT INTO fleet_agent_runs(id,runtime_id,observation_id,owner_subject,started_at,metadata,facts) VALUES($1,$2,$3,$4,$5,$6::jsonb,$7::jsonb)`,
			id, s.runtimeID, observation, principal.Subject, run.StartedAt, canonicalAgentJSON(metadata), canonicalAgentJSON(facts))
		return err
	})
	if err != nil {
		return nil, ErrPersistence
	}
	return run, nil
}

func (s *Service) FinishAgentRun(ctx context.Context, id string, input AgentFinish) (AgentReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.poisonOnPanic()
	principal, ok := auth.FromContext(ctx)
	if !ok || principal.Role != auth.Proposer {
		return AgentReceipt{}, ErrUnauthorized
	}
	if !agentID.MatchString(id) || input.RuntimeID != s.runtimeID || input.ObservationID == "" || !input.Completion.valid() || len(canonicalAgentJSON(input)) > 32768 {
		return AgentReceipt{}, ErrAgentRecord
	}
	digest := sha256.Sum256(canonicalAgentJSON(input))
	hash := hex.EncodeToString(digest[:])
	receipt := AgentReceipt{RunID: id}
	var denied error
	var created proposal.Proposal
	err := s.transact(ctx, func(tx *db.Tx) error {
		run, owner, oldHash, oldReceipt, err := s.loadAgentRun(tx, id)
		if err != nil {
			return err
		}
		if run == nil || owner != principal.Subject || run.RuntimeID != s.runtimeID || run.ObservationID != input.ObservationID {
			denied = ErrAgentRecord
			return nil
		}
		if run.CompletedAt != nil {
			if oldHash != hash {
				denied = ErrAgentRecord
				return nil
			}
			receipt = oldReceipt
			return nil
		}
		if input.Completion.Status == "accepted" && !verifyAgentDecision(input.Completion.Decision, run.Facts, run.Metadata.Objective) {
			denied = ErrAgentRecord
			return nil
		}
		if input.Submit != nil && (input.Completion.Status != "accepted" || input.Completion.Decision == nil || input.Completion.Decision.Kind != "select") {
			denied = ErrAgentRecord
			return nil
		}
		receipt, created, err = s.completeAgentRun(tx, run, input, hash)
		return err
	})
	if err != nil {
		if input.Submit != nil {
			s.persistenceFailure(receipt.Result.Proposal.ID, false)
		}
		return AgentReceipt{RunID: id}, ErrPersistence
	}
	if created.ID != "" {
		s.canonical[created.ID] = created
	}
	return receipt, denied
}

func (s *Service) loadAgentRun(tx *db.Tx, id string) (*AgentRun, string, string, AgentReceipt, error) {
	var run AgentRun
	var owner string
	var hash *string
	var metadata, facts, completion, receipt []byte
	err := tx.QueryRow(`SELECT id,runtime_id,observation_id,owner_subject,started_at,completed_at,metadata,facts,completion,completion_hash,proposal_id,receipt FROM fleet_agent_runs WHERE id=$1 FOR UPDATE`, id).
		Scan(&run.ID, &run.RuntimeID, &run.ObservationID, &owner, &run.StartedAt, &run.CompletedAt, &metadata, &facts, &completion, &hash, &run.ProposalID, &receipt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", "", AgentReceipt{}, nil
	}
	if err != nil {
		return nil, "", "", AgentReceipt{}, err
	}
	var result AgentReceipt
	run.SubmissionOutcome = "not_completed"
	if json.Unmarshal(metadata, &run.Metadata) != nil || json.Unmarshal(facts, &run.Facts) != nil {
		return nil, "", "", result, ErrPersistence
	}
	if run.CompletedAt != nil {
		if json.Unmarshal(completion, &run.Completion) != nil || json.Unmarshal(receipt, &result) != nil {
			return nil, "", "", result, ErrPersistence
		}
		run.DecisionVerified = run.Completion.Status == "accepted" && verifyAgentDecision(run.Completion.Decision, run.Facts, run.Metadata.Objective)
		elapsed := run.CompletedAt.Sub(run.StartedAt).Milliseconds()
		run.ServerElapsedMS = &elapsed
		run.SubmissionOutcome = "not_requested"
		if result.Denial != "" {
			run.SubmissionOutcome = result.Denial
		} else if run.ProposalID != nil {
			run.SubmissionOutcome = "pending_proposal_created"
		}
	}
	run.CostStatus = "unavailable"
	oldHash := ""
	if hash != nil {
		oldHash = *hash
	}
	return &run, owner, oldHash, result, nil
}

func (s *Service) ReadAgentRuns(ctx context.Context, id string, limit int) ([]AgentRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	principal, ok := auth.FromContext(ctx)
	if !ok || !allowed(string(principal.Role), "proposer", "operator") {
		return nil, ErrUnauthorized
	}
	if limit < 1 || limit > 20 || id != "" && !agentID.MatchString(id) {
		return nil, ErrAgentRecord
	}
	runs := []AgentRun{}
	err := s.transact(ctx, func(tx *db.Tx) error {
		// A bounded ID aggregation avoids exposing the database cursor outside its transaction.
		var ids []byte
		err := tx.QueryRow(`SELECT COALESCE(jsonb_agg(id),'[]'::jsonb) FROM (SELECT id FROM fleet_agent_runs WHERE runtime_id=$1 AND ($2 OR owner_subject=$3) AND ($4='' OR id=$4) ORDER BY started_at DESC,id LIMIT $5) q`, s.runtimeID, principal.Role == auth.Operator, principal.Subject, id, limit).Scan(&ids)
		if err != nil {
			return err
		}
		var keys []string
		if json.Unmarshal(ids, &keys) != nil {
			return ErrPersistence
		}
		for _, key := range keys {
			run, _, _, _, err := s.loadAgentRun(tx, key)
			if err != nil {
				return err
			}
			if run.ProposalID != nil {
				stored, err := s.load(tx, *run.ProposalID)
				if err != nil {
					return err
				}
				if stored == nil || stored.runtimeID != s.runtimeID {
					return ErrPersistence
				}
				run.Proposal = &ProposalView{stored.proposal, stored.execution, stored.submittedBy, true}
			}
			runs = append(runs, *run)
		}
		return nil
	})
	if err != nil {
		return nil, ErrPersistence
	}
	return runs, nil
}

func (s *Service) completeAgentRun(tx *db.Tx, run *AgentRun, input AgentFinish, hash string) (AgentReceipt, proposal.Proposal, error) {
	receipt := AgentReceipt{RunID: run.ID}
	var created proposal.Proposal
	if input.Submit != nil {
		d := input.Completion.Decision
		proposalID, err := newID()
		if err != nil {
			return receipt, created, err
		}
		s.at = s.clock().UTC()
		key := *d.Selected
		p := proposal.Proposal{ID: proposalID, Action: proposal.ReassignUnstartedJob, Reassignment: safety.ReassignUnstartedJobRequest{JobID: key.JobID, FromRobotID: key.FromRobotID, ToRobotID: key.ToRobotID}, CreatedAt: s.at, ExpiresAt: input.Submit.ExpiresAt.UTC()}
		observation := input.ObservationID
		elapsed := s.at.Sub(run.StartedAt)
		if run.Metadata.Contract == "recovery-decision-v3" && observation != s.observationID() &&
			elapsed >= 0 && elapsed <= 30*time.Second && s.stillGroundedRecovery(run) {
			observation = s.observationID()
		}
		candidate, admission, err := s.createProposal(tx, p, []string{observation})
		if err != nil {
			return receipt, created, err
		}
		receipt.Result = Result{Proposal: candidate.Proposal, Execution: NotExecuted, AuditCommitted: true}
		if admission == nil {
			created = candidate.Proposal
			run.ProposalID = &created.ID
		} else {
			receipt.Denial = "proposal_denied"
			if errors.Is(admission, ErrObservation) {
				receipt.Denial = "stale_observation"
			}
		}
	}
	completed := s.clock().UTC()
	if completed.Before(run.StartedAt) {
		return receipt, created, ErrAgentRecord
	}
	receipt.Recorded = true
	_, err := tx.Exec(`UPDATE fleet_agent_runs SET completed_at=$2,completion=$3::jsonb,completion_hash=$4,proposal_id=$5,receipt=$6::jsonb WHERE id=$1`, run.ID, completed, canonicalAgentJSON(input.Completion), hash, run.ProposalID, canonicalAgentJSON(receipt))
	return receipt, created, err
}
