CREATE UNIQUE INDEX IF NOT EXISTS spaces_provider_remote_id
    ON spaces (provider_id, remote_id) WHERE remote_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS lists_provider_remote_id
    ON lists (provider_id, remote_id) WHERE remote_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS tasks_provider_remote_id
    ON tasks (provider_id, remote_id) WHERE remote_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS spaces_provider_idx ON spaces (provider_id, is_deleted, updated_at);
CREATE INDEX IF NOT EXISTS lists_provider_space_idx ON lists (provider_id, space_id, is_deleted, updated_at);
CREATE INDEX IF NOT EXISTS tasks_provider_list_idx ON tasks (provider_id, list_id, is_deleted, updated_at);
CREATE INDEX IF NOT EXISTS tasks_provider_parent_idx ON tasks (provider_id, parent_task_id, is_deleted);
CREATE INDEX IF NOT EXISTS tasks_status_idx ON tasks (provider_id, status, is_deleted);
CREATE INDEX IF NOT EXISTS tasks_priority_idx ON tasks (provider_id, priority, is_deleted);
CREATE INDEX IF NOT EXISTS tasks_due_idx ON tasks (provider_id, due_at, is_deleted);
CREATE INDEX IF NOT EXISTS tasks_sync_state_idx ON tasks (provider_id, sync_state, is_deleted);
CREATE INDEX IF NOT EXISTS sync_operations_claim_idx
    ON sync_operations (provider_id, status, next_attempt_at, created_at, id);
CREATE INDEX IF NOT EXISTS sync_operations_entity_idx
    ON sync_operations (provider_id, entity_type, entity_id, created_at, id);
CREATE INDEX IF NOT EXISTS conflicts_entity_idx ON conflicts (provider_id, entity_type, entity_id, status);

CREATE TRIGGER IF NOT EXISTS spaces_provider_immutable
BEFORE UPDATE OF provider_id ON spaces
WHEN OLD.provider_id <> NEW.provider_id
BEGIN SELECT RAISE(ABORT, 'space provider_id is immutable'); END;
CREATE TRIGGER IF NOT EXISTS lists_provider_immutable
BEFORE UPDATE OF provider_id ON lists
WHEN OLD.provider_id <> NEW.provider_id
BEGIN SELECT RAISE(ABORT, 'list provider_id is immutable'); END;
CREATE TRIGGER IF NOT EXISTS tasks_provider_immutable
BEFORE UPDATE OF provider_id ON tasks
WHEN OLD.provider_id <> NEW.provider_id
BEGIN SELECT RAISE(ABORT, 'task provider_id is immutable'); END;

CREATE TRIGGER IF NOT EXISTS provider_metadata_validate_insert
BEFORE INSERT ON provider_metadata
BEGIN
    SELECT CASE
        WHEN NEW.entity_type = 'provider' AND NOT EXISTS (SELECT 1 FROM providers WHERE id = NEW.entity_id AND id = NEW.provider_id) THEN RAISE(ABORT, 'provider metadata entity/provider mismatch')
        WHEN NEW.entity_type = 'space' AND NOT EXISTS (SELECT 1 FROM spaces WHERE id = NEW.entity_id AND provider_id = NEW.provider_id) THEN RAISE(ABORT, 'space metadata entity/provider mismatch')
        WHEN NEW.entity_type = 'list' AND NOT EXISTS (SELECT 1 FROM lists WHERE id = NEW.entity_id AND provider_id = NEW.provider_id) THEN RAISE(ABORT, 'list metadata entity/provider mismatch')
        WHEN NEW.entity_type = 'task' AND NOT EXISTS (SELECT 1 FROM tasks WHERE id = NEW.entity_id AND provider_id = NEW.provider_id) THEN RAISE(ABORT, 'task metadata entity/provider mismatch')
    END;
