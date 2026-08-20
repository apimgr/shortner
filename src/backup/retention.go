package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/disk"
)

// Tier is the retention class a backup file was assigned, per AI.md
// PART 21 "Retention Priority Order".
type Tier string

// Retention tiers, highest priority first. TierIncremental covers the
// always-exactly-one daily/hourly archives, which PART 21 excludes from
// count-based retention entirely ("Daily incremental is NOT counted").
const (
	TierYearly      Tier = "yearly"
	TierMonthly     Tier = "monthly"
	TierWeekly      Tier = "weekly"
	TierDaily       Tier = "daily"
	TierIncremental Tier = "incremental"
	// TierExpired marks a file selected for deletion.
	TierExpired Tier = "expired"
)

// File is one backup archive found in the backup directory.
type File struct {
	Name string
	Path string
	Size int64
	Kind Kind
	// Date is the calendar date the backup represents: parsed from the
	// filename when the name carries one, else the file's modification
	// time.
	Date time.Time
	Tier Tier
}

// Policy is the resolved `server.backup.retention` block. MaxTotalBytes is
// the already-resolved absolute cap (a "10%" config value is turned into
// bytes by the caller, which knows the backup volume's size); 0 disables
// the cap, per AI.md PART 21.
type Policy struct {
	MaxBackups    int
	KeepWeekly    int
	KeepMonthly   int
	KeepYearly    int
	MaxTotalBytes int64
}

// List returns every backup archive in dir that this app could have
// created, oldest first. Per AI.md PART 21 "Backup Cleanup Logic",
// anything matching the app's naming is in scope for pruning — nothing in
// the backup directory matching those patterns is exempt.
func List(dir, prefix string) ([]File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("backup: read backup dir %s: %w", dir, err)
	}

	var files []File
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		kind, date, ok := parseBackupName(prefix, e.Name(), info.ModTime())
		if !ok {
			continue
		}
		files = append(files, File{
			Name: e.Name(),
			Path: filepath.Join(dir, e.Name()),
			Size: info.Size(),
			Kind: kind,
			Date: date,
		})
	}

	sort.Slice(files, func(i, j int) bool {
		if files[i].Date.Equal(files[j].Date) {
			return files[i].Name < files[j].Name
		}
		return files[i].Date.Before(files[j].Date)
	})
	return files, nil
}

// parseBackupName classifies one filename against AI.md PART 21's naming
// patterns. The final fallback is deliberate: "Any file matching the
// {project_name}_backup_* or {project_name}-*.tar.gz* prefix that is not
// otherwise classified is treated as a daily backup for retention
// purposes", dated by its mtime since the name carries no date.
func parseBackupName(prefix, name string, modTime time.Time) (Kind, time.Time, bool) {
	base := strings.TrimSuffix(name, EncryptedSuffix)
	if !strings.HasSuffix(base, ".tar.gz") {
		return "", time.Time{}, false
	}
	base = strings.TrimSuffix(base, ".tar.gz")

	switch base {
	case prefix + "-daily":
		return KindDaily, modTime.UTC(), true
	case prefix + "-hourly":
		return KindHourly, modTime.UTC(), true
	}

	if stamp, ok := strings.CutPrefix(base, prefix+"_backup_"); ok {
		if t, err := time.Parse("2006-01-02_150405", stamp); err == nil {
			return KindManual, t.UTC(), true
		}
		if t, err := time.Parse("2006-01-02", stamp); err == nil {
			return KindFull, t.UTC(), true
		}
		return KindFull, modTime.UTC(), true
	}
	if strings.HasPrefix(base, prefix+"-") {
		return KindFull, modTime.UTC(), true
	}
	return "", time.Time{}, false
}

// Plan assigns every file a retention tier and returns the files to keep
// and the files to delete, implementing AI.md PART 21 "Backup Cleanup
// Logic" steps 1-8 exactly: yearly, then monthly, then weekly, then daily,
// each tier consuming its slots newest-first, a file counting only toward
// the highest tier it qualifies for, and the optional size cap applied
// last so it overrides every count limit.
//
// Plan is pure — it touches no filesystem — so the classification can be
// exercised directly by tests.
func Plan(files []File, p Policy) (keep []File, remove []File) {
	ordered := make([]File, len(files))
	copy(ordered, files)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Date.Equal(ordered[j].Date) {
			return ordered[i].Name < ordered[j].Name
		}
		return ordered[i].Date.Before(ordered[j].Date)
	})

	assigned := make([]Tier, len(ordered))
	for i := range ordered {
		if ordered[i].Kind.Incremental() {
			assigned[i] = TierIncremental
		}
	}

	tiers := []struct {
		tier    Tier
		slots   int
		matches func(time.Time) bool
	}{
		{TierYearly, p.KeepYearly, func(t time.Time) bool { return t.Month() == time.January && t.Day() == 1 }},
		{TierMonthly, p.KeepMonthly, func(t time.Time) bool { return t.Day() == 1 }},
		{TierWeekly, p.KeepWeekly, func(t time.Time) bool { return t.Weekday() == time.Sunday }},
		{TierDaily, p.MaxBackups, func(time.Time) bool { return true }},
	}

	for _, tier := range tiers {
		if tier.slots <= 0 {
			continue
		}
		filled := 0
		// Newest first: the most recent qualifying backup takes the slot.
		for i := len(ordered) - 1; i >= 0 && filled < tier.slots; i-- {
			if assigned[i] != "" || !tier.matches(ordered[i].Date) {
				continue
			}
			assigned[i] = tier.tier
			filled++
		}
	}

	for i := range ordered {
		if assigned[i] == "" {
			assigned[i] = TierExpired
		}
		ordered[i].Tier = assigned[i]
	}

	for i := range ordered {
		if ordered[i].Tier == TierExpired {
			remove = append(remove, ordered[i])
		} else {
			keep = append(keep, ordered[i])
		}
	}

	keep, overCap := applySizeCap(keep, p.MaxTotalBytes)
	remove = append(remove, overCap...)
	sort.Slice(remove, func(i, j int) bool { return remove[i].Date.Before(remove[j].Date) })
	return keep, remove
}

