# P4-04 implementation report

The existing React dashboard now displays Go-detected incidents, live job
ownership/progress and both operator-approved actions. All changes are in `web/`.
The actual P4-03 API already supplies every required field; no backend expansion
or schema, simulator, safety, database or lifecycle edits were made.

## Behavior

- Fleet monitoring reads incidents, supporting telemetry and jobs in the same
  `fleetState` response as robot positions and layout. Incident type/severity come
  directly from Go; the frontend introduces no detector or classification rules.
- Active incidents show affected robot, severity, battery, position, status,
  assigned job, wait ticks and blocked flag. Current snapshot scope and lack of
  persisted incident history are explicit. Empty, loading, poisoned and stale
  states remain distinct. Malformed/missing/mismatched incident data does not
  become a misleading empty incident list.
- Inbox parsing and cards distinguish SetCharging from ReassignUnstartedJob.
  Reassignment shows job/source/recipient/status, submitter and expiration.
  Unsupported or inconsistent action payloads fail closed. Expired, foreign,
  non-pending, busy or stale/unavailable decisions are disabled.
- Existing authenticated approval/rejection operations are retained, with no
  identity inputs or new execution endpoints. Go enforces operator role,
  independent approval, expiry and current eligibility. Viewer/proposer inboxes
  remain forbidden while fleet/incident/job monitoring continues.
- The last decision receipt retains action details and stored proposal status,
  execution outcome, whether this call executed, committed-audit status and
  sanitized errors. Unknown/uncommitted/malformed responses never claim success
  or trigger mutation retries. Historical execution on duplicate decisions is
  distinguished from execution by the current call. A failed status refresh
  marks retained status as stale.
- Approval triggers the existing fleet/inbox/status refresh path. Map markers
  display current job IDs; job rows show assignment, recipient position/status,
  awaiting pickup, picked-up/delivering, and Delivered only for Go status `done`.
  A successful reassignment receipt confirms transfer, not delivery completion.
  SetCharging still shows charging-at-current-station behavior and outcome.

## Verification

Node.js 24.21.0 and the existing project toolchain were used. No new dependencies,
package changes, lockfile changes, credentials or API keys were introduced.

| Check | Result | Exit |
| --- | --- | --- |
| `npm --prefix web test` | 34 tests in 5 files passed | 0 |
| `npm --prefix web run typecheck` | Passed | 0 |
| `npm --prefix web run build` | Passed; 20 modules transformed | 0 |
| `web/scripts/verify-integration.ps1` | Both charging and reassignment profiles passed; final Go test result 5.715s | 0 |
| Browser interactions and layout | Passed using disposable protocol fixture | Passed |
| Whitespace | Tracked diff and all new files passed | 0 |

Frontend coverage includes mixed charging/reassignment inboxes, read-only
monitoring, incident labels/severity/telemetry, empty/loading/unavailable/poisoned
states, changed ownership and coordinates, pickup vs delivery, malformed
responses, expired/foreign/stale controls, authorization errors, duplicate and
uncertain decisions, and retained status after failed refresh. Existing polling
tests verify stale reads, recovery, cancellation, no overlapping requests,
discarding superseded reads and no mutation retry after response loss.

The new integration harness bundles the real frontend client and React views
with existing Vite, starts isolated PostgreSQL, and compiles a test-only adapter
inside a temporary backend copy. Repository backend files are untouched. It
reuses P4-03's trusted simulator fixture for recovery, filling only the missing
test-constructor layout from the same fixture's actual simulator cells. Normal
production fleet.New already supplies its layout.

Both test profiles use real Go GraphQL, authentication, proposal lifecycle,
execution and PostgreSQL. They verify viewer/proposer monitoring and forbidden
inboxes, invalid authentication, viewer/proposer/self approval denial, rejection
without state change, operator approval, canonical status, duplicate and stale
denial, and expiry without state change. Reassignment clears R1, assigns R3 and
renders an awaiting-pickup job; trusted test-process ticks produce actual pickup,
changed recipient position and delivery. Charging starts R1 at S1. No simulator
control endpoint or UI control is added; test ticks use child-process I/O only.

An in-app browser check of the built React application used a disposable
frontend protocol fixture, separately from the real Go integration. It verified
operator sign-in, both action cards, reassignment approval, busy controls, the
transfer receipt, refreshed recipient/job state, charging rejection, stored-status
refresh, disconnect/token clearing, viewer-only monitoring and stale incident/job
warnings after the fixture server stopped. Desktop screenshots were inspected;
the browser screenshot is illustrative test data, not a live deployment or
persisted production incident. No browser server remains running.

Browser fixture preview (illustrative protocol data, not the real Go integration):

![Incident and job panels from browser verification](dist/p404-operations.jpg)

The first restricted Vitest run hit the sandbox temporary-directory limitation;
the same tests passed with approved execution outside that restriction. No
auto-review action remains blocked.

## Remaining limits and scope

The inbox remains the existing bounded current-runtime pending list (50 maximum),
with the last decision receipt held in tab memory. Reload/disconnect clears it;
this ticket adds no persisted activity-history view. Polling may miss intermediate
positions if simulation ticks advance faster than its one-second refresh. A stale
snapshot cannot establish current progress. Go remains authoritative for safety
and execution; the dashboard never assumes approval alone means delivery.

No implementation requirement remains intentionally unfinished or guessed.
No backend, safety-rule, database, simulator or lifecycle edits; no deployment,
public-demo authentication changes, paid/model calls, commits, pushes, merges,
branch changes or P4-05 work occurred.

## Files changed

- `web/P4-04-REPORT.md`
- `web/README.md`
- `web/scripts/check-backend.mjs`
- `web/scripts/contract-entry.ts`
- `web/scripts/go-dashboard-integration_test.go`
- `web/scripts/verify-integration.ps1`
- `web/src/App.test.tsx`
- `web/src/App.tsx`
- `web/src/ApprovalInbox.test.tsx`
- `web/src/ApprovalInbox.tsx`
- `web/src/fleet.test.ts`
- `web/src/fleet.ts`
- `web/src/Operations.tsx`
- `web/src/proposals.test.ts`
- `web/src/proposals.ts`
- `web/src/reassignment.test.tsx`
- `web/src/styles.css`
