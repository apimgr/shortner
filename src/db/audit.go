package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// AuditMirrorEntry is the database projection of one authoritative audit
// file entry. AuditID remains inside Details; the table's row ID is local.
type AuditMirrorEntry struct {
	AuditID    string
	Timestamp  int64
	Level      string
	Category   string
	Action     string
	ActorIP    string
	TargetType string
	TargetID   string
	Details    map[string]any
	Success    bool
}

// InsertAuditMirror appends one event to audit_log. The file logger remains
// authoritative; callers should invoke this only after the file write succeeds.
func InsertAuditMirror(ctx context.Context, sqlDB Handle, entry AuditMirrorEntry) error {
	if sqlDB == nil {
		return errors.New("db: audit mirror requires a database")
	}
	if entry.AuditID == "" || entry.Timestamp <= 0 || entry.Level == "" || entry.Category == "" || entry.Action == "" {
		return errors.New("db: audit mirror entry is missing required fields")
	}
	details := make(map[string]any, len(entry.Details)+1)
	for key, value := range entry.Details {
		details[key] = value
	}
	details["audit_id"] = entry.AuditID
	encoded, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("db: encode audit mirror details: %w", err)
	}
	var actorIP, targetType, targetID any
	if entry.ActorIP != "" {
		actorIP = entry.ActorIP
	}
	if entry.TargetType != "" {
		targetType = entry.TargetType
	}
	if entry.TargetID != "" {
		targetID = entry.TargetID
	}
	success := 0
	if entry.Success {
		success = 1
	}
	_, err = sqlDB.ExecContext(ctx, `INSERT INTO audit_log
		(timestamp, level, category, action, actor_ip, target_type, target_id, details, success)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, entry.Timestamp, entry.Level, entry.Category,
		entry.Action, actorIP, targetType, targetID, string(encoded), success)
	if err != nil {
		return fmt.Errorf("db: insert audit mirror: %w", err)
	}
	return nil
}