END;
CREATE TRIGGER IF NOT EXISTS provider_metadata_validate_update
BEFORE UPDATE OF provider_id, entity_type, entity_id ON provider_metadata
BEGIN
    SELECT CASE
        WHEN NEW.entity_type = 'provider' AND NOT EXISTS (SELECT 1 FROM providers WHERE id = NEW.entity_id AND id = NEW.provider_id) THEN RAISE(ABORT, 'provider metadata entity/provider mismatch')
        WHEN NEW.entity_type = 'space' AND NOT EXISTS (SELECT 1 FROM spaces WHERE id = NEW.entity_id AND provider_id = NEW.provider_id) THEN RAISE(ABORT, 'space metadata entity/provider mismatch')
        WHEN NEW.entity_type = 'list' AND NOT EXISTS (SELECT 1 FROM lists WHERE id = NEW.entity_id AND provider_id = NEW.provider_id) THEN RAISE(ABORT, 'list metadata entity/provider mismatch')
        WHEN NEW.entity_type = 'task' AND NOT EXISTS (SELECT 1 FROM tasks WHERE id = NEW.entity_id AND provider_id = NEW.provider_id) THEN RAISE(ABORT, 'task metadata entity/provider mismatch')
    END;
END;

CREATE TRIGGER IF NOT EXISTS sync_operations_validate_insert
BEFORE INSERT ON sync_operations
BEGIN
    SELECT CASE
        WHEN NEW.entity_type = 'provider' AND NOT EXISTS (SELECT 1 FROM providers WHERE id = NEW.entity_id AND id = NEW.provider_id) THEN RAISE(ABORT, 'sync operation entity/provider mismatch')
        WHEN NEW.entity_type = 'space' AND NOT EXISTS (SELECT 1 FROM spaces WHERE id = NEW.entity_id AND provider_id = NEW.provider_id) THEN RAISE(ABORT, 'sync operation entity/provider mismatch')
        WHEN NEW.entity_type = 'list' AND NOT EXISTS (SELECT 1 FROM lists WHERE id = NEW.entity_id AND provider_id = NEW.provider_id) THEN RAISE(ABORT, 'sync operation entity/provider mismatch')
        WHEN NEW.entity_type = 'task' AND NOT EXISTS (SELECT 1 FROM tasks WHERE id = NEW.entity_id AND provider_id = NEW.provider_id) THEN RAISE(ABORT, 'sync operation entity/provider mismatch')
    END;
END;
CREATE TRIGGER IF NOT EXISTS sync_operations_validate_update
BEFORE UPDATE OF provider_id, entity_type, entity_id ON sync_operations
BEGIN
    SELECT CASE
        WHEN NEW.entity_type = 'provider' AND NOT EXISTS (SELECT 1 FROM providers WHERE id = NEW.entity_id AND id = NEW.provider_id) THEN RAISE(ABORT, 'sync operation entity/provider mismatch')
        WHEN NEW.entity_type = 'space' AND NOT EXISTS (SELECT 1 FROM spaces WHERE id = NEW.entity_id AND provider_id = NEW.provider_id) THEN RAISE(ABORT, 'sync operation entity/provider mismatch')
        WHEN NEW.entity_type = 'list' AND NOT EXISTS (SELECT 1 FROM lists WHERE id = NEW.entity_id AND provider_id = NEW.provider_id) THEN RAISE(ABORT, 'sync operation entity/provider mismatch')
        WHEN NEW.entity_type = 'task' AND NOT EXISTS (SELECT 1 FROM tasks WHERE id = NEW.entity_id AND provider_id = NEW.provider_id) THEN RAISE(ABORT, 'sync operation entity/provider mismatch')
    END;
END;

CREATE TRIGGER IF NOT EXISTS sync_bases_validate_insert
BEFORE INSERT ON sync_bases
BEGIN
    SELECT CASE
        WHEN NEW.entity_type = 'provider' AND NOT EXISTS (SELECT 1 FROM providers WHERE id = NEW.entity_id AND id = NEW.provider_id) THEN RAISE(ABORT, 'sync base entity/provider mismatch')
        WHEN NEW.entity_type = 'space' AND NOT EXISTS (SELECT 1 FROM spaces WHERE id = NEW.entity_id AND provider_id = NEW.provider_id) THEN RAISE(ABORT, 'sync base entity/provider mismatch')
        WHEN NEW.entity_type = 'list' AND NOT EXISTS (SELECT 1 FROM lists WHERE id = NEW.entity_id AND provider_id = NEW.provider_id) THEN RAISE(ABORT, 'sync base entity/provider mismatch')
        WHEN NEW.entity_type = 'task' AND NOT EXISTS (SELECT 1 FROM tasks WHERE id = NEW.entity_id AND provider_id = NEW.provider_id) THEN RAISE(ABORT, 'sync base entity/provider mismatch')
    END;