// applySizeCap implements AI.md PART 21 "Backup Cleanup Logic" step 8:
// after count-based pruning, keep deleting the oldest files until the
// total is under the cap. Incrementals are excluded from this sweep —
// they are replaced in place on the very next run, so deleting them frees
// nothing durable while destroying the newest recoverable state.
func applySizeCap(keep []File, maxTotal int64) ([]File, []File) {
	if maxTotal <= 0 {
		return keep, nil
	}

	total := int64(0)
	for _, f := range keep {
		total += f.Size
	}
	if total <= maxTotal {
		return keep, nil
	}

	var kept, removed []File
	// Oldest first; keep is already ordered oldest-first by Plan.
	for i, f := range keep {
		if total > maxTotal && !f.Kind.Incremental() && i < len(keep)-1 {
			total -= f.Size
			removed = append(removed, f)
			continue
		}
		kept = append(kept, f)
	}
	return kept, removed
}

// Apply runs the retention sweep over dir and deletes every expired file,
// returning what was deleted. AI.md PART 21 requires this to run at
// startup and after every successful backup — and never before a backup is
// verified, so a bad new backup can never cost a good old one.
func Apply(dir, prefix string, p Policy) ([]File, error) {
	files, err := List(dir, prefix)
	if err != nil {
		return nil, err
	}
	_, remove := Plan(files, p)

	var deleted []File
	for _, f := range remove {
		if err := os.Remove(f.Path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return deleted, fmt.Errorf("backup: delete %s: %w", f.Name, err)
		}
		deleted = append(deleted, f)
	}
	return deleted, nil
}

// TotalSize sums the on-disk size of every backup file.
func TotalSize(files []File) int64 {
	var total int64
	for _, f := range files {
		total += f.Size
	}
	return total
}

// Newest returns the most recent non-incremental backup, or nil when the
// directory holds none.
func Newest(files []File) *File {
	var newest *File
	for i := range files {
		if files[i].Kind.Incremental() {
			continue
		}
		if newest == nil || files[i].Date.After(newest.Date) {
			newest = &files[i]
		}
	}
	return newest
}

// DiskStatus is the backup volume's free/total space and usage percent.
type DiskStatus struct {
	FreeBytes   uint64
	TotalBytes  uint64
	UsedPercent float64
}

// DiskFullError reports that AI.md PART 21 "Backup Creation Flow" step 2
// aborted the run. Its fields are exactly the data the
// `backup.skipped_disk_full` audit event must carry.
type DiskFullError struct {
	FreeBytes     uint64
	UsedPercent   float64
	Threshold     int
	RequiredBytes int64
}

func (e *DiskFullError) Error() string {
	if e.RequiredBytes > 0 && uint64(e.RequiredBytes) > e.FreeBytes {
		return fmt.Sprintf("backup: skipped — %d bytes free, %d required (2x the most recent backup)", e.FreeBytes, e.RequiredBytes)
	}
	return fmt.Sprintf("backup: skipped — disk usage %.1f%% exceeds threshold %d%%", e.UsedPercent, e.Threshold)
}

// Disk reports free/total space for the filesystem holding dir.
func Disk(dir string) (DiskStatus, error) {
	usage, err := disk.Usage(dir)
	if err != nil {
		return DiskStatus{}, fmt.Errorf("backup: disk usage for %s: %w", dir, err)
	}
	return DiskStatus{
		FreeBytes:   usage.Free,
		TotalBytes:  usage.Total,
		UsedPercent: usage.UsedPercent,
	}, nil
}

// CheckDiskSpace implements AI.md PART 21 "Backup Creation Flow" step 2:
// abort when free space is under twice the most recent backup's size, or
// when disk usage exceeds the configured threshold.
func CheckDiskSpace(status DiskStatus, lastBackupSize int64, thresholdPercent int) error {
	required := lastBackupSize * 2
	if required > 0 && status.FreeBytes < uint64(required) {
		return &DiskFullError{
			FreeBytes:     status.FreeBytes,
			UsedPercent:   status.UsedPercent,
			Threshold:     thresholdPercent,
			RequiredBytes: required,
		}
	}
	if thresholdPercent > 0 && status.UsedPercent > float64(thresholdPercent) {
		return &DiskFullError{
			FreeBytes:   status.FreeBytes,
			UsedPercent: status.UsedPercent,
			Threshold:   thresholdPercent,
		}
	}
	return nil
}

// ResolveMaxTotalBytes turns a percent-of-volume cap into an absolute byte
// count. percent <= 0 means the cap was configured as an absolute size (or
// disabled) and absolute is returned unchanged.
func ResolveMaxTotalBytes(percent int, absolute int64, status DiskStatus) int64 {
	if percent > 0 {
		return int64(status.TotalBytes) * int64(percent) / 100
	}
	return absolute
}
