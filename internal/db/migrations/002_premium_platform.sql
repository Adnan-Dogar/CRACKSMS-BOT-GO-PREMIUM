CREATE TABLE IF NOT EXISTS bot_instances (
    id bigserial PRIMARY KEY,
    parent_id bigint REFERENCES bot_instances(id) ON DELETE CASCADE,
    owner_user_id bigint REFERENCES users(id),
    name text NOT NULL,
    username text NOT NULL DEFAULT '',
    token_config jsonb NOT NULL DEFAULT '{}'::jsonb,
    token_hash text NOT NULL DEFAULT '',
    tier text NOT NULL DEFAULT 'free' CHECK (tier IN ('free','pro','enterprise')),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','running','stopped','rejected','error')),
    enabled boolean NOT NULL DEFAULT false,
    is_main boolean NOT NULL DEFAULT false,
    default_theme smallint NOT NULL DEFAULT 0 CHECK (default_theme BETWEEN 0 AND 9),
    default_group_privacy text NOT NULL DEFAULT 'visible' CHECK (default_group_privacy IN ('visible','masked','hidden')),
    settings jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    approved_at timestamptz,
    approved_by bigint REFERENCES users(id),
    last_started_at timestamptz,
    last_error text NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS one_main_bot_instance ON bot_instances(is_main) WHERE is_main;
CREATE UNIQUE INDEX IF NOT EXISTS bot_instance_username_unique ON bot_instances(lower(username)) WHERE username<>'';
CREATE UNIQUE INDEX IF NOT EXISTS bot_instance_token_hash_unique ON bot_instances(token_hash) WHERE token_hash<>'';
INSERT INTO bot_instances(id,name,status,enabled,is_main,tier)
VALUES(1,'CrackSMS Main','approved',true,true,'enterprise') ON CONFLICT(id) DO NOTHING;
SELECT setval(pg_get_serial_sequence('bot_instances','id'), GREATEST((SELECT max(id) FROM bot_instances),1));

ALTER TABLE assignments ADD COLUMN IF NOT EXISTS bot_instance_id bigint NOT NULL DEFAULT 1 REFERENCES bot_instances(id);
ALTER TABLE otp_events ADD COLUMN IF NOT EXISTS bot_instance_id bigint NOT NULL DEFAULT 1 REFERENCES bot_instances(id);
ALTER TABLE otp_events ADD COLUMN IF NOT EXISTS country text NOT NULL DEFAULT '';
ALTER TABLE panels ADD COLUMN IF NOT EXISTS bot_instance_id bigint NOT NULL DEFAULT 1 REFERENCES bot_instances(id);
ALTER TABLE panel_ingest_jobs ADD COLUMN IF NOT EXISTS bot_instance_id bigint NOT NULL DEFAULT 1 REFERENCES bot_instances(id);
ALTER TABLE delivery_jobs ADD COLUMN IF NOT EXISTS bot_instance_id bigint NOT NULL DEFAULT 1 REFERENCES bot_instances(id);
ALTER TABLE delivery_jobs ADD COLUMN IF NOT EXISTS theme_id smallint NOT NULL DEFAULT 0 CHECK (theme_id BETWEEN 0 AND 9);
ALTER TABLE delivery_jobs ADD COLUMN IF NOT EXISTS otp_visibility text NOT NULL DEFAULT 'visible' CHECK (otp_visibility IN ('visible','masked','hidden'));
ALTER TABLE otp_group_destinations ADD COLUMN IF NOT EXISTS bot_instance_id bigint NOT NULL DEFAULT 1 REFERENCES bot_instances(id);
ALTER TABLE otp_group_destinations ADD COLUMN IF NOT EXISTS otp_visibility text NOT NULL DEFAULT 'visible' CHECK (otp_visibility IN ('visible','masked','hidden'));
ALTER TABLE otp_group_destinations ADD COLUMN IF NOT EXISTS theme_id smallint CHECK (theme_id BETWEEN 0 AND 9);
ALTER TABLE required_chats ADD COLUMN IF NOT EXISTS bot_instance_id bigint NOT NULL DEFAULT 1 REFERENCES bot_instances(id);
ALTER TABLE tutorials ADD COLUMN IF NOT EXISTS bot_instance_id bigint NOT NULL DEFAULT 1 REFERENCES bot_instances(id);
ALTER TABLE tutorials ADD COLUMN IF NOT EXISTS description text NOT NULL DEFAULT '';
ALTER TABLE tutorials ADD COLUMN IF NOT EXISTS content_type text NOT NULL DEFAULT 'text' CHECK (content_type IN ('text','photo','video','document'));

ALTER TABLE panels DROP CONSTRAINT IF EXISTS panels_name_key;
CREATE UNIQUE INDEX IF NOT EXISTS panels_instance_name_unique ON panels(bot_instance_id,name);
ALTER TABLE otp_group_destinations DROP CONSTRAINT IF EXISTS otp_group_destinations_pkey;
ALTER TABLE otp_group_destinations ADD PRIMARY KEY(bot_instance_id,chat_id);
ALTER TABLE required_chats DROP CONSTRAINT IF EXISTS required_chats_pkey;
ALTER TABLE required_chats ADD PRIMARY KEY(bot_instance_id,chat_id);
ALTER TABLE delivery_jobs DROP CONSTRAINT IF EXISTS delivery_jobs_otp_event_id_target_kind_target_id_key;
CREATE UNIQUE INDEX IF NOT EXISTS delivery_jobs_tenant_target_unique
    ON delivery_jobs(otp_event_id,bot_instance_id,target_kind,target_id);
CREATE INDEX IF NOT EXISTS assignments_instance_user_idx ON assignments(bot_instance_id,user_id,assigned_at DESC);
CREATE INDEX IF NOT EXISTS otp_events_instance_user_idx ON otp_events(bot_instance_id,assigned_user_id,received_at DESC);
CREATE INDEX IF NOT EXISTS panels_instance_enabled_idx ON panels(bot_instance_id,id) WHERE enabled;

CREATE TABLE IF NOT EXISTS tenant_admins (
    bot_instance_id bigint NOT NULL REFERENCES bot_instances(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    permissions text[] NOT NULL DEFAULT ARRAY['*']::text[],
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(bot_instance_id,user_id)
);

CREATE TABLE IF NOT EXISTS bot_instance_users (
    bot_instance_id bigint NOT NULL REFERENCES bot_instances(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    joined_at timestamptz NOT NULL DEFAULT now(),
    last_active_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(bot_instance_id,user_id)
);
CREATE INDEX IF NOT EXISTS bot_instance_users_active_idx ON bot_instance_users(bot_instance_id,last_active_at DESC);

CREATE TABLE IF NOT EXISTS user_subscriptions (
    bot_instance_id bigint NOT NULL REFERENCES bot_instances(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tier text NOT NULL DEFAULT 'free' CHECK (tier IN ('free','pro','enterprise')),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','expired','cancelled')),
    starts_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz,
    granted_by bigint REFERENCES users(id),
    PRIMARY KEY(bot_instance_id,user_id)
);

CREATE TABLE IF NOT EXISTS user_preferences (
    bot_instance_id bigint NOT NULL REFERENCES bot_instances(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    theme_id smallint CHECK (theme_id BETWEEN 0 AND 9),
    language text NOT NULL DEFAULT 'en',
    compact_menu boolean NOT NULL DEFAULT true,
    timezone text NOT NULL DEFAULT 'Asia/Karachi',
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(bot_instance_id,user_id)
);

CREATE TABLE IF NOT EXISTS webhook_endpoints (
    id bigserial PRIMARY KEY,
    bot_instance_id bigint NOT NULL REFERENCES bot_instances(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    url text NOT NULL,
    secret_config jsonb NOT NULL,
    events text[] NOT NULL DEFAULT ARRAY['otp.received']::text[],
    enabled boolean NOT NULL DEFAULT true,
    consecutive_failures integer NOT NULL DEFAULT 0,
    last_success_at timestamptz,
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS webhook_endpoint_user_idx ON webhook_endpoints(bot_instance_id,user_id);

CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id bigserial PRIMARY KEY,
    endpoint_id bigint NOT NULL REFERENCES webhook_endpoints(id) ON DELETE CASCADE,
    otp_event_id uuid NOT NULL REFERENCES otp_events(id) ON DELETE CASCADE,
    event_name text NOT NULL,
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','sending','retry','sent','failed')),
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    claimed_at timestamptz,
    response_code integer,
    last_error text NOT NULL DEFAULT '',
    sent_at timestamptz,
    UNIQUE(endpoint_id,otp_event_id,event_name)
);
CREATE INDEX IF NOT EXISTS webhook_delivery_pending_idx ON webhook_deliveries(next_attempt_at,id)
    WHERE state IN ('pending','sending','retry');

CREATE TABLE IF NOT EXISTS scheduled_messages (
    id bigserial PRIMARY KEY,
    bot_instance_id bigint NOT NULL REFERENCES bot_instances(id) ON DELETE CASCADE,
    creator_user_id bigint NOT NULL REFERENCES users(id),
    target_kind text NOT NULL CHECK (target_kind IN ('user','group','all_users')),
    target_id bigint,
    body text NOT NULL,
    parse_mode text NOT NULL DEFAULT 'HTML',
    deliver_at timestamptz NOT NULL,
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','sending','sent','cancelled','failed')),
    attempts integer NOT NULL DEFAULT 0,
    claimed_at timestamptz,
    last_error text NOT NULL DEFAULT '',
    sent_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS scheduled_messages_due_idx ON scheduled_messages(deliver_at,id)
    WHERE state IN ('pending','sending');

CREATE TABLE IF NOT EXISTS api_keys (
    id bigserial PRIMARY KEY,
    bot_instance_id bigint NOT NULL REFERENCES bot_instances(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL,
    key_prefix text NOT NULL,
    key_hash text NOT NULL UNIQUE,
    scopes text[] NOT NULL DEFAULT ARRAY['analytics:read','history:read']::text[],
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz,
    last_used_at timestamptz
);
CREATE INDEX IF NOT EXISTS api_keys_user_idx ON api_keys(bot_instance_id,user_id);

CREATE TABLE IF NOT EXISTS audit_log (
    id bigserial PRIMARY KEY,
    bot_instance_id bigint NOT NULL REFERENCES bot_instances(id) ON DELETE CASCADE,
    actor_user_id bigint,
    action text NOT NULL,
    target_type text NOT NULL DEFAULT '',
    target_id text NOT NULL DEFAULT '',
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS audit_log_instance_time_idx ON audit_log(bot_instance_id,created_at DESC);

CREATE TABLE IF NOT EXISTS custom_otp_patterns (
    id bigserial PRIMARY KEY,
    bot_instance_id bigint NOT NULL REFERENCES bot_instances(id) ON DELETE CASCADE,
    name text NOT NULL,
    pattern text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    created_by bigint REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(bot_instance_id,name)
);
