package applog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Severity is an audit-entry severity, per AI.md PART 11 "Severity Levels".
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarn     Severity = "warn"
	SeverityError    Severity = "error"
	SeverityCritical Severity = "critical"
)

// Result is the outcome of an audited action.
type Result string

const (
	ResultSuccess Result = "success"
	ResultFailure Result = "failure"
)

// Actor identifies who performed an audited action.
type Actor struct {
	Type   string `json:"type,omitempty"`
	ID     string `json:"id,omitempty"`
	IP     string `json:"ip,omitempty"`
	UserID string `json:"-"`
}

func (a *Actor) UnmarshalJSON(data []byte) error {
	var value struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		IP   string `json:"ip"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	a.Type, a.ID, a.UserID, a.IP = value.Type, value.ID, value.ID, value.IP
	return nil
}

// Target identifies what an audited action acted upon.
type Target struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Entry is one audit-log record.
type Entry struct {
	ID       string         `json:"id"`
	Time     time.Time      `json:"time"`
	Event    string         `json:"event"`
	Category string         `json:"category"`
	Severity Severity       `json:"severity"`
	Actor    Actor          `json:"actor"`
	Target   *Target        `json:"target,omitempty"`
	Details  map[string]any `json:"details,omitempty"`
	Reason   string         `json:"reason,omitempty"`
	Source   string         `json:"source,omitempty"`
	Result   Result         `json:"result"`
	Prev     string         `json:"prev"`
}

type auditEntryJSON struct {
	ID       string         `json:"id"`
	Time     string         `json:"time"`
	Event    string         `json:"event"`
	Category string         `json:"category"`
	Severity Severity       `json:"severity"`
	Actor    Actor          `json:"actor"`
	Target   *Target        `json:"target,omitempty"`
	Details  map[string]any `json:"details,omitempty"`
	Reason   string         `json:"reason,omitempty"`
	Source   string         `json:"source,omitempty"`
	Result   Result         `json:"result"`
	Prev     string         `json:"prev"`
}

const zeroHash = "0000000000000000000000000000000000000000000000000000000000000000"

// AuditLogger appends JSON Lines to an audit file and serializes its hash chain.
type AuditLogger struct {
	logger   *Logger
	path     string
	mu       sync.Mutex
	prev     string
	broken   string
	line     int
	reported string
	mirror   func(Entry) error
}

// SetMirror installs a best-effort database mirror for subsequent events.
func (a *AuditLogger) SetMirror(mirror func(Entry) error) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.mirror = mirror
	a.mu.Unlock()
}

// SetRotationPolicy configures the audit file's rotation and retention.
func (a *AuditLogger) SetRotationPolicy(rotate, keep string, compress bool) error {
	if a == nil || a.logger == nil {
		return nil
	}
	return a.logger.SetRotationPolicy(rotate, keep, compress)
}

// NewAuditLogger opens the append-only log and verifies its active hash chain.
func NewAuditLogger(path string) (*AuditLogger, error) {
	logger, err := Open(path, LevelInfo)
	if err != nil {
		return nil, err
	}
	a := &AuditLogger{logger: logger, path: path, prev: zeroHash}
	if err := a.loadChain(); err != nil {
		_ = logger.Close()
		return nil, err
	}
	if a.broken != "" && a.reported != a.broken {
		line := a.line
		if err := a.Write(Entry{
			Event: "security.audit_chain_broken", Category: "security", Severity: SeverityCritical,
			Actor: Actor{Type: "system"}, Details: map[string]any{"file": filepath.Base(path), "line": line},
			Reason: a.broken, Result: ResultFailure,
		}); err != nil {
			_ = logger.Close()
			return nil, err
		}
	}
	return a, nil
}

func (a *AuditLogger) loadChain() error {
	data, err := os.ReadFile(a.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("applog: read audit log: %w", err)
	}
	anchorPath := filepath.Join(filepath.Dir(a.path), "audit.anchor")
	anchorBytes, anchorErr := os.ReadFile(anchorPath)
	hasAnchor := anchorErr == nil
	if anchorErr != nil && !errors.Is(anchorErr, os.ErrNotExist) {
		return fmt.Errorf("applog: read audit anchor: %w", anchorErr)
	}
	anchor := strings.TrimSpace(string(anchorBytes))
	if len(data) == 0 {
		if hasAnchor {
			if validHash(anchor) {
				a.prev = anchor
			} else {
				a.broken = "anchor_mismatch"
				a.line = 1
			}
		}
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	expected := zeroHash
	if hasAnchor {
		if !validHash(anchor) {
			a.broken = "anchor_mismatch"
			a.line = 1
		} else {
			expected = anchor
		}
	}
	for index, line := range lines {
		if line == "" {
			continue
		}
		var record struct {
			Prev   string `json:"prev"`
			Event  string `json:"event"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			if a.broken == "" {
				a.broken = "chain_mismatch"
				a.line = index + 1
			}
		} else if index == 0 && !hasAnchor && record.Prev != zeroHash {
			if a.broken == "" {
				a.broken = "anchor_missing"
				a.line = index + 1
			}
		} else if index == 0 && hasAnchor && record.Prev != expected {
			if a.broken == "" {
				a.broken = "anchor_mismatch"
				a.line = index + 1
			}
		} else if record.Prev != expected {
			if a.broken == "" {
				a.broken = "chain_mismatch"
				a.line = index + 1
			}
		}
		if record.Event == "security.audit_chain_broken" {
			a.reported = record.Reason
		}
		expected = hashLine([]byte(line))
	}
	if len(lines) > 0 {
		a.prev = expected
	}
	return nil
}

