# FleetOps V2 Operations Manager spending

The Operations Manager uses one PostgreSQL ledger in the `fleetops_operations`
schema. Hosts serving the same public FleetOps visitors must point at the same
database. The ledger charges every request to its UTC admission date, rejects
requests in the last 20 seconds before midnight, and starts a fresh allowance
on the next UTC date. Visitor-schema cleanup has no effect on this ledger.

The host commits the reservation before launching the worker. A conditional
PostgreSQL update enforces the $5.00 daily limit across processes and sessions.
Provider-reported token usage can release the difference once. A timeout,
worker crash, missing usage, or ambiguous database outcome leaves the full
reservation in place. Known pricing or usage anomalies freeze the admission day.

The only reviewed model is GPT-6 Luna via OpenAI Responses, using Standard
text pricing with zero tools, zero retries, and at most 768 output tokens
(including reasoning). The worker allows a single HTTPS request to OpenAI.
The Go host bounds worker execution to eight seconds. The estimate uses all
1,050,000 documented context tokens at the configured maximum input rate.
It prices every input token at a conservative upper bound that includes
long-context, cache-write and regional premiums. Output uses a conservative
upper bound that includes the long-context and regional premiums.

- `FLEETOPS_OPERATIONS_INPUT_MICRO_USD_PER_MILLION` requires an explicit
  integer of at least `275000` ($0.275 per million input tokens).
- `FLEETOPS_OPERATIONS_OUTPUT_MICRO_USD_PER_MILLION` requires an explicit
  integer of at least `825000` ($0.825 per million output tokens).
- `FLEETOPS_OPERATIONS_AGENT` is the absolute path to the built
  `agent/dist/operations-worker.js`. Build it with `npm run build` in `agent`.
- `OPENAI_API_KEY` belongs only in the server environment. Use a dedicated
  OpenAI project and key for this runtime.

At those rates, one request reserves $0.289384. Reconciliation multiplies
the reported token counts by the same conservative rates. That figure is
ledger accounting rather than a provider invoice. Review provider pricing
before enabling calls and after pricing changes. Configure project-level
limits and monitoring as an additional safeguard where available. The
application ledger does not cover requests made outside this application,
provider billing delays, service or pricing changes, and unauthorized use
of the key.

- [GPT-6 Luna context and pricing](https://developers.openai.com/api/docs/models/gpt-6-luna)
- [OpenAI Responses API](https://developers.openai.com/api/reference/resources/responses/methods/create)

Set `FLEETOPS_OPERATIONS_ENABLED=1` in the Go visitor server environment to
opt in. The default is deterministic dispatch. Missing credentials, pricing,
worker or ledger configuration leave that mode in place. Only a playing
warehouse-v2 scenario schedules model cycles. Each visitor can start at
most one cycle every 30 seconds, with two workers per server process.
Only the offered jobs and robots get a dispatch window, which ends on the
worker result, pause, stop or expiry and lasts no more than 20 simulator
ticks. Movement, charging and collision avoidance keep running throughout.
Go may reject an otherwise valid proposal when these live conditions change.

Each observation is recorded against its runtime in
`fleetops_operations.activity`. The host records a grounded proposal before
it enters the Go command boundary, which supplies its own durable receipt.
The activity reports terminal results, including abstention, denied budget,
stale proposals and uncertain execution. `recorded: false` means that the
activity projection could not be confirmed in the shared audit store.
A confirmed Go receipt remains authoritative about command execution.

V2 status and SSE frames expose up to 12 recent activities per runtime.
An `operations` SSE event advances the same generation-scoped sequence as
warehouse ticks, and reconnect snapshots include the recent history.
Telemetry carries typed decisions, IDs and usage only. It contains no
prompts, private reasoning or credentials. V1 routes keep their current
approval workflow.
