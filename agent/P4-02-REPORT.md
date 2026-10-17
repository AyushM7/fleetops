# P4-02: real LLM incident reasoning

Completed 2026-10-16 on `phase4-agent`, starting from committed `3b536f9`.
No commit, push, merge, branch change or P4-03 work occurred.

## Implementation

Optional live execution uses the existing pinned Mastra `Agent.generate` and
provider router with structured output. The host reads one real Go fleet
snapshot, validates its internal consistency, and supplies only its observed
facts, approved snapshot-scoped references, supported action candidate and
explicit demo target. Provider keys and bearer tokens never enter model messages.

The result separates host-owned `observedFacts`, `aiAnalysis`, validated
`recommendedAction`, freshness review in `submissionCheck`, and backend `proposal`
status. Offline execution remains deterministic and makes no model requests.

References are exactly `{scope:"snapshot", tick, robotId, type}`. They are
**snapshot-scoped references, NOT stable incident IDs**, and are **not valid across
separate simulator runs**. The approved clarification is implemented without any
GraphQL schema change, persistent identity, episode tracker or migration.

Validation requires every supplied Go incident exactly once, an exact reference
at the supplied tick, unchanged severity, and matching robot telemetry. Missing,
invented, duplicate, mismatched, ambiguous and wrong-tick references are rejected.
Unknown fields, including invented persistent incident IDs, are rejected too.
Source snapshots with duplicate identities or contradictory telemetry fail before
model generation. There is no TypeScript incident-detection algorithm.

Only null or an evidenced SetCharging proposal is accepted. Action targets,
purpose, robot/station telemetry and tick must match the supplied snapshot and
host-supported candidate. Demonstration intent must be explicitly enabled by the
host. Existing incident types still require operator attention; SetCharging is
not represented as movement, offline recovery or traffic recovery.

The model has no submission tool. Generation activates no tools and cannot
mutate the fleet before validation. Only after a complete response passes
validation may the host submit, and only with `--submit`. It rereads the real
state, checks snapshot equality and current eligibility, then uses the existing
Go proposal API. A changed snapshot or unavailable fresh read aborts submission
without automatic paid reanalysis. Go remains authoritative for safety,
authorization, human approval, approval-time revalidation and execution.

## Files changed

All changes are inside `agent/`:

| Files | Purpose |
| --- | --- |
| `src/contracts.ts`, `src/reasoning.ts` | Typed reasoning schema, snapshot references, facts and grounding validation |
| `src/submission.ts` | Fresh-state review before submission, shared by offline and live execution |
| `src/agent.ts`, `src/index.ts` | Mastra orchestration, read-only model access, validated-result flow and exports |
| `src/cli.ts` | Cost notice and failure/staleness exit codes |
| `src/fixtures.ts` | Deterministic reasoning fixtures and a local provider adapter, excluded from the production build |
| `src/reasoning.test.ts`, `src/mastra.test.ts`, `src/cli.test.ts`, `src/agent.test.ts` | Grounding, cost gates, actual Mastra loop, output separation, freshness and CLI tests |
| `src/backend.integration.test.ts`, `scripts/check-incident-fixture.mjs` | Real backend submission and Go-to-Mastra incident contract checks without paid requests |
| `.env.example`, `README.md`, `P4-02-REPORT.md` | Provider setup, reference semantics, results and limitations |

No dependencies, lockfile, backend, GraphQL schema, dashboard, simulator, frozen
safety rule or tool input signature changed. The model's tool map was reduced to
read-only access; the existing host proposal tool remains available after review.

## Verification

Node **24.21.0**, existing TypeScript **7.0.2**, Vitest **5.0.2** and Mastra
**1.75.0** were used. Integration ran with Go **1.27.1** and PostgreSQL **18.6**,
with database checks required rather than skipped.

