# Phase 4 acceptance — P4-05

Date: 2026-10-17. Branch: `phase4-agent`, inspected HEAD `ea05ac8`.

**Status: automated verification, independent human approval/recovery and the
real backend outage/Disconnect check passed.
Live LLM reasoning was not demonstrated or authorized.** No paid model
requests occurred. Deterministic adapter output is not presented as live AI.

## What was verified in this run

| Check | Actual result | Exit |
| --- | --- | --- |
| Agent unit tests | 82 passed, 7 files | 0 |
| Agent TypeScript check/build | Passed | 0 |
| Dashboard unit tests | 34 passed, 5 files | 0 |
| Dashboard TypeScript check/build | Passed | 0 |
| Existing agent integration harness | Full tagged Go race suite, real PostgreSQL and both local Mastra contracts passed | 0 |
| Phase 2 regression checks | Full simulator suite, both goldens and all 30 S1–S4 acceptance checks passed; simulator package 77.264s | 0 |
| Agent real-backend integration | 5 tests passed | 0 |
| Existing dashboard integration harness | Charging and reassignment profiles passed; package result 9.877s | 0 |
| New demo CheckOnly | Real incident, identical baseline, local Mastra adapter, explicit opt-in, pending proposal, committed create audit and no execution passed; package result 5.336s | 0 |
| Default submission guard | Runner refuses startup/submission without `-Submit` | Passed (expected rejection) |
| New JavaScript syntax check | Passed | 0 |
| Real Go-backed browser viewer/Disconnect check | Passed; details below | Observed |
| Human-linked approval/recovery/audit session | Human approval observed; atomic transfer at tick 42, pickup at 43, delivery at 45; committed create/approve/execute audits | Verified from harness evidence |
| Real backend outage and operator Disconnect | Retained stale snapshot, all stale warnings visible, Approve/Reject disabled; Disconnect cleared token and fleet/inbox views | Browser assertions passed |
| Live paid model | Disabled; zero provider requests | Not run |
| Whitespace and scope | `git diff --check` and explicit checks of all five new files passed; only ticket files added | 0 |

The initial database checks failed because Docker Desktop's engine was stopped.
The installed per-user Docker Desktop app was started, then the actual harnesses
were rerun successfully. The new demo's first compile check exposed an unsupported
database helper assumption; its test-only audit reads were corrected to use the
existing `QueryRow` API. No product code or database interface was changed.
There were no unresolved auto-review refusals.

## Recovery and safety evidence

The repeatable session uses the existing P4-03 trusted Go scenario: source R1 is
healthy, battery 100%, moving with assigned/uncollected J1, and becomes blocked
through ordinary ticks. Offline R2 occupies the only pocket exit. This is a
traffic-blocked scenario, not a low-battery incident. Automatic dispatch,
navigation, charging and recovery remain enabled; no detector broadening or
runtime fault-control endpoint is introduced.

Both scenarios start with identical tick-42 simulator snapshots. The unchanged
baseline is advanced 200 ticks: at tick 242 J1 remains assigned to blocked R1.
The new CheckOnly run independently verified this and a committed create audit
with proposer subject `proposer`; the simulator stayed unchanged after submission.

The real Go-backed React viewer displayed tick 42, R1/J1, battery 100%, wait 42,
high-severity traffic blockage, offline R2, idle eligible R3, and J1 awaiting pickup
on R1. It correctly denied the operator inbox while keeping fleet/incident/job
monitoring available. Disconnect returned to the masked blank sign-in form and
removed robot/map/job/inbox data. No approval was performed by the agent.

Automated real Go/PostgreSQL integrations verified rejection and expiry without
command-induced state change; stale state/evidence rejection; invalid tokens;
viewer/proposer/self-approval denial; duplicate decisions; atomic source clearing
and recipient assignment; ordinary pickup and delivery; canonical proposal reads;
and committed audit flags. Full Go tests also cover failed/uncertain commits and
existing fail-closed behavior. Existing SetCharging remains passing.

The actual Mastra generation loop consumed real Go fleet/incident/job/candidate
data using a **deterministic local model adapter**. Exact snapshot references,
telemetry and action evidence passed existing host grounding validation, then
the host's fresh-snapshot check submitted one explicitly opted-in pending
proposal. This verifies integration and output validation, not model intelligence
or live provider quality. Tokens/private configuration were never in the model
prompt; its network path was restricted to the loopback GraphQL endpoint.

The separate automated dashboard integration confirmed transfer initially leaves
J1 assigned/awaiting pickup; normal Go ticks then produce in-progress/delivery
states. Only Go status `done` renders Delivered. These scripted operator
decisions are not claimed as an independent human dashboard demonstration.

## Human approval and recovery outcome

The completed review session used proposal
`P3abd0368bf4e73a9d56838b4911ea04a` at `http://127.0.0.1:49701`.
The owner confirmed approving J1, R1 → R3, through the real React dashboard.
The harness independently observed an approved proposal with execution succeeded,
recorded `OperatorDecisionObserved=true`, and verified the following real Go states:

