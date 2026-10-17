-- 002's payload-only CHECK restricts the action to SetCharging.
-- Keep all historical payloads and widen only this constraint.
ALTER TABLE fleet_proposals DROP CONSTRAINT fleet_proposals_payload_check;
ALTER TABLE fleet_proposals ADD CONSTRAINT fleet_proposals_action_check
    CHECK (payload->>'Action' IN ('set_charging', 'reassign_unstarted_job'));
