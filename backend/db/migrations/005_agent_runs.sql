CREATE TABLE fleet_agent_runs (
    id text PRIMARY KEY,
    runtime_id text NOT NULL,
    observation_id text NOT NULL CHECK (observation_id ~ '^[a-f0-9]{64}$'),
    owner_subject text NOT NULL,
    started_at timestamptz NOT NULL,
    completed_at timestamptz,
    metadata jsonb NOT NULL CHECK (octet_length(metadata::text) <= 4096),
    facts jsonb NOT NULL CHECK (octet_length(facts::text) <= 32768),
    completion jsonb CHECK (octet_length(completion::text) <= 32768),
    completion_hash text,
    proposal_id text UNIQUE REFERENCES fleet_proposals(id),
    receipt jsonb,
    CHECK ((completed_at IS NULL AND completion IS NULL AND completion_hash IS NULL AND proposal_id IS NULL AND receipt IS NULL)
        OR (completed_at IS NOT NULL AND completed_at >= started_at AND completion IS NOT NULL AND completion_hash IS NOT NULL AND completion_hash ~ '^[a-f0-9]{64}$' AND receipt IS NOT NULL))
);
CREATE INDEX fleet_agent_runs_owner ON fleet_agent_runs(runtime_id, owner_subject, started_at DESC, id);
