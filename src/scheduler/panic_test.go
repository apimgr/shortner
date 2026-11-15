package scheduler

import (
	"context"
	"testing"
)

func TestRunNowRecoversTaskPanicAndRecordsFailure(t *testing.T) {
	sqlDB := openTestDB(t)
	sched, err := New(sqlDB, testLogger(t), "UTC", "1h")
	if err != nil {
		t.Fatal(err)
	}
	if err := sched.Register(context.Background(), TaskDef{
		ID: "panic_task", Name: "Panic Task", Schedule: "@daily", Enabled: true,
		Run: func(context.Context) error { panic("test panic") },
	}); err != nil {
		t.Fatal(err)
	}
	if err := sched.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer sched.Stop()
	if err := sched.RunNow(context.Background(), "panic_task"); err == nil {
		t.Fatal("RunNow() returned nil for panicking task")
	}
	row, err := sched.Show(context.Background(), "panic_task")
	if err != nil {
		t.Fatal(err)
	}
	if row.LastStatus != "failed" || row.FailCount != 1 {
		t.Fatalf("task state = status %q, failures %d; want failed/1", row.LastStatus, row.FailCount)
	}
	if row.LastError == "" {
		t.Fatal("task failure has no error detail")
	}
}
