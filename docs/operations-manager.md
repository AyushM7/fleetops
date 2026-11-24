# FleetOps V2 Operations Manager spending

The Operations Manager uses one PostgreSQL ledger in the `fleetops_operations`
schema. Hosts serving the same public FleetOps visitors must point at the same
database. The ledger records each request on its UTC admission date and rejects
requests in the last 20 seconds before midnight. Visitor-schema cleanup has no
effect on this ledger.

The host commits an accounting reservation before launching the worker.
There is no application spending ceiling. Provider-reported token usage
settles the reservation once and releases the unused estimate. A timeout,
worker crash, missing usage, or ambiguous database outcome leaves the
reservation unsettled at its full estimate. Known pricing or usage anomalies
freeze admissions for the affected UTC day. Startup migrates the legacy daily
constraint and preserves existing ledger rows and freeze flags.

The only reviewed model is GPT-6 Luna via OpenAI Responses, using Standard
text pricing with zero tools, zero retries, and at most 768 output tokens
(including reasoning). The worker attaches its private key only after validating a single HTTPS
request to OpenAI Responses. The price review covers the default 30-minute
cache policy. The request guard excludes extended cache settings and explicit
cache breakpoints.
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

At those rates, one request reserves $0.289384. Settlement estimates Standard
text charges using the reported token counts and releases the unused amount.
Short-context rates per million tokens are $0.10 uncached input, $0.01
cached input, $0.125 default-duration cache writes, and $0.50 output.
Above 272,000 input tokens, input rates double and the output rate is 1.5
times the short-context rate. Cache counts are stored when the worker reports
them. Tokens without a reported cache class use the uncached rate for the estimate. New ledger rows
record the pricing basis beside reported cache counts, and prior settled
amounts remain as recorded. The estimate is local accounting. Review provider pricing
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
warehouse-v2 scenario schedules model cycles. Transport reviews have a
30-second cooldown and the server allows two workers per process. During
that cooldown, a newly eligible maintenance occurrence can open a
maintenance-only review. The same occurrence cannot open another
maintenance-only review in that runtime. Every review uses the same
worker timeout, usage accounting and admission ledger.

The authenticated dispatch window ends on the worker result, pause, stop,
or expiry and lasts no more than 20 simulator ticks. Movement, charging
and collision avoidance keep running. Go rechecks live robot and fault
identity, job ownership, service-cell feasibility, energy and attempt
history before admitting maintenance. A cancelled or rejected decision
consumes no repair attempt and deterministic scheduling can resume.

Each observation is recorded against its runtime in
`fleetops_operations.activity`. The host records a grounded proposal before
it enters the Go command boundary, which supplies its own durable receipt.
The activity reports terminal results, including abstention, ledger admission denial,
stale proposals and uncertain execution. `recorded: false` means that the
activity projection could not be confirmed in the shared audit store.
A confirmed Go receipt remains authoritative about command execution.
For maintenance, the receipt confirms admission. Follow the matching
Go simulator attempt for repair progress and verification, and the
original job record for delivery. Go permits one admitted attempt per
fault occurrence. Unreachable service sites, critical battery, and
exhausted attempts may require external assistance. See the
[Phase 4 verification procedure](v2-phase4-acceptance.md#reproducible-demonstration).

V2 status and SSE frames expose up to 12 recent activities per runtime.
An `operations` SSE event advances the same generation-scoped sequence as
warehouse ticks, and reconnect snapshots include the recent history.
Telemetry carries typed decisions, IDs and usage only. It contains no
prompts, private reasoning or credentials. V1 routes keep their current
approval workflow.

The production scratch image currently ships the Go server and web assets.
Public enablement requires a deployment that also supplies Node and the built
Operations worker, a dedicated provider credential, and the shared PostgreSQL
ledger. Keep deterministic mode as the default. Removing the opt-in from the
server environment and restarting disables automatic AI dispatch. Visitor
limits control request frequency. Provider-side project controls govern
spending.

## V3 incident worker

The default remains deterministic. The optional Operations image is built with
`docker build --target operations -t fleetops-v2-operations:local .`. It includes
the normal server, the compiled Mastra worker and the React build.

Use the normal visitor server configuration for the database, private evidence
directory and public origin. In an independently approved live environment, set
`FLEETOPS_OPERATIONS_ENABLED=1`, `FLEETOPS_OPERATIONS_PROTOCOL=v3`, the two existing
upper-bound token prices, and positive integer caps
`FLEETOPS_OPERATIONS_DAILY_REQUESTS` and `FLEETOPS_OPERATIONS_DAILY_MICRO_USD`.
Supply the provider credential to Go through the server environment. The
Operations image already sets the worker path. A native installation uses the
absolute path to the compiled `agent/dist/operations-worker.js`.

Go starts one proposer-scoped run per active incident episode and sends the worker
a detached snapshot. The simulator continues ticking. Each worker provider
request travels over bounded stdio to Go. The worker receives no provider key.
Go reserves the configured worst case in the shared ledger before forwarding a
request to the fixed Responses endpoint. A review allows at most six sends and
30 seconds with zero retries. The daily caps use the shared database day, include
outstanding reservations, and serialize across visitor runtimes. Confirmed usage
is linked by provider response ID and settled using configured upper rates.
Uncertain sends retain their full reservation. Invalid or out-of-budget results
produce an audit outcome without a proposal.

Go can admit a recent completion across harmless revisions only when the
verified evidence still matches. Any admitted proposal waits for a separate
operator, and Go rechecks the evidence under the approval transaction before
running its existing feasibility preflight. The worker has read tools and no
command or approval capability.
