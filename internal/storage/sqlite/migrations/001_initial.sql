CREATE TABLE providers (
    id TEXT PRIMARY KEY NOT NULL,
    type TEXT NOT NULL CHECK (length(trim(type)) > 0),
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    configuration TEXT,
    sync_state TEXT NOT NULL DEFAULT 'local'
        CHECK (sync_state IN ('synced', 'pending', 'syncing', 'conflict', 'failed', 'local')),
    sync_cursor TEXT,
    sync_error TEXT,
    last_sync_at TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (id)
);

CREATE TABLE spaces (
    id TEXT PRIMARY KEY NOT NULL,
    provider_id TEXT NOT NULL,
    remote_id TEXT,
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    sync_state TEXT NOT NULL DEFAULT 'local'
        CHECK (sync_state IN ('synced', 'pending', 'syncing', 'conflict', 'failed', 'local')),
    remote_updated_at TEXT,
    is_deleted INTEGER NOT NULL DEFAULT 0 CHECK (is_deleted IN (0, 1)),
    deleted_at TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    FOREIGN KEY (provider_id) REFERENCES providers (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    UNIQUE (provider_id, id)
);

CREATE TABLE lists (
    id TEXT PRIMARY KEY NOT NULL,
    provider_id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    remote_id TEXT,
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    sync_state TEXT NOT NULL DEFAULT 'local'
        CHECK (sync_state IN ('synced', 'pending', 'syncing', 'conflict', 'failed', 'local')),
    remote_updated_at TEXT,
    is_deleted INTEGER NOT NULL DEFAULT 0 CHECK (is_deleted IN (0, 1)),
    deleted_at TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    FOREIGN KEY (provider_id) REFERENCES providers (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    FOREIGN KEY (provider_id, space_id) REFERENCES spaces (provider_id, id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    UNIQUE (provider_id, id)
);

CREATE TABLE tasks (
    id TEXT PRIMARY KEY NOT NULL,
    provider_id TEXT NOT NULL,
    list_id TEXT NOT NULL,
    remote_id TEXT,
    parent_task_id TEXT,
    title TEXT NOT NULL CHECK (length(trim(title)) > 0),
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (length(trim(status)) > 0),
    priority TEXT NOT NULL DEFAULT 'normal' CHECK (length(trim(priority)) > 0),
    due_at TEXT,
    completed_at TEXT,
    sync_state TEXT NOT NULL DEFAULT 'local'
        CHECK (sync_state IN ('synced', 'pending', 'syncing', 'conflict', 'failed', 'local')),
    remote_updated_at TEXT,
    is_deleted INTEGER NOT NULL DEFAULT 0 CHECK (is_deleted IN (0, 1)),
    deleted_at TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    CHECK (parent_task_id IS NULL OR parent_task_id <> id),
    FOREIGN KEY (provider_id) REFERENCES providers (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    FOREIGN KEY (provider_id, list_id) REFERENCES lists (provider_id, id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    FOREIGN KEY (provider_id, parent_task_id) REFERENCES tasks (provider_id, id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    UNIQUE (provider_id, id)
);

CREATE TABLE provider_metadata (
    provider_id TEXT NOT NULL,
    entity_type TEXT NOT NULL CHECK (entity_type IN ('provider', 'space', 'list', 'task')),
    entity_id TEXT NOT NULL,
    key TEXT NOT NULL CHECK (length(trim(key)) > 0),
    value TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (provider_id, entity_type, entity_id, key),
    FOREIGN KEY (provider_id) REFERENCES providers (id) ON UPDATE RESTRICT ON DELETE RESTRICT
);

CREATE TABLE sync_operations (
    id TEXT PRIMARY KEY NOT NULL,
    provider_id TEXT NOT NULL,
    entity_type TEXT NOT NULL CHECK (entity_type IN ('provider', 'space', 'list', 'task')),
    entity_id TEXT NOT NULL,
    operation TEXT NOT NULL CHECK (operation IN ('create', 'update', 'delete')),
    payload TEXT,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'syncing', 'failed', 'completed')),
    error TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_attempt_at TEXT,
    next_attempt_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    lease_owner TEXT,
    lease_expires_at TEXT,
    completed_at TEXT,
    FOREIGN KEY (provider_id) REFERENCES providers (id) ON UPDATE RESTRICT ON DELETE RESTRICT
);

CREATE TABLE sync_bases (
    provider_id TEXT NOT NULL,
    entity_type TEXT NOT NULL CHECK (entity_type IN ('provider', 'space', 'list', 'task')),
    entity_id TEXT NOT NULL,
    remote_id TEXT,
    sync_state TEXT NOT NULL DEFAULT 'local'
        CHECK (sync_state IN ('synced', 'pending', 'syncing', 'conflict', 'failed', 'local')),
    remote_updated_at TEXT,
    payload TEXT NOT NULL,
    remote_version TEXT,
    captured_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (provider_id, entity_type, entity_id),
    FOREIGN KEY (provider_id) REFERENCES providers (id) ON UPDATE RESTRICT ON DELETE RESTRICT
);

CREATE TABLE conflicts (
    id TEXT PRIMARY KEY NOT NULL,
    provider_id TEXT NOT NULL,
    entity_type TEXT NOT NULL CHECK (entity_type IN ('provider', 'space', 'list', 'task')),
    entity_id TEXT NOT NULL,
    base_value TEXT,
    local_value TEXT NOT NULL,
    remote_value TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved', 'dismissed')),
    resolution TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    resolved_at TEXT,
    FOREIGN KEY (provider_id) REFERENCES providers (id) ON UPDATE RESTRICT ON DELETE RESTRICT
);

CREATE TABLE app_state (
    key TEXT PRIMARY KEY NOT NULL CHECK (length(trim(key)) > 0),
    value TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE UNIQUE INDEX spaces_provider_remote_id
    ON spaces (provider_id, remote_id) WHERE remote_id IS NOT NULL;
CREATE UNIQUE INDEX lists_provider_remote_id
    ON lists (provider_id, remote_id) WHERE remote_id IS NOT NULL;
CREATE UNIQUE INDEX tasks_provider_remote_id
    ON tasks (provider_id, remote_id) WHERE remote_id IS NOT NULL;

CREATE INDEX spaces_provider_idx ON spaces (provider_id, is_deleted, updated_at);
CREATE INDEX lists_provider_space_idx ON lists (provider_id, space_id, is_deleted, updated_at);
CREATE INDEX tasks_provider_list_idx ON tasks (provider_id, list_id, is_deleted, updated_at);
CREATE INDEX tasks_provider_parent_idx ON tasks (provider_id, parent_task_id, is_deleted);
CREATE INDEX tasks_status_idx ON tasks (provider_id, status, is_deleted);
CREATE INDEX tasks_priority_idx ON tasks (provider_id, priority, is_deleted);
CREATE INDEX tasks_due_idx ON tasks (provider_id, due_at, is_deleted);
CREATE INDEX tasks_sync_state_idx ON tasks (provider_id, sync_state, is_deleted);
CREATE INDEX sync_operations_claim_idx
    ON sync_operations (provider_id, status, next_attempt_at, created_at, id);
CREATE INDEX sync_operations_entity_idx
    ON sync_operations (provider_id, entity_type, entity_id, created_at, id);
CREATE INDEX conflicts_entity_idx ON conflicts (provider_id, entity_type, entity_id, status);

CREATE TRIGGER spaces_provider_immutable
BEFORE UPDATE OF provider_id ON spaces
WHEN OLD.provider_id <> NEW.provider_id
BEGIN
    SELECT RAISE(ABORT, 'space provider_id is immutable');
END;

CREATE TRIGGER lists_provider_immutable
BEFORE UPDATE OF provider_id ON lists
WHEN OLD.provider_id <> NEW.provider_id
BEGIN
    SELECT RAISE(ABORT, 'list provider_id is immutable');
END;

CREATE TRIGGER tasks_provider_immutable
BEFORE UPDATE OF provider_id ON tasks
WHEN OLD.provider_id <> NEW.provider_id
BEGIN
    SELECT RAISE(ABORT, 'task provider_id is immutable');
END;

CREATE TRIGGER provider_metadata_validate_insert
BEFORE INSERT ON provider_metadata
BEGIN
    SELECT CASE
        WHEN NEW.entity_type = 'provider' AND NOT EXISTS (
            SELECT 1 FROM providers WHERE id = NEW.entity_id AND id = NEW.provider_id
        ) THEN RAISE(ABORT, 'provider metadata entity/provider mismatch')
        WHEN NEW.entity_type = 'space' AND NOT EXISTS (
            SELECT 1 FROM spaces WHERE id = NEW.entity_id AND provider_id = NEW.provider_id
        ) THEN RAISE(ABORT, 'space metadata entity/provider mismatch')
        WHEN NEW.entity_type = 'list' AND NOT EXISTS (
            SELECT 1 FROM lists WHERE id = NEW.entity_id AND provider_id = NEW.provider_id
        ) THEN RAISE(ABORT, 'list metadata entity/provider mismatch')
        WHEN NEW.entity_type = 'task' AND NOT EXISTS (
            SELECT 1 FROM tasks WHERE id = NEW.entity_id AND provider_id = NEW.provider_id
        ) THEN RAISE(ABORT, 'task metadata entity/provider mismatch')
    END;
END;

CREATE TRIGGER provider_metadata_validate_update
BEFORE UPDATE OF provider_id, entity_type, entity_id ON provider_metadata
BEGIN
    SELECT CASE
        WHEN NEW.entity_type = 'provider' AND NOT EXISTS (
            SELECT 1 FROM providers WHERE id = NEW.entity_id AND id = NEW.provider_id
        ) THEN RAISE(ABORT, 'provider metadata entity/provider mismatch')
        WHEN NEW.entity_type = 'space' AND NOT EXISTS (
            SELECT 1 FROM spaces WHERE id = NEW.entity_id AND provider_id = NEW.provider_id
        ) THEN RAISE(ABORT, 'space metadata entity/provider mismatch')
        WHEN NEW.entity_type = 'list' AND NOT EXISTS (
            SELECT 1 FROM lists WHERE id = NEW.entity_id AND provider_id = NEW.provider_id
        ) THEN RAISE(ABORT, 'list metadata entity/provider mismatch')
        WHEN NEW.entity_type = 'task' AND NOT EXISTS (
            SELECT 1 FROM tasks WHERE id = NEW.entity_id AND provider_id = NEW.provider_id
        ) THEN RAISE(ABORT, 'task metadata entity/provider mismatch')
    END;
END;

CREATE TRIGGER sync_operations_validate_insert
BEFORE INSERT ON sync_operations
BEGIN
    SELECT CASE
        WHEN NEW.entity_type = 'provider' AND NOT EXISTS (
            SELECT 1 FROM providers WHERE id = NEW.entity_id AND id = NEW.provider_id
        ) THEN RAISE(ABORT, 'sync operation entity/provider mismatch')
        WHEN NEW.entity_type = 'space' AND NOT EXISTS (
            SELECT 1 FROM spaces WHERE id = NEW.entity_id AND provider_id = NEW.provider_id
        ) THEN RAISE(ABORT, 'sync operation entity/provider mismatch')
        WHEN NEW.entity_type = 'list' AND NOT EXISTS (
            SELECT 1 FROM lists WHERE id = NEW.entity_id AND provider_id = NEW.provider_id
        ) THEN RAISE(ABORT, 'sync operation entity/provider mismatch')
        WHEN NEW.entity_type = 'task' AND NOT EXISTS (
            SELECT 1 FROM tasks WHERE id = NEW.entity_id AND provider_id = NEW.provider_id
        ) THEN RAISE(ABORT, 'sync operation entity/provider mismatch')
    END;
END;

CREATE TRIGGER sync_operations_validate_update
BEFORE UPDATE OF provider_id, entity_type, entity_id ON sync_operations
BEGIN
    SELECT CASE
        WHEN NEW.entity_type = 'provider' AND NOT EXISTS (
            SELECT 1 FROM providers WHERE id = NEW.entity_id AND id = NEW.provider_id
        ) THEN RAISE(ABORT, 'sync operation entity/provider mismatch')
        WHEN NEW.entity_type = 'space' AND NOT EXISTS (
            SELECT 1 FROM spaces WHERE id = NEW.entity_id AND provider_id = NEW.provider_id
        ) THEN RAISE(ABORT, 'sync operation entity/provider mismatch')
        WHEN NEW.entity_type = 'list' AND NOT EXISTS (
            SELECT 1 FROM lists WHERE id = NEW.entity_id AND provider_id = NEW.provider_id
        ) THEN RAISE(ABORT, 'sync operation entity/provider mismatch')
        WHEN NEW.entity_type = 'task' AND NOT EXISTS (
            SELECT 1 FROM tasks WHERE id = NEW.entity_id AND provider_id = NEW.provider_id
        ) THEN RAISE(ABORT, 'sync operation entity/provider mismatch')
    END;
END;

CREATE TRIGGER sync_bases_validate_insert
BEFORE INSERT ON sync_bases
BEGIN
    SELECT CASE
        WHEN NEW.entity_type = 'provider' AND NOT EXISTS (
            SELECT 1 FROM providers WHERE id = NEW.entity_id AND id = NEW.provider_id
        ) THEN RAISE(ABORT, 'sync base entity/provider mismatch')
        WHEN NEW.entity_type = 'space' AND NOT EXISTS (
            SELECT 1 FROM spaces WHERE id = NEW.entity_id AND provider_id = NEW.provider_id
        ) THEN RAISE(ABORT, 'sync base entity/provider mismatch')
        WHEN NEW.entity_type = 'list' AND NOT EXISTS (
            SELECT 1 FROM lists WHERE id = NEW.entity_id AND provider_id = NEW.provider_id
        ) THEN RAISE(ABORT, 'sync base entity/provider mismatch')
        WHEN NEW.entity_type = 'task' AND NOT EXISTS (
            SELECT 1 FROM tasks WHERE id = NEW.entity_id AND provider_id = NEW.provider_id
        ) THEN RAISE(ABORT, 'sync base entity/provider mismatch')
    END;
END;

CREATE TRIGGER sync_bases_validate_update
BEFORE UPDATE OF provider_id, entity_type, entity_id ON sync_bases
BEGIN
    SELECT CASE
        WHEN NEW.entity_type = 'provider' AND NOT EXISTS (
            SELECT 1 FROM providers WHERE id = NEW.entity_id AND id = NEW.provider_id
        ) THEN RAISE(ABORT, 'sync base entity/provider mismatch')
        WHEN NEW.entity_type = 'space' AND NOT EXISTS (
            SELECT 1 FROM spaces WHERE id = NEW.entity_id AND provider_id = NEW.provider_id
        ) THEN RAISE(ABORT, 'sync base entity/provider mismatch')
        WHEN NEW.entity_type = 'list' AND NOT EXISTS (
            SELECT 1 FROM lists WHERE id = NEW.entity_id AND provider_id = NEW.provider_id
        ) THEN RAISE(ABORT, 'sync base entity/provider mismatch')
        WHEN NEW.entity_type = 'task' AND NOT EXISTS (
            SELECT 1 FROM tasks WHERE id = NEW.entity_id AND provider_id = NEW.provider_id
        ) THEN RAISE(ABORT, 'sync base entity/provider mismatch')
    END;
END;

CREATE TRIGGER conflicts_validate_insert
BEFORE INSERT ON conflicts
BEGIN
    SELECT CASE
        WHEN NEW.entity_type = 'provider' AND NOT EXISTS (
            SELECT 1 FROM providers WHERE id = NEW.entity_id AND id = NEW.provider_id
        ) THEN RAISE(ABORT, 'conflict entity/provider mismatch')
        WHEN NEW.entity_type = 'space' AND NOT EXISTS (
            SELECT 1 FROM spaces WHERE id = NEW.entity_id AND provider_id = NEW.provider_id
        ) THEN RAISE(ABORT, 'conflict entity/provider mismatch')
        WHEN NEW.entity_type = 'list' AND NOT EXISTS (
            SELECT 1 FROM lists WHERE id = NEW.entity_id AND provider_id = NEW.provider_id
        ) THEN RAISE(ABORT, 'conflict entity/provider mismatch')
        WHEN NEW.entity_type = 'task' AND NOT EXISTS (
            SELECT 1 FROM tasks WHERE id = NEW.entity_id AND provider_id = NEW.provider_id
        ) THEN RAISE(ABORT, 'conflict entity/provider mismatch')
    END;
END;

CREATE TRIGGER conflicts_validate_update
BEFORE UPDATE OF provider_id, entity_type, entity_id ON conflicts
BEGIN
    SELECT CASE
        WHEN NEW.entity_type = 'provider' AND NOT EXISTS (
            SELECT 1 FROM providers WHERE id = NEW.entity_id AND id = NEW.provider_id
        ) THEN RAISE(ABORT, 'conflict entity/provider mismatch')
        WHEN NEW.entity_type = 'space' AND NOT EXISTS (
            SELECT 1 FROM spaces WHERE id = NEW.entity_id AND provider_id = NEW.provider_id
        ) THEN RAISE(ABORT, 'conflict entity/provider mismatch')
        WHEN NEW.entity_type = 'list' AND NOT EXISTS (
            SELECT 1 FROM lists WHERE id = NEW.entity_id AND provider_id = NEW.provider_id
        ) THEN RAISE(ABORT, 'conflict entity/provider mismatch')
        WHEN NEW.entity_type = 'task' AND NOT EXISTS (
            SELECT 1 FROM tasks WHERE id = NEW.entity_id AND provider_id = NEW.provider_id
        ) THEN RAISE(ABORT, 'conflict entity/provider mismatch')
    END;
END;
