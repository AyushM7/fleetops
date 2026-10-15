// Package sim defines pure fleet data contracts and grid configuration.
// Future calls fall into four classes:
//   - DYNAMICS: Tick models background fleet behavior, including dispatch.
//   - READ: Snapshot and CellAt are read-only.
//   - DRIVER: Inject* operations are trusted scenario/test operations, never agent tools.
//   - COMMAND: SetCharging is a future operator command requiring validation and approval.
//
// Background dispatch within Tick is not an agent/operator-originated command.
// It must not become a bypass around future safety validation. The Phase 3
// executor owns commands and their validation and approval boundaries.
// The future Sim is not goroutine-safe; Phase 3 will serialize access.
// P1-02 will ensure all State slices serialize as [] rather than null.
// CellOutOfBounds is a lookup result, not a State cell.
// No Sim or future call implementation is provided here.
package sim
