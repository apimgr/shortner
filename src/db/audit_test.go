package db

import (
	"context"
	"encoding/json"
	"testing"
)

func TestInsertAuditMirrorAddsAuditIDAndPreservesDetails(t *testing.T) {
	sqlDB := openTestDB(t)
	entry := AuditMirrorEntry{
		AuditID: "01JHZQ8M3K5N7P9R2T4V6W8X0Y", Timestamp: 1736937000,
		Level: "security", Category: "security", Action: "cli.token_rejected",
		ActorIP: "192.0.2.9", Details: map[string]any{"token_prefix": "own_abcd..."},
		Success: false,
	}
	if err := InsertAuditMirror(context.Background(), sqlDB, entry); err != nil {
		t.Fatalf("InsertAuditMirror() error = %v", err)
	}
	var rowID int64
	var timestamp int64
	var level, category, action, actorIP, details string
	var success int
	err := sqlDB.QueryRow(`SELECT id, timestamp, level, category, action, actor_ip, details, success
		FROM audit_log WHERE action = ?`, entry.Action).Scan(&rowID, &timestamp, &level, &category, &action, &actorIP, &details, &success)
	if err != nil {
		t.Fatalf("query audit mirror: %v", err)
	}
	if rowID < 1 || timestamp != entry.Timestamp || level != entry.Level || category != entry.Category || actorIP != entry.ActorIP || success != 0 {
		t.Fatalf("stored audit row = id:%d timestamp:%d level:%q category:%q actor:%q success:%d", rowID, timestamp, level, category, actorIP, success)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(details), &decoded); err != nil {
		t.Fatalf("decode details: %v", err)
	}
	if decoded["audit_id"] != entry.AuditID || decoded["token_prefix"] != "own_abcd..." {
		t.Fatalf("details = %#v, want audit_id and supplied fields", decoded)
	}
	if _, exists := entry.Details["audit_id"]; exists {
		t.Fatal("InsertAuditMirror mutated the caller's details map")
	}
}

func TestInsertAuditMirrorRejectsMissingAuditID(t *testing.T) {
	if err := InsertAuditMirror(context.Background(), openTestDB(t), AuditMirrorEntry{
		Timestamp: 1, Level: "info", Category: "server", Action: "server.started",
	}); err == nil {
		t.Fatal("InsertAuditMirror() accepted an empty audit ID")
	}
}
