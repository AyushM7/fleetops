// Package incident reconciles fleet incidents from complete simulator snapshots.
// It accepts values only, changes neither input, and has no simulator capability,
// database, authentication, randomness, or clock dependency.
//
// Incidents are keyed by robot and type while open. Persisting conditions retain
// their IDs and update severity. Clearing a condition resolves its incident and
// freezes its last severity; recurrence allocates a new ID. Resolved history is
// retained. A robot absent from the complete snapshot has no active conditions.
//
// Results and newly allocated IDs are ordered by numeric robot ID, then lexical
// incident type. Historical episodes of the same type are ordered by numeric
// incident ID. Counter is the last allocated incident number: start at zero,
// then pass the returned counter and incidents to the next reconciliation.
// Callers must retain the counter even if they prune resolved history.
//
// Historical FaultState entries do not establish current conditions. In
// particular, injected unresponsiveness is detected only when status is offline,
// and Blocked means traffic_blocked, never a confirmed aisle_conflict. This
// package does not create separate incidents from historical fault evidence.
package incident
