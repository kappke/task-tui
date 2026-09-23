CREATE TABLE task_list_memberships (
    provider_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    list_id TEXT NOT NULL,
    PRIMARY KEY (provider_id, task_id, list_id),
    FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE RESTRICT,
    FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE,
    FOREIGN KEY (list_id) REFERENCES lists(id) ON DELETE CASCADE
);

CREATE INDEX task_list_memberships_list_idx
    ON task_list_memberships (provider_id, list_id, task_id);

INSERT INTO task_list_memberships (provider_id, task_id, list_id)
SELECT provider_id, id, list_id FROM tasks;
