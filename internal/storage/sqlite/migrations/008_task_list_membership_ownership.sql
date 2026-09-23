CREATE TABLE task_list_memberships_v2 (
    provider_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    list_id TEXT NOT NULL,
    PRIMARY KEY (provider_id, task_id, list_id),
    FOREIGN KEY (provider_id, task_id) REFERENCES tasks (provider_id, id) ON DELETE CASCADE,
    FOREIGN KEY (provider_id, list_id) REFERENCES lists (provider_id, id) ON DELETE CASCADE
);

INSERT INTO task_list_memberships_v2 (provider_id, task_id, list_id)
SELECT membership.provider_id, membership.task_id, membership.list_id
FROM task_list_memberships AS membership
JOIN tasks AS task ON task.provider_id = membership.provider_id AND task.id = membership.task_id
WHERE membership.list_id <> task.list_id;

DROP TABLE task_list_memberships;
ALTER TABLE task_list_memberships_v2 RENAME TO task_list_memberships;

CREATE INDEX task_list_memberships_list_idx
    ON task_list_memberships (provider_id, list_id, task_id);
