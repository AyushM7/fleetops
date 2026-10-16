// Package safety provides deterministic, read-only operator-command preflight.
// A successful preflight is only a statement about the observed snapshot, never
// execution authorization. The later execution gate must check a valid pending
// proposal, operator approval and expiry, revalidate current state under serialized
// simulator access, and audit every accepted or rejected execution attempt.
//
// SetCharging neither assigns jobs nor moves robots, so the frozen job-assignment
// and route-aisle rules do not apply to this action. Preflight itself changes no
// state and does not implement proposal lifecycle, approval, execution, or audit.
package safety
