ALTER TABLE panel_sources ADD COLUMN origin_source_id bigint REFERENCES panel_sources(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX panel_source_origin_unique ON panel_sources(bot_instance_id,origin_source_id) WHERE origin_source_id IS NOT NULL;
ALTER TABLE bot_instances ADD COLUMN share_main_otps boolean NOT NULL DEFAULT false;
ALTER TABLE bot_instances ADD COLUMN otp_share_enabled_at timestamptz;
ALTER TABLE otp_events ADD COLUMN shared_from_event_id uuid REFERENCES otp_events(id);
ALTER TABLE panel_ingest_jobs ALTER COLUMN panel_id DROP NOT NULL;
ALTER TABLE panel_ingest_jobs ADD COLUMN shared_from_event_id uuid REFERENCES otp_events(id);
CREATE UNIQUE INDEX shared_ingest_unique ON panel_ingest_jobs(bot_instance_id,shared_from_event_id) WHERE shared_from_event_id IS NOT NULL;

ALTER TABLE broadcasts DROP CONSTRAINT broadcasts_state_check;
ALTER TABLE broadcasts ADD CONSTRAINT broadcasts_state_check CHECK(state IN ('preparing','pending','running','completed','cancelled','failed'));
ALTER TABLE broadcasts ADD COLUMN prepare_cursor bigint NOT NULL DEFAULT 0;
ALTER TABLE broadcasts ADD COLUMN prepare_before timestamptz NOT NULL DEFAULT now();
ALTER TABLE broadcasts ADD COLUMN prepare_error text NOT NULL DEFAULT '';

CREATE TABLE user_backup_jobs (
 id bigserial PRIMARY KEY,
 actor_id bigint NOT NULL REFERENCES users(id),
 chat_id bigint NOT NULL,
 kind text NOT NULL CHECK(kind IN ('export','restore')),
 state text NOT NULL CHECK(state IN ('uploading','queued','processing','preview','sending','succeeded','failed','uncertain','cancelled')),
 backup_id text NOT NULL DEFAULT '',
 phase text NOT NULL DEFAULT 'inspect',
 summary jsonb NOT NULL DEFAULT '{}',
 claimed_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz NOT NULL DEFAULT now()+interval '1 day',
 error_code text NOT NULL DEFAULT ''
);
CREATE INDEX user_backup_jobs_pending_idx ON user_backup_jobs(id) WHERE state='queued';
CREATE TABLE user_backup_parts (
 job_id bigint NOT NULL REFERENCES user_backup_jobs(id) ON DELETE CASCADE,
 part integer NOT NULL,
 total integer NOT NULL,
 data bytea NOT NULL,
 PRIMARY KEY(job_id,part)
);
CREATE TABLE user_backup_restores (
 backup_id text PRIMARY KEY,
 actor_id bigint NOT NULL REFERENCES users(id),
 summary jsonb NOT NULL,
 restored_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE user_backup_jobs ADD COLUMN request_key text NOT NULL;
CREATE UNIQUE INDEX user_backup_request_unique ON user_backup_jobs(actor_id,kind,request_key);
