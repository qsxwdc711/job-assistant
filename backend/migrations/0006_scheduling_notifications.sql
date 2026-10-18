CREATE TABLE scheduled_collections (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), schedule_key text NOT NULL UNIQUE,
    source_code text NOT NULL REFERENCES sources(code), domain text NOT NULL,
    state text NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','running','succeeded','failed','cancelled')),
    attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 3), available_at timestamptz NOT NULL,
    lease_until timestamptz, lease_token text NOT NULL DEFAULT '',
    error_code text NOT NULL DEFAULT '', fetched integer NOT NULL DEFAULT 0, published integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL
);
CREATE INDEX collection_pending ON scheduled_collections(available_at) WHERE state IN ('queued','running');

CREATE TABLE subscriptions (
    owner_user_id uuid PRIMARY KEY REFERENCES users(id), filters_query text NOT NULL DEFAULT '',
    mode text NOT NULL DEFAULT 'filtered' CHECK(mode IN ('filtered','personalized')),
    enabled boolean NOT NULL DEFAULT false, revision bigint NOT NULL DEFAULT 1,
    last_cutoff timestamptz, created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL
);
CREATE TABLE notification_channels (
    owner_user_id uuid PRIMARY KEY REFERENCES users(id), encrypted_config bytea NOT NULL,
    has_secret boolean NOT NULL, enabled boolean NOT NULL DEFAULT false, revision bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL
);
CREATE TABLE notification_batches (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), owner_user_id uuid NOT NULL REFERENCES users(id),
    batch_key text NOT NULL, cutoff timestamptz NOT NULL, kind text NOT NULL CHECK(kind IN ('digest','test')),
    mode text NOT NULL, subscription_revision bigint NOT NULL, channel_revision bigint NOT NULL,
    profile_version_id uuid REFERENCES profile_versions(id) ON DELETE SET NULL,
    state text NOT NULL CHECK(state IN ('queued','empty','station_only','cancelled')),
    items jsonb NOT NULL DEFAULT '[]', sources jsonb NOT NULL DEFAULT '{}', created_at timestamptz NOT NULL,
    UNIQUE(owner_user_id,batch_key,channel_revision)
);
CREATE INDEX notification_batches_owner ON notification_batches(owner_user_id,created_at DESC);
CREATE TABLE notification_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), owner_user_id uuid NOT NULL REFERENCES users(id),
    job_id uuid NOT NULL REFERENCES jobs(id), job_version bigint NOT NULL,
    event_key text NOT NULL, kind text NOT NULL, priority integer NOT NULL,
    batch_id uuid REFERENCES notification_batches(id), created_at timestamptz NOT NULL,
    UNIQUE(owner_user_id,event_key)
);
CREATE TABLE notification_deliveries (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), batch_id uuid NOT NULL UNIQUE REFERENCES notification_batches(id),
    state text NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','sending','sent','failed','delivery_unknown','cancelled','simulated')),
    attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 3), available_at timestamptz NOT NULL, sending_at timestamptz,
    error_code text NOT NULL DEFAULT '', payload_bytes integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL
);
CREATE INDEX delivery_pending ON notification_deliveries(available_at) WHERE state='queued';
CREATE TABLE digest_requests (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), actor_user_id uuid NOT NULL REFERENCES users(id),
    state text NOT NULL DEFAULT 'queued', lease_until timestamptz, created_at timestamptz NOT NULL,
    error_code text NOT NULL DEFAULT ''
);
