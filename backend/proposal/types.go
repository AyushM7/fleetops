package proposal

import (
	"errors"
	"time"

	"github.com/AyushM7/fleetops/backend/safety"
)

type State string

const (
	Pending  State = "pending"
	Approved State = "approved"
	Rejected State = "rejected"
	Expired  State = "expired"
)

type Action string

const SetCharging Action = "set_charging"
const ReassignUnstartedJob Action = "reassign_unstarted_job"

type Decision string

const (
	Approve Decision = "approve"
	Reject  Decision = "reject"
)

type Attempt string

const (
	AttemptCreate Attempt = "create"
	AttemptExpire Attempt = "expire"
)

// Clock supplies one timestamp per operation. Production callers must supply a
// trusted clock; no proposal or operator-supplied timestamp is used for decisions.
type Clock func() time.Time

// OperatorIdentity must come from the future trusted authentication adapter,
// never from the proposed action or an untrusted request's operator field.
type OperatorIdentity struct {
	Subject string
}

// Proposal is a detached view. Editing it cannot alter the canonical record,
// parameters, expiry, or decision held by Lifecycle.
type Proposal struct {
	ID           string
	Action       Action
	Request      safety.SetChargingRequest
	Reassignment safety.ReassignUnstartedJobRequest
	CreatedAt    time.Time
	ExpiresAt    time.Time
	State        State
	Operator     OperatorIdentity
	DecidedAt    time.Time
}

// DecisionEvent describes one attempt, including denied attempts. Proposal holds
// the candidate on creation (even invalid input), otherwise the canonical view.
// An unknown ID has only Proposal.ID populated. Attempt preserves invalid
// decision values, and Reason is empty only for accepted attempts.
type DecisionEvent struct {
	Attempt  Attempt
	At       time.Time
	Operator OperatorIdentity
	Before   State
	Proposal Proposal
	Accepted bool
	Reason   string
}

type Result struct {
	Proposal Proposal
	Event    DecisionEvent
}

var (
	ErrInvalidAction       = errors.New("unsupported or mixed proposal action")
	ErrInvalidDependencies = errors.New("reader and clock are required")
	ErrInvalidID           = errors.New("proposal ID must be nonempty without surrounding whitespace or control characters")
	ErrDuplicateID         = errors.New("proposal ID already exists")
	ErrInvalidExpiration   = errors.New("expiration must be after creation")
	ErrInvalidClock        = errors.New("clock must be nonzero and not precede proposal creation")
	ErrUnknownProposal     = errors.New("unknown proposal")
	ErrInvalidOperator     = errors.New("operator subject must be nonempty without surrounding whitespace or control characters")
	ErrInvalidDecision     = errors.New("decision must be approve or reject")
	ErrInvalidTransition   = errors.New("only pending proposals permit an operator decision")
	ErrExpired             = errors.New("proposal expired")
	ErrNotExpired          = errors.New("proposal has not expired")
)
