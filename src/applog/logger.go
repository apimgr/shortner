package applog

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type rotationPolicy struct {
	never        bool
	period       string
	maxBytes     int64
	keepCount    int
	keepDuration time.Duration
	keepForever  bool
	compress     bool
}

func defaultRotationPolicy(path string) rotationPolicy {
	base := filepath.Base(path)
	if base == "access.log" {
		return rotationPolicy{period: "monthly"}
	}
	if base == "audit.log" {
		return rotationPolicy{period: "daily"}
	}
	return rotationPolicy{period: "weekly", maxBytes: 50 * 1024 * 1024}
}

func parseRotationPolicy(rotate, keep string, compress bool) (rotationPolicy, error) {
	policy := rotationPolicy{compress: compress}
	if rotate == "" {
		return policy, fmt.Errorf("applog: empty rotation policy")
	}
	for _, part := range strings.Split(rotate, ",") {
		switch part {
		case "never", "daily", "weekly", "monthly", "yearly":
			if part == "never" {
				policy.never = true
			} else {
				policy.period = part
			}
		default:
			var size int64
			var unit int64
			if strings.HasSuffix(part, "MB") {
				unit = 1024 * 1024
				part = strings.TrimSuffix(part, "MB")
			}
			if strings.HasSuffix(part, "GB") {
				unit = 1024 * 1024 * 1024
				part = strings.TrimSuffix(part, "GB")
			}
			value, err := strconv.ParseInt(part, 10, 64)
			if unit == 0 || err != nil || value <= 0 {
				return policy, fmt.Errorf("applog: invalid rotation rule %q", part)
			}
			size = value * unit
			policy.maxBytes = size
		}
	}
	if policy.never && (policy.period != "" || policy.maxBytes > 0) {
		return policy, fmt.Errorf("applog: never cannot be combined with other rotation rules")
	}
	if keep == "" || keep == "none" {
		return policy, nil
	}
	if keep == "forever" {
		policy.keepForever = true
		return policy, nil
	}
	for _, suffix := range []string{"d", "w", "m"} {
		if strings.HasSuffix(keep, suffix) {
			n, err := strconv.Atoi(strings.TrimSuffix(keep, suffix))
			if err != nil || n < 1 {
				return policy, fmt.Errorf("applog: invalid retention %q", keep)
			}
			switch suffix {
			case "d":
				policy.keepDuration = time.Duration(n) * 24 * time.Hour
			case "w":
				policy.keepDuration = time.Duration(n) * 7 * 24 * time.Hour
			case "m":
				policy.keepDuration = time.Duration(n) * 30 * 24 * time.Hour
			}
			return policy, nil
		}
	}
	n, err := strconv.Atoi(keep)
	if err != nil || n < 1 {
		return policy, fmt.Errorf("applog: invalid retention %q", keep)
	}
	policy.keepCount = n
	return policy, nil
}

func (p rotationPolicy) due(info os.FileInfo, now time.Time) bool {
	if info.Size() == 0 {
		return false
	}
	if p.maxBytes > 0 && info.Size() >= p.maxBytes {
		return true
	}
	modified := info.ModTime().UTC()
	switch p.period {
	case "daily":
		return modified.YearDay() != now.YearDay() || modified.Year() != now.Year()
	case "weekly":
		y1, w1 := modified.ISOWeek()
		y2, w2 := now.ISOWeek()
		return y1 != y2 || w1 != w2
	case "monthly":
		return modified.Month() != now.Month() || modified.Year() != now.Year()
	case "yearly":
		return modified.Year() != now.Year()
	}
	return false
}

// ringCap bounds the in-memory recent-lines buffer used to serve the
// AI.md PART 20 `loki` metrics service without re-reading the log file.
// Sized generously above the spec's default loki.max_entries (1000) so
// a smaller configured max_entries never runs out of buffered history.
const ringCap = 5000

// RingEntry is one buffered log line, used by Recent to serve the `loki`
// metrics service (AI.md PART 20 "Service Semantics" -> loki).
type RingEntry struct {
	Time  time.Time
	Level Level
	Line  string
}

