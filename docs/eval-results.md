# Evaluation results

## P5-00 — evaluation foundation (2026-10-17)

The offline framework and deterministic double passed both foundation contract
cases: grounded charging in place and low-battery operator attention/abstention.
This is framework validation, not real LLM accuracy or recovery effectiveness.
See [evaluation usage and exact scoring criteria](../evals/README.md).

Actual verification from `evals/`:

| Command | Actual output/result | Exit |
| --- | --- | --- |
| `npm test` | `'vitest' is not recognized`; local Windows shims missing | 1 |
| `npm run typecheck` | `'tsc' is not recognized`; local Windows shims missing | 1 |
| `node node_modules/vitest/vitest.mjs run` | Startup error: missing native rolldown binding | 1 |
| `node node_modules/typescript/bin/tsc --noEmit` | Missing `@typescript/typescript-win32-x64` | 1 |
| `node ../agent/node_modules/vitest/vitest.mjs run` | `Test Files 1 passed (1)`; `Tests 32 passed (32)` | 0 |
| `node ../agent/node_modules/typescript/bin/tsc --noEmit` | No diagnostics | 0 |
| `npm run benchmark --silent` | Two cases passed; zero unsuccessful; JSON stdout and summary stderr | 0 |

The final suite also launches the CLI twice, checks both exit codes are zero,
parses the JSON totals and verifies byte-identical stdout. During implementation,
an initial fallback typecheck exposed third-party Mastra declaration errors and
missing Node ambient types; the eval config now matches the agent's existing
skipLibCheck policy and reuses its installed Node types. No dependencies changed.

Both cases pass incident-reference, structured-evidence, recommendation-validity
and command-allowlist checks. Operator attention and abstention each pass in one
case and are not applicable in the charging case. Submission, execution and job
delivery are all explicitly `not_measured`. No live model/API, Docker, simulator
execution, proposal submission, or simulator mutation occurred.

Remaining limitations: two synthetic cases, fixed deterministic outputs, no prose
semantic grading, no behavioral/recovery measurements, no live-model comparison.
P5-01 will supply broader independently reviewed scenarios; it was not started.
