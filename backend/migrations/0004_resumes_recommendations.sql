CREATE TABLE private_tasks (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), owner_user_id uuid NOT NULL REFERENCES users(id),
    actor_user_id uuid NOT NULL REFERENCES users(id), auth_version bigint NOT NULL, owner_auth_version bigint NOT NULL,
    kind text NOT NULL CHECK(kind IN ('extract','parse','recommend')),
    resource_id uuid NOT NULL, resource_revision bigint NOT NULL DEFAULT 1,
    state text NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','running','succeeded','failed','cancelled')),
    attempts integer NOT NULL DEFAULT 0, max_attempts integer NOT NULL DEFAULT 3,
    available_at timestamptz NOT NULL, lease_until timestamptz, lease_token text NOT NULL DEFAULT '',
    error_code text NOT NULL DEFAULT '', created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL
);
CREATE INDEX private_tasks_pending ON private_tasks(available_at,created_at) WHERE state IN ('queued','running');
CREATE INDEX private_tasks_owner ON private_tasks(owner_user_id,created_at DESC);

CREATE TABLE resumes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), owner_user_id uuid NOT NULL REFERENCES users(id),
    filename text NOT NULL, storage_key text NOT NULL UNIQUE, content_sha256 text NOT NULL, size_bytes integer NOT NULL CHECK(size_bytes BETWEEN 1 AND 10485760),
    sensitive_names jsonb NOT NULL DEFAULT '[]', status text NOT NULL DEFAULT 'queued', error_code text NOT NULL DEFAULT '',
    redacted_text text NOT NULL DEFAULT '', draft jsonb NOT NULL DEFAULT '{}', cloud_approved boolean NOT NULL DEFAULT false,
    revision bigint NOT NULL DEFAULT 1, last_task_id uuid REFERENCES private_tasks(id),
    created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, deleted_at timestamptz
);
CREATE INDEX resumes_owner ON resumes(owner_user_id,created_at DESC) WHERE deleted_at IS NULL;

CREATE TABLE profile_versions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), owner_user_id uuid NOT NULL REFERENCES users(id),
    resume_id uuid NOT NULL REFERENCES resumes(id) ON DELETE CASCADE,
    candidate jsonb NOT NULL, redacted_text text NOT NULL, cloud_approved boolean NOT NULL,
    created_at timestamptz NOT NULL
);
CREATE INDEX profile_versions_owner ON profile_versions(owner_user_id,created_at DESC);
CREATE TABLE current_profiles (
    owner_user_id uuid PRIMARY KEY REFERENCES users(id), profile_version_id uuid REFERENCES profile_versions(id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL
);
ALTER TABLE applications ADD CONSTRAINT applications_resume_version_fk FOREIGN KEY(resume_version_id) REFERENCES profile_versions(id) ON DELETE SET NULL;

CREATE TABLE recommendations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), owner_user_id uuid NOT NULL REFERENCES users(id),
    profile_version_id uuid NOT NULL REFERENCES profile_versions(id) ON DELETE CASCADE,
    job_id uuid NOT NULL REFERENCES jobs(id), job_version bigint NOT NULL,
    variant_key text NOT NULL, variant_hash text NOT NULL, rule_version text NOT NULL, model_version text NOT NULL,
    eligibility jsonb NOT NULL DEFAULT '{}', semantic jsonb NOT NULL DEFAULT '{}',
    semantic_status text NOT NULL DEFAULT 'pending', error_code text NOT NULL DEFAULT '',
    last_task_id uuid REFERENCES private_tasks(id), created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
    UNIQUE(owner_user_id,profile_version_id,job_id,job_version,variant_key,variant_hash,rule_version,model_version)
);
CREATE INDEX recommendations_owner ON recommendations(owner_user_id,created_at DESC);
CREATE TABLE private_file_deletions (storage_key text PRIMARY KEY, created_at timestamptz NOT NULL);
