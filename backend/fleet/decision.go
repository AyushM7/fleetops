package fleet

import (
	"context"
	"time"

	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/safety"
	"github.com/AyushM7/fleetops/backend/sim"
)

func (s *Service) decide(tx *db.Tx, id string, decision proposal.Decision, operator proposal.OperatorIdentity) (Result, error, error) {
	p := proposal.Proposal{ID: id}
	var denied error
	if s.poisoned {
		denied = ErrPoisoned
	} else if !s.authorize(tx.Context(), operator) {
		denied = ErrUnauthorized
	}
	if denied != nil {
		return s.deny(tx, p, decision, operator, denied)
	}
	row, err := s.load(tx, id)
	if err != nil {
		return Result{}, nil, err
	}
	if row == nil {
		return s.deny(tx, p, decision, operator, proposal.ErrUnknownProposal)
	}
	p = row.proposal
	if row.runtimeID != s.runtimeID {
		return s.deny(tx, p, decision, operator, ErrForeignRuntime)
	}
	if decision == proposal.Approve && row.submittedBy != "" && row.submittedBy == operator.Subject {
		return s.deny(tx, p, decision, operator, ErrSelfApproval)
	}
	if cached, exists := s.canonical[id]; !exists || cached != p {
		return s.deny(tx, p, decision, operator, ErrProposalMismatch)
	}
	// Lock acquisition may have taken time; use a fresh clock for the lifecycle.
	s.at = s.clock().UTC()
	candidate, denied := s.lifecycle.Decide(id, decision, operator)
	result := Result{Proposal: candidate.Proposal, Execution: row.execution}
	if err := s.save(tx, result.Proposal, result.Execution); err != nil {
		return result, denied, err
	}
	if err := s.audit(tx, candidate.Event); err != nil {
		return result, denied, err
	}
	if denied != nil || decision != proposal.Approve {
		return result, denied, nil
	}
	return s.execute(tx, result, operator)
}

func (s *Service) deny(tx *db.Tx, p proposal.Proposal, decision proposal.Decision, operator proposal.OperatorIdentity, reason error) (Result, error, error) {
	event := deniedResult(p, proposal.Attempt(decision), operator, s.at, reason).Event
	return Result{Proposal: p, Execution: NotExecuted}, reason, s.audit(tx, event)
}

func (s *Service) execute(tx *db.Tx, result Result, operator proposal.OperatorIdentity) (Result, error, error) {
	event := proposal.DecisionEvent{Attempt: "execute", At: s.clock().UTC(), Operator: operator, Before: proposal.Approved, Proposal: result.Proposal}
	// Stage an execution event before crossing the irreversible boundary. Its
	// final outcome is updated below; nothing is durable until COMMIT succeeds.
	event.Reason = "execution not completed"
	auditID, err := s.insertAudit(tx, event)
	if err != nil {
		return result, nil, err
	}
	denied := s.executionGate(tx.Context(), result.Proposal, operator)
	if denied == nil {
		result.ExecutionAttempted = true
		if err := s.commander.SetCharging(result.Proposal.Request.RobotID, result.Proposal.Request.StationID); err != nil {
			result.Execution, denied = Failed, ErrExecution
		} else {
			result.Execution = Succeeded
		}
	}
	if denied == proposal.ErrExpired {
		s.at = s.clock().UTC()
		expired, _ := s.lifecycle.Expire(result.Proposal.ID)
		result.Proposal = expired.Proposal
	}
	event.Proposal = result.Proposal
	event.At, event.Accepted, event.Reason = s.clock().UTC(), denied == nil, ""
	if denied != nil {
		event.Reason = denied.Error()
	}
	if err := s.save(tx, result.Proposal, result.Execution); err != nil {
		return result, denied, err
	}
	return result, denied, s.updateAudit(tx, auditID, event)
}

func (s *Service) executionGate(ctx context.Context, p proposal.Proposal, operator proposal.OperatorIdentity) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.authorize(ctx, operator) {
		return ErrUnauthorized
	}
	if err := safety.PreflightSetCharging(struct{ sim.Reader }{Reader: s.commander}, p.Request); err != nil {
		return err
	}
	now := s.clock().UTC()
	if now.IsZero() || now.Before(p.CreatedAt) {
		return proposal.ErrInvalidClock
	}
	if !now.Before(p.ExpiresAt) {
		return proposal.ErrExpired
	}
	if p.State != proposal.Approved {
		return proposal.ErrInvalidTransition
	}
	return ctx.Err()
}

func deniedResult(p proposal.Proposal, attempt proposal.Attempt, operator proposal.OperatorIdentity, at time.Time, reason error) proposal.Result {
	event := proposal.DecisionEvent{Attempt: attempt, At: at, Operator: operator, Before: p.State, Proposal: p, Reason: reason.Error()}
	return proposal.Result{Proposal: p, Event: event}
}