END;
CREATE TRIGGER IF NOT EXISTS sync_bases_validate_update
BEFORE UPDATE OF provider_id, entity_type, entity_id ON sync_bases
BEGIN
    SELECT CASE
        WHEN NEW.entity_type = 'provider' AND NOT EXISTS (SELECT 1 FROM providers WHERE id = NEW.entity_id AND id = NEW.provider_id) THEN RAISE(ABORT, 'sync base entity/provider mismatch')
        WHEN NEW.entity_type = 'space' AND NOT EXISTS (SELECT 1 FROM spaces WHERE id = NEW.entity_id AND provider_id = NEW.provider_id) THEN RAISE(ABORT, 'sync base entity/provider mismatch')
        WHEN NEW.entity_type = 'list' AND NOT EXISTS (SELECT 1 FROM lists WHERE id = NEW.entity_id AND provider_id = NEW.provider_id) THEN RAISE(ABORT, 'sync base entity/provider mismatch')
        WHEN NEW.entity_type = 'task' AND NOT EXISTS (SELECT 1 FROM tasks WHERE id = NEW.entity_id AND provider_id = NEW.provider_id) THEN RAISE(ABORT, 'sync base entity/provider mismatch')
    END;
END;

CREATE TRIGGER IF NOT EXISTS conflicts_validate_insert
BEFORE INSERT ON conflicts
BEGIN
    SELECT CASE
        WHEN NEW.entity_type = 'provider' AND NOT EXISTS (SELECT 1 FROM providers WHERE id = NEW.entity_id AND id = NEW.provider_id) THEN RAISE(ABORT, 'conflict entity/provider mismatch')
        WHEN NEW.entity_type = 'space' AND NOT EXISTS (SELECT 1 FROM spaces WHERE id = NEW.entity_id AND provider_id = NEW.provider_id) THEN RAISE(ABORT, 'conflict entity/provider mismatch')
        WHEN NEW.entity_type = 'list' AND NOT EXISTS (SELECT 1 FROM lists WHERE id = NEW.entity_id AND provider_id = NEW.provider_id) THEN RAISE(ABORT, 'conflict entity/provider mismatch')
        WHEN NEW.entity_type = 'task' AND NOT EXISTS (SELECT 1 FROM tasks WHERE id = NEW.entity_id AND provider_id = NEW.provider_id) THEN RAISE(ABORT, 'conflict entity/provider mismatch')
    END;
END;
CREATE TRIGGER IF NOT EXISTS conflicts_validate_update
BEFORE UPDATE OF provider_id, entity_type, entity_id ON conflicts
BEGIN
    SELECT CASE
        WHEN NEW.entity_type = 'provider' AND NOT EXISTS (SELECT 1 FROM providers WHERE id = NEW.entity_id AND id = NEW.provider_id) THEN RAISE(ABORT, 'conflict entity/provider mismatch')
        WHEN NEW.entity_type = 'space' AND NOT EXISTS (SELECT 1 FROM spaces WHERE id = NEW.entity_id AND provider_id = NEW.provider_id) THEN RAISE(ABORT, 'conflict entity/provider mismatch')
        WHEN NEW.entity_type = 'list' AND NOT EXISTS (SELECT 1 FROM lists WHERE id = NEW.entity_id AND provider_id = NEW.provider_id) THEN RAISE(ABORT, 'conflict entity/provider mismatch')
        WHEN NEW.entity_type = 'task' AND NOT EXISTS (SELECT 1 FROM tasks WHERE id = NEW.entity_id AND provider_id = NEW.provider_id) THEN RAISE(ABORT, 'conflict entity/provider mismatch')
    END;
END;
