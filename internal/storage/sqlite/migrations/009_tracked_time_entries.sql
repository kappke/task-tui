CREATE TABLE tracked_time_entries (
    id TEXT PRIMARY KEY NOT NULL,
    provider_id TEXT NOT NULL,
    task_id TEXT,
    remote_task_id TEXT NOT NULL DEFAULT '',
    remote_entry_id TEXT,
    task_title TEXT NOT NULL CHECK (length(trim(task_title)) > 0),
    started_at TEXT NOT NULL,
    ended_at TEXT NOT NULL,
    duration_ms INTEGER NOT NULL CHECK (duration_ms >= 0),
    CHECK (task_id IS NOT NULL OR length(trim(remote_task_id)) > 0),
    CHECK (ended_at >= started_at),
    FOREIGN KEY (provider_id) REFERENCES providers (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    FOREIGN KEY (provider_id, task_id) REFERENCES tasks (provider_id, id)
        ON UPDATE RESTRICT ON DELETE RESTRICT
);

CREATE UNIQUE INDEX tracked_time_entries_remote_idx
    ON tracked_time_entries (provider_id, remote_entry_id) WHERE remote_entry_id IS NOT NULL;
CREATE INDEX tracked_time_entries_started_idx
    ON tracked_time_entries (started_at DESC, id DESC);
CREATE INDEX tracked_time_entries_provider_started_idx
    ON tracked_time_entries (provider_id, started_at DESC, id DESC);
