CREATE TABLE users (
    id UUID PRIMARY KEY,
    email TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT users_email_lowercase CHECK (email = lower(email))
);

CREATE UNIQUE INDEX users_email_unique ON users (lower(email));

CREATE TABLE devices (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX devices_user_id_idx ON devices (user_id);

CREATE TABLE refresh_tokens (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    token_hash BYTEA NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ
);

CREATE INDEX refresh_tokens_active_idx
    ON refresh_tokens (user_id, device_id, expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE projects (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    color_value BIGINT NOT NULL,
    type TEXT NOT NULL,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ,
    PRIMARY KEY (user_id, id)
);

CREATE INDEX projects_user_updated_idx ON projects (user_id, updated_at DESC);

CREATE TABLE tasks (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    handle TEXT NOT NULL,
    project_id TEXT NOT NULL,
    title TEXT NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    board_status_id TEXT NOT NULL,
    urgency TEXT NOT NULL,
    scheduled_date TIMESTAMPTZ NOT NULL,
    start_time TIMESTAMPTZ,
    end_time TIMESTAMPTZ,
    has_time_range BOOLEAN NOT NULL DEFAULT false,
    is_favorite BOOLEAN NOT NULL DEFAULT false,
    progress INTEGER NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    estimated_minutes INTEGER CHECK (estimated_minutes IS NULL OR estimated_minutes >= 0),
    actual_minutes INTEGER CHECK (actual_minutes IS NULL OR actual_minutes >= 0),
    board_order TEXT NOT NULL,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,
    PRIMARY KEY (user_id, id),
    CONSTRAINT tasks_project_fk FOREIGN KEY (user_id, project_id)
        REFERENCES projects(user_id, id) DEFERRABLE INITIALLY DEFERRED
);

CREATE UNIQUE INDEX tasks_user_handle_unique ON tasks (user_id, handle);
CREATE INDEX tasks_project_idx ON tasks (user_id, project_id, deleted_at);
CREATE INDEX tasks_schedule_idx ON tasks (user_id, scheduled_date, deleted_at);

CREATE TABLE board_columns (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL,
    id TEXT NOT NULL,
    label TEXT NOT NULL,
    linked_status TEXT,
    sort_order INTEGER NOT NULL,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ,
    PRIMARY KEY (user_id, project_id, id),
    CONSTRAINT board_columns_project_fk FOREIGN KEY (user_id, project_id)
        REFERENCES projects(user_id, id) DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX board_columns_order_idx
    ON board_columns (user_id, project_id, sort_order)
    WHERE deleted_at IS NULL;

CREATE TABLE changes (
    revision BIGSERIAL PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    entity_type TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    operation_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    changed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX changes_user_revision_idx ON changes (user_id, revision);

CREATE TABLE processed_mutations (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    mutation_id TEXT NOT NULL,
    result JSONB NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, mutation_id)
);

CREATE INDEX processed_mutations_processed_idx ON processed_mutations (processed_at);
