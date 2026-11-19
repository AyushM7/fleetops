ALTER TABLE fleet_automatic_audit
    ADD COLUMN command_key text CHECK (command_key ~ '^[a-zA-Z0-9_-]{8,64}$'),
    ADD COLUMN fingerprint text CHECK (fingerprint ~ '^[a-f0-9]{64}$'),
    ADD COLUMN replay_of bigint REFERENCES fleet_automatic_audit (id),
    ADD CHECK ((command_key IS NULL) = (fingerprint IS NULL)),
    ADD CHECK (replay_of IS NULL OR command_key IS NULL),
    DROP CONSTRAINT fleet_automatic_status,
    DROP CONSTRAINT fleet_automatic_receipt,
    ADD CONSTRAINT fleet_automatic_status CHECK (status IN ('pending', 'rejected', 'applied', 'replayed')),
    ADD CONSTRAINT fleet_automatic_receipt CHECK (
        (status = 'pending' AND finished_at IS NULL AND reason = '' AND replay_of IS NULL) OR
        (status = 'rejected' AND finished_at IS NOT NULL AND reason <> '' AND replay_of IS NULL) OR
        (status = 'applied' AND finished_at IS NOT NULL AND reason = '' AND replay_of IS NULL) OR
        (status = 'replayed' AND finished_at IS NOT NULL AND reason = '' AND replay_of IS NOT NULL)
    );
CREATE UNIQUE INDEX fleet_automatic_command_key ON fleet_automatic_audit(runtime_id, command_key) WHERE command_key IS NOT NULL;
