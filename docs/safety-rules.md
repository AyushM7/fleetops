# Frozen safety rules

1. Every action needs a valid pending Proposal and operator approval.
2. Target robot must exist, not be faulted or offline, and have battery >= 30%.
3. A job cannot be assigned to two robots.
4. A route must not enter an aisle another robot occupies.
5. Charging requires a free station.
6. Rejected or expired proposals change nothing.
7. Every attempt, accepted or rejected, is written to the audit log.
