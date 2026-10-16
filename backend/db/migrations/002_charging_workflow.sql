CREATE TABLE fleet_proposals (
    id text PRIMARY KEY,
    runtime_id text NOT NULL,
    state text NOT NULL CHECK (state IN ('pending', 'approved', 'rejected', 'expired')),
    execution text NOT NULL CHECK (execution IN ('none', 'succeeded', 'failed')),
    payload jsonb NOT NULL,
    CHECK (payload->>'ID' = id),
    CHECK (payload->>'Action' = 'set_charging'),
    CHECK (payload->>'State' = state),
    CHECK (execution = 'none' OR state IN ('approved', 'expired'))
);

-- No proposal FK: unknown IDs and failed submissions must also be auditable.
CREATE TABLE fleet_audit (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    runtime_id text NOT NULL,
    proposal_id text NOT NULL,
    attempt text NOT NULL,
    at timestamptz NOT NULL,
    operator_subject text NOT NULL,
    accepted boolean NOT NULL,
    reason text NOT NULL,
    payload jsonb NOT NULL
);

CREATE INDEX fleet_audit_proposal ON fleet_audit (proposal_id, id);