func validHash(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func hashLine(line []byte) string {
	sum := sha256.Sum256(line)
	return hex.EncodeToString(sum[:])
}

// Close closes the underlying file.
func (a *AuditLogger) Close() error {
	if a == nil || a.logger == nil {
		return nil
	}
	return a.logger.Close()
}

// RotateIfDue closes and verifies the active audit file before archiving it.
// The anchor is durable before retention can remove the closed file.
func (a *AuditLogger) RotateIfDue(now time.Time) error {
	if a == nil || a.logger == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	l := a.logger
	l.mu.Lock()
	if l.rotation.never {
		l.mu.Unlock()
		return nil
	}
	info, err := l.file.Stat()
	if err != nil {
		l.mu.Unlock()
		return fmt.Errorf("applog: stat %s: %w", l.path, err)
	}
	if !l.rotation.due(info, now.UTC()) {
		l.mu.Unlock()
		return nil
	}
	l.mu.Unlock()
	broken, line, err := a.verifyActive()
	if err != nil {
		return err
	}
	if broken != "" && a.reported != broken {
		a.reported = broken
		if err := a.writeLocked(Entry{
			Event: "security.audit_chain_broken", Category: "security", Severity: SeverityCritical,
			Actor: Actor{Type: "system"}, Details: map[string]any{"file": filepath.Base(l.path), "line": line},
			Reason: broken, Result: ResultFailure,
		}, false); err != nil {
			return err
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	info, err = l.file.Stat()
	if err != nil {
		return fmt.Errorf("applog: stat %s: %w", l.path, err)
	}
	if !l.rotation.due(info, now.UTC()) {
		return nil
	}
	if err := l.file.Sync(); err != nil {
		return fmt.Errorf("applog: sync %s: %w", l.path, err)
	}
	if err := l.file.Close(); err != nil {
		return fmt.Errorf("applog: close %s: %w", l.path, err)
	}
	archive := fmt.Sprintf("%s.%s", l.path, now.UTC().Format("2006-01-02"))
	if _, err := os.Stat(archive); err == nil {
		archive = fmt.Sprintf("%s.%s", archive, now.UTC().Format("150405.000000000"))
	}
	if err := os.Rename(l.path, archive); err != nil {
		l.file, _ = os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, logFilePerm)
		return fmt.Errorf("applog: archive %s: %w", l.path, err)
	}
	active, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, logFilePerm)
	if err != nil {
		_ = os.Rename(archive, l.path)
		l.file, _ = os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, logFilePerm)
		return fmt.Errorf("applog: reopen %s: %w", l.path, err)
	}
	l.file = active
	if err := writeAuditAnchor(filepath.Join(filepath.Dir(l.path), "audit.anchor"), a.prev); err != nil {
		return err
	}
	if l.rotation.compress {
		if err := compressLog(archive); err != nil {
			return err
		}
	}
	return l.pruneLocked(now.UTC())
}

