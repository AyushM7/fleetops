// Package fleet provides the serialized proposal-to-execution workflow.
package fleet

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/AyushM7/fleetops/backend/auth"

	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/safety"
	"github.com/AyushM7/fleetops/backend/sim"
)

var (
	ErrConfig           = errors.New("fleet database, clock and authorizer are required")
	ErrUnauthorized     = errors.New("operator is not authorized")
	ErrSelfApproval     = errors.New("submitter cannot approve their own proposal")
	ErrPersistence      = errors.New("fleet persistence failed")
	ErrPoisoned         = errors.New("fleet runtime poisoned; manual reconciliation required")
	ErrExecution        = errors.New("command execution failed")
	ErrForeignRuntime   = errors.New("proposal belongs to another simulator runtime")
	ErrProposalMismatch = errors.New("stored proposal differs from the canonical runtime record")
	ErrObservation      = errors.New("observation is missing, stale, or from another runtime")
)

type Execution string

const (
	NotExecuted Execution = "none"
	Succeeded   Execution = "succeeded"
	Failed      Execution = "failed"
	Unknown     Execution = "unknown"
)

// Authorizer is a trusted authentication/authorization adapter. It must verify
// context independently of the claimed identity; a nonempty subject is not auth.
// It is called under the service mutex and must not reenter the service.
type Authorizer func(context.Context, proposal.OperatorIdentity) bool

// Result reports the stored outcome. On a denied repeat, Execution can describe
// an earlier execution; ExecutionAttempted refers only to this call.
type Result struct {
	Proposal           proposal.Proposal
	Execution          Execution
	AuditCommitted     bool
	ExecutionAttempted bool
}

type Service struct {
	mu        sync.Mutex
	commander sim.Commander
	tick      func()
	transact  func(context.Context, func(*db.Tx) error) error
	lifecycle *proposal.Lifecycle
	clock     proposal.Clock
	authorize Authorizer
	at        time.Time
	runtimeID string
	revision  uint64
	canonical map[string]proposal.Proposal
	poisoned  bool
	layout    LayoutView
}

// New creates and exclusively owns a simulator. Callers never receive its
// Commander or Driver capabilities. Migrate the database before using it.
func New(cfg sim.Config, seed int64, pool *db.Pool, clock proposal.Clock, authorize Authorizer) (*Service, error) {
	if pool == nil || clock == nil || authorize == nil {
		return nil, ErrConfig
	}
	simulator, err := sim.New(cfg, seed)
	if err != nil {
		return nil, err
	}
	service, err := newService(simulator, simulator.Tick, pool, clock, authorize)
	if err != nil {
		return nil, err
	}
	service.layout = layoutFromConfig(cfg)
	return service, nil
}

func newService(commander sim.Commander, tick func(), pool *db.Pool, clock proposal.Clock, authorize Authorizer) (*Service, error) {
	id, err := newID()
	if err != nil {
		return nil, err
	}
	s := &Service{commander: commander, tick: tick, transact: pool.Transaction, clock: clock, authorize: authorize, runtimeID: id, canonical: make(map[string]proposal.Proposal)}
	reader := struct{ sim.Reader }{Reader: commander}
	s.lifecycle, err = proposal.New(reader, func() time.Time { return s.at })
	return s, err
}

func (s *Service) Snapshot() sim.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commander.Snapshot()
}

func (s *Service) Poisoned() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.poisoned
}

// Tick is existing simulator dynamics, serialized with all operator operations.
func (s *Service) Tick() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.poisonOnPanic()
	if s.poisoned {
		return ErrPoisoned
	}
	if err := s.advanceRevision(); err != nil {
		return err
	}
	s.tick()
	return nil
}

func (s *Service) Submit(ctx context.Context, request safety.SetChargingRequest, expiresAt time.Time, expectedObservation ...string) (Result, error) {
	return s.submit(ctx, proposal.Proposal{Action: proposal.SetCharging, Request: request, ExpiresAt: expiresAt.UTC()}, expectedObservation)
}

