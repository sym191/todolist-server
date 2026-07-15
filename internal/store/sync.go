package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sym191/todolist-server/internal/syncmodel"
)

func (s *Store) ApplyMutations(
	ctx context.Context,
	userID string,
	mutations []syncmodel.Mutation,
) ([]syncmodel.MutationResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	results := make([]syncmodel.MutationResult, 0, len(mutations))
	for index, mutation := range mutations {
		if err := mutation.Validate(); err != nil {
			return nil, fmt.Errorf("mutation %d: %w", index, err)
		}
		result, found, err := existingMutationResult(ctx, tx, userID, mutation.ID)
		if err != nil {
			return nil, err
		}
		if found {
			result.Status = "already_applied"
			results = append(results, result)
			continue
		}

		canonical, err := applyMutation(ctx, tx, userID, mutation)
		if err != nil {
			return nil, fmt.Errorf("mutation %s: %w", mutation.ID, err)
		}
		var revision int64
		err = tx.QueryRow(ctx, `
			INSERT INTO changes(user_id, entity_type, entity_id, operation_type, payload)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING revision`,
			userID, mutation.EntityType, mutation.EntityID, mutation.OperationType, canonical,
		).Scan(&revision)
		if err != nil {
			return nil, fmt.Errorf("append change: %w", err)
		}
		result = syncmodel.MutationResult{
			ID: mutation.ID, Status: "applied", Revision: revision, Canonical: canonical,
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO processed_mutations(user_id, mutation_id, result)
			VALUES ($1, $2, $3)`, userID, mutation.ID, encoded); err != nil {
			return nil, fmt.Errorf("record mutation: %w", err)
		}
		results = append(results, result)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return results, nil
}

func existingMutationResult(
	ctx context.Context,
	tx pgx.Tx,
	userID, mutationID string,
) (syncmodel.MutationResult, bool, error) {
	var encoded []byte
	err := tx.QueryRow(ctx, `
		SELECT result FROM processed_mutations
		WHERE user_id = $1 AND mutation_id = $2`, userID, mutationID,
	).Scan(&encoded)
	if errors.Is(err, pgx.ErrNoRows) {
		return syncmodel.MutationResult{}, false, nil
	}
	if err != nil {
		return syncmodel.MutationResult{}, false, err
	}
	var result syncmodel.MutationResult
	if err := json.Unmarshal(encoded, &result); err != nil {
		return syncmodel.MutationResult{}, false, err
	}
	return result, true, nil
}

func applyMutation(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	mutation syncmodel.Mutation,
) (json.RawMessage, error) {
	switch mutation.EntityType {
	case syncmodel.EntityProject:
		return applyProject(ctx, tx, userID, mutation)
	case syncmodel.EntityTask:
		return applyTask(ctx, tx, userID, mutation)
	case syncmodel.EntityBoardColumn:
		return applyBoardColumn(ctx, tx, userID, mutation)
	default:
		return nil, fmt.Errorf("unsupported entity type %q", mutation.EntityType)
	}
}

func applyProject(ctx context.Context, tx pgx.Tx, userID string, mutation syncmodel.Mutation) (json.RawMessage, error) {
	if mutation.IsDelete() {
		var project syncmodel.Project
		err := scanProject(tx.QueryRow(ctx, `
			UPDATE projects SET deleted_at = now(), updated_at = now(), version = version + 1
			WHERE user_id = $1 AND id = $2
			RETURNING id, name, description, color_value, type, version, created_at, updated_at, deleted_at`,
			userID, mutation.EntityID), &project)
		if errors.Is(err, pgx.ErrNoRows) {
			return tombstone(mutation.EntityID)
		}
		return marshalCanonical(project, err)
	}
	var input syncmodel.Project
	if err := json.Unmarshal(mutation.Payload, &input); err != nil {
		return nil, fmt.Errorf("decode project: %w", err)
	}
	if input.ID != "" && input.ID != mutation.EntityID {
		return nil, errors.New("payload project id does not match entity id")
	}
	if strings.TrimSpace(input.Name) == "" || len(input.Name) > 200 {
		return nil, errors.New("project name is required and must not exceed 200 characters")
	}
	now := time.Now().UTC()
	createdAt := input.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	var project syncmodel.Project
	err := scanProject(tx.QueryRow(ctx, `
		INSERT INTO projects(user_id, id, name, description, color_value, type, created_at, updated_at, deleted_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (user_id, id) DO UPDATE SET
			name = EXCLUDED.name,
			description = EXCLUDED.description,
			color_value = EXCLUDED.color_value,
			type = EXCLUDED.type,
			updated_at = EXCLUDED.updated_at,
			deleted_at = EXCLUDED.deleted_at,
			version = projects.version + 1
		RETURNING id, name, description, color_value, type, version, created_at, updated_at, deleted_at`,
		userID, mutation.EntityID, input.Name, input.Description, input.ColorValue,
		input.Type, createdAt, now, input.DeletedAt,
	), &project)
	return marshalCanonical(project, err)
}

func applyTask(ctx context.Context, tx pgx.Tx, userID string, mutation syncmodel.Mutation) (json.RawMessage, error) {
	if mutation.IsDelete() {
		var task syncmodel.Task
		err := scanTask(tx.QueryRow(ctx, taskDeleteSQL, userID, mutation.EntityID), &task)
		if errors.Is(err, pgx.ErrNoRows) {
			return tombstone(mutation.EntityID)
		}
		return marshalCanonical(task, err)
	}
	var input syncmodel.Task
	if err := json.Unmarshal(mutation.Payload, &input); err != nil {
		return nil, fmt.Errorf("decode task: %w", err)
	}
	if input.ID != "" && input.ID != mutation.EntityID {
		return nil, errors.New("payload task id does not match entity id")
	}
	if input.ProjectID == "" || strings.TrimSpace(input.Title) == "" || input.Handle == "" {
		return nil, errors.New("task projectId, handle and title are required")
	}
	if input.Progress < 0 || input.Progress > 100 {
		return nil, errors.New("task progress must be between 0 and 100")
	}
	now := time.Now().UTC()
	createdAt := input.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	if input.ScheduledDate.IsZero() {
		input.ScheduledDate = now
	}
	var task syncmodel.Task
	err := scanTask(tx.QueryRow(ctx, taskUpsertSQL,
		userID, mutation.EntityID, input.Handle, input.ProjectID, input.Title,
		input.Summary, input.Description, input.Status, input.BoardStatusID, input.Urgency,
		input.ScheduledDate, input.StartTime, input.EndTime, input.HasTimeRange,
		input.IsFavorite, input.Progress, input.EstimatedMinutes, input.ActualMinutes,
		input.BoardOrder, createdAt, now, input.CompletedAt, input.DeletedAt,
	), &task)
	return marshalCanonical(task, err)
}

const taskColumns = `id, handle, project_id, title, summary, description, status,
	board_status_id, urgency, scheduled_date, start_time, end_time, has_time_range,
	is_favorite, progress, estimated_minutes, actual_minutes, board_order, version,
	created_at, updated_at, completed_at, deleted_at`

const taskDeleteSQL = `
	UPDATE tasks SET deleted_at = now(), updated_at = now(), version = version + 1
	WHERE user_id = $1 AND id = $2
	RETURNING ` + taskColumns

const taskUpsertSQL = `
	INSERT INTO tasks(
		user_id, id, handle, project_id, title, summary, description, status,
		board_status_id, urgency, scheduled_date, start_time, end_time, has_time_range,
		is_favorite, progress, estimated_minutes, actual_minutes, board_order,
		created_at, updated_at, completed_at, deleted_at
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
		$14, $15, $16, $17, $18, $19, $20, $21, $22, $23
	)
	ON CONFLICT (user_id, id) DO UPDATE SET
		handle = EXCLUDED.handle,
		project_id = EXCLUDED.project_id,
		title = EXCLUDED.title,
		summary = EXCLUDED.summary,
		description = EXCLUDED.description,
		status = EXCLUDED.status,
		board_status_id = EXCLUDED.board_status_id,
		urgency = EXCLUDED.urgency,
		scheduled_date = EXCLUDED.scheduled_date,
		start_time = EXCLUDED.start_time,
		end_time = EXCLUDED.end_time,
		has_time_range = EXCLUDED.has_time_range,
		is_favorite = EXCLUDED.is_favorite,
		progress = EXCLUDED.progress,
		estimated_minutes = EXCLUDED.estimated_minutes,
		actual_minutes = EXCLUDED.actual_minutes,
		board_order = EXCLUDED.board_order,
		updated_at = EXCLUDED.updated_at,
		completed_at = EXCLUDED.completed_at,
		deleted_at = EXCLUDED.deleted_at,
		version = tasks.version + 1
	RETURNING ` + taskColumns

func applyBoardColumn(ctx context.Context, tx pgx.Tx, userID string, mutation syncmodel.Mutation) (json.RawMessage, error) {
	var input syncmodel.BoardColumn
	if err := json.Unmarshal(mutation.Payload, &input); err != nil {
		return nil, fmt.Errorf("decode board column: %w", err)
	}
	if input.ProjectID == "" {
		return nil, errors.New("board column projectId is required")
	}
	if mutation.IsDelete() {
		var column syncmodel.BoardColumn
		err := scanBoardColumn(tx.QueryRow(ctx, `
			UPDATE board_columns SET deleted_at = now(), updated_at = now(), version = version + 1
			WHERE user_id = $1 AND project_id = $2 AND id = $3
			RETURNING id, project_id, label, linked_status, sort_order, version, created_at, updated_at, deleted_at`,
			userID, input.ProjectID, mutation.EntityID), &column)
		if errors.Is(err, pgx.ErrNoRows) {
			return tombstone(mutation.EntityID)
		}
		return marshalCanonical(column, err)
	}
	if strings.TrimSpace(input.Label) == "" || len(input.Label) > 100 {
		return nil, errors.New("board column label is required and must not exceed 100 characters")
	}
	now := time.Now().UTC()
	createdAt := input.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	var column syncmodel.BoardColumn
	err := scanBoardColumn(tx.QueryRow(ctx, `
		INSERT INTO board_columns(
			user_id, project_id, id, label, linked_status, sort_order, created_at, updated_at, deleted_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (user_id, project_id, id) DO UPDATE SET
			label = EXCLUDED.label,
			linked_status = EXCLUDED.linked_status,
			sort_order = EXCLUDED.sort_order,
			updated_at = EXCLUDED.updated_at,
			deleted_at = EXCLUDED.deleted_at,
			version = board_columns.version + 1
		RETURNING id, project_id, label, linked_status, sort_order, version, created_at, updated_at, deleted_at`,
		userID, input.ProjectID, mutation.EntityID, input.Label, input.LinkedStatus,
		input.SortOrder, createdAt, now, input.DeletedAt,
	), &column)
	return marshalCanonical(column, err)
}

func (s *Store) PullChanges(
	ctx context.Context,
	userID string,
	cursor int64,
	limit int,
) ([]syncmodel.Change, int64, bool, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT revision, entity_type, entity_id, operation_type, payload, changed_at
		FROM changes
		WHERE user_id = $1 AND revision > $2
		ORDER BY revision
		LIMIT $3`, userID, cursor, limit+1)
	if err != nil {
		return nil, cursor, false, err
	}
	defer rows.Close()

	changes := make([]syncmodel.Change, 0, limit)
	for rows.Next() {
		var change syncmodel.Change
		if err := rows.Scan(
			&change.Revision, &change.EntityType, &change.EntityID,
			&change.OperationType, &change.Payload, &change.ChangedAt,
		); err != nil {
			return nil, cursor, false, err
		}
		changes = append(changes, change)
	}
	if err := rows.Err(); err != nil {
		return nil, cursor, false, err
	}
	hasMore := len(changes) > limit
	if hasMore {
		changes = changes[:limit]
	}
	newCursor := cursor
	if len(changes) > 0 {
		newCursor = changes[len(changes)-1].Revision
	}
	return changes, newCursor, hasMore, nil
}

func (s *Store) Snapshot(ctx context.Context, userID string) (syncmodel.Snapshot, error) {
	var snapshot syncmodel.Snapshot
	if err := s.pool.QueryRow(ctx,
		"SELECT COALESCE(MAX(revision), 0) FROM changes WHERE user_id = $1", userID,
	).Scan(&snapshot.Cursor); err != nil {
		return snapshot, err
	}
	projects, err := s.snapshotProjects(ctx, userID)
	if err != nil {
		return snapshot, err
	}
	tasks, err := s.snapshotTasks(ctx, userID)
	if err != nil {
		return snapshot, err
	}
	columns, err := s.snapshotBoardColumns(ctx, userID)
	if err != nil {
		return snapshot, err
	}
	snapshot.Projects = projects
	snapshot.Tasks = tasks
	snapshot.BoardColumns = columns
	return snapshot, nil
}

func (s *Store) snapshotProjects(ctx context.Context, userID string) ([]syncmodel.Project, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, description, color_value, type, version, created_at, updated_at, deleted_at
		FROM projects WHERE user_id = $1 AND deleted_at IS NULL ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]syncmodel.Project, 0)
	for rows.Next() {
		var value syncmodel.Project
		if err := scanProject(rows, &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *Store) snapshotTasks(ctx context.Context, userID string) ([]syncmodel.Task, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+prefixedTaskColumns("t")+`
		FROM tasks t
		JOIN projects p ON p.user_id = t.user_id AND p.id = t.project_id
		WHERE t.user_id = $1 AND t.deleted_at IS NULL AND p.deleted_at IS NULL
		ORDER BY t.created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]syncmodel.Task, 0)
	for rows.Next() {
		var value syncmodel.Task
		if err := scanTask(rows, &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *Store) snapshotBoardColumns(ctx context.Context, userID string) ([]syncmodel.BoardColumn, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT b.id, b.project_id, b.label, b.linked_status, b.sort_order, b.version,
			b.created_at, b.updated_at, b.deleted_at
		FROM board_columns b
		JOIN projects p ON p.user_id = b.user_id AND p.id = b.project_id
		WHERE b.user_id = $1 AND b.deleted_at IS NULL AND p.deleted_at IS NULL
		ORDER BY b.project_id, b.sort_order`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]syncmodel.BoardColumn, 0)
	for rows.Next() {
		var value syncmodel.BoardColumn
		if err := scanBoardColumn(rows, &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanProject(row scanner, value *syncmodel.Project) error {
	return row.Scan(
		&value.ID, &value.Name, &value.Description, &value.ColorValue, &value.Type,
		&value.Version, &value.CreatedAt, &value.UpdatedAt, &value.DeletedAt,
	)
}

func scanTask(row scanner, value *syncmodel.Task) error {
	return row.Scan(
		&value.ID, &value.Handle, &value.ProjectID, &value.Title, &value.Summary,
		&value.Description, &value.Status, &value.BoardStatusID, &value.Urgency,
		&value.ScheduledDate, &value.StartTime, &value.EndTime, &value.HasTimeRange,
		&value.IsFavorite, &value.Progress, &value.EstimatedMinutes, &value.ActualMinutes,
		&value.BoardOrder, &value.Version, &value.CreatedAt, &value.UpdatedAt,
		&value.CompletedAt, &value.DeletedAt,
	)
}

func scanBoardColumn(row scanner, value *syncmodel.BoardColumn) error {
	return row.Scan(
		&value.ID, &value.ProjectID, &value.Label, &value.LinkedStatus, &value.SortOrder,
		&value.Version, &value.CreatedAt, &value.UpdatedAt, &value.DeletedAt,
	)
}

func marshalCanonical(value any, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(value)
	return encoded, err
}

func tombstone(id string) (json.RawMessage, error) {
	return json.Marshal(map[string]any{
		"id": id, "deletedAt": time.Now().UTC(),
	})
}

func prefixedTaskColumns(alias string) string {
	columns := strings.Split(taskColumns, ",")
	for index, column := range columns {
		columns[index] = alias + "." + strings.TrimSpace(column)
	}
	return strings.Join(columns, ", ")
}