func (a *AuditLogger) verifyActive() (string, int, error) {
	data, err := os.ReadFile(a.path)
	if err != nil {
		return "", 0, fmt.Errorf("applog: verify audit log: %w", err)
	}
	if len(data) == 0 {
		return "", 0, nil
	}
	anchorBytes, err := os.ReadFile(filepath.Join(filepath.Dir(a.path), "audit.anchor"))
	hasAnchor := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", 0, fmt.Errorf("applog: read audit anchor: %w", err)
	}
	expected := zeroHash
	if hasAnchor {
		expected = strings.TrimSpace(string(anchorBytes))
		if !validHash(expected) {
			return "anchor_mismatch", 1, nil
		}
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	for index, line := range lines {
		var entry struct {
			Prev string `json:"prev"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return "chain_mismatch", index + 1, nil
		}
		if entry.Prev != expected {
			if index == 0 && hasAnchor {
				return "anchor_mismatch", index + 1, nil
			}
			if index == 0 && !hasAnchor {
				return "anchor_missing", index + 1, nil
			}
			return "chain_mismatch", index + 1, nil
		}
		expected = hashLine([]byte(line))
	}
	if expected != a.prev {
		return "chain_mismatch", len(lines), nil
	}
	return "", 0, nil
}

func writeAuditAnchor(path, hash string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("applog: open audit anchor: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("applog: secure audit anchor: %w", err)
	}
	_, writeErr := file.WriteString(hash + "\n")
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return fmt.Errorf("applog: write audit anchor: %w", errors.Join(writeErr, syncErr, closeErr))
	}
	return nil
}

// Write assigns a raw ULID and timestamp, chains, appends, and syncs the entry.
func (a *AuditLogger) Write(e Entry) error {
	if a == nil || a.logger == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.writeLocked(e, true)
}

func (a *AuditLogger) writeLocked(e Entry, mirror bool) error {
	if e.ID == "" {
		id, err := GenerateULID()
		if err != nil {
			return fmt.Errorf("applog: generate audit id: %w", err)
		}
		e.ID = id
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	e.Prev = a.prev
	actor := e.Actor
	if actor.ID == "" {
		actor.ID = actor.UserID
	}
	if actor.Type == "" {
		switch {
		case actor.ID != "":
			actor.Type = "owner"
		case actor.IP != "":
			actor.Type = "anonymous"
		default:
			actor.Type = "system"
		}
	}
	e.Actor = actor
	wire := auditEntryJSON{ID: e.ID, Time: e.Time.UTC().Format("2006-01-02T15:04:05.000Z"), Event: e.Event,
		Category: e.Category, Severity: e.Severity, Actor: actor, Target: e.Target, Details: e.Details,
		Reason: e.Reason, Source: e.Source, Result: e.Result, Prev: e.Prev}
	line, err := json.Marshal(wire)
	if err != nil {
		return fmt.Errorf("applog: marshal audit entry: %w", err)
	}
	if err := a.logger.WriteLine(LevelInfo, string(line)+"\n"); err != nil {
		return err
	}
	a.prev = hashLine(line)
	a.line++
	file, err := os.OpenFile(a.path, os.O_WRONLY|os.O_APPEND, logFilePerm)
	if err != nil {
		return fmt.Errorf("applog: open audit log for sync: %w", err)
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if syncErr != nil || closeErr != nil {
		return fmt.Errorf("applog: sync audit log: %w", errors.Join(syncErr, closeErr))
	}
	if mirror && a.mirror != nil && e.Event != "audit.mirror_failed" {
		if err := a.mirror(e); err != nil {
			return a.writeLocked(Entry{
				Event: "audit.mirror_failed", Category: "audit", Severity: SeverityWarn,
				Actor: Actor{Type: "system"}, Details: map[string]any{"failed_audit_id": e.ID}, Result: ResultFailure,
			}, false)
		}
	}
	return nil
}

// MaskToken renders only the first eight token characters.
func MaskToken(raw string) string {
	const visible = 8
	if len(raw) <= visible {
		return raw + "..."
	}
	return raw[:visible] + "..."
}
