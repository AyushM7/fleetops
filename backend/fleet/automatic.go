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
	ErrAutomaticCommand     = errors.New("invalid V2 automatic command")
	ErrAutomaticExpired     = errors.New("automatic command has expired or exceeds its freshness window")
	ErrAutomaticKeyConflict = errors.New("automatic command key already belongs to a different request")
	ErrAutomaticPriorDenial = errors.New("automatic command was previously rejected")
	ErrAutomaticDisabled    = errors.New("V2 automatic commands are disabled")
)

const automaticMaxTTL = 30 * time.Second

type AutomaticCommand struct {
	RuntimeID            string                              `json:"runtimeId"`
	ObservationID        string                              `json:"observationId"`
	IdempotencyKey       string                              `json:"idempotencyKey"`
	Preconditions        map[string]uint64                   `json:"preconditions"`
	ExpiresAt            time.Time                           `json:"expiresAt"`
	AssignJobBatch       *sim.AssignJobBatchRequest          `json:"assignJobBatch,omitempty"`
	ReassignUnstartedJob *safety.ReassignUnstartedJobRequest `json:"reassignUnstartedJob,omitempty"`
}

type AutomaticResult struct {
	AuditID        int64     `json:"auditId"`
	PriorAuditID   int64     `json:"priorAuditId,omitempty"`
	Execution      Execution `json:"execution"`
	AuditCommitted bool      `json:"auditCommitted"`
}

// SetAutomaticCommandsEnabled is only for the trusted Go host. Disabling
// invalidates outstanding observations without removing durable receipts.
func (s *Service) SetAutomaticCommandsEnabled(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if enabled && !s.warehouseV2 {
		return ErrUnauthorized
	}
	s.automaticEnabled = enabled
	if !enabled {
		s.commander.SetOperationsDispatchHold(nil)
		s.automaticReads, s.automaticVersions = nil, nil
	}
	return nil
}

func (c AutomaticCommand) validate() error {
	if len(c.RuntimeID) != 33 || len(c.ObservationID) != 33 || !validCommandKey(c.IdempotencyKey) ||
		(c.AssignJobBatch == nil) == (c.ReassignUnstartedJob == nil) {
		return ErrAutomaticCommand
	}
	var targets []string
	if c.AssignJobBatch != nil {
		if err := c.AssignJobBatch.Validate(); err != nil {
			return err
		}
		targets = make([]string, 0, 2*len(c.AssignJobBatch.Assignments))
		for _, assignment := range c.AssignJobBatch.Assignments {
			targets = append(targets, assignment.JobID, assignment.RobotID)
		}
	} else {
		r := c.ReassignUnstartedJob
		if !prefixedCommandID(r.JobID, 'J') || !sim.IsV2TransportID(r.FromRobotID) || !sim.IsV2TransportID(r.ToRobotID) || r.FromRobotID == r.ToRobotID {
			return ErrAutomaticCommand
		}
		targets = []string{r.JobID, r.FromRobotID, r.ToRobotID}
	}
	if len(c.Preconditions) != len(targets) {
		return ErrAutomaticCommand
	}
	for _, id := range targets {
		if c.Preconditions[id] == 0 {
			return ErrAutomaticCommand
		}
	}
	return nil
}

func validCommandKey(id string) bool {
	if len(id) < 8 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func prefixedCommandID(id string, prefix byte) bool {
	if len(id) < 2 || len(id) > 20 || id[0] != prefix || id[1] == '0' {
		return false
	}
	for _, c := range id[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ExecuteAutomatic owns admission, serialized Go execution and the durable receipt.
func (s *Service) ExecuteAutomatic(ctx context.Context, command AutomaticCommand) (AutomaticResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.poisonOnPanic()
	received := s.clock().UTC()
	command.ExpiresAt = command.ExpiresAt.UTC()
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
	}

	// A canceled request cannot cancel the bounded write that records its outcome.
	auditContext := context.WithoutCancel(ctx)
	result := AutomaticResult{Execution: NotExecuted}
	var prior *automaticReceipt
	err := s.transact(auditContext, func(tx *db.Tx) error {
		var storageErr error
		result.AuditID, prior, denied, storageErr = s.stageAutomatic(ctx, tx, command, principal.Subject, payload, received, denied)
		return storageErr
	})
	if err != nil {
		return s.automaticPersistenceFailure(result)
	}
	if prior != nil {
		result.PriorAuditID = prior.id
		switch prior.status {
		case "applied":
			result.Execution, result.AuditCommitted = Succeeded, true
			return result, nil
		case "rejected":
			result.AuditCommitted = true
			return result, ErrAutomaticPriorDenial
		default:
			return s.automaticPersistenceFailure(result)
		}
	}
	if denied != nil {
		result.AuditCommitted = true
		return result, denied
	}

	// Stage the attempt durably before crossing the simulator mutation boundary.
	denied = s.automaticGate(ctx, command, principal.Subject, s.clock().UTC())
	if denied == nil {
		denied = s.applyAutomatic(command)
	}
	status, reason := "applied", ""
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
	if !s.automaticEnabled {
		return ErrAutomaticDisabled
	}
	return nil
}

func (s *Service) automaticGate(ctx context.Context, command AutomaticCommand, subject string, now time.Time) error {
	if s.poisoned {
		return ErrPoisoned
	}
	if now.IsZero() || !now.Before(command.ExpiresAt) || command.ExpiresAt.Sub(now) > automaticMaxTTL {
		return ErrAutomaticExpired
	}
	if err := s.checkAutomaticObservation(command, subject, now); err != nil {
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
