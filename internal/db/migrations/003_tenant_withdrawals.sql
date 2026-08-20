ALTER TABLE withdrawals
    ADD COLUMN IF NOT EXISTS bot_instance_id bigint NOT NULL DEFAULT 1 REFERENCES bot_instances(id);

CREATE INDEX IF NOT EXISTS withdrawals_instance_state_idx
    ON withdrawals(bot_instance_id,state,created_at,id);
