package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/db"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := db.Open(":memory:", db.DefaultPool(), nil)
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	return sqlDB
}

func testLogger(t *testing.T) *applog.Logger {
	t.Helper()
	l, err := applog.Open(t.TempDir()+"/test.log", applog.LevelInfo)
	if err != nil {
		t.Fatalf("applog.Open() error = %v", err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

func TestParseSchedule(t *testing.T) {
	cases := []string{"@hourly", "@daily", "@weekly", "@monthly", "@every 5m", "@every 1h", "0 2 * * *"}
	for _, sched := range cases {
		if _, err := parseSchedule(sched); err != nil {
			t.Errorf("parseSchedule(%q) error = %v", sched, err)
		}
	}
	if _, err := parseSchedule("@every not-a-duration"); err == nil {
		t.Error("parseSchedule(@every not-a-duration) expected error, got nil")
	}
}

func TestRegisterAndRunNow(t *testing.T) {
	sqlDB := openTestDB(t)
	sched, err := New(sqlDB, testLogger(t), "UTC", "1h")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var ran bool
	ctx := context.Background()
	err = sched.Register(ctx, TaskDef{
		ID: "test_task", Name: "Test Task", Schedule: "@every 1h", Enabled: true,
		Run: func(ctx context.Context) error { ran = true; return nil },
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := sched.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer sched.Stop()

	if err := sched.RunNow(ctx, "test_task"); err != nil {
		t.Fatalf("RunNow() error = %v", err)
	}
	if !ran {
		t.Error("RunNow() did not execute the task")
	}

	task, err := sched.Show(ctx, "test_task")
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if task.LastStatus != "success" || task.RunCount != 1 {
		t.Errorf("LastStatus=%q RunCount=%d, want success/1", task.LastStatus, task.RunCount)
	}

	if _, err := sched.List(ctx); err != nil {
		t.Errorf("List() error = %v", err)
	}
	if _, err := sched.History(ctx, "test_task", 10); err != nil {
		t.Errorf("History() error = %v", err)
	}
}

func TestRunNowRecordsFailure(t *testing.T) {
	sqlDB := openTestDB(t)
	sched, err := New(sqlDB, testLogger(t), "UTC", "1h")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	wantErr := errors.New("boom")
	if err := sched.Register(ctx, TaskDef{
		ID: "failing_task", Name: "Failing Task", Schedule: "@daily", Enabled: true,
		Run: func(ctx context.Context) error { return wantErr },
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := sched.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer sched.Stop()

	if err := sched.RunNow(ctx, "failing_task"); !errors.Is(err, wantErr) {
		t.Errorf("RunNow() error = %v, want %v", err, wantErr)
	}
	task, err := sched.Show(ctx, "failing_task")
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if task.LastStatus != "failed" || task.FailCount != 1 {
		t.Errorf("LastStatus=%q FailCount=%d, want failed/1", task.LastStatus, task.FailCount)
	}
}

func TestRunNowUnknownTask(t *testing.T) {
	sqlDB := openTestDB(t)
	sched, err := New(sqlDB, testLogger(t), "UTC", "1h")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := sched.RunNow(context.Background(), "no_such_task"); err == nil {
		t.Error("RunNow(unknown) expected error, got nil")
	}
}

func TestSetEnabled(t *testing.T) {
	sqlDB := openTestDB(t)
	sched, err := New(sqlDB, testLogger(t), "UTC", "1h")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	if err := sched.Register(ctx, TaskDef{
		ID: "toggle_task", Name: "Toggle Task", Schedule: "@daily", Enabled: true,
		Run: func(ctx context.Context) error { return nil },
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if err := sched.SetEnabled(ctx, "toggle_task", false); err != nil {
		t.Fatalf("SetEnabled() error = %v", err)
	}
	task, err := sched.Show(ctx, "toggle_task")
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if task.Enabled {
		t.Error("Enabled = true after SetEnabled(false)")
	}

	if err := sched.SetEnabled(ctx, "unknown_task", true); err == nil {
		t.Error("SetEnabled(unknown) expected error, got nil")
	}
}

func TestNewInvalidTimezoneFallsBackToUTC(t *testing.T) {
	sqlDB := openTestDB(t)
	if _, err := New(sqlDB, testLogger(t), "Not/AZone", "1h"); err != nil {
		t.Fatalf("New() with invalid timezone should fall back to UTC, got error = %v", err)
	}
}

func TestStartRejectsInvalidSchedule(t *testing.T) {
	sqlDB := openTestDB(t)
	sched, err := New(sqlDB, testLogger(t), "UTC", "1h")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	if err := sched.Register(ctx, TaskDef{
		ID: "bad_schedule", Name: "Bad Schedule", Schedule: "not a cron expr",
		Run: func(ctx context.Context) error { return nil },
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := sched.Start(ctx); err == nil {
		t.Error("Start() with invalid cron expression expected error, got nil")
	}
}

func TestCatchUpRunsOverdueTask(t *testing.T) {
	sqlDB := openTestDB(t)
	sched, err := New(sqlDB, testLogger(t), "UTC", "1h")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()

	ranCh := make(chan struct{}, 1)
	if err := sched.Register(ctx, TaskDef{
		ID: "catchup_task", Name: "Catchup Task", Schedule: "@daily", Enabled: true,
		Run: func(ctx context.Context) error { ranCh <- struct{}{}; return nil },
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	// Simulate a prior run whose next_run has already passed but is still
	// within the 1h catch-up window, per AI.md PART 18 "Startup Behavior".
	overdueNext := time.Now().Add(-10 * time.Minute)
	if err := db.RecordSchedulerRun(ctx, sqlDB, "catchup_task", time.Now().Add(-24*time.Hour), time.Now().Add(-24*time.Hour), &overdueNext, "success", ""); err != nil {
		t.Fatalf("RecordSchedulerRun() error = %v", err)
	}

	if err := sched.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer sched.Stop()

	select {
	case <-ranCh:
	case <-time.After(5 * time.Second):
		t.Fatal("catch-up did not run the overdue task within 5s")
	}
}
