package proposal

import (
	"strings"
	"time"
	"unicode"

	"github.com/AyushM7/fleetops/backend/safety"
	"github.com/AyushM7/fleetops/backend/sim"
)

// Lifecycle owns canonical records; it is intentionally not a persistence or
// execution service. It must not be copied after use or accessed concurrently.
type Lifecycle struct {
	reader  sim.Reader
	clock   Clock
	records map[string]Proposal
}

func New(reader sim.Reader, clock Clock) (*Lifecycle, error) {
	if reader == nil || clock == nil {
		return nil, ErrInvalidDependencies
	}
	return &Lifecycle{reader: reader, clock: clock, records: make(map[string]Proposal)}, nil
}

// Create preflights the immutable request before admitting a pending proposal.
// A failed attempt never inserts or replaces a canonical record.
func (l *Lifecycle) Create(id string, request safety.SetChargingRequest, expiresAt time.Time) (Result, error) {
	now := l.clock().UTC()
	p := Proposal{ID: id, Action: SetCharging, Request: request, CreatedAt: now, ExpiresAt: expiresAt.UTC()}
	err := l.validateCreation(p)
	if err == nil {
		err = safety.PreflightSetCharging(l.reader, request)
	}
	if err == nil {
		p.State = Pending
		l.records[id] = p
	}
	return result(p, "", AttemptCreate, OperatorIdentity{}, now, err)
}

func (l *Lifecycle) validateCreation(p Proposal) error {
	if !validIdentity(p.ID) {
		return ErrInvalidID
	}
	if _, exists := l.records[p.ID]; exists {
		return ErrDuplicateID
	}
	if p.CreatedAt.IsZero() {
		return ErrInvalidClock
	}
	if !p.ExpiresAt.After(p.CreatedAt) {
		return ErrInvalidExpiration
	}
	return nil
}

// Decide permits exactly one human decision on a live pending proposal.
// Approval reuses preflight; execution must still revalidate later. Rejection
// needs no simulator read. Expiration takes precedence over operator/decision
// validation and is recorded even when the attempted decision is denied.
func (l *Lifecycle) Decide(id string, decision Decision, operator OperatorIdentity) (Result, error) {
	now := l.clock().UTC()
	p, before, err := l.current(id, now)
	if err == nil {
		err = validateDecision(p, decision, operator)
	}
	if err == nil && decision == Approve {
		err = safety.PreflightSetCharging(l.reader, p.Request)
	}
	if err == nil {
		p.State = Rejected
		if decision == Approve {
			p.State = Approved
		}
		p.Operator, p.DecidedAt = operator, now
		l.records[id] = p
	}
	return result(p, before, Attempt(decision), operator, now, err)
}

func validateDecision(p Proposal, decision Decision, operator OperatorIdentity) error {
	if !validIdentity(operator.Subject) {
		return ErrInvalidOperator
	}
	if decision != Approve && decision != Reject {
		return ErrInvalidDecision
	}
	if p.State != Pending {
		return ErrInvalidTransition
	}
	return nil
}

// Expire makes time-driven expiration explicit and auditable, without a timer or
// background goroutine. Approved proposals also expire; rejected ones stay final.
func (l *Lifecycle) Expire(id string) (Result, error) {
	now := l.clock().UTC()
	p, before, err := l.current(id, now)
	if err == ErrExpired && before != Expired {
		err = nil
	} else if err == nil {
		err = ErrNotExpired
		if p.State == Rejected {
			err = ErrInvalidTransition
		}
	}
	return result(p, before, AttemptExpire, OperatorIdentity{}, now, err)
}

// current materializes expiration once; a later backwards clock cannot restore
// an expired proposal. Expiration never overwrites the original human decision.
func (l *Lifecycle) current(id string, now time.Time) (Proposal, State, error) {
	p, ok := l.records[id]
	if !ok {
		return Proposal{ID: id}, "", ErrUnknownProposal
	}
	before := p.State
	if now.IsZero() || now.Before(p.CreatedAt) {
		return p, before, ErrInvalidClock
	}
	if (p.State == Pending || p.State == Approved) && !now.Before(p.ExpiresAt) {
		p.State = Expired
		l.records[id] = p
	}
	if p.State == Expired {
		return p, before, ErrExpired
	}
	return p, before, nil
}

func validIdentity(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
}

func result(p Proposal, before State, attempt Attempt, operator OperatorIdentity, now time.Time, err error) (Result, error) {
	event := DecisionEvent{Attempt: attempt, At: now, Operator: operator, Before: before, Proposal: p, Accepted: err == nil}
	if err != nil {
		event.Reason = err.Error()
	}
	return Result{Proposal: p, Event: event}, err
}
