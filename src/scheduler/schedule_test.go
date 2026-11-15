package scheduler

import (
	"testing"
	"time"
)

func TestNextScheduledUsesConfiguredTimezoneAndSteps(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 4, 2, 10, 1, 12, 0, loc)
	got := nextScheduled("*/5 * * * *", from, loc)
	want := time.Date(2026, 4, 2, 10, 5, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("nextScheduled() = %s, want %s", got, want)
	}
}

func TestNextScheduledInterval(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := nextScheduled("@every 15m", from, time.UTC)
	if want := from.Add(15 * time.Minute); !got.Equal(want) {
		t.Fatalf("nextScheduled() = %s, want %s", got, want)
	}
}
