package applog

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

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
	mu    sync.Mutex
	file  *os.File
	path  string
	level Level
	ring  []RingEntry
}

// Open opens (creating if necessary) the log file at path in append mode
// with 0640 permissions, and returns a Logger that filters lines below
// minLevel.
func Open(path string, minLevel Level) (*Logger, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, logFilePerm)
	if err != nil {
		return nil, fmt.Errorf("applog: open %s: %w", path, err)
	}
	return &Logger{file: f, path: path, level: minLevel}, nil
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

// Close closes the underlying file.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}

// Rotate closes the current file and reopens path fresh, without
// renaming or compressing the prior contents. Time/size-triggered
// scheduled rotation (AI.md PART 11 "Rotation Options") requires the
// scheduler defined in AI.md PART 18, which does not exist yet — callers
// that need scheduled rotation must invoke this manually until then
// (tracked in TODO.AI.md).
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
