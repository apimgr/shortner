package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestParseBackupName(t *testing.T) {
	mod := day(2025, time.June, 6)
	cases := []struct {
		name     string
		wantKind Kind
		wantDate time.Time
		wantOK   bool
	}{
		{"shortner_backup_2025-01-15.tar.gz", KindFull, day(2025, time.January, 15), true},
		{"shortner_backup_2025-01-15.tar.gz.enc", KindFull, day(2025, time.January, 15), true},
		{"shortner_backup_2025-01-15_103045.tar.gz", KindManual, time.Date(2025, time.January, 15, 10, 30, 45, 0, time.UTC), true},
		{"shortner-daily.tar.gz", KindDaily, mod, true},
		{"shortner-hourly.tar.gz.enc", KindHourly, mod, true},
		// Unclassified but app-prefixed files are treated as daily fulls.
		{"shortner-weekly-manual.tar.gz", KindFull, mod, true},
		{"shortner_backup_not-a-date.tar.gz", KindFull, mod, true},
		// Foreign files are never touched.
		{"other-app_backup_2025-01-15.tar.gz", "", time.Time{}, false},
		{"shortner_backup_2025-01-15.zip", "", time.Time{}, false},
		{"notes.txt", "", time.Time{}, false},
	}

	for _, c := range cases {
		kind, date, ok := parseBackupName("shortner", c.name, mod)
		if ok != c.wantOK {
			t.Errorf("parseBackupName(%q) ok = %t, want %t", c.name, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if kind != c.wantKind {
			t.Errorf("parseBackupName(%q) kind = %q, want %q", c.name, kind, c.wantKind)
		}
		if !date.Equal(c.wantDate) {
			t.Errorf("parseBackupName(%q) date = %s, want %s", c.name, date, c.wantDate)
		}
	}
}

// planFile builds a File for the pure-classification tests.
func planFile(name string, kind Kind, date time.Time, size int64) File {
	return File{Name: name, Path: "/backups/" + name, Size: size, Kind: kind, Date: date}
}

func names(files []File) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Name)
	}
	return out
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestPlanDefaultRetentionKeepsOneFullPlusIncremental(t *testing.T) {
	// AI.md PART 21 "Example: Default settings (max=1, weekly=0,
	// monthly=0, yearly=0)" — two files remain: yesterday's full plus the
	// incremental.
	files := []File{
		planFile("shortner_backup_2025-01-13.tar.gz", KindFull, day(2025, time.January, 13), 10),
		planFile("shortner_backup_2025-01-14.tar.gz", KindFull, day(2025, time.January, 14), 10),
		planFile("shortner_backup_2025-01-15.tar.gz", KindFull, day(2025, time.January, 15), 10),
		planFile("shortner-daily.tar.gz", KindDaily, day(2025, time.January, 16), 2),
	}

	keep, remove := Plan(files, Policy{MaxBackups: 1})
	keptNames := names(keep)
	if len(keep) != 2 {
		t.Fatalf("keep = %v, want the newest full plus the incremental", keptNames)
	}
	if !contains(keptNames, "shortner_backup_2025-01-15.tar.gz") || !contains(keptNames, "shortner-daily.tar.gz") {
		t.Errorf("keep = %v, want the newest full and the daily incremental", keptNames)
	}
	if len(remove) != 2 {
		t.Errorf("remove = %v, want the two oldest fulls", names(remove))
	}
}

func TestPlanTierPriority(t *testing.T) {
	// AI.md PART 21 "Example: Keep 1 weekly + 1 monthly + 1 yearly" — a
	// file counts only toward the highest tier it qualifies for.
	files := []File{
		// Jan 1 2025 is a Wednesday: yearly + monthly, not weekly.
		planFile("shortner_backup_2025-01-01.tar.gz", KindFull, day(2025, time.January, 1), 10),
		// Dec 1 2024 is a Sunday: monthly + weekly.
		planFile("shortner_backup_2024-12-01.tar.gz", KindFull, day(2024, time.December, 1), 10),
		// Jan 12 2025 is a Sunday: weekly.
		planFile("shortner_backup_2025-01-12.tar.gz", KindFull, day(2025, time.January, 12), 10),
		planFile("shortner_backup_2025-01-15.tar.gz", KindFull, day(2025, time.January, 15), 10),
		planFile("shortner_backup_2025-01-14.tar.gz", KindFull, day(2025, time.January, 14), 10),
		planFile("shortner-daily.tar.gz", KindDaily, day(2025, time.January, 16), 2),
	}

	keep, remove := Plan(files, Policy{MaxBackups: 1, KeepWeekly: 1, KeepMonthly: 1, KeepYearly: 1})
	keptNames := names(keep)

	for _, want := range []string{
		// Yearly.
		"shortner_backup_2025-01-01.tar.gz",
		// Monthly (Jan 1 is already spent on yearly).
		"shortner_backup_2024-12-01.tar.gz",
		// Weekly.
		"shortner_backup_2025-01-12.tar.gz",
		// Daily.
		"shortner_backup_2025-01-15.tar.gz",
		// Incremental, never counted.
		"shortner-daily.tar.gz",
	} {
		if !contains(keptNames, want) {
			t.Errorf("keep = %v, missing %q", keptNames, want)
		}
	}
	if len(keep) != 5 {
		t.Errorf("keep = %v, want exactly 5 files", keptNames)
	}
	if len(remove) != 1 || remove[0].Name != "shortner_backup_2025-01-14.tar.gz" {
		t.Errorf("remove = %v, want only the surplus daily", names(remove))
	}
}

