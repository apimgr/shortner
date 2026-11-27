package applog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditLoggerChainsEntriesAndUsesRawULID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	audit, err := NewAuditLogger(path)
	if err != nil {
		t.Fatalf("NewAuditLogger() error = %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := audit.Write(Entry{Event: "server.started", Category: "server", Severity: SeverityInfo, Result: ResultSuccess}); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}
	if err := audit.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	lines := readAuditLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	var first, second map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatal(err)
	}
	if id, _ := first["id"].(string); len(id) != 26 || strings.HasPrefix(id, "audit_") {
		t.Errorf("id = %q, want raw 26-character ULID", id)
	}
	if first["prev"] != zeroHash {
		t.Errorf("first prev = %v, want zero hash", first["prev"])
	}
	if second["prev"] != hashLine([]byte(lines[0])) {
		t.Errorf("second prev = %v, want hash of first exact line", second["prev"])
	}
	if actor, ok := first["actor"].(map[string]any); !ok || actor["type"] != "system" {
		t.Errorf("actor = %v, want system actor", first["actor"])
	}
}

func TestAuditLoggerRotationKeepsChainAnchor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	audit, err := NewAuditLogger(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := audit.Write(Entry{Event: "first", Category: "server", Severity: SeverityInfo, Result: ResultSuccess}); err != nil {
		t.Fatal(err)
	}
	if err := audit.SetRotationPolicy("daily", "none", false); err != nil {
		t.Fatal(err)
	}
	if err := audit.RotateIfDue(time.Now().Add(48 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	anchor, err := os.ReadFile(filepath.Join(dir, "audit.anchor"))
	if err != nil {
		t.Fatal(err)
	}
	if !validHash(strings.TrimSpace(string(anchor))) {
		t.Fatalf("anchor = %q, want lowercase SHA-256", anchor)
	}
	if err := audit.Write(Entry{Event: "second", Category: "server", Severity: SeverityInfo, Result: ResultSuccess}); err != nil {
		t.Fatal(err)
	}
	if err := audit.Close(); err != nil {
		t.Fatal(err)
	}
	archive, err := filepath.Glob(path + ".*")
	if err != nil {
		t.Fatal(err)
	}
	if len(archive) != 0 {
		t.Fatalf("archives = %v, want none for keep:none", archive)
	}
	audit, err = NewAuditLogger(path)
	if err != nil {
		t.Fatalf("reopen after rotation: %v", err)
	}
	if err := audit.Close(); err != nil {
		t.Fatal(err)
	}
	lines := readAuditLines(t, path)
	var second map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &second); err != nil {
		t.Fatal(err)
	}
	if second["prev"] != strings.TrimSpace(string(anchor)) {
		t.Errorf("first post-rotation prev = %v, want anchor %s", second["prev"], strings.TrimSpace(string(anchor)))
	}
}

func TestAuditLoggerContinuesChainAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	for i := 0; i < 2; i++ {
		audit, err := NewAuditLogger(path)
		if err != nil {
			t.Fatalf("NewAuditLogger() error = %v", err)
		}
		if err := audit.Write(Entry{Event: "server.started", Category: "server", Severity: SeverityInfo, Result: ResultSuccess}); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
		if err := audit.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}
	lines := readAuditLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	var second map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatal(err)
	}
	if second["prev"] != hashLine([]byte(lines[0])) {
		t.Errorf("second prev = %v, want hash of prior process' last entry", second["prev"])
	}
}

func TestAuditLoggerReportsBrokenChainOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	first := `{"id":"01JHZQ8M3K5N7P9R2T4V6W8X0Y","time":"2025-01-15T10:30:00.123Z","event":"test","category":"security","severity":"info","actor":{"type":"system"},"result":"success","prev":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
	if err := os.WriteFile(path, []byte(first+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	audit, err := NewAuditLogger(path)
	if err != nil {
		t.Fatalf("NewAuditLogger() error = %v", err)
	}
	if err := audit.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	lines := readAuditLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want original plus one integrity event", len(lines))
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &event); err != nil {
		t.Fatal(err)
	}
	if event["event"] != "security.audit_chain_broken" || event["reason"] != "anchor_missing" {
		t.Errorf("integrity event = %v, want anchor_missing", event)
	}
	audit, err = NewAuditLogger(path)
	if err != nil {
		t.Fatalf("reopening broken chain: %v", err)
	}
	if err := audit.Close(); err != nil {
		t.Fatalf("closing reopened audit logger: %v", err)
	}
	if got := len(readAuditLines(t, path)); got != 2 {
		t.Errorf("reopening reported the same chain break again: got %d lines, want 2", got)
	}
}

func TestAuditLoggerReportsModifiedEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	audit, err := NewAuditLogger(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := audit.Write(Entry{Event: "first", Category: "server", Severity: SeverityInfo, Result: ResultSuccess}); err != nil {
		t.Fatal(err)
	}
	if err := audit.Write(Entry{Event: "second", Category: "server", Severity: SeverityInfo, Result: ResultSuccess}); err != nil {
		t.Fatal(err)
	}
	if err := audit.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `"event":"first"`, `"event":"edit"`, 1))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	audit, err = NewAuditLogger(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := audit.Close(); err != nil {
		t.Fatal(err)
	}
	lines := readAuditLines(t, path)
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want integrity event appended", len(lines))
	}
}

func readAuditLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}