// logFilePerm matches AI.md PART 11 "Audit Log Integrity" -> "File
// Permissions": "audit.log: 0640 (rw-r-----)". Applied to all log files
// this package writes, not just the audit log.
const logFilePerm = 0o640

// Level is a log severity, per AI.md PART 11 "Configuration" ->
// "level: debug, info, warn, error".
type Level int

// Log levels, ordered least to most severe.
const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

// String returns the canonical upper-case level name used in log output.
func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// Logger appends raw-text log lines to a single file, per AI.md PART 11
// "Log Output Rules": "All log FILES MUST use raw text only ... Plain
// ASCII text only." It is safe for concurrent use.
type Logger struct {
	mu       sync.Mutex
	file     *os.File
	path     string
	level    Level
	ring     []RingEntry
	rotation rotationPolicy
}

// Open opens (creating if necessary) the log file at path in append mode
// with 0640 permissions, and returns a Logger that filters lines below
// minLevel.
func Open(path string, minLevel Level) (*Logger, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, logFilePerm)
	if err != nil {
		return nil, fmt.Errorf("applog: open %s: %w", path, err)
	}
	return &Logger{file: f, path: path, level: minLevel, rotation: defaultRotationPolicy(path)}, nil
}

// WriteLine appends a single pre-formatted line (which must already end
// in "\n") to the log file, gated by level. Pass LevelInfo (or the
// Logger's own configured floor) for lines that are not level-gated, such
// as access-log or audit-log entries which have their own severity model.
func (l *Logger) WriteLine(level Level, line string) error {
	if level < l.level {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err := l.file.WriteString(line)
	if err != nil {
		return fmt.Errorf("applog: write %s: %w", l.path, err)
	}
	l.ring = append(l.ring, RingEntry{Time: time.Now(), Level: level, Line: strings.TrimRight(line, "\n")})
	if len(l.ring) > ringCap {
		l.ring = l.ring[len(l.ring)-ringCap:]
	}
	return nil
}

// Recent returns the most recent buffered log entries newer than maxAge
// (or all buffered entries if maxAge <= 0), capped at maxEntries (or
// unbounded if maxEntries <= 0), oldest first. Used to serve the AI.md
// PART 20 `loki` metrics service. The returned slice is a copy safe to
// use without holding any lock.
func (l *Logger) Recent(maxEntries int, maxAge time.Duration) []RingEntry {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := time.Time{}
	if maxAge > 0 {
		cutoff = time.Now().Add(-maxAge)
	}
	var filtered []RingEntry
	for _, e := range l.ring {
		if !cutoff.IsZero() && e.Time.Before(cutoff) {
			continue
		}
		filtered = append(filtered, e)
	}
	if maxEntries > 0 && len(filtered) > maxEntries {
		filtered = filtered[len(filtered)-maxEntries:]
	}
	out := make([]RingEntry, len(filtered))
	copy(out, filtered)
	return out
}

// SetRotationPolicy configures the policy used by RotateIfDue. The accepted
// values follow PART 11: rotate is never/daily/weekly/monthly/yearly, a size
// such as 50MB, or a comma-separated time and size policy; keep is none, N,
// Nd, Nw, Nm, or forever.
func (l *Logger) SetRotationPolicy(rotate, keep string, compress bool) error {
	policy, err := parseRotationPolicy(rotate, keep, compress)
	if err != nil {
		return err
	}
	l.mu.Lock()
	l.rotation = policy
	l.mu.Unlock()
	return nil
}

// RotateIfDue performs scheduled time/size rotation and applies retention.
func (l *Logger) RotateIfDue(now time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rotation.never {
		return nil
	}
	info, err := l.file.Stat()
	if err != nil {
		return fmt.Errorf("applog: stat %s: %w", l.path, err)
	}
	if !l.rotation.due(info, now.UTC()) {
		return nil
	}
	if err := l.rotateLocked(now.UTC()); err != nil {
		return err
	}
	return l.pruneLocked(now.UTC())
}

func (l *Logger) rotateLocked(now time.Time) error {
	if err := l.file.Sync(); err != nil {
		return fmt.Errorf("applog: sync %s: %w", l.path, err)
	}
	if err := l.file.Close(); err != nil {
		return fmt.Errorf("applog: close %s: %w", l.path, err)
	}
	archive := fmt.Sprintf("%s.%s", l.path, now.Format("2006-01-02"))
	if _, err := os.Stat(archive); err == nil {
		archive = fmt.Sprintf("%s.%s", archive, now.Format("150405.000000000"))
	}
	if err := os.Rename(l.path, archive); err != nil {
		file, reopenErr := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, logFilePerm)
		if reopenErr == nil {
			l.file = file
		}
		return fmt.Errorf("applog: archive %s: %w", l.path, err)
	}
	file, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, logFilePerm)
	if err != nil {
		_ = os.Rename(archive, l.path)
		file, reopenErr := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, logFilePerm)
		if reopenErr == nil {
			l.file = file
		}
		return fmt.Errorf("applog: reopen %s: %w", l.path, err)
	}
	l.file = file
	if l.rotation.keepCount == 0 && l.rotation.keepDuration == 0 && !l.rotation.keepForever {
		if err := os.Remove(archive); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("applog: remove unretained archive %s: %w", archive, err)
		}
		return nil
	}
	if l.rotation.compress {
		if err := compressLog(archive); err != nil {
			return err
		}
	}
	return nil
}