func TestPlanIncrementalsAreNeverCounted(t *testing.T) {
	files := []File{
		planFile("shortner-daily.tar.gz", KindDaily, day(2025, time.January, 16), 5),
		planFile("shortner-hourly.tar.gz", KindHourly, day(2025, time.January, 16), 5),
	}
	keep, remove := Plan(files, Policy{MaxBackups: 1})
	if len(keep) != 2 || len(remove) != 0 {
		t.Fatalf("keep = %v, remove = %v, want both incrementals kept", names(keep), names(remove))
	}
	for _, f := range keep {
		if f.Tier != TierIncremental {
			t.Errorf("%s tier = %q, want incremental", f.Name, f.Tier)
		}
	}
}

func TestPlanSizeCapOverridesCountLimits(t *testing.T) {
	// AI.md PART 21 step 8: the size cap is applied after count-based
	// pruning and overrides every count limit.
	files := []File{
		planFile("shortner_backup_2025-01-13.tar.gz", KindFull, day(2025, time.January, 13), 100),
		planFile("shortner_backup_2025-01-14.tar.gz", KindFull, day(2025, time.January, 14), 100),
		planFile("shortner_backup_2025-01-15.tar.gz", KindFull, day(2025, time.January, 15), 100),
	}

	keep, remove := Plan(files, Policy{MaxBackups: 3, MaxTotalBytes: 150})
	if len(keep) != 1 || keep[0].Name != "shortner_backup_2025-01-15.tar.gz" {
		t.Fatalf("keep = %v, want only the newest full under the cap", names(keep))
	}
	if len(remove) != 2 {
		t.Fatalf("remove = %v, want the two oldest deleted for size", names(remove))
	}
}

func TestPlanSizeCapNeverDeletesTheLastBackup(t *testing.T) {
	files := []File{
		planFile("shortner_backup_2025-01-15.tar.gz", KindFull, day(2025, time.January, 15), 1000),
	}
	keep, remove := Plan(files, Policy{MaxBackups: 1, MaxTotalBytes: 1})
	if len(keep) != 1 || len(remove) != 0 {
		t.Fatalf("keep = %v, remove = %v, want the only backup kept", names(keep), names(remove))
	}
}

func TestListAndApply(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"shortner_backup_2025-01-13.tar.gz",
		"shortner_backup_2025-01-15.tar.gz",
		"shortner-daily.tar.gz",
		"unrelated.txt",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("payload"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	files, err := List(dir, "shortner")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("List returned %v, want the 3 app-owned archives", names(files))
	}

	deleted, err := Apply(dir, "shortner", Policy{MaxBackups: 1})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(deleted) != 1 || deleted[0].Name != "shortner_backup_2025-01-13.tar.gz" {
		t.Fatalf("deleted = %v, want the oldest full", names(deleted))
	}
	if _, err := os.Stat(filepath.Join(dir, "unrelated.txt")); err != nil {
		t.Errorf("Apply removed an unrelated file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "shortner_backup_2025-01-13.tar.gz")); !os.IsNotExist(err) {
		t.Errorf("expired backup still on disk: %v", err)
	}
}

func TestListMissingDirIsNotAnError(t *testing.T) {
	files, err := List(filepath.Join(t.TempDir(), "nope"), "shortner")
	if err != nil {
		t.Fatalf("List of a missing dir: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("List = %v, want empty", names(files))
	}
}

func TestTotalSizeAndNewest(t *testing.T) {
	files := []File{
		planFile("shortner_backup_2025-01-13.tar.gz", KindFull, day(2025, time.January, 13), 10),
		planFile("shortner_backup_2025-01-15.tar.gz", KindFull, day(2025, time.January, 15), 20),
		planFile("shortner-daily.tar.gz", KindDaily, day(2025, time.January, 16), 5),
	}
	if got := TotalSize(files); got != 35 {
		t.Errorf("TotalSize = %d, want 35", got)
	}
	newest := Newest(files)
	if newest == nil || newest.Name != "shortner_backup_2025-01-15.tar.gz" {
		t.Errorf("Newest = %v, want the newest non-incremental", newest)
	}
	if Newest(nil) != nil {
		t.Error("Newest(nil) should be nil")
	}
}

func TestCheckDiskSpace(t *testing.T) {
	// Free space below twice the most recent backup aborts the run.
	status := DiskStatus{FreeBytes: 100, TotalBytes: 1000, UsedPercent: 90}
	err := CheckDiskSpace(status, 60, 95)
	if err == nil {
		t.Fatal("CheckDiskSpace accepted insufficient free space")
	}

	// Disk usage over the threshold aborts too.
	status = DiskStatus{FreeBytes: 10_000, TotalBytes: 100_000, UsedPercent: 95}
	if err := CheckDiskSpace(status, 10, 90); err == nil {
		t.Fatal("CheckDiskSpace accepted a disk over the usage threshold")
	}

	// Plenty of room, well under threshold.
	if err := CheckDiskSpace(status, 10, 99); err != nil {
		t.Fatalf("CheckDiskSpace rejected a healthy disk: %v", err)
	}
}

func TestResolveMaxTotalBytes(t *testing.T) {
	status := DiskStatus{TotalBytes: 1000}
	if got := ResolveMaxTotalBytes(10, 0, status); got != 100 {
		t.Errorf("ResolveMaxTotalBytes(10%%) = %d, want 100", got)
	}
	if got := ResolveMaxTotalBytes(0, 4096, status); got != 4096 {
		t.Errorf("ResolveMaxTotalBytes(absolute) = %d, want 4096", got)
	}
}

func TestDiskReadsTheRealVolume(t *testing.T) {
	status, err := Disk(t.TempDir())
	if err != nil {
		t.Fatalf("Disk: %v", err)
	}
	if status.TotalBytes == 0 {
		t.Error("TotalBytes = 0, want the real volume size")
	}
}
