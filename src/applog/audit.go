package applog

import (
	"encoding/json"
	"fmt"
	"time"
)

// Severity is an audit-entry severity, per AI.md PART 11 "Severity
// Levels".
type Severity string

// Audit severities, per AI.md PART 11 "Severity Levels".
const (
	SeverityInfo     Severity = "info"
	SeverityWarn     Severity = "warn"
	SeverityError    Severity = "error"
	SeverityCritical Severity = "critical"
)

// Result is the outcome of an audited action, per AI.md PART 11 "Audit
// Log Format" -> "result": "success" or "failure".
type Result string

// Audit results, per AI.md PART 11 "Audit Log Format".
const (
	ResultSuccess Result = "success"
	ResultFailure Result = "failure"
)

// Actor identifies who performed an audited action, per AI.md PART 11
// "Audit Log Format" -> "actor". UserID is omitted for unauthenticated
// (operator-token or anonymous) actions.
type Actor struct {
	IP     string `json:"ip"`
	UserID string `json:"user_id,omitempty"`
}

// Target identifies what an audited action acted upon, per AI.md
// PART 11 "Audit Log Format" -> "target". Per "Authentication & Identity
// Rules", ID must be an opaque identifier — never a raw sequential
// primary key.
type Target struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Entry is one audit-log record, per AI.md PART 11 "Audit Log Format".
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
	Result   Result         `json:"result"`
}

// auditEntryJSON is Entry's wire shape — Time needs the millisecond-
// precision UTC format AI.md PART 11 mandates ("ISO 8601 timestamp with
// milliseconds, UTC"), which time.Time's default JSON marshaling does not
// guarantee across Go versions, so it is rendered explicitly.
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
	Result   Result         `json:"result"`
}

// AuditLogger appends Entry records as JSON Lines to audit.log, per
// AI.md PART 11 "Audit Log Format": "All audit logs are JSON format, one
// entry per line." It is append-only by construction — Logger never
// truncates or seeks, only appends (AI.md PART 11 "Audit Log Integrity").
type AuditLogger struct {
	logger *Logger
}

// NewAuditLogger opens (or creates) the audit log file at path.
func NewAuditLogger(path string) (*AuditLogger, error) {
	logger, err := Open(path, LevelInfo)
	if err != nil {
		return nil, err
	}
	return &AuditLogger{logger: logger}, nil
}

// Close closes the underlying file.
func (a *AuditLogger) Close() error {
	return a.logger.Close()
}

// Write assigns e a fresh ULID and UTC timestamp if unset, then appends
// it as one JSON line. NEVER pass raw secrets/tokens in Details — see
// AI.md PART 11 "Audit Log Rules" -> "NEVER Log" and use MaskToken for
// any token/ID reference.
func (a *AuditLogger) Write(e Entry) error {
	if e.ID == "" {
		id, err := GenerateULID()
		if err != nil {
			return fmt.Errorf("applog: generate audit id: %w", err)
		}
		e.ID = "audit_" + id
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}

	wire := auditEntryJSON{
		ID:       e.ID,
		Time:     e.Time.UTC().Format("2006-01-02T15:04:05.000Z"),
		Event:    e.Event,
		Category: e.Category,
		Severity: e.Severity,
		Actor:    e.Actor,
		Target:   e.Target,
		Details:  e.Details,
		Reason:   e.Reason,
		Result:   e.Result,
	}

	line, err := json.Marshal(wire)
	if err != nil {
		return fmt.Errorf("applog: marshal audit entry: %w", err)
	}
	return a.logger.WriteLine(LevelInfo, string(line)+"\n")
}

// MaskToken renders a token/secret reference for logs per AI.md PART 11
// "Token/ID Masking": "Show only first 8 characters: token_abc12345...".
// Never pass the full raw token to any logger.
func MaskToken(raw string) string {
	const visible = 8
	if len(raw) <= visible {
		return raw + "..."
	}
	return raw[:visible] + "..."
}