| Tick | Observed result |
| --- | --- |
| 42 before approval | J1 assigned to blocked R1; submission did not change simulator state |
| 42 after approval | R1 idle with no job; R3 moving with J1; J1 assigned, awaiting pickup |
| 43 | R3 at (4,4); J1 in_progress |
| 44 | R3 at (5,4); J1 in_progress |
| 45 | R3 at delivery (6,4); J1 done; R3 idle with no active job |
| 242 | Intervention J1 remains done on R3; identical baseline J1 still assigned to blocked R1 |

The harness queried PostgreSQL after commit and recorded three accepted rows:
`create/proposer`, `approve/operator`, `execute/operator`. Source and approver
subjects differ. Execution success meant transfer, with delivery occurring later.
The agent did not click Approve or send an approval mutation.

The completed session's sanitized evidence and reasoning are preserved under
`evals/dist/p405/completed-human`. On continuation the original server was
unreachable and its disposable Docker container no longer existed. A fresh SQL
query could therefore not be repeated; the audit evidence above is the harness's
actual post-commit PostgreSQL read, not an inference from a GraphQL response.
The original interactive process exit code is unavailable after the session ended;
its saved evidence confirms the recovery and audit assertions completed.

## Interactive outage and Disconnect

A separate disposable real Go/PostgreSQL/React session at
`http://127.0.0.1:50604` was used because the original server had ended.
Its local-adapter proposal `Pa3ceff6e31d4aef61933d98b007d4889` remained pending;
the agent made no approval or rejection decision. Operator login displayed the
actual tick-42 fleet, incidents, J1 ownership and pending inbox. The runner's
normal stop signal closed the real Go HTTP listener and cleaned up the session.
This was a real service outage, not a mocked network response.

The already loaded React dashboard then displayed:

- `Backend unavailable. Retrying… Showing the last received snapshot; positions may be stale.`
- `Last received incidents; current conditions cannot be confirmed.`
- `Last received job state; progress may be stale.`
- `Approval inbox unavailable. Retrying… Displayed proposals may be stale; decisions are disabled.`

Browser assertions confirmed both Approve and Reject disabled. Clicking Disconnect
returned to the sign-in screen, with an empty bearer-token field and no job or
inbox panels. Sanitized screenshot/DOM evidence is retained in ignored
`evals/dist/p405/outage-browser.jpg`, `outage-browser.txt` and
`disconnect-browser.jpg`. No private credentials were captured in those files.
The outage session does not replace or claim another human recovery run.
Shutdown was verified: the new disposable container and private credential file
were absent. A leftover temporary checkout from the ended original session was
removed within the verified demo run directory. The final whitespace check passed
with exit 0. No full regression rerun was needed for these documentation updates;
the earlier test results above remain the applicable verification.

No actual LLM provider call was authorized. A live LLM demonstration remains a
separate conditional acceptance item requiring explicit permission, private model
configuration/key and acknowledgement of possible charges. The paid path stays
off. The supplied guide intentionally demonstrates a local adapter and says so.

## Repeat and inspect

Use [the local demo guide](phase4-demo.md) for exact commands, independent browser
review, direct PostgreSQL SELECT verification, outage/Disconnect checks and
shutdown. Run it in a separate PowerShell process. It uses only an isolated
loopback PostgreSQL container and a temporary backend copy; no existing Compose
volume or service is modified.

Ignored artifacts under `evals/dist/p405` contain sanitized `reasoning.json`,
`evidence.json` and infrastructure metadata. `evidence.json` records identical
initial snapshots, baseline/final/progress states and audit rows queried directly
from PostgreSQL after commit. `session.private.json` holds ephemeral credentials;
it is never served by HTTP, printed, sent to a model or committed, and is removed
on shutdown. The demo has a 20-minute review window and a requested 30-minute
proposal expiry. It never auto-approves. Normal shutdown is the guide's `stop`
file, which permits schema/container/credential cleanup.

## Files changed and scope

- `docs/phase4-demo.md`: repeatable local commands, human review, expected states,
  audit/outage verification and shutdown.
- `docs/phase4-acceptance.md`: this actual-results report and remaining limits.
- `evals/phase4/start-demo.ps1`: explicit opt-in, isolated demo lifecycle and cleanup.
- `evals/phase4/demo_test.go`: test-only real Go/GraphQL/React session, identical
  baseline, human-only approval wait, normal Go progress and direct SQL audit checks.
- `agent/scripts/phase4-demo-reasoning.mjs`: clearly labeled local Mastra adapter,
  grounded real state, fresh-evidence guard and opted-in pending submission.

No production feature, UI, schema, command/tool signature, simulator behavior,
safety rule, dependency, database migration, public authentication or deployment
change was made. No commits, pushes, merges, branch changes or Phase 5 work occurred.
Live-LLM verification remains unfinished as stated above; no result is inferred
from silence or replaced by a mock demonstration.
