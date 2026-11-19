package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/safety"
	"github.com/AyushM7/fleetops/backend/sim"
)

var (
	ErrAutomaticCommand = errors.New("invalid V2 automatic command")
	ErrAutomaticExpired = errors.New("automatic command has expired or exceeds its freshness window")
)

const automaticMaxTTL = 30 * time.Second

type AutomaticCommand struct {
	RuntimeID            string                              `json:"runtimeId"`
	ObservationID        string                              `json:"observationId"`
	ExpiresAt            time.Time                           `json:"expiresAt"`
	AssignJobBatch       *sim.AssignJobBatchRequest          `json:"assignJobBatch,omitempty"`
	ReassignUnstartedJob *safety.ReassignUnstartedJobRequest `json:"reassignUnstartedJob,omitempty"`
}

type AutomaticResult struct {
	AuditID        int64     `json:"auditId"`
	Execution      Execution `json:"execution"`
	AuditCommitted bool      `json:"auditCommitted"`
}

func (c AutomaticCommand) validate() error {
	if len(c.RuntimeID) != 33 || len(c.ObservationID) != 64 || (c.AssignJobBatch == nil) == (c.ReassignUnstartedJob == nil) {
		return ErrAutomaticCommand
	}
	if c.AssignJobBatch != nil {
		return c.AssignJobBatch.Validate()
	}
	r := c.ReassignUnstartedJob
	for _, id := range []string{r.JobID, r.FromRobotID, r.ToRobotID} {
		if len(id) < 2 || len(id) > 20 {
			return ErrAutomaticCommand
		}
	}
	return nil
}

// ExecuteAutomatic owns admission, serialized Go execution and the durable receipt.
func (s *Service) ExecuteAutomatic(ctx context.Context, command AutomaticCommand) (AutomaticResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.poisonOnPanic()
	received := s.clock().UTC()
	principal, _ := auth.FromContext(ctx)
	payload := []byte("{}")
	denied := command.validate()
	if denied == nil {
		var err error
		payload, err = json.Marshal(command)
		if err != nil {
			denied, payload = ErrAutomaticCommand, []byte("{}")
		}
	}
	if err := s.automaticAccess(principal, command.RuntimeID); err != nil {
		denied, payload = err, []byte("{}")
	} else if denied == nil {
		denied = s.automaticGate(ctx, command, received)
	}

	// A canceled request cannot cancel the bounded write that records its outcome.
	auditContext := context.WithoutCancel(ctx)
	result := AutomaticResult{Execution: NotExecuted}
	status, reason := "pending", ""
	var finished *time.Time
	if denied != nil {
		status, reason, finished = "rejected", denied.Error(), &received
	}
	err := s.transact(auditContext, func(tx *db.Tx) error {
		return tx.QueryRow(`INSERT INTO fleet_automatic_audit (runtime_id, subject, received_at, finished_at, command, status, reason)
			VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7) RETURNING id`, s.runtimeID, principal.Subject, received, finished, payload, status, reason).Scan(&result.AuditID)
	})
	if err != nil {
		return s.automaticPersistenceFailure(result)
	}
	if denied != nil {
		result.AuditCommitted = true
		return result, denied
	}

	// Stage the attempt durably before crossing the simulator mutation boundary.
	denied = s.automaticGate(ctx, command, s.clock().UTC())
	if denied == nil {
		denied = s.applyAutomatic(command)
	}
	status = "applied"
	if denied != nil {
		status, reason = "rejected", denied.Error()
	} else {
		result.Execution = Succeeded
	}
	err = s.transact(auditContext, func(tx *db.Tx) error {
		tag, err := tx.Exec(`UPDATE fleet_automatic_audit SET status=$1, reason=$2, finished_at=$3
			WHERE id=$4 AND runtime_id=$5 AND status='pending'`, status, reason, s.clock().UTC(), result.AuditID, s.runtimeID)
		if err == nil && tag.RowsAffected() != 1 {
			return ErrPersistence
		}
		return err
	})
	if err != nil {
		return s.automaticPersistenceFailure(result)
	}
	result.AuditCommitted = true
	return result, denied
}

func (s *Service) automaticAccess(principal auth.Principal, runtimeID string) error {
	if !s.warehouseV2 || principal.Role != auth.Automatic || principal.RuntimeID != s.runtimeID || runtimeID != s.runtimeID {
		return ErrUnauthorized
	}
	return nil
}

func (s *Service) automaticGate(ctx context.Context, command AutomaticCommand, now time.Time) error {
	if s.poisoned {
		return ErrPoisoned
	}
	if now.IsZero() || !now.Before(command.ExpiresAt) || command.ExpiresAt.Sub(now) > automaticMaxTTL {
		return ErrAutomaticExpired
	}
	if err := s.checkObservation([]string{command.ObservationID}, true); err != nil {
		return err
	}
	return ctx.Err()
}

func (s *Service) applyAutomatic(command AutomaticCommand) error {
	if err := s.advanceRevision(); err != nil {
		return err
	}
	if command.AssignJobBatch != nil {
		return s.commander.AssignJobBatch(*command.AssignJobBatch)
	}
	r := *command.ReassignUnstartedJob
	if err := safety.PreflightReassignment(s.commander, r); err != nil {
		return err
	}
	return s.commander.ReassignUnstartedJob(r.JobID, r.FromRobotID, r.ToRobotID)
}

func (s *Service) automaticPersistenceFailure(result AutomaticResult) (AutomaticResult, error) {
	s.poisoned = true
	result.Execution = Unknown
	return result, errors.Join(ErrPersistence, ErrPoisoned)
}
