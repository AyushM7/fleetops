// Package incident reconciles fleet incidents from complete simulator snapshots.
// It accepts values only, changes neither input, and has no simulator capability,
// database, authentication, randomness, or clock dependency.
//
// Incidents are keyed by robot and type while open. Persisting conditions retain
// their IDs and update severity. Clearing a condition resolves its incident and
// freezes its last severity; recurrence allocates a new ID. Resolved history is
// retained. A robot absent from the complete snapshot has no active conditions.
//
// Results and newly allocated IDs are ordered by transport ID number, then M1,
// then by incident type within each robot. Episodes of the same type are ordered by numeric
// incident ID. Counter is the last allocated incident number: start at zero,
// then pass the returned counter and incidents to the next reconciliation.
// Callers must retain the counter even if they prune resolved history.
//
// V2 breakdown incidents use the robot's active fault, including communication
// loss before offline confirmation. Legacy snapshots still use offline status
// for unresponsiveness. Historical injections never establish active conditions.
// Blocked means traffic_blocked, regardless of historical aisle conflict events.
package incident
