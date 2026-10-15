# Architecture

Phase 1 implements the deterministic Go simulator. Later phases will add routing, command execution, APIs, and application integration; local PostgreSQL remains separate from simulator state.

## Simulator

The `backend/sim` package owns validated configuration, a warehouse grid of free cells, aisles, walls, and charging stations, and the robot, job, and fault state. `New` validates and clones configuration. Robots hold integer battery percentages; jobs progress from pending through assigned and in-progress to done, retaining assignment history.

Ticks advance deterministically using a per-Sim seeded RNG for job generation. Background dispatch processes robots and pending jobs in numeric ID order. Movement is a one-cell X-first, then Y placeholder with no wall avoidance or collision handling. Actual movement drains battery, including arrival steps; idle robots drain periodically. Charging requires an idle, job-free robot already at a free station. Charge is capped at 100, and completion frees the station without dispatching on that tick.

| Call class | Operations | Boundary |
| --- | --- | --- |
| DYNAMICS | `Tick`, including background dispatch | Background simulation, not an operator command |
| READ | `Snapshot`, `CellAt` | Snapshot owns deep copies; grid lookup is read-only |
| DRIVER | `InjectLowBattery`, `InjectUnresponsive`, `InjectAisleConflict` | Trusted tests, evaluations, and scenario triggers only; never exposed to the agent |
| COMMAND | `SetCharging` | Future operator execution requires validation and approval |

Background dispatch must not become a bypass around future safety validation. Phase 3 owns the validator, executor, operator approval, auditing, and reassignment under the frozen safety rules. Sim is not goroutine-safe; Phase 3 will serialize access. Agent access will use a later GraphQL API, never direct simulator imports.

Unresponsive countdowns run before other robot behavior, even while faulted. Expiry makes the robot offline, overriding faulted without another movement, drain, or charging step. Offline robots retain their jobs and occupied stations. Status transitions create no fault records; successful trusted injections do. Phase 1 has no recovery, pathfinding, or conflict resolution; Phase 2 owns routing and conflicts.

The standalone `cmd/simrun` CLI emits only the final indented Snapshot JSON. Golden snapshot tests, static import/RNG guards, and invariant checks across seeded injection and charging scenarios verify reproducibility and state consistency. Map iteration that could affect determinism remains subject to tests and source review.
