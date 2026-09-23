package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const taskSelect = `
	SELECT id, provider_id, list_id, remote_id, parent_task_id, assignee, title, description,
		status, priority, time_estimate_ms, time_tracked_ms, due_at, completed_at, sync_state, remote_updated_at,
		is_deleted, deleted_at, created_at, updated_at
	FROM tasks`

func (s *Store) createTask(ctx context.Context, task Task) (Task, error) {
	task, err := prepareTask(task)
	if err != nil {
		return Task{}, err
	}
	if task.ID == "" {
		if err := assignTaskID(&task); err != nil {
			return Task{}, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, fmt.Errorf("sqlite: begin task creation %q: %w", task.ID, err)
	}
	if err := s.insertTask(ctx, tx, task); err != nil {
		_ = tx.Rollback()
		return Task{}, fmt.Errorf("sqlite: create task %q: %w", task.ID, err)
	}
	if err := replaceTaskMemberships(ctx, tx, task); err != nil {
		_ = tx.Rollback()
		return Task{}, fmt.Errorf("sqlite: create task %q memberships: %w", task.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return Task{}, fmt.Errorf("sqlite: commit task creation %q: %w", task.ID, err)
	}
	return s.getTask(ctx, s.db, task.ID, task.ProviderID)
}

func (s *Store) getTaskByID(ctx context.Context, id string) (Task, error) {
	return s.getTask(ctx, s.db, id, "")
}

func (s *Store) getTaskByProvider(ctx context.Context, providerID, id string) (Task, error) {
	return s.getTask(ctx, s.db, id, providerID)
}

func (s *Store) getTaskByRemoteID(ctx context.Context, providerID, remoteID string) (Task, error) {
	if remoteID == "" {
		return Task{}, fmt.Errorf("sqlite: get task by remote id: empty remote id")
	}
	task, err := scanTask(s.db.QueryRowContext(ctx,
		taskSelect+" WHERE provider_id = ? AND remote_id = ?", providerID, remoteID))
	if err != nil {
		return Task{}, queryError("get task by remote id", providerID+"/"+remoteID, err)
	}
	tasks := []Task{task}
	if err := loadTaskMemberships(ctx, s.db, tasks); err != nil {
		return Task{}, fmt.Errorf("sqlite: get task by remote id memberships: %w", err)
	}
	return tasks[0], nil
}

func (s *Store) listTasks(ctx context.Context, providerID, listID string) ([]Task, error) {
	query := taskSelect + " WHERE provider_id = ? AND is_deleted = 0 AND (list_id = ? OR EXISTS (SELECT 1 FROM task_list_memberships m WHERE m.provider_id = tasks.provider_id AND m.task_id = tasks.id AND m.list_id = ?)) ORDER BY due_at IS NULL, due_at, created_at, id"
	return taskQuery(ctx, s.db, query, providerID, listID, listID)
}

func (s *Store) listTasksByList(ctx context.Context, providerID, listID string) ([]Task, error) {
	return s.listTasks(ctx, providerID, listID)
}

func (s *Store) listTasksByListID(ctx context.Context, listID string) ([]Task, error) {
	return taskQuery(ctx, s.db,
		taskSelect+" WHERE is_deleted = 0 AND (list_id = ? OR EXISTS (SELECT 1 FROM task_list_memberships m WHERE m.task_id = tasks.id AND m.list_id = ?)) ORDER BY due_at IS NULL, due_at, created_at, id", listID, listID)
}

func (s *Store) listTasksPage(ctx context.Context, providerID, listID string, limit, offset int) ([]Task, error) {
	return taskQuery(ctx, s.db, taskSelect+" WHERE provider_id = ? AND is_deleted = 0 AND (list_id = ? OR EXISTS (SELECT 1 FROM task_list_memberships m WHERE m.provider_id = tasks.provider_id AND m.task_id = tasks.id AND m.list_id = ?)) ORDER BY due_at IS NULL, due_at, created_at, id LIMIT ? OFFSET ?", providerID, listID, listID, limit, offset)
}

func (s *Store) listAllTasksPage(ctx context.Context, providerID string, limit, offset int) ([]Task, error) {
	return taskQuery(ctx, s.db, taskSelect+" WHERE provider_id = ? AND is_deleted = 0 ORDER BY due_at IS NULL, due_at, created_at, id LIMIT ? OFFSET ?", providerID, limit, offset)
}

func (s *Store) listAllTasks(ctx context.Context, providerID string) ([]Task, error) {
	return taskQuery(ctx, s.db,
		taskSelect+" WHERE provider_id = ? AND is_deleted = 0 ORDER BY due_at IS NULL, due_at, created_at, id", providerID)
}

func (s *Store) updateTask(ctx context.Context, task Task) (Task, error) {
	return s.mutateTask(ctx, task, nil)
}

func (s *Store) updateTaskWithQueue(ctx context.Context, task Task, intent *SyncOperation) (Task, error) {
	return s.mutateTask(ctx, task, intent)
}

func (s *Store) upsertTask(ctx context.Context, task Task) (Task, error) {
	task, err := prepareTask(task)
	if err != nil {
		return Task{}, err
	}
	if err := s.resolveTaskID(ctx, &task); err != nil {
		return Task{}, err
	}
	if task.ID == "" {
		if err := assignTaskID(&task); err != nil {
			return Task{}, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, fmt.Errorf("sqlite: begin task upsert %q: %w", task.ID, err)
	}
	if err := s.insertTask(ctx, tx, task); err != nil {
		if !isConstraintError(err) {
			_ = tx.Rollback()
			return Task{}, fmt.Errorf("sqlite: upsert task %q: %w", task.ID, err)
		}
		if _, updateErr := tx.ExecContext(ctx, `
			UPDATE tasks SET list_id = ?, remote_id = ?, parent_task_id = ?, assignee = ?, title = ?,
				description = ?, status = ?, priority = ?, time_estimate_ms = ?, time_tracked_ms = ?, due_at = ?, completed_at = ?,
				sync_state = ?, remote_updated_at = ?, is_deleted = ?, deleted_at = ?, updated_at = ?
			WHERE provider_id = ? AND id = ?`,
			task.ListID,
			remoteIDValue(task.RemoteID),
			task.ParentTaskID,
			task.Assignee,
			task.Title,
			task.Description,
			task.Status,
			task.Priority,
			nullableDuration(task.TimeEstimate),
			nullableDuration(task.TimeTracked),
			nullableTime(task.DueAt),
			nullableTime(task.CompletedAt),
			task.SyncState,
			nullableTime(task.RemoteUpdatedAt),
			boolInt(task.IsDeleted),
			nullableTime(task.DeletedAt),
			formatTime(task.UpdatedAt),
			task.ProviderID,
			task.ID,
		); updateErr != nil {
			_ = tx.Rollback()
			return Task{}, fmt.Errorf("sqlite: upsert task %q: %w", task.ID, updateErr)
		}
	}
	if err := replaceTaskMemberships(ctx, tx, task); err != nil {
		_ = tx.Rollback()
		return Task{}, fmt.Errorf("sqlite: upsert task %q memberships: %w", task.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return Task{}, fmt.Errorf("sqlite: commit task upsert %q: %w", task.ID, err)
	}
	return s.getTaskByProvider(ctx, task.ProviderID, task.ID)
}

func (s *Store) deleteTaskByProvider(ctx context.Context, providerID, id string) error {
	return s.tombstoneTask(ctx, providerID, id, nil)
}

func (s *Store) deleteTaskWithQueue(ctx context.Context, providerID, id string, intent *SyncOperation) error {
	task, err := s.getTaskByProvider(ctx, providerID, id)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	task.IsDeleted = true
	task.DeletedAt = &now
	task.UpdatedAt = now
	_, err = s.mutateTask(ctx, task, intent)
	return err
}

func (s *Store) tombstoneTask(ctx context.Context, providerID, id string, deletedAt *time.Time) error {
	if deletedAt == nil {
		now := time.Now().UTC()
		deletedAt = &now
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE tasks SET is_deleted = 1, deleted_at = ?, sync_state = ?, updated_at = ?
		WHERE provider_id = ? AND id = ?`,
		formatTime(*deletedAt),
		SyncStateSynced,
		formatTime(time.Now()),
		providerID,
		id,
	)
	if err != nil {
		return fmt.Errorf("sqlite: tombstone task %q: %w", id, err)
	}
	return requireAffected(result, "tombstone task", id)
}

func (s *Store) mutateTask(ctx context.Context, task Task, intent *SyncOperation) (Task, error) {
	if task.ID == "" || task.ProviderID == "" {
		return Task{}, fmt.Errorf("sqlite: update task: provider and id are required")
	}
	existing, err := s.getTaskByProvider(ctx, task.ProviderID, task.ID)
	if err != nil {
		return Task{}, err
	}
	if len(task.ListIDs) == 0 {
		if task.ListID != existing.ListID {
			task.ListIDs = []string{task.ListID}
		} else {
			task.ListIDs = append([]string(nil), existing.ListIDs...)
		}
	}
	task = mergeTaskTimes(task, existing)
	if task.SyncState == "" {
		task.SyncState = existing.SyncState
	}
	if intent != nil && task.SyncState == SyncStateLocal {
		task.SyncState = SyncStatePending
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, fmt.Errorf("sqlite: begin task mutation %q: %w", task.ID, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err = updateTaskTx(ctx, tx, task); err != nil {
		return Task{}, fmt.Errorf("sqlite: update task %q: %w", task.ID, err)
	}
	if err = replaceTaskMemberships(ctx, tx, task); err != nil {
		return Task{}, fmt.Errorf("sqlite: update task %q memberships: %w", task.ID, err)
	}
	if intent != nil {
		var prepared SyncOperation
		prepared, err = prepareTaskIntent(*intent, task)
		if err != nil {
			return Task{}, err
		}
		if err = enqueueExec(ctx, tx, prepared); err != nil {
			return Task{}, fmt.Errorf("sqlite: enqueue task mutation %q: %w", task.ID, err)
		}
	}
	if err = tx.Commit(); err != nil {
		return Task{}, fmt.Errorf("sqlite: commit task mutation %q: %w", task.ID, err)
	}
	committed = true
	return s.getTaskByProvider(ctx, task.ProviderID, task.ID)
}

func (s *Store) createTaskWithQueue(ctx context.Context, task Task, intent *SyncOperation) (Task, error) {
	var err error
	task, err = prepareTask(task)
	if err != nil {
		return Task{}, err
	}
	if task.ID == "" {
		if err := assignTaskID(&task); err != nil {
			return Task{}, err
		}
	}
	if intent != nil && task.SyncState == SyncStateLocal {
		task.SyncState = SyncStatePending
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, fmt.Errorf("sqlite: begin task creation %q: %w", task.ID, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := s.insertTask(ctx, tx, task); err != nil {
		return Task{}, fmt.Errorf("sqlite: create task %q: %w", task.ID, err)
	}
	if err := replaceTaskMemberships(ctx, tx, task); err != nil {
		return Task{}, fmt.Errorf("sqlite: create task %q memberships: %w", task.ID, err)
	}
	if intent != nil {
		prepared, err := prepareTaskIntent(*intent, task)
		if err != nil {
			return Task{}, err
		}
		if err := enqueueExec(ctx, tx, prepared); err != nil {
			return Task{}, fmt.Errorf("sqlite: enqueue task creation %q: %w", task.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Task{}, fmt.Errorf("sqlite: commit task creation %q: %w", task.ID, err)
	}
	committed = true
	return s.getTaskByProvider(ctx, task.ProviderID, task.ID)
}

func updateTaskTx(ctx context.Context, tx *sql.Tx, task Task) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE tasks SET
			list_id = ?, remote_id = ?, parent_task_id = ?, assignee = ?, title = ?, description = ?,
			status = ?, priority = ?, time_estimate_ms = ?, time_tracked_ms = ?, due_at = ?, completed_at = ?, sync_state = ?,
			remote_updated_at = ?, is_deleted = ?, deleted_at = ?, updated_at = ?
		WHERE provider_id = ? AND id = ?`,
		task.ListID,
		remoteIDValue(task.RemoteID),
		task.ParentTaskID,
		task.Assignee,
		task.Title,
		task.Description,
		task.Status,
		task.Priority,
		nullableDuration(task.TimeEstimate),
		nullableDuration(task.TimeTracked),
		nullableTime(task.DueAt),
		nullableTime(task.CompletedAt),
		task.SyncState,
		nullableTime(task.RemoteUpdatedAt),
		boolInt(task.IsDeleted),
		nullableTime(task.DeletedAt),
		formatTime(task.UpdatedAt),
		task.ProviderID,
		task.ID,
	)
	if err != nil {
		return err
	}
	return requireAffected(result, "update task", task.ID)
}

func (s *Store) insertTask(ctx context.Context, tx *sql.Tx, task Task) error {
	_, err := execerFor(s.db, tx).ExecContext(ctx, `
		INSERT INTO tasks (
			id, provider_id, list_id, remote_id, parent_task_id, assignee, title, description,
			status, priority, time_estimate_ms, time_tracked_ms, due_at, completed_at, sync_state, remote_updated_at,
			is_deleted, deleted_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID,
		task.ProviderID,
		task.ListID,
		remoteIDValue(task.RemoteID),
		task.ParentTaskID,
		task.Assignee,
		task.Title,
		task.Description,
		task.Status,
		task.Priority,
		nullableDuration(task.TimeEstimate),
		nullableDuration(task.TimeTracked),
		nullableTime(task.DueAt),
		nullableTime(task.CompletedAt),
		task.SyncState,
		nullableTime(task.RemoteUpdatedAt),
		boolInt(task.IsDeleted),
		nullableTime(task.DeletedAt),
		formatTime(task.CreatedAt),
		formatTime(task.UpdatedAt),
	)
	return err
}

func replaceTaskMemberships(ctx context.Context, tx *sql.Tx, task Task) error {
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM task_list_memberships WHERE provider_id = ? AND task_id = ?`,
		task.ProviderID, task.ID,
	); err != nil {
		return err
	}
	for _, listID := range task.ListIDs {
		if listID == task.ListID {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO task_list_memberships (provider_id, task_id, list_id)
			VALUES (?, ?, ?)`, task.ProviderID, task.ID, listID); err != nil {
			return err
		}
	}
	return nil
}

func loadTaskMemberships(ctx context.Context, queryer queryer, tasks []Task) error {
	if len(tasks) == 0 {
		return nil
	}
	indexes := make(map[string]int, len(tasks))
	ids := make([]string, 0, len(tasks))
	for index := range tasks {
		tasks[index].ListIDs = []string{tasks[index].ListID}
		indexes[tasks[index].ProviderID+"\x00"+tasks[index].ID] = index
		ids = append(ids, tasks[index].ID)
	}
	for start := 0; start < len(ids); start += 500 {
		end := start + 500
		if end > len(ids) {
			end = len(ids)
		}
		rows, err := queryer.QueryContext(ctx,
			`SELECT provider_id, task_id, list_id FROM task_list_memberships WHERE task_id IN (`+placeholders(end-start)+`) ORDER BY task_id, list_id`,
			stringArgs(ids[start:end])...,
		)
		if err != nil {
			return err
		}
		for rows.Next() {
			var providerID, taskID, listID string
			if err := rows.Scan(&providerID, &taskID, &listID); err != nil {
				_ = rows.Close()
				return err
			}
			index, ok := indexes[providerID+"\x00"+taskID]
			if ok && listID != tasks[index].ListID {
				tasks[index].ListIDs = append(tasks[index].ListIDs, listID)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	return nil
}

func stringArgs(values []string) []any {
	args := make([]any, len(values))
	for index, value := range values {
		args[index] = value
	}
	return args
}

func (s *Store) resolveTaskID(ctx context.Context, task *Task) error {
	if task.ID != "" {
		return nil
	}
	if task.RemoteID != nil && *task.RemoteID != "" {
		var id string
		err := s.db.QueryRowContext(ctx,
			"SELECT id FROM tasks WHERE provider_id = ? AND remote_id = ?",
			task.ProviderID,
			*task.RemoteID,
		).Scan(&id)
		if err == nil {
			task.ID = id
			return nil
		}
		if !isNoRows(err) {
			return fmt.Errorf("sqlite: resolve task remote id: %w", err)
		}
	}
	return nil
}

func (s *Store) getTask(ctx context.Context, queryer queryer, id, providerID string) (Task, error) {
	query := taskSelect + " WHERE id = ?"
	args := []any{id}
	if providerID != "" {
		query += " AND provider_id = ?"
		args = append(args, providerID)
	}
	task, err := scanTask(queryer.QueryRowContext(ctx, query, args...))
	if err != nil {
		return Task{}, queryError("get task", id, err)
	}
	tasks := []Task{task}
	if err := loadTaskMemberships(ctx, queryer, tasks); err != nil {
		return Task{}, fmt.Errorf("sqlite: get task memberships %q: %w", id, err)
	}
	return tasks[0], nil
}

func taskQuery(ctx context.Context, queryer queryer, query string, args ...any) ([]Task, error) {
	rows, err := queryer.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list tasks: %w", err)
	}
	defer rows.Close()
	tasks := make([]Task, 0)
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan task: %w", err)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list tasks: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("sqlite: close task rows: %w", err)
	}
	if err := loadTaskMemberships(ctx, queryer, tasks); err != nil {
		return nil, fmt.Errorf("sqlite: load task memberships: %w", err)
	}
	return tasks, nil
}

func (s *Store) searchTaskRecords(ctx context.Context, search TaskSearch) ([]TaskResult, error) {
	query := `
		SELECT t.id, t.provider_id, t.list_id, t.remote_id, t.parent_task_id, t.assignee, t.title,
			t.description, t.status, t.priority, t.time_estimate_ms, t.time_tracked_ms, t.due_at, t.completed_at, t.sync_state,
			t.remote_updated_at, t.is_deleted, t.deleted_at, t.created_at, t.updated_at,
			l.id, l.provider_id, l.space_id, l.remote_id, l.name, l.sync_state,
			l.remote_updated_at, l.is_deleted, l.deleted_at, l.created_at, l.updated_at,
			s.id, s.provider_id, s.remote_id, s.name, s.sync_state, s.remote_updated_at,
			s.is_deleted, s.deleted_at, s.created_at, s.updated_at,
			p.id, p.type, p.name, p.enabled, p.configuration, p.sync_state, p.sync_cursor,
			p.sync_error, p.last_sync_at, p.created_at, p.updated_at
		FROM tasks t
		JOIN lists l ON l.provider_id = t.provider_id AND l.id = t.list_id
		JOIN spaces s ON s.provider_id = l.provider_id AND s.id = l.space_id
		JOIN providers p ON p.id = t.provider_id
		WHERE 1 = 1`
	args := make([]any, 0, 16)
	filter := search.Filter
	if !filter.IncludeDeleted {
		query += " AND t.is_deleted = 0 AND l.is_deleted = 0 AND s.is_deleted = 0"
	}
	queryText := search.Query
	if queryText == "" {
		queryText = filter.Query
	}
	if value := strings.TrimSpace(queryText); value != "" {
		pattern := "%" + escapeLike(value) + "%"
		query += " AND (t.title LIKE ? ESCAPE '\\' COLLATE NOCASE OR t.description LIKE ? ESCAPE '\\' COLLATE NOCASE OR l.name LIKE ? ESCAPE '\\' COLLATE NOCASE OR EXISTS (SELECT 1 FROM task_list_memberships m JOIN lists ml ON ml.provider_id = m.provider_id AND ml.id = m.list_id WHERE m.provider_id = t.provider_id AND m.task_id = t.id AND ml.name LIKE ? ESCAPE '\\' COLLATE NOCASE) OR s.name LIKE ? ESCAPE '\\' COLLATE NOCASE OR EXISTS (SELECT 1 FROM task_list_memberships m JOIN lists ml ON ml.provider_id = m.provider_id AND ml.id = m.list_id JOIN spaces ms ON ms.provider_id = ml.provider_id AND ms.id = ml.space_id WHERE m.provider_id = t.provider_id AND m.task_id = t.id AND ms.name LIKE ? ESCAPE '\\' COLLATE NOCASE) OR p.name LIKE ? ESCAPE '\\' COLLATE NOCASE)"
		args = append(args, pattern, pattern, pattern, pattern, pattern, pattern, pattern)
	}
	if filter.ProviderID != "" {
		query += " AND t.provider_id = ?"
		args = append(args, filter.ProviderID)
	}
	if len(filter.ProviderIDs) > 0 {
		query += " AND t.provider_id IN (" + placeholders(len(filter.ProviderIDs)) + ")"
		for _, providerID := range filter.ProviderIDs {
			args = append(args, providerID)
		}
	}
	if filter.SpaceID != "" {
		query += " AND (l.space_id = ? OR EXISTS (SELECT 1 FROM task_list_memberships m JOIN lists ml ON ml.provider_id = m.provider_id AND ml.id = m.list_id WHERE m.provider_id = t.provider_id AND m.task_id = t.id AND ml.space_id = ?))"
		args = append(args, filter.SpaceID, filter.SpaceID)
	}
	if filter.ListID != "" {
		query += " AND (t.list_id = ? OR EXISTS (SELECT 1 FROM task_list_memberships m WHERE m.provider_id = t.provider_id AND m.task_id = t.id AND m.list_id = ?))"
		args = append(args, filter.ListID, filter.ListID)
	}
	if filter.Status != "" {
		query += " AND t.status = ?"
		args = append(args, filter.Status)
	}
	if len(filter.Statuses) > 0 {
		query += " AND t.status IN (" + placeholders(len(filter.Statuses)) + ")"
		for _, status := range filter.Statuses {
			args = append(args, status)
		}
	}
	if filter.Priority != "" {
		query += " AND t.priority = ?"
		args = append(args, filter.Priority)
	}
	priorityRank := "CASE t.priority WHEN 'none' THEN 0 WHEN 'low' THEN 1 WHEN 'normal' THEN 2 WHEN 'high' THEN 3 WHEN 'urgent' THEN 4 ELSE -1 END"
	if filter.MinPriority != nil {
		query += " AND " + priorityRank + " >= ?"
		args = append(args, *filter.MinPriority)
	}
	if filter.MaxPriority != nil {
		query += " AND " + priorityRank + " <= ?"
		args = append(args, *filter.MaxPriority)
	}
	if filter.SyncState != "" {
		query += " AND t.sync_state = ?"
		args = append(args, filter.SyncState)
	}
	if filter.DueBefore != nil {
		query += " AND t.due_at IS NOT NULL AND t.due_at <= ?"
		args = append(args, formatTime(*filter.DueBefore))
	}
	if filter.DueAfter != nil {
		query += " AND t.due_at IS NOT NULL AND t.due_at >= ?"
		args = append(args, formatTime(*filter.DueAfter))
	}
	if filter.Completed != nil {
		if *filter.Completed {
			query += " AND t.completed_at IS NOT NULL"
		} else {
			query += " AND t.completed_at IS NULL"
		}
	}
	query += " ORDER BY t.due_at IS NULL, t.due_at, t.updated_at DESC, t.id"
	if search.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, search.Limit)
		if search.Offset > 0 {
			query += " OFFSET ?"
			args = append(args, search.Offset)
		}
	} else if search.Offset > 0 {
		query += " LIMIT -1 OFFSET ?"
		args = append(args, search.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: search tasks: %w", err)
	}
	defer rows.Close()
	results := make([]TaskResult, 0)
	for rows.Next() {
		result, err := scanTaskResult(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan task search result: %w", err)
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: search tasks: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("sqlite: close task search rows: %w", err)
	}
	tasks := make([]Task, 0, len(results))
	for _, result := range results {
		tasks = append(tasks, result.Task)
	}
	if err := loadTaskMemberships(ctx, s.db, tasks); err != nil {
		return nil, fmt.Errorf("sqlite: load searched task memberships: %w", err)
	}
	for index := range results {
		results[index].Task.ListIDs = tasks[index].ListIDs
	}
	return results, nil
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func placeholders(count int) string {
	if count <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", count), ",")
}

func (s *Store) filterTaskRecords(ctx context.Context, filter TaskFilter) ([]Task, error) {
	results, err := s.searchTaskRecords(ctx, TaskSearch{Filter: filter})
	if err != nil {
		return nil, err
	}
	tasks := make([]Task, 0, len(results))
	for _, result := range results {
		tasks = append(tasks, result.Task)
	}
	return tasks, nil
}

func prepareTask(task Task) (Task, error) {
	if task.ProviderID == "" {
		return Task{}, fmt.Errorf("sqlite: task provider id is empty")
	}
	if task.ListID == "" {
		return Task{}, fmt.Errorf("sqlite: task list id is empty")
	}
	if len(task.ListIDs) == 0 {
		task.ListIDs = []string{task.ListID}
	} else {
		seen := make(map[string]struct{}, len(task.ListIDs))
		primaryIncluded := false
		for _, listID := range task.ListIDs {
			if strings.TrimSpace(listID) == "" {
				return Task{}, fmt.Errorf("sqlite: task list membership is empty")
			}
			if _, exists := seen[listID]; exists {
				return Task{}, fmt.Errorf("sqlite: duplicate task list membership %q", listID)
			}
			seen[listID] = struct{}{}
			if listID == task.ListID {
				primaryIncluded = true
			}
		}
		if !primaryIncluded {
			return Task{}, fmt.Errorf("sqlite: task primary list is missing from memberships")
		}
	}
	task.RemoteID = normalizeRemoteID(task.RemoteID)
	if task.Status == "" {
		task.Status = "todo"
	}
	if task.Priority == "" {
		task.Priority = "normal"
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now().UTC()
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = task.CreatedAt
	}
	if task.SyncState == "" {
		task.SyncState = SyncStateLocal
	}
	if task.IsDeleted && task.DeletedAt == nil {
		now := time.Now().UTC()
		task.DeletedAt = &now
	}
	return task, nil
}

func assignTaskID(task *Task) error {
	id, err := newID()
	if err != nil {
		return fmt.Errorf("sqlite: generate task id: %w", err)
	}
	task.ID = id
	return nil
}

func mergeTaskTimes(task, existing Task) Task {
	if task.CreatedAt.IsZero() {
		task.CreatedAt = existing.CreatedAt
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = time.Now().UTC()
	}
	return task
}

func prepareTaskIntent(intent SyncOperation, task Task) (SyncOperation, error) {
	if intent.ID == "" {
		id, err := newID()
		if err != nil {
			return SyncOperation{}, fmt.Errorf("sqlite: generate task operation id: %w", err)
		}
		intent.ID = id
	}
	if intent.ProviderID == "" {
		intent.ProviderID = task.ProviderID
	}
	if intent.EntityType == "" {
		intent.EntityType = EntityTask
	}
	if intent.EntityID == "" {
		intent.EntityID = task.ID
	}
	if intent.Operation == "" {
		if task.IsDeleted {
			intent.Operation = OperationDelete
		} else {
			intent.Operation = OperationUpdate
		}
	}
	return prepareOperation(intent)
}

func scanTask(row rowScanner) (Task, error) {
	var (
		task                                           Task
		remoteID, parentTaskID                         sql.NullString
		dueAt, completedAt, remoteUpdatedAt, deletedAt sql.NullString
		timeEstimate, timeTracked                      sql.NullInt64
		syncState                                      string
		isDeleted                                      int
		createdAt, updatedAt                           sql.NullString
	)
	if err := row.Scan(
		&task.ID,
		&task.ProviderID,
		&task.ListID,
		&remoteID,
		&parentTaskID,
		&task.Assignee,
		&task.Title,
		&task.Description,
		&task.Status,
		&task.Priority,
		&timeEstimate,
		&timeTracked,
		&dueAt,
		&completedAt,
		&syncState,
		&remoteUpdatedAt,
		&isDeleted,
		&deletedAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Task{}, err
	}
	task.RemoteID = nullableString(remoteID)
	task.ParentTaskID = nullableString(parentTaskID)
	task.SyncState = SyncState(syncState)
	task.IsDeleted = isDeleted != 0
	var err error
	if task.DueAt, err = scanNullableTime(dueAt); err != nil {
		return Task{}, fmt.Errorf("due timestamp: %w", err)
	}
	if task.CompletedAt, err = scanNullableTime(completedAt); err != nil {
		return Task{}, fmt.Errorf("completed timestamp: %w", err)
	}
	if task.RemoteUpdatedAt, err = scanNullableTime(remoteUpdatedAt); err != nil {
		return Task{}, fmt.Errorf("remote updated timestamp: %w", err)
	}
	if task.TimeEstimate, err = scanNullableDuration(timeEstimate); err != nil {
		return Task{}, fmt.Errorf("time estimate: %w", err)
	}
	if task.TimeTracked, err = scanNullableDuration(timeTracked); err != nil {
		return Task{}, fmt.Errorf("time tracked: %w", err)
	}
	if task.DeletedAt, err = scanNullableTime(deletedAt); err != nil {
		return Task{}, fmt.Errorf("deleted timestamp: %w", err)
	}
	if task.CreatedAt, err = parseNullableRequiredTime(createdAt); err != nil {
		return Task{}, fmt.Errorf("created timestamp: %w", err)
	}
	if task.UpdatedAt, err = parseNullableRequiredTime(updatedAt); err != nil {
		return Task{}, fmt.Errorf("updated timestamp: %w", err)
	}
	return task, nil
}

func scanTaskResult(row rowScanner) (TaskResult, error) {
	var (
		result TaskResult
		tr     Task
		lr     List
		sr     Space
		pr     Provider
	)
	var (
		trRemote, trParent, trDue, trCompleted, trRemoteUpdated, trDeleted sql.NullString
		trTimeEstimate, trTimeTracked                                      sql.NullInt64
		trSync                                                             string
		trDeletedFlag                                                      int
		trCreated, trUpdated                                               sql.NullString
		lrRemote, lrRemoteUpdated, lrDeleted                               sql.NullString
		lrSync                                                             string
		lrDeletedFlag                                                      int
		lrCreated, lrUpdated                                               sql.NullString
		srRemote, srRemoteUpdated, srDeleted                               sql.NullString
		srSync                                                             string
		srDeletedFlag                                                      int
		srCreated, srUpdated                                               sql.NullString
		prEnabled                                                          int
		prConfiguration                                                    []byte
		prSync, prCursor, prError, prLastSync, prCreated, prUpdated        sql.NullString
	)
	if err := row.Scan(
		&tr.ID, &tr.ProviderID, &tr.ListID, &trRemote, &trParent, &tr.Assignee, &tr.Title,
		&tr.Description, &tr.Status, &tr.Priority, &trTimeEstimate, &trTimeTracked, &trDue, &trCompleted, &trSync,
		&trRemoteUpdated, &trDeletedFlag, &trDeleted, &trCreated, &trUpdated,
		&lr.ID, &lr.ProviderID, &lr.SpaceID, &lrRemote, &lr.Name, &lrSync,
		&lrRemoteUpdated, &lrDeletedFlag, &lrDeleted, &lrCreated, &lrUpdated,
		&sr.ID, &sr.ProviderID, &srRemote, &sr.Name, &srSync, &srRemoteUpdated,
		&srDeletedFlag, &srDeleted, &srCreated, &srUpdated,
		&pr.ID, &pr.Type, &pr.Name, &prEnabled, &prConfiguration, &prSync, &prCursor,
		&prError, &prLastSync, &prCreated, &prUpdated,
	); err != nil {
		return TaskResult{}, err
	}
	tr.RemoteID, tr.ParentTaskID = nullableString(trRemote), nullableString(trParent)
	tr.SyncState, tr.IsDeleted = SyncState(trSync), trDeletedFlag != 0
	lr.RemoteID, lr.SyncState, lr.IsDeleted = nullableString(lrRemote), SyncState(lrSync), lrDeletedFlag != 0
	sr.RemoteID, sr.SyncState, sr.IsDeleted = nullableString(srRemote), SyncState(srSync), srDeletedFlag != 0
	pr.Enabled, pr.Configuration, pr.SyncState = prEnabled != 0, append([]byte(nil), prConfiguration...), SyncState(prSync.String)
	var err error
	if tr.DueAt, err = scanNullableTime(trDue); err != nil {
		return TaskResult{}, err
	}
	if tr.CompletedAt, err = scanNullableTime(trCompleted); err != nil {
		return TaskResult{}, err
	}
	if tr.RemoteUpdatedAt, err = scanNullableTime(trRemoteUpdated); err != nil {
		return TaskResult{}, err
	}
	if tr.TimeEstimate, err = scanNullableDuration(trTimeEstimate); err != nil {
		return TaskResult{}, err
	}
	if tr.TimeTracked, err = scanNullableDuration(trTimeTracked); err != nil {
		return TaskResult{}, err
	}
	if tr.DeletedAt, err = scanNullableTime(trDeleted); err != nil {
		return TaskResult{}, err
	}
	if tr.CreatedAt, err = parseNullableRequiredTime(trCreated); err != nil {
		return TaskResult{}, err
	}
	if tr.UpdatedAt, err = parseNullableRequiredTime(trUpdated); err != nil {
		return TaskResult{}, err
	}
	if lr.RemoteUpdatedAt, err = scanNullableTime(lrRemoteUpdated); err != nil {
		return TaskResult{}, err
	}
	if lr.DeletedAt, err = scanNullableTime(lrDeleted); err != nil {
		return TaskResult{}, err
	}
	if lr.CreatedAt, err = parseNullableRequiredTime(lrCreated); err != nil {
		return TaskResult{}, err
	}
	if lr.UpdatedAt, err = parseNullableRequiredTime(lrUpdated); err != nil {
		return TaskResult{}, err
	}
	if sr.RemoteUpdatedAt, err = scanNullableTime(srRemoteUpdated); err != nil {
		return TaskResult{}, err
	}
	if sr.DeletedAt, err = scanNullableTime(srDeleted); err != nil {
		return TaskResult{}, err
	}
	if sr.CreatedAt, err = parseNullableRequiredTime(srCreated); err != nil {
		return TaskResult{}, err
	}
	if sr.UpdatedAt, err = parseNullableRequiredTime(srUpdated); err != nil {
		return TaskResult{}, err
	}
	pr.SyncCursor, pr.SyncError = nullableString(prCursor), nullableString(prError)
	if pr.LastSyncAt, err = scanNullableTime(prLastSync); err != nil {
		return TaskResult{}, err
	}
	if pr.CreatedAt, err = parseNullableRequiredTime(prCreated); err != nil {
		return TaskResult{}, err
	}
	if pr.UpdatedAt, err = parseNullableRequiredTime(prUpdated); err != nil {
		return TaskResult{}, err
	}
	result.Task, result.List, result.Space, result.Provider = tr, lr, sr, pr
	return result, nil
}