func (l *Logger) pruneLocked(now time.Time) error {
	matches, err := filepath.Glob(l.path + ".*")
	if err != nil {
		return err
	}
	archives := make([]os.FileInfo, 0, len(matches))
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() || !isLogArchive(l.path, path) {
			continue
		}
		archives = append(archives, info)
	}
	sort.Slice(archives, func(i, j int) bool { return archives[i].ModTime().After(archives[j].ModTime()) })
	for i, info := range archives {
		path := filepath.Join(filepath.Dir(l.path), info.Name())
		remove := l.rotation.keepCount > 0 && i >= l.rotation.keepCount
		if l.rotation.keepDuration > 0 && now.Sub(info.ModTime()) > l.rotation.keepDuration {
			remove = true
		}
		if !l.rotation.keepForever && l.rotation.keepCount == 0 && l.rotation.keepDuration == 0 {
			remove = true
		}
		if remove {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("applog: prune %s: %w", path, err)
			}
		}
	}
	return nil
}

func isLogArchive(activePath, candidate string) bool {
	name := strings.TrimPrefix(candidate, activePath+".")
	name = strings.TrimSuffix(name, ".gz")
	if len(name) < len("2006-01-02") {
		return false
	}
	if _, err := time.Parse("2006-01-02", name[:len("2006-01-02")]); err != nil {
		return false
	}
	if len(name) == len("2006-01-02") {
		return true
	}
	return name[len("2006-01-02")] == '.'
}

func compressLog(path string) error {
	input, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("applog: open archive %s: %w", path, err)
	}
	defer input.Close()
	outputPath := path + ".gz"
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, logFilePerm)
	if err != nil {
		return fmt.Errorf("applog: create compressed archive %s: %w", outputPath, err)
	}
	writer := gzip.NewWriter(output)
	_, copyErr := io.Copy(writer, input)
	closeErr := writer.Close()
	fileErr := output.Close()
	if copyErr != nil || closeErr != nil || fileErr != nil {
		_ = os.Remove(outputPath)
		return fmt.Errorf("applog: compress %s: %w", path, errors.Join(copyErr, closeErr, fileErr))
	}
	if err := os.Remove(path); err != nil {
		_ = os.Remove(outputPath)
		return fmt.Errorf("applog: remove uncompressed archive %s: %w", path, err)
	}
	return nil
}

// Close closes the underlying file.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}

// Rotate closes and reopens the active path without archiving its contents.
// Scheduled rotation uses RotateIfDue, which applies the configured policy.
func (l *Logger) Rotate() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.file.Close(); err != nil {
		return fmt.Errorf("applog: rotate close %s: %w", l.path, err)
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, logFilePerm)
	if err != nil {
		return fmt.Errorf("applog: rotate reopen %s: %w", l.path, err)
	}
	l.file = f
	return nil
}
