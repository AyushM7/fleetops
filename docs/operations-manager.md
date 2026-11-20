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
