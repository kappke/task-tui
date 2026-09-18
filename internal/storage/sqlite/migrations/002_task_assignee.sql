ALTER TABLE tasks ADD COLUMN assignee TEXT NOT NULL DEFAULT '';

CREATE INDEX tasks_assignee_idx ON tasks (provider_id, assignee, is_deleted);
