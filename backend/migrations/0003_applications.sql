CREATE TABLE favorites (
    owner_user_id uuid NOT NULL REFERENCES users(id), job_id uuid NOT NULL REFERENCES jobs(id),
    created_at timestamptz NOT NULL, PRIMARY KEY(owner_user_id,job_id)
);
CREATE TABLE applications (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), owner_user_id uuid NOT NULL REFERENCES users(id),
    job_id uuid NOT NULL REFERENCES jobs(id),
    status text NOT NULL CHECK(status IN ('draft','submitted','written_test','interview','offer','rejected','withdrawn')),
    notes text NOT NULL DEFAULT '', next_action text NOT NULL DEFAULT '', next_action_at timestamptz,
    resume_version_id uuid, version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
    UNIQUE(owner_user_id,job_id)
);
CREATE INDEX applications_owner ON applications(owner_user_id,updated_at DESC);
CREATE TABLE application_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), application_id uuid NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    actor_user_id uuid NOT NULL REFERENCES users(id), status text NOT NULL,
    notes text NOT NULL, next_action text NOT NULL, next_action_at timestamptz,
    version bigint NOT NULL, created_at timestamptz NOT NULL,
    UNIQUE(application_id,version)
);
