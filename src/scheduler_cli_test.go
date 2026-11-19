package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/scheduler"
)

func testSchedulerDB(t *testing.T) *db.Store {
	t.Helper()
	projectOrg, internalName := testProjectTempNamespace(t)
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
	t.Cleanup(func() { _ = sqlDB.Close(); _ = os.RemoveAll(dir) })
	return sqlDB
}

func testProjectTempNamespace(t *testing.T) (string, string) {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), ".."))
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
	return projectOrg, internalName
}

func testSchedulerLogger(t *testing.T) *applog.Logger {
	t.Helper()
	l, err := applog.Open(t.TempDir()+"/scheduler.log", applog.LevelInfo)
	if err != nil {
		t.Fatalf("applog.Open() error = %v", err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

// testScheduler builds a Scheduler with one registered, started task ("t1")
// for exercising the --scheduler CLI dispatch.
func testScheduler(t *testing.T) *scheduler.Scheduler {
	t.Helper()
	sqlDB := testSchedulerDB(t)
	sched, err := scheduler.New(sqlDB, testSchedulerLogger(t), "UTC", "1h")
	if err != nil {
		t.Fatalf("scheduler.New() error = %v", err)
	}
	ctx := context.Background()
	err = sched.Register(ctx, scheduler.TaskDef{
		ID: "t1", Name: "Test Task", Schedule: "@daily", Enabled: true,
		Run: func(ctx context.Context) error { return nil },
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := sched.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { sched.Stop() })
	return sched
}

func TestRunSchedulerCLIHelp(t *testing.T) {
	sched := testScheduler(t)
	for _, cmd := range []string{"", "help", "--help", "-h"} {
		out, _, code := captureOutput(t, func() int { return runSchedulerCLI("shortner", sched, cmd, "") })
		if code != 0 {
			t.Errorf("cmd=%q code = %d, want 0", cmd, code)
		}
		if !strings.Contains(out, "Manage the built-in background task scheduler") {
			t.Errorf("cmd=%q output = %q, want scheduler help text", cmd, out)
		}
	}
}

func TestRunSchedulerCLIUnknownCommand(t *testing.T) {
	sched := testScheduler(t)
	_, stderr, code := captureOutput(t, func() int { return runSchedulerCLI("shortner", sched, "bogus", "") })
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr, `unknown --scheduler command "bogus"`) {
		t.Errorf("stderr = %q, want unknown-command message", stderr)
	}
}

func TestRunSchedulerCLIList(t *testing.T) {
	sched := testScheduler(t)
	out, _, code := captureOutput(t, func() int { return runSchedulerCLI("shortner", sched, "list", "") })
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !strings.Contains(out, "Test Task") {
		t.Errorf("output = %q, want to contain task name", out)
	}
}

func TestRunSchedulerCLIShow(t *testing.T) {
	sched := testScheduler(t)
	out, _, code := captureOutput(t, func() int { return runSchedulerCLI("shortner", sched, "show", "t1") })
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !strings.Contains(out, "Test Task") {
		t.Errorf("output = %q, want task detail", out)
	}

	_, stderr, code := captureOutput(t, func() int { return runSchedulerCLI("shortner", sched, "show", "") })
	if code != 2 {
		t.Errorf("code = %d, want 2 (missing id)", code)
	}
	if !strings.Contains(stderr, "requires a task ID") {
		t.Errorf("stderr = %q, want missing-id message", stderr)
	}

	_, stderr, code = captureOutput(t, func() int { return runSchedulerCLI("shortner", sched, "show", "no_such") })
	if code != 1 {
		t.Errorf("code = %d, want 1 (unknown task)", code)
	}
	if !strings.Contains(stderr, "not found") {
		t.Errorf("stderr = %q, want a not-found message", stderr)
	}
}

func TestRunSchedulerCLIRun(t *testing.T) {
	sched := testScheduler(t)
	out, _, code := captureOutput(t, func() int { return runSchedulerCLI("shortner", sched, "run", "t1") })
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !strings.Contains(out, "completed successfully") {
		t.Errorf("output = %q, want success message", out)
	}

	_, stderr, code := captureOutput(t, func() int { return runSchedulerCLI("shortner", sched, "run", "no_such") })
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "task failed") {
		t.Errorf("stderr = %q, want failure message", stderr)
	}
}

func TestRunSchedulerCLIEnableDisable(t *testing.T) {
	sched := testScheduler(t)
	out, _, code := captureOutput(t, func() int { return runSchedulerCLI("shortner", sched, "disable", "t1") })
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !strings.Contains(out, "disabled") {
		t.Errorf("output = %q, want disabled message", out)
	}

	out, _, code = captureOutput(t, func() int { return runSchedulerCLI("shortner", sched, "enable", "t1") })
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !strings.Contains(out, "enabled") {
		t.Errorf("output = %q, want enabled message", out)
	}

	_, stderr, code := captureOutput(t, func() int { return runSchedulerCLI("shortner", sched, "enable", "") })
	if code != 2 {
		t.Errorf("code = %d, want 2 (missing id)", code)
	}
	if !strings.Contains(stderr, "requires a task ID") {
		t.Errorf("stderr = %q, want missing-id message", stderr)
	}
}

func TestRunSchedulerCLIHistory(t *testing.T) {
	sched := testScheduler(t)
	ctx := context.Background()
	if err := sched.RunNow(ctx, "t1"); err != nil {
		t.Fatalf("RunNow() error = %v", err)
	}

	out, _, code := captureOutput(t, func() int { return runSchedulerCLI("shortner", sched, "history", "t1") })
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !strings.Contains(out, "success") {
		t.Errorf("output = %q, want a success row", out)
	}

	_, stderr, code := captureOutput(t, func() int { return runSchedulerCLI("shortner", sched, "history", "") })
	if code != 2 {
		t.Errorf("code = %d, want 2 (missing id)", code)
	}
	if !strings.Contains(stderr, "requires a task ID") {
		t.Errorf("stderr = %q, want missing-id message", stderr)
	}

	out, _, code = captureOutput(t, func() int { return runSchedulerCLI("shortner", sched, "history", "empty_task") })
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !strings.Contains(out, "no history") {
		t.Errorf("output = %q, want no-history message", out)
	}
}
