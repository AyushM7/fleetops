CREATE TABLE fleet_automatic_audit (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    runtime_id text NOT NULL,
    subject text NOT NULL,
    received_at timestamptz NOT NULL,
    finished_at timestamptz,
    command jsonb NOT NULL CHECK (octet_length(command::text) <= 8192),
    status text NOT NULL CONSTRAINT fleet_automatic_status CHECK (status IN ('pending', 'rejected', 'applied')),
    reason text NOT NULL DEFAULT '',
    CONSTRAINT fleet_automatic_receipt CHECK (
        (status = 'pending' AND finished_at IS NULL AND reason = '') OR
        (status = 'rejected' AND finished_at IS NOT NULL AND reason <> '') OR
        (status = 'applied' AND finished_at IS NOT NULL AND reason = '')
    )
);
CREATE INDEX fleet_automatic_audit_runtime ON fleet_automatic_audit(runtime_id, id);
