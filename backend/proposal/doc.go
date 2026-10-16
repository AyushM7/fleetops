// Package proposal owns the SetCharging proposal and operator-decision lifecycle.
// Callers must serialize access to a Lifecycle and its simulator Reader.
//
// IDs are allocated by a trusted application boundary and must be unique within
// this lifecycle. Future persistence must enforce global uniqueness and atomic
// state transitions. OperatorIdentity is trusted authentication context supplied
// separately from proposal input; checking its syntax does not authenticate it.
// Untrusted clients must never populate this context themselves.
//
// Every creation, decision, and expiration attempt returns a DecisionEvent,
// including failures. These values and the in-memory records are not durable
// audit. Future persistence must record every accepted or rejected attempt and
// atomically persist accepted transitions before acknowledging them.
//
// Approval never executes a command. A future executor must serialize access,
// load the canonical proposal, verify approval and expiry, revalidate with safety
// immediately before execution, and durably audit the attempt. A returned copy
// of an approved proposal is not an execution capability. No Commander or Driver
// is accepted or exposed here.
package proposal