func (s *Service) SubmitReassignment(ctx context.Context, request safety.ReassignUnstartedJobRequest, expiresAt time.Time, expectedObservation ...string) (Result, error) {
	return s.submit(ctx, proposal.Proposal{Action: proposal.ReassignUnstartedJob, Reassignment: request, ExpiresAt: expiresAt.UTC()}, expectedObservation)
}

func (s *Service) submit(ctx context.Context, p proposal.Proposal, expectedObservation []string) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.poisonOnPanic()
	id, err := newID()
	if err != nil {
		return Result{}, err
	}
	s.at = s.clock().UTC()
	p.ID, p.CreatedAt = id, s.at
	var candidate proposal.Result
	var denied error
	principal, authenticated := auth.FromContext(ctx)
	err = s.transact(ctx, func(tx *db.Tx) error {
		if authenticated && principal.Role != auth.Proposer && principal.Role != auth.Operator {
			denied = ErrUnauthorized
			candidate = deniedResult(p, proposal.AttemptCreate, proposal.OperatorIdentity{Subject: principal.Subject}, s.at, denied)
		} else if s.poisoned {
			denied = ErrPoisoned
			candidate = deniedResult(p, proposal.AttemptCreate, proposal.OperatorIdentity{}, s.at, denied)
		} else if denied = s.checkObservation(expectedObservation, authenticated && principal.Role == auth.Proposer); denied != nil {
			candidate = deniedResult(p, proposal.AttemptCreate, proposal.OperatorIdentity{}, s.at, denied)
		} else {
			if p.Action == proposal.ReassignUnstartedJob {
				candidate, denied = s.lifecycle.CreateReassignment(id, p.Reassignment, p.ExpiresAt)
			} else {
				candidate, denied = s.lifecycle.Create(id, p.Request, p.ExpiresAt)
			}
		}
		if authenticated {
			candidate.Event.Operator.Subject = principal.Subject
		}
		if denied == nil {
			if err := s.insert(tx, candidate.Proposal, principal.Subject); err != nil {
				return err
			}
		}
		return s.audit(tx, candidate.Event)
	})
	if err != nil {
		return s.persistenceFailure(id, false)
	}
	if denied == nil {
		s.canonical[id] = candidate.Proposal
	}
	return Result{Proposal: candidate.Proposal, Execution: NotExecuted, AuditCommitted: true}, denied
}

// Decide combines the lifecycle decision and execution under one mutex and one
// DB transaction. Lifecycle.Decide itself remains approval-only and unchanged.
func (s *Service) Decide(ctx context.Context, id string, decision proposal.Decision, operator proposal.OperatorIdentity) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.poisonOnPanic()
	s.at = s.clock().UTC()
	result := Result{Execution: NotExecuted}
	var denied error
	err := s.transact(ctx, func(tx *db.Tx) error {
		var err error
		result, denied, err = s.decide(tx, id, decision, operator)
		return err
	})
	if err != nil {
		return s.persistenceFailure(id, result.ExecutionAttempted)
	}
	result.AuditCommitted = true
	if _, exists := s.canonical[id]; exists && result.Proposal.ID == id && result.Proposal.State != "" && denied != ErrProposalMismatch && denied != ErrForeignRuntime {
		s.canonical[id] = result.Proposal
	}
	return result, denied
}

func (s *Service) persistenceFailure(id string, attempted bool) (Result, error) {
	// Lifecycle state may already have advanced, and COMMIT may have succeeded
	// without acknowledgement. Neither memory nor simulator mutations can roll back.
	s.poisoned = true
	return Result{Proposal: proposal.Proposal{ID: id}, Execution: Unknown, ExecutionAttempted: attempted}, errors.Join(ErrPersistence, ErrPoisoned)
}

func (s *Service) poisonOnPanic() {
	if cause := recover(); cause != nil {
		s.poisoned = true
		panic(cause)
	}
}

func newID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", ErrConfig
	}
	return "P" + hex.EncodeToString(bytes[:]), nil
}