| Command/check | Actual result | Exit |
| --- | --- | --- |
| `npm test` | 6 files passed, **67 tests passed** | 0 |
| `npm run typecheck` | No TypeScript errors | 0 |
| `npm run build` (integration harness) | Compilation passed | 0 |
| `go test -p 1 -parallel 1 -race ./incident/... ./db/... ./fleet/... ./graphql/... ./cmd/server/... -count=1` | All five suites passed, including PostgreSQL integration | 0 |
| `npm run test:integration` | **5 tests passed** against the real Go server and PostgreSQL | 0 |
| `go test ./graphql/... -run '^TestAgentIncidentHTTPClient$' -count=1 -v` | Go-to-TypeScript/Mastra incident contract check passed | 0 |
| `pwsh -NoProfile -File scripts/verify-integration.ps1` | Completed; isolated server and database container removed | 0 |
| Whitespace checks | Tracked and new files passed; Windows CRLF accepted | 0 |

The cross-language check printed:

```text
PASS: 6 Go-classified incidents; actual Mastra generation with a local provider double; grounded snapshot references; stale-reference rejection; zero mutations or paid model requests.
```

Tests exercise exact and wrong-tick references, unknown robots/types, missing and
duplicate interpretations (including duplicates with the correct total count),
ambiguous source identities, altered severity/telemetry, unsupported commands,
explicit demo intent, provider failures, fresh-read failures, changed state at
the same tick, stale ticks, private-error sanitization, read-only defaults and
validated real proposal submission without execution. Test-only fault snapshots
remain behind the existing trusted Go test fixture; no fault endpoint was added.

The actual Mastra loop and structured-output parser run against a deterministic
in-process provider adapter. Application orchestration tests also use generation
doubles, including a test-only API key for the opt-in guard. Neither reaches a
paid provider endpoint. **Paid requests: 0. Real provider API requests: 0.**

## Model provider setup

Use Node 24.21.0. Configure `agent/.env` privately from `.env.example` with the
backend URL and a proposer-only `FLEETOPS_PROPOSER_TOKEN`. Choose a provider/model
supporting structured output using `FLEETOPS_MODEL=provider/model`; there is no
default model. Configure only the matching private key:

| Provider prefix | Private key variable |
| --- | --- |
| `openai` | `OPENAI_API_KEY` |
| `anthropic` | `ANTHROPIC_API_KEY` |
| `google` | `GOOGLE_GENERATIVE_AI_API_KEY` |

From `agent/`, run `npm run build`, then `npm start` for deterministic analysis.
After reviewing provider pricing and accepting charges, `npm start -- --live`
enables LLM reasoning. `--submit` is an independent opt-in; the existing stationary
demo uses `--live --demo --submit` with explicitly configured demo IDs.
No live demonstration was requested or performed during this ticket.

Live generation is bounded to one step, 2400 output tokens, a 30-second timeout
and zero model retries. Input tokens and provider pricing still determine costs;
this is not a guaranteed monetary cap. No raw provider exceptions or rejected
model responses are printed. Grounding rejection is reported as `rejected`;
provider/SDK parsing failures are `failed`. Both prevent submission and retain
the observed facts. The CLI exits 1 for either outcome or a failed freshness check.

## Remaining limitations

- Snapshot references are local to the supplied snapshot and simulator run.
  The backend exposes no runtime identity; a restart producing an identical tick
  and identical telemetry cannot be distinguished by this client. References must
  never be reused across runs or treated as persistent incident identity.
- Read and submit are separate requests. Changes after the fresh read remain
  subject to Go validation and human approval; the client cannot make them atomic.
- Structured identities, measurements and command fields are checked. Free-form
  AI assessments/rationales remain untrusted interpretations; validation is not a
  proof that every natural-language assertion is correct.
- No paid provider/model quality evaluation has occurred. Large responses may
  hit the output limit and fail closed. No real-provider performance or accuracy
  claim is made.
- There is no incident history, autonomous approval, new recovery command,
  cross-run proposal deduplication or public-demo UI in this ticket. The P4-01
  dependency audit remains applicable; no dependency remediation was attempted.

All requested P4-02 implementation and verification is complete. The snapshot-key
clarification was explicitly approved; no unresolved requirement was guessed.
