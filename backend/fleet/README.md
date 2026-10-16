# Charging vertical slice

`fleet.New(simConfig, seed, pool, clock, authorizer)` creates and exclusively owns
a simulator. Run `pool.Migrate(ctx)` first. `Submit` accepts only a typed
`safety.SetChargingRequest` and expiration; the service generates the proposal ID.
`Decide(ctx, id, proposal.Approve, identity)` combines approval and execution.
Use `proposal.Reject` for an explicit human rejection. `Snapshot` returns a copy;
`Tick` advances existing simulator dynamics. All simulator access is serialized
with the same mutex, including validation, approval, execution, and snapshots.
No simulator Commander or Driver escapes the facade.

The robot must already be idle and job-free at a free charging station, with at
least 30% battery. Submission uses P3-01 `Lifecycle.Create`, which reuses P3-00
validation. A decision locks the PostgreSQL proposal row, verifies runtime and
canonical parameters, and calls unchanged P3-01 `Lifecycle.Decide`. Approval
alone still does not execute in that package. The service stages the approval
and execution audit, checks authorization and current state again immediately
before calling the existing `Commander.SetCharging`, then persists its outcome
and commits. Duplicate or concurrent decisions cannot execute the proposal again.
No command is automatically retried.

## Trusted boundary

The injected `Authorizer` must authenticate and authorize the context and verify
the separately supplied `proposal.OperatorIdentity`; matching a claimed subject
alone is insufficient. It is mandatory, defaults to nothing, and is called again
at execution. A future HTTP/GraphQL adapter must supply this context from trusted
authentication, never bind it from proposal input. No authentication machinery or
transport endpoints are implemented here. Callbacks must not reenter the service.

## Persistence and failure semantics

`Result.AuditCommitted` is true only after the PostgreSQL transaction commits.
This also applies to rejected submissions, unauthorized/unknown/duplicate
decisions, expiration and validation failures. Unknown IDs have audit rows without
a proposal foreign key. Proposal JSON preserves P3-01's timestamps, immutable
parameters and four lifecycle states, including nanosecond precision. Separate
`execution` values (`none`, `succeeded`, `failed`) avoid adding lifecycle states.
A failed approval preflight remains pending, as in P3-01; a consumed approval
cannot be retried even if command execution fails. Revalidation or authorization
failure at the final execution gate is audited without calling the command.
`Result.Execution` is the recorded outcome, including a prior execution on a
denied repeat; `ExecutionAttempted` refers only to the current call. Expiration
does not erase the outcome of an earlier successful execution.

Simulator mutations cannot roll back with a database transaction. Any persistence
error poisons this service permanently: lifecycle memory may have advanced, or a
commit acknowledgement may have been lost. If the command was invoked, the error
result sets `ExecutionAttempted=true`, `Execution=unknown`, and
`AuditCommitted=false`; it never claims that execution audit is durable. A
post-command write failure and a COMMIT failure both take this path. Snapshots
remain readable, but ticks, submissions and execution are refused. Refusals are
audited if persistence is available. There is no reset or automatic recovery;
manual reconciliation is required after an uncertain outcome.
Unexpected panics also poison the service and are rethrown after transaction
rollback; they cannot leave a recovered process free to continue mutations.

Each service receives a fresh runtime identity. Persisted proposals from another
runtime cannot execute against a newly constructed simulator. This slice does
not restore simulator or lifecycle state after restart. Such proposals require
operator reconciliation rather than replay. The PostgreSQL unique ID constraint
and row lock supplement the runtime mutex; no in-memory audit is called durable.

## Demonstration and verification

Privately export `FLEETOPS_TEST_DATABASE_URL` for Compose PostgreSQL and run
`make test-integration`. That existing target now includes the fleet workflow and
database tests with the race detector. It skips without a URL locally and fails
when `FLEETOPS_REQUIRE_DB=1`. Each test has a private schema, runs the embedded
migrations, and drops only its schema afterward.

`TestIntegrationChargingVerticalSlice` demonstrates persisted submission,
approval, actual SetCharging state changes, committed audit, and refused replay.
Additional tests cover concurrent approvals, 30% admission, stale/rejected/expired
and unauthorized requests, final-gate revalidation, failed command execution,
and PostgreSQL triggers that fail writes or deferred COMMIT after mutation.
Failure tests assert poisoning and the absence of a false durable-audit claim.

No SendToCharge, ReassignJob, simulator-tick expiration, GraphQL, incident
persistence or UI is implemented. Frozen safety rules and Phase 2 behavior remain
unchanged, including the enabled S3/seed99 soak failure.
