CREATE TABLE number_import_jobs (
    id bigserial PRIMARY KEY,
    bot_instance_id bigint NOT NULL REFERENCES bot_instances(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id),
    nonce text NOT NULL,
    file_name text NOT NULL,
    input_config jsonb,
    settings jsonb NOT NULL DEFAULT '{}',
    result jsonb NOT NULL DEFAULT '[]',
    valid_count integer NOT NULL DEFAULT 0,
    invalid_count integer NOT NULL DEFAULT 0,
    duplicate_count integer NOT NULL DEFAULT 0,
    state text NOT NULL DEFAULT 'draft' CHECK (state IN ('draft','queued','running','succeeded','failed','cancelled')),
    error_code text NOT NULL DEFAULT '',
    attempts integer NOT NULL DEFAULT 0,
    lease_token text NOT NULL DEFAULT '',
    lease_until timestamptz,
    chat_id bigint NOT NULL,
    message_id integer NOT NULL DEFAULT 0,
    notified_at timestamptz,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(bot_instance_id,user_id,nonce)
);
CREATE INDEX number_import_queue_idx ON number_import_jobs(bot_instance_id,state,id);
CREATE INDEX number_import_owner_idx ON number_import_jobs(bot_instance_id,user_id,id DESC);
CREATE INDEX number_import_retention_idx ON number_import_jobs(expires_at) WHERE input_config IS NOT NULL;
