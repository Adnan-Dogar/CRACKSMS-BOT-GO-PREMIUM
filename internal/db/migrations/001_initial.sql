CREATE TABLE IF NOT EXISTS schema_migrations (
    version bigint PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS users (
    id bigint PRIMARY KEY,
    username text NOT NULL DEFAULT '',
    first_name text NOT NULL DEFAULT '',
    last_name text NOT NULL DEFAULT '',
    balance_pkr numeric(18,4) NOT NULL DEFAULT 0,
    balance_usd numeric(18,6) NOT NULL DEFAULT 0,
    total_otps bigint NOT NULL DEFAULT 0,
    referred_by bigint REFERENCES users(id),
    referral_code text UNIQUE,
    referral_reward_given boolean NOT NULL DEFAULT false,
    banned boolean NOT NULL DEFAULT false,
    joined_at timestamptz NOT NULL DEFAULT now(),
    last_active_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS admins (
    user_id bigint PRIMARY KEY,
    permissions text[] NOT NULL DEFAULT ARRAY['*']::text[],
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS service_countries (
    service text NOT NULL,
    country text NOT NULL,
    country_code text NOT NULL DEFAULT '',
    price_pkr numeric(18,4) NOT NULL DEFAULT 0,
    price_usd numeric(18,6) NOT NULL DEFAULT 0,
    numbers_per_cycle integer NOT NULL DEFAULT 3 CHECK (numbers_per_cycle > 0),
    enabled boolean NOT NULL DEFAULT true,
    PRIMARY KEY(service, country)
);

CREATE TYPE number_state AS ENUM ('available', 'assigned', 'consumed', 'disabled');

CREATE TABLE IF NOT EXISTS numbers (
    id bigserial PRIMARY KEY,
    phone text NOT NULL UNIQUE,
    normalized_phone text NOT NULL UNIQUE,
    service text NOT NULL,
    country text NOT NULL,
    state number_state NOT NULL DEFAULT 'available',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY(service, country) REFERENCES service_countries(service, country)
);
CREATE INDEX IF NOT EXISTS numbers_available_idx ON numbers(service, country, id) WHERE state = 'available';

CREATE TYPE assignment_state AS ENUM ('active', 'expired', 'completed', 'cancelled');

CREATE TABLE IF NOT EXISTS assignments (
    id uuid PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id),
    service text NOT NULL,
    country text NOT NULL,
    state assignment_state NOT NULL DEFAULT 'active',
    assigned_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    closed_at timestamptz
);
CREATE INDEX IF NOT EXISTS assignments_expiry_idx ON assignments(expires_at) WHERE state = 'active';

CREATE TABLE IF NOT EXISTS assignment_numbers (
    assignment_id uuid NOT NULL REFERENCES assignments(id) ON DELETE CASCADE,
    number_id bigint NOT NULL REFERENCES numbers(id),
    consumed_at timestamptz,
    released_at timestamptz,
    otp_event_id uuid,
    PRIMARY KEY(assignment_id, number_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS one_active_assignment_per_number
    ON assignment_numbers(number_id) WHERE consumed_at IS NULL AND released_at IS NULL;

CREATE TABLE IF NOT EXISTS number_user_exclusions (
    number_id bigint NOT NULL REFERENCES numbers(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY(number_id, user_id)
);
CREATE INDEX IF NOT EXISTS number_exclusion_expiry_idx ON number_user_exclusions(expires_at);

CREATE TABLE IF NOT EXISTS otp_events (
    id uuid PRIMARY KEY,
    dedup_key text NOT NULL UNIQUE,
    panel_id bigint,
    panel_name text NOT NULL DEFAULT '',
    phone text NOT NULL,
    normalized_phone text NOT NULL,
    service text NOT NULL DEFAULT '',
    message text NOT NULL,
    code text NOT NULL DEFAULT '',
    provider_timestamp timestamptz,
    received_at timestamptz NOT NULL DEFAULT now(),
    assigned_user_id bigint REFERENCES users(id),
    counted boolean NOT NULL DEFAULT false
);
CREATE INDEX IF NOT EXISTS otp_events_phone_idx ON otp_events(normalized_phone, received_at DESC);

ALTER TABLE assignment_numbers
    ADD CONSTRAINT assignment_numbers_otp_event_fk
    FOREIGN KEY (otp_event_id) REFERENCES otp_events(id);

CREATE TABLE IF NOT EXISTS otp_group_destinations (
    chat_id bigint PRIMARY KEY,
    title text NOT NULL DEFAULT '',
    buttons_enabled boolean NOT NULL DEFAULT true,
    enabled boolean NOT NULL DEFAULT true,
    healthy boolean NOT NULL DEFAULT true,
    last_error text NOT NULL DEFAULT '',
    last_success_at timestamptz,
    created_by bigint REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TYPE delivery_state AS ENUM ('pending', 'sending', 'sent', 'retry', 'failed');

CREATE TABLE IF NOT EXISTS delivery_jobs (
    id bigserial PRIMARY KEY,
    otp_event_id uuid NOT NULL REFERENCES otp_events(id) ON DELETE CASCADE,
    target_kind text NOT NULL CHECK (target_kind IN ('group', 'user')),
    target_id bigint NOT NULL,
    buttons_enabled boolean NOT NULL DEFAULT false,
    state delivery_state NOT NULL DEFAULT 'pending',
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    claimed_at timestamptz,
    last_error text NOT NULL DEFAULT '',
    sent_at timestamptz,
    UNIQUE(otp_event_id, target_kind, target_id)
);
CREATE INDEX IF NOT EXISTS delivery_jobs_pending_idx ON delivery_jobs(next_attempt_at, id)
    WHERE state IN ('pending', 'retry', 'sending');

CREATE TABLE IF NOT EXISTS user_daily_progress (
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    local_date date NOT NULL,
    otp_count integer NOT NULL DEFAULT 0,
    base_earnings_pkr numeric(18,4) NOT NULL DEFAULT 0,
    reward_earnings_pkr numeric(18,4) NOT NULL DEFAULT 0,
    PRIMARY KEY(user_id, local_date)
);

CREATE TABLE IF NOT EXISTS reward_schedules (
    id bigserial PRIMARY KEY,
    name text NOT NULL,
    user_id bigint REFERENCES users(id) ON DELETE CASCADE,
    enabled boolean NOT NULL DEFAULT true,
    effective_from date NOT NULL DEFAULT CURRENT_DATE,
    created_by bigint REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS one_enabled_global_reward_schedule
    ON reward_schedules((user_id IS NULL)) WHERE enabled AND user_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS one_enabled_user_reward_schedule
    ON reward_schedules(user_id) WHERE enabled AND user_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS reward_rules (
    id bigserial PRIMARY KEY,
    schedule_id bigint NOT NULL REFERENCES reward_schedules(id) ON DELETE CASCADE,
    threshold integer NOT NULL CHECK (threshold > 0),
    reward_pkr numeric(18,4) NOT NULL CHECK (reward_pkr > 0),
    UNIQUE(schedule_id, threshold)
);

CREATE TABLE IF NOT EXISTS reward_awards (
    id bigserial PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id),
    local_date date NOT NULL,
    rule_id bigint NOT NULL REFERENCES reward_rules(id),
    otp_count integer NOT NULL,
    amount_pkr numeric(18,4) NOT NULL,
    awarded_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(user_id, local_date, rule_id)
);

CREATE TABLE IF NOT EXISTS balance_ledger (
    id bigserial PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id),
    currency text NOT NULL CHECK (currency IN ('PKR', 'USD')),
    amount numeric(18,6) NOT NULL,
    entry_type text NOT NULL,
    reference_type text NOT NULL,
    reference_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(user_id, currency, entry_type, reference_type, reference_id)
);

CREATE TABLE IF NOT EXISTS panels (
    id bigserial PRIMARY KEY,
    name text NOT NULL UNIQUE,
    kind text NOT NULL CHECK (kind IN ('token_api', 'legacy_api', 'login', 'websocket')),
    config jsonb NOT NULL DEFAULT '{}'::jsonb,
    poll_interval interval NOT NULL DEFAULT interval '2 seconds',
    enabled boolean NOT NULL DEFAULT true,
    healthy boolean NOT NULL DEFAULT true,
    consecutive_failures integer NOT NULL DEFAULT 0,
    last_cursor text NOT NULL DEFAULT '',
    last_success_at timestamptz,
    last_error_at timestamptz,
    last_error text NOT NULL DEFAULT '',
    otp_count bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS panel_ingest_jobs (
    id bigserial PRIMARY KEY,
    panel_id bigint NOT NULL REFERENCES panels(id) ON DELETE CASCADE,
    dedup_key text NOT NULL,
    payload jsonb NOT NULL,
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','processing','retry','processed','failed')),
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    claimed_at timestamptz,
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz,
    UNIQUE(panel_id,dedup_key)
);
CREATE INDEX IF NOT EXISTS panel_ingest_pending_idx ON panel_ingest_jobs(next_attempt_at,id)
    WHERE state IN ('pending','processing','retry');

CREATE TABLE IF NOT EXISTS referrals (
    user_id bigint PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    referrer_id bigint REFERENCES users(id),
    qualified boolean NOT NULL DEFAULT false,
    reward_pkr numeric(18,4) NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TYPE withdrawal_state AS ENUM ('pending', 'approved', 'rejected', 'paid');
CREATE TABLE IF NOT EXISTS withdrawals (
    id bigserial PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id),
    method text NOT NULL,
    amount_pkr numeric(18,4) NOT NULL DEFAULT 0,
    amount_usd numeric(18,6) NOT NULL DEFAULT 0,
    details text NOT NULL,
    state withdrawal_state NOT NULL DEFAULT 'pending',
    created_at timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz,
    resolved_by bigint REFERENCES users(id)
);

CREATE TABLE IF NOT EXISTS settings (
    key text PRIMARY KEY,
    value jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS required_chats (
    chat_id bigint PRIMARY KEY,
    title text NOT NULL,
    invite_url text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tutorials (
    id bigserial PRIMARY KEY,
    title text NOT NULL,
    body text NOT NULL,
    media_file_id text NOT NULL DEFAULT '',
    enabled boolean NOT NULL DEFAULT true,
    created_by bigint REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now()
);
