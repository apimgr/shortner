package db

import (
	"context"
	"testing"
	"time"
)

func TestSchedulerTaskLifecycle(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()

	if err := UpsertSchedulerTask(ctx, sqlDB, "token_cleanup", "Token Cleanup", "@every 15m"); err != nil {
		t.Fatalf("UpsertSchedulerTask() error = %v", err)
	}
	// Upsert again with a changed name/schedule to confirm it updates
	// name/schedule in place rather than erroring on the duplicate id.
	if err := UpsertSchedulerTask(ctx, sqlDB, "token_cleanup", "Token Cleanup v2", "@every 15m"); err != nil {
		t.Fatalf("UpsertSchedulerTask() second call error = %v", err)
	}

	task, err := GetSchedulerTask(ctx, sqlDB, "token_cleanup")
	if err != nil {
		t.Fatalf("GetSchedulerTask() error = %v", err)
	}
	if task.Name != "Token Cleanup v2" {
		t.Errorf("Name = %q, want updated name", task.Name)
	}
	if !task.Enabled {
		t.Error("Enabled = false, want true (default)")
	}
	if task.LastRun != nil {
		t.Error("LastRun should be nil before any run")
	}

	if err := SetSchedulerTaskEnabled(ctx, sqlDB, "token_cleanup", false); err != nil {
		t.Fatalf("SetSchedulerTaskEnabled() error = %v", err)
	}
	task, err = GetSchedulerTask(ctx, sqlDB, "token_cleanup")
	if err != nil {
		t.Fatalf("GetSchedulerTask() error = %v", err)
	}
	if task.Enabled {
		t.Error("Enabled = true after disabling, want false")
	}

	if err := SetSchedulerTaskEnabled(ctx, sqlDB, "no_such_task", true); err != ErrNotFound {
		t.Errorf("SetSchedulerTaskEnabled(unknown) error = %v, want ErrNotFound", err)
	}
}

func TestRecordSchedulerRunAndHistory(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()

	if err := UpsertSchedulerTask(ctx, sqlDB, "healthcheck_self", "Self Health Check", "@every 5m"); err != nil {
		t.Fatalf("UpsertSchedulerTask() error = %v", err)
	}

	started := time.Now().Add(-time.Second)
	finished := time.Now()
	next := finished.Add(5 * time.Minute)
	if err := RecordSchedulerRun(ctx, sqlDB, "healthcheck_self", started, finished, &next, "success", ""); err != nil {
		t.Fatalf("RecordSchedulerRun(success) error = %v", err)
	}
	if err := RecordSchedulerRun(ctx, sqlDB, "healthcheck_self", started, finished, &next, "failed", "boom"); err != nil {
		t.Fatalf("RecordSchedulerRun(failed) error = %v", err)
	}

	task, err := GetSchedulerTask(ctx, sqlDB, "healthcheck_self")
	if err != nil {
		t.Fatalf("GetSchedulerTask() error = %v", err)
	}
	if task.RunCount != 1 || task.FailCount != 1 {
		t.Errorf("RunCount=%d FailCount=%d, want 1 and 1", task.RunCount, task.FailCount)
	}
	if task.LastStatus != "failed" || task.LastError != "boom" {
		t.Errorf("LastStatus=%q LastError=%q, want failed/boom", task.LastStatus, task.LastError)
	}

	history, err := SchedulerHistory(ctx, sqlDB, "healthcheck_self", 10)
	if err != nil {
		t.Fatalf("SchedulerHistory() error = %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("len(history) = %d, want 2", len(history))
	}
	if history[0].Status != "failed" {
		t.Errorf("history[0].Status = %q, want failed (newest first)", history[0].Status)
	}

	if err := PruneSchedulerHistory(ctx, sqlDB, 1); err != nil {
		t.Fatalf("PruneSchedulerHistory() error = %v", err)
	}
	history, err = SchedulerHistory(ctx, sqlDB, "healthcheck_self", 10)
	if err != nil {
		t.Fatalf("SchedulerHistory() after prune error = %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("len(history) after prune = %d, want 1", len(history))
	}
}

func TestListSchedulerTasks(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()

	for _, id := range []string{"b_task", "a_task"} {
		if err := UpsertSchedulerTask(ctx, sqlDB, id, id, "@daily"); err != nil {
			t.Fatalf("UpsertSchedulerTask(%s) error = %v", id, err)
		}
	}

	tasks, err := ListSchedulerTasks(ctx, sqlDB)
	if err != nil {
		t.Fatalf("ListSchedulerTasks() error = %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("len(tasks) = %d, want 2", len(tasks))
	}
	if tasks[0].ID != "a_task" || tasks[1].ID != "b_task" {
		t.Errorf("tasks not ordered by id: got %q, %q", tasks[0].ID, tasks[1].ID)
	}
}
