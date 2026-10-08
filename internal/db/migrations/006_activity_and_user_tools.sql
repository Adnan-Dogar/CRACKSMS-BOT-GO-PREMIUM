ALTER TABLE user_preferences ADD COLUMN IF NOT EXISTS display_format text NOT NULL DEFAULT 'auto'
    CHECK (display_format IN ('auto','rich','classic'));

CREATE INDEX IF NOT EXISTS otp_activity_period_idx ON otp_events(bot_instance_id,received_at DESC) WHERE code<>'';

CREATE TABLE user_favorites (
    id bigserial PRIMARY KEY,
    bot_instance_id bigint NOT NULL REFERENCES bot_instances(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    service text NOT NULL, country text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(bot_instance_id,user_id,service,country)
);
CREATE TABLE user_last_selections (
    bot_instance_id bigint NOT NULL REFERENCES bot_instances(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    service text NOT NULL, country text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(bot_instance_id,user_id)
);
CREATE TABLE availability_watches (
    id bigserial PRIMARY KEY,
    bot_instance_id bigint NOT NULL REFERENCES bot_instances(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    service text NOT NULL, country text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    was_available boolean NOT NULL DEFAULT false,
    last_notified_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(bot_instance_id,user_id,service,country)
);
CREATE INDEX availability_watches_active_idx ON availability_watches(bot_instance_id,id) WHERE enabled;
CREATE TABLE ui_notification_jobs (
    id bigserial PRIMARY KEY,
    bot_instance_id bigint NOT NULL REFERENCES bot_instances(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    watch_id bigint NOT NULL REFERENCES availability_watches(id) ON DELETE CASCADE,
    state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','sending','sent','failed','skipped','uncertain')),
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    claimed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX ui_notification_one_pending_watch ON ui_notification_jobs(watch_id) WHERE state IN ('pending','sending');
CREATE INDEX ui_notification_pending_idx ON ui_notification_jobs(bot_instance_id,next_attempt_at) WHERE state='pending';
CREATE TABLE live_screen_sessions (
    bot_instance_id bigint NOT NULL REFERENCES bot_instances(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    message_id bigint NOT NULL,
    view text NOT NULL DEFAULT 'private',
    period text NOT NULL DEFAULT '24h',
    page integer NOT NULL DEFAULT 0,
    paused boolean NOT NULL DEFAULT true,
    expires_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(bot_instance_id,user_id)
);

CREATE TABLE telegram_command_scopes (
 bot_instance_id bigint NOT NULL REFERENCES bot_instances(id) ON DELETE CASCADE,
 user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 PRIMARY KEY(bot_instance_id,user_id)
);
