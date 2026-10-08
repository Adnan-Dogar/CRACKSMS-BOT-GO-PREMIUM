CREATE TABLE panel_sources (
 id bigserial PRIMARY KEY,
 bot_instance_id bigint NOT NULL REFERENCES bot_instances(id),
 name text NOT NULL,
 kind text NOT NULL,
 url text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(bot_instance_id,name)
);
ALTER TABLE panels ADD COLUMN source_id bigint REFERENCES panel_sources(id);
ALTER TABLE panels ADD COLUMN account_label text NOT NULL DEFAULT 'Default account';
INSERT INTO panel_sources(bot_instance_id,name,kind)
 SELECT bot_instance_id,name,kind FROM panels;
UPDATE panels p SET source_id=s.id FROM panel_sources s
 WHERE s.bot_instance_id=p.bot_instance_id AND s.name=p.name;
CREATE INDEX panels_source_idx ON panels(source_id);

CREATE TABLE broadcasts (
 id bigserial PRIMARY KEY,
 bot_instance_id bigint NOT NULL REFERENCES bot_instances(id),
 creator_id bigint NOT NULL REFERENCES users(id),
 confirmation_key text NOT NULL,
 kind text NOT NULL CHECK(kind IN ('text','photo','video')),
 body text NOT NULL DEFAULT '',
 entities jsonb NOT NULL DEFAULT '[]',
 file_id text NOT NULL DEFAULT '',
 audience text NOT NULL,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','running','completed','cancelled')),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(bot_instance_id,creator_id,confirmation_key)
);
CREATE TABLE broadcast_recipients (
 broadcast_id bigint NOT NULL REFERENCES broadcasts(id) ON DELETE CASCADE,
 user_id bigint NOT NULL REFERENCES users(id),
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','sending','sent','failed','skipped','uncertain')),
 attempts integer NOT NULL DEFAULT 0,
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 claimed_at timestamptz,
 last_error text NOT NULL DEFAULT '',
 PRIMARY KEY(broadcast_id,user_id)
);
CREATE INDEX broadcast_pending_idx ON broadcast_recipients(next_attempt_at,broadcast_id) WHERE state='pending';

ALTER TABLE panels DROP CONSTRAINT panels_kind_check;
ALTER TABLE panels ADD CONSTRAINT panels_kind_check CHECK(kind IN ('token_api','legacy_api','login','websocket','ivas'));
