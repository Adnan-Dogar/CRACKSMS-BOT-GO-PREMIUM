CREATE TABLE IF NOT EXISTS withdrawal_accounts (
    id bigserial PRIMARY KEY,
    bot_instance_id bigint NOT NULL REFERENCES bot_instances(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    method text NOT NULL CHECK (method IN ('jazzcash','easypaisa','binance','usdt_bep20')),
    display_hint text NOT NULL,
    details_hash text NOT NULL,
    details_config jsonb NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(bot_instance_id,user_id,method,details_hash)
);
CREATE INDEX IF NOT EXISTS withdrawal_accounts_user_idx
    ON withdrawal_accounts(bot_instance_id,user_id,enabled,id);

ALTER TABLE withdrawals
    ADD COLUMN IF NOT EXISTS account_id bigint REFERENCES withdrawal_accounts(id) ON DELETE SET NULL;
ALTER TABLE withdrawals
    ADD COLUMN IF NOT EXISTS details_config jsonb;

CREATE TABLE IF NOT EXISTS service_profiles (
    bot_instance_id bigint NOT NULL REFERENCES bot_instances(id) ON DELETE CASCADE,
    service_key text NOT NULL,
    display_name text NOT NULL,
    custom_emoji_id text NOT NULL,
    created_by bigint REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(bot_instance_id,service_key)
);

CREATE TABLE IF NOT EXISTS telegram_flows (
    bot_instance_id bigint NOT NULL REFERENCES bot_instances(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind text NOT NULL,
    step text NOT NULL,
    data_config jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    PRIMARY KEY(bot_instance_id,user_id)
);
CREATE INDEX IF NOT EXISTS telegram_flows_expiry_idx ON telegram_flows(expires_at);
