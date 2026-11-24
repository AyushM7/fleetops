# FleetOps V2 Phase 4 acceptance

FleetOps opens the V2 warehouse with ten transport robots, maintenance robot M1, and four chargers. Go owns deterministic scheduling, cell and edge reservations, charging, parking, maintenance, recovery, and execution. The production warehouse uses the verified 85% charging target and bounded job deferral. The public deployment defaults to deterministic operation.

## Reproducible demonstration

Run from `backend`:

```sh
go run ./cmd/simrun -layout=warehouse-v2 -seed=42 -ticks=139 -breakdown=48:R4:sensor_failure
go test ./sim -run 'Maintenance|PassageYield|Congestion|Golden' -count=1
```

The seed-42 trace records the R4 fault at tick 48, repair at 108, J4 delivery at 122, and M1 back at its bay at 139. The retained 25,000-tick seed-2 runs recorded:

| Workload | Deliveries | Longest gap while work was queued |
| --- | ---: | ---: |
| Normal | 1,713 | 109 ticks |
| Charging and parking | 453 | 262 ticks |
| Simultaneous faults | 692 | 545 ticks |

These runs exercise the recorded workloads. Physical blockages and energy-infeasible jobs can still leave a backlog.

## Recovery Decision V3

The normal Operations Manager can opt in to V3. The Mastra worker reviews a pinned Go observation and proposes or abstains. Go validates evidence, freshness, and eligibility, accounts for provider requests in PostgreSQL, and requires a separate operator approval before execution. Replayed approvals retain one execution receipt. A prior authorized live run reported two V3 cases and six confirmed GPT-6 Luna requests. The release checks are credential-free. See [Operations controls](operations-manager.md) and the [V3 contract](recovery-decision-v3.md).

## Current legacy status

The historical V2 decision audit recorded 19 accepted cases, four grounding rejections, and one case that stopped before finalization. Keep the V2 manual and historical replay paths while their callers still depend on the V2 contract. Detailed investigation logs and provider traces are retained as local evidence.

## Release and staging

Use `make test` and the PostgreSQL-backed `make test-integration` gate. Build the dashboard and agent from their lockfiles, and validate the production Compose configuration. The [Lightsail replacement runbook](AWS-REPLACEMENT.md) covers backups, isolated staging, verification, and rollback. Review GitHub checks on the exact release revision before merging. Staging and any production cutover require separate approval.
