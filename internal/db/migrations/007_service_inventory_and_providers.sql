-- Keep number IDs and their assignment/ledger references intact.
ALTER TABLE panels DROP CONSTRAINT IF EXISTS panels_kind_check;
ALTER TABLE panels ADD CONSTRAINT panels_kind_check CHECK(kind IN ('token_api','legacy_api','login','websocket','ivas','socketio','axon_asp','augestel'));
ALTER TABLE panel_sources DROP CONSTRAINT IF EXISTS panel_sources_kind_check;
ALTER TABLE panel_sources ADD CONSTRAINT panel_sources_kind_check CHECK(kind IN ('token_api','legacy_api','login','websocket','ivas','socketio','axon_asp','augestel'));
ALTER TABLE numbers DROP CONSTRAINT IF EXISTS numbers_phone_key;
ALTER TABLE numbers DROP CONSTRAINT IF EXISTS numbers_normalized_phone_key;
ALTER TABLE numbers ADD COLUMN service_key text GENERATED ALWAYS AS (lower(btrim(service))) STORED;
CREATE UNIQUE INDEX numbers_phone_service_unique ON numbers(normalized_phone,service_key);

ALTER TABLE otp_events ADD COLUMN provider_record_id text NOT NULL DEFAULT '';
ALTER TABLE otp_events ADD COLUMN sender text NOT NULL DEFAULT '';
ALTER TABLE otp_events ADD COLUMN delivery_status text NOT NULL DEFAULT '';
ALTER TABLE otp_events ADD COLUMN provider_range text NOT NULL DEFAULT '';
ALTER TABLE otp_events ADD COLUMN provider_profit numeric(20,6);
ALTER TABLE otp_events ADD COLUMN provider_currency text NOT NULL DEFAULT '';
CREATE INDEX otp_activity_event_time_idx ON otp_events(bot_instance_id,(COALESCE(provider_timestamp,received_at)) DESC) WHERE code<>'';

ALTER TABLE panels ADD COLUMN last_sms_at timestamptz;
ALTER TABLE panels ADD COLUMN next_retry_at timestamptz;
ALTER TABLE panels ADD COLUMN backlog_pages integer NOT NULL DEFAULT 0;
ALTER TABLE panels ADD COLUMN connection_status text NOT NULL DEFAULT '';
CREATE TABLE panel_service_mappings (
 id bigserial PRIMARY KEY,
 panel_id bigint NOT NULL REFERENCES panels(id) ON DELETE CASCADE,
 match_kind text NOT NULL CHECK(match_kind IN ('service','sender','message','range')),
 match_value text NOT NULL,
 service text NOT NULL,
 country text NOT NULL DEFAULT '',
 UNIQUE(panel_id,match_kind,match_value)
);
CREATE TABLE panel_unmapped_sms (
 id bigserial PRIMARY KEY,
 panel_id bigint NOT NULL REFERENCES panels(id) ON DELETE CASCADE,
 dedup_key text NOT NULL,
 payload jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(panel_id,dedup_key)
);
CREATE TABLE provider_request_budgets (
 key_hash text PRIMARY KEY,
 requests timestamptz[] NOT NULL DEFAULT '{}',
 paused_until timestamptz,
 request_limit integer NOT NULL DEFAULT 5 CHECK(request_limit>0),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE panel_connection_tests (
 panel_id bigint PRIMARY KEY REFERENCES panels(id) ON DELETE CASCADE,
 next_attempt_at timestamptz NOT NULL,
 enable_after boolean NOT NULL DEFAULT false,
 config_fingerprint text NOT NULL
);
CREATE TABLE provider_response_cache (
 panel_id bigint NOT NULL REFERENCES panels(id) ON DELETE CASCADE,
 cache_key text NOT NULL,
 payload jsonb NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(panel_id,cache_key)
);
ALTER TABLE user_preferences ADD COLUMN availability_notifications boolean NOT NULL DEFAULT true;
ALTER TABLE user_preferences ADD COLUMN quiet_start integer CHECK(quiet_start BETWEEN 0 AND 23);
ALTER TABLE user_preferences ADD COLUMN quiet_end integer CHECK(quiet_end BETWEEN 0 AND 23);
