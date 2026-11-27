package scheduler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/db"
)

type offlineTestHandle struct{ db.Handle }

func (offlineTestHandle) Offline() bool { return true }

func TestSchedulerStartsAndRegistersDuringDatabaseMaintenance(t *testing.T) {
	sched, err := New(offlineTestHandle{}, nil, "UTC", "1h")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := sched.Register(context.Background(), TaskDef{
		ID: "maintenance_test", Name: "Maintenance test", Schedule: "0 * * * *", Enabled: true,
		Run: func(context.Context) error { return nil },
	}); err != nil {
		t.Fatalf("Register() while offline error = %v", err)
	}
	if err := sched.Start(context.Background()); err != nil {
		t.Fatalf("Start() while offline error = %v", err)
	}
	if !sched.Healthy() {
		t.Fatal("scheduler is not healthy while registered in memory during maintenance")
	}
	if err := sched.Stop(); err != nil {
		t.Fatalf("Stop() while offline error = %v", err)
	}
}

func openTestDB(t *testing.T) *db.Store {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	data, err := os.ReadFile(filepath.Join(root, "IDEA.md"))
	if err != nil {
		t.Fatal(err)
	}
	var projectOrg, internalName string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "project_org:") {
			projectOrg = strings.TrimSpace(strings.TrimPrefix(line, "project_org:"))
		}
		if strings.HasPrefix(line, "internal_name:") {
			internalName = strings.TrimSpace(strings.TrimPrefix(line, "internal_name:"))
		}
	}
	if projectOrg == "" || internalName == "" {
		t.Fatal("IDEA.md is missing test temp namespace variables")
	}
	base := filepath.Join(os.TempDir(), projectOrg)
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(base, internalName+"-")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(filepath.Join(dir, "server.db"), db.DefaultPool(), nil)
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("db.Close() error = %v", err)
		}
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("os.RemoveAll(%q) error = %v", dir, err)
		}
	})
	return sqlDB
}

func testLogger(t *testing.T) *applog.Logger {
	t.Helper()
	l, err := applog.Open(t.TempDir()+"/test.log", applog.LevelInfo)
	if err != nil {
		t.Fatalf("applog.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := l.Close(); err != nil {
			t.Errorf("Logger.Close() error = %v", err)
		}
	})
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
	t.Cleanup(func() {
		if err := sched.Stop(); err != nil {
			t.Errorf("Scheduler.Stop() error = %v", err)
		}
	})

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

func TestStartImmediatelyRetriesPersistedInterruption(t *testing.T) {
	sqlDB := openTestDB(t)
	sched, err := New(sqlDB, testLogger(t), "UTC", "1h")
	if err != nil {
		t.Fatal(err)
	}
	run := make(chan struct{}, 1)
	ctx := context.Background()
	if err := sched.Register(ctx, TaskDef{
		ID: "interrupted_task", Name: "Interrupted Task", Schedule: "@every 1h", Enabled: true,
		Run: func(context.Context) error { run <- struct{}{}; return nil },
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkSchedulerTaskRunning(ctx, sqlDB, "interrupted_task", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := sched.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sched.Stop(); err != nil {
			t.Errorf("Scheduler.Stop() error = %v", err)
		}
	})
	select {
	case <-run:
	case <-time.After(2 * time.Second):
		t.Fatal("interrupted task was not retried immediately on start")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state, err := sched.Show(ctx, "interrupted_task")
		if err != nil {
			t.Fatal(err)
		}
		if state.LastStatus == "success" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("retried task did not persist success")
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
	t.Cleanup(func() {
		if err := sched.Stop(); err != nil {
			t.Errorf("Scheduler.Stop() error = %v", err)
		}
	})

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

func TestRegisterRejectsInvalidSchedule(t *testing.T) {
	sqlDB := openTestDB(t)
	sched, err := New(sqlDB, testLogger(t), "UTC", "1h")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	if err := sched.Register(ctx, TaskDef{
		ID: "bad_schedule", Name: "Bad Schedule", Schedule: "not a cron expr",
		Run: func(ctx context.Context) error { return nil },
	}); err == nil {
		t.Fatal("Register() with invalid cron expression expected error, got nil")
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
	t.Cleanup(func() {
		if err := sched.Stop(); err != nil {
			t.Errorf("Scheduler.Stop() error = %v", err)
		}
	})

	select {
	case <-ranCh:
	case <-time.After(5 * time.Second):
		t.Fatal("catch-up did not run the overdue task within 5s")
	}
}
