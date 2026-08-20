package db

import (
	"context"
	"database/sql"
	"time"
)

// SchedulerTask mirrors the scheduler_tasks table, per AI.md PART 18
// "Scheduler State (Persistent)".
type SchedulerTask struct {
	ID         string
	Name       string
	Enabled    bool
	Schedule   string
	LastRun    *time.Time
	NextRun    *time.Time
	LastStatus string
	LastError  string
	RunCount   int64
	FailCount  int64
}

// UpsertSchedulerTask inserts task if it does not already exist, or updates
// only its Name/Schedule columns (never the run-state columns) if it does —
// this lets a binary upgrade change a built-in task's default schedule
// without clobbering the operator's recorded history on restart.
func UpsertSchedulerTask(ctx context.Context, sqlDB *sql.DB, id, name, schedule string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := sqlDB.ExecContext(ctx, `
		INSERT INTO scheduler_tasks (id, name, schedule, enabled)
		VALUES (?, ?, ?, 1)
		ON CONFLICT(id) DO UPDATE SET name = excluded.name`,
		id, name, schedule)
	return HandleQueryError(err)
}

// GetSchedulerTask returns a single task's persisted state.
func GetSchedulerTask(ctx context.Context, sqlDB *sql.DB, id string) (*SchedulerTask, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	row := sqlDB.QueryRowContext(ctx, `
		SELECT id, name, enabled, schedule, last_run, next_run, last_status, last_error, run_count, fail_count
		FROM scheduler_tasks WHERE id = ?`, id)
	return scanSchedulerTask(row)
}

// ListSchedulerTasks returns every task's persisted state, ordered by id.
func ListSchedulerTasks(ctx context.Context, sqlDB *sql.DB) ([]SchedulerTask, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	rows, err := sqlDB.QueryContext(ctx, `
		SELECT id, name, enabled, schedule, last_run, next_run, last_status, last_error, run_count, fail_count
		FROM scheduler_tasks ORDER BY id`)
	if err != nil {
		return nil, HandleQueryError(err)
	}
	defer rows.Close()

	var tasks []SchedulerTask
	for rows.Next() {
		t, err := scanSchedulerTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, *t)
	}
	return tasks, HandleQueryError(rows.Err())
}

// SetSchedulerTaskEnabled toggles a task's enabled flag.
func SetSchedulerTaskEnabled(ctx context.Context, sqlDB *sql.DB, id string, enabled bool) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	res, err := sqlDB.ExecContext(ctx, `UPDATE scheduler_tasks SET enabled = ? WHERE id = ?`, enabled, id)
	if err != nil {
		return HandleQueryError(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return HandleQueryError(err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordSchedulerRun updates a task's run-state columns after an execution
// and appends a scheduler_history row, per AI.md PART 18 "Task Execution
// Flow".
func RecordSchedulerRun(ctx context.Context, sqlDB *sql.DB, id string, startedAt, finishedAt time.Time, nextRun *time.Time, status, errMsg string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	return WithTransaction(ctx, sqlDB, func(tx *sql.Tx) error {
		var nextRunVal any
		if nextRun != nil {
			nextRunVal = nextRun.Unix()
		}
		failIncr := 0
		runIncr := 0
		if status == "success" {
			runIncr = 1
		} else if status == "failed" {
			failIncr = 1
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE scheduler_tasks
			SET last_run = ?, next_run = ?, last_status = ?, last_error = ?,
			    run_count = run_count + ?, fail_count = fail_count + ?
			WHERE id = ?`,
			startedAt.Unix(), nextRunVal, status, nullIfEmpty(errMsg), runIncr, failIncr, id); err != nil {
			return err
		}
		durationMS := finishedAt.Sub(startedAt).Milliseconds()
		_, err := tx.ExecContext(ctx, `
			INSERT INTO scheduler_history (task_id, started_at, finished_at, status, error, duration_ms)
			VALUES (?, ?, ?, ?, ?, ?)`,
			id, startedAt.Unix(), finishedAt.Unix(), status, nullIfEmpty(errMsg), durationMS)
		return err
	})
}

// SchedulerHistory returns the most recent runs of task id, newest first,
// capped at limit rows.
func SchedulerHistory(ctx context.Context, sqlDB *sql.DB, id string, limit int) ([]SchedulerHistoryEntry, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	rows, err := sqlDB.QueryContext(ctx, `
		SELECT id, task_id, started_at, finished_at, status, error, duration_ms
		FROM scheduler_history WHERE task_id = ? ORDER BY started_at DESC, id DESC LIMIT ?`, id, limit)
	if err != nil {
		return nil, HandleQueryError(err)
	}
	defer rows.Close()

	var out []SchedulerHistoryEntry
	for rows.Next() {
		var e SchedulerHistoryEntry
		var startedAt int64
		var finishedAt, durationMS sql.NullInt64
		var errMsg sql.NullString
		if err := rows.Scan(&e.ID, &e.TaskID, &startedAt, &finishedAt, &e.Status, &errMsg, &durationMS); err != nil {
			return nil, HandleQueryError(err)
		}
		e.StartedAt = time.Unix(startedAt, 0).UTC()
		if finishedAt.Valid {
			t := time.Unix(finishedAt.Int64, 0).UTC()
			e.FinishedAt = &t
		}
		e.Error = errMsg.String
		e.DurationMS = durationMS.Int64
		out = append(out, e)
	}
	return out, HandleQueryError(rows.Err())
}

// PruneSchedulerHistory keeps only the most recent keep rows per task_id,
// per AI.md PART 10's commented retention query for scheduler_history.
func PruneSchedulerHistory(ctx context.Context, sqlDB *sql.DB, keep int) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := sqlDB.ExecContext(ctx, `
		DELETE FROM scheduler_history WHERE id NOT IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (PARTITION BY task_id ORDER BY started_at DESC) AS rn
				FROM scheduler_history
			) WHERE rn <= ?
		)`, keep)
	return HandleQueryError(err)
}

// SchedulerHistoryEntry mirrors one scheduler_history row.
type SchedulerHistoryEntry struct {
	ID         int64
	TaskID     string
	StartedAt  time.Time
	FinishedAt *time.Time
	Status     string
	Error      string
	DurationMS int64
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func scanSchedulerTask(row rowScanner) (*SchedulerTask, error) {
	var t SchedulerTask
	var enabled int
	var lastRun, nextRun sql.NullInt64
	var lastStatus, lastError sql.NullString
	if err := row.Scan(&t.ID, &t.Name, &enabled, &t.Schedule, &lastRun, &nextRun, &lastStatus, &lastError, &t.RunCount, &t.FailCount); err != nil {
		return nil, HandleQueryError(err)
	}
	t.Enabled = enabled != 0
	if lastRun.Valid {
		v := time.Unix(lastRun.Int64, 0).UTC()
		t.LastRun = &v
	}
	if nextRun.Valid {
		v := time.Unix(nextRun.Int64, 0).UTC()
		t.NextRun = &v
	}
	t.LastStatus = lastStatus.String
	t.LastError = lastError.String
	return &t, nil
}
