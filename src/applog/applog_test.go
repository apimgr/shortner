package applog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGenerateULIDFormat(t *testing.T) {
	id, err := GenerateULID()
	if err != nil {
		t.Fatalf("GenerateULID() error = %v", err)
	}
	if len(id) != 26 {
		t.Errorf("len(ULID) = %d, want 26", len(id))
	}
	for _, c := range id {
		if !strings.ContainsRune(crockfordAlphabet, c) {
			t.Errorf("ULID contains non-Crockford char %q", c)
		}
	}
}

func TestGenerateULIDUnique(t *testing.T) {
	a, _ := GenerateULID()
	b, _ := GenerateULID()
	if a == b {
		t.Errorf("two generated ULIDs were identical: %q", a)
	}
}

func TestFormatText(t *testing.T) {
	ts := time.Date(2024, 10, 10, 13, 55, 36, 0, time.FixedZone("-0400", -4*3600))
	got := FormatText(ts, "info", "Server started on :8080")
	want := "2024-10-10T13:55:36-04:00 [INFO] Server started on :8080\n"
	if got != want {
		t.Errorf("FormatText() = %q, want %q", got, want)
	}
}

func TestFormatLogfmt(t *testing.T) {
	ts := time.Date(2026, 5, 13, 10, 58, 0, 0, time.FixedZone("-0400", -4*3600))
	got := FormatLogfmt(ts, "info", "user created", map[string]string{"id": "abc123", "ip": "1.2.3.4"})
	want := `time=2026-05-13T10:58:00-04:00 level=INFO msg="user created" id=abc123 ip=1.2.3.4` + "\n"
	if got != want {
		t.Errorf("FormatLogfmt() = %q, want %q", got, want)
	}
}

func TestFormatApache(t *testing.T) {
	ts := time.Date(2024, 10, 10, 13, 55, 36, 0, time.FixedZone("-0700", -7*3600))
	e := AccessLogEntry{IP: "127.0.0.1", Time: ts, Method: "GET", Path: "/api/v1/server/healthz", Protocol: "HTTP/1.1", Status: 200, Size: 2326, UserAgent: "curl/7.64.1"}
	got := FormatApache(e)
	if !strings.Contains(got, `"GET /api/v1/server/healthz HTTP/1.1" 200 2326`) {
		t.Errorf("FormatApache() = %q, missing expected request/status/size segment", got)
	}
	if !strings.HasPrefix(got, "127.0.0.1 - - [") {
		t.Errorf("FormatApache() = %q, missing IP prefix", got)
	}
	if !strings.HasSuffix(got, `"curl/7.64.1"`+"\n") {
		t.Errorf("FormatApache() = %q, missing UA suffix", got)
	}
}

func TestFormatFail2ban(t *testing.T) {
	ts := time.Date(2024, 10, 10, 13, 55, 36, 0, time.FixedZone("-0400", -4*3600))
	got := FormatFail2ban(ts, "Failed authentication attempt from 192.168.1.100")
	want := "2024-10-10T13:55:36-04:00 [security] Failed authentication attempt from 192.168.1.100\n"
	if got != want {
		t.Errorf("FormatFail2ban() = %q, want %q", got, want)
	}
}

func TestFormatSyslogRFC3164(t *testing.T) {
	ts := time.Date(2026, 5, 13, 10, 58, 0, 0, time.UTC)
	got := FormatSyslogRFC3164("hostname", "shortner", 1234, ts, "auth: token_id=xxx ip=1.2.3.4 result=fail reason=invalid_token")
	if !strings.Contains(got, "hostname shortner[1234]: auth:") {
		t.Errorf("FormatSyslogRFC3164() = %q, missing expected header", got)
	}
}

func TestLoggerWriteLineFiltersByLevel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.log")

	logger, err := Open(path, LevelWarn)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer logger.Close()

	if err := logger.WriteLine(LevelDebug, "debug line\n"); err != nil {
		t.Fatalf("WriteLine(debug) error = %v", err)
	}
	if err := logger.WriteLine(LevelError, "error line\n"); err != nil {
		t.Fatalf("WriteLine(error) error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	content := string(data)
	if strings.Contains(content, "debug line") {
		t.Errorf("log file contains filtered-out debug line: %q", content)
	}
	if !strings.Contains(content, "error line") {
		t.Errorf("log file missing error line: %q", content)
	}
}

func TestLoggerFilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	logger, err := Open(path, LevelInfo)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer logger.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != logFilePerm {
		t.Errorf("file mode = %o, want %o", info.Mode().Perm(), logFilePerm)
	}
}

func TestLoggerRotate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.log")

	logger, err := Open(path, LevelInfo)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer logger.Close()

	if err := logger.WriteLine(LevelInfo, "before rotate\n"); err != nil {
		t.Fatalf("WriteLine() error = %v", err)
	}
	if err := logger.Rotate(); err != nil {
		t.Fatalf("Rotate() error = %v", err)
	}
	if err := logger.WriteLine(LevelInfo, "after rotate\n"); err != nil {
		t.Fatalf("WriteLine() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), "before rotate") || !strings.Contains(string(data), "after rotate") {
		t.Errorf("rotated file missing expected content: %q", string(data))
	}
}

func TestAuditLoggerWriteAssignsIDAndTime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	al, err := NewAuditLogger(path)
	if err != nil {
		t.Fatalf("NewAuditLogger() error = %v", err)
	}
	defer al.Close()

	err = al.Write(Entry{
		Event:    "config.updated",
		Category: "config",
		Severity: SeverityInfo,
		Actor:    Actor{IP: "192.168.1.100"},
		Target:   &Target{Type: "config", ID: "server.port"},
		Details:  map[string]any{"changed_keys": []string{"server.port"}},
		Result:   ResultSuccess,
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("len(lines) = %d, want 1", len(lines))
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	id, _ := decoded["id"].(string)
	if !strings.HasPrefix(id, "audit_") {
		t.Errorf("id = %q, want audit_ prefix", id)
	}
	if decoded["event"] != "config.updated" {
		t.Errorf("event = %v, want config.updated", decoded["event"])
	}
	if decoded["result"] != "success" {
		t.Errorf("result = %v, want success", decoded["result"])
	}
	timeStr, _ := decoded["time"].(string)
	if _, err := time.Parse("2006-01-02T15:04:05.000Z", timeStr); err != nil {
		t.Errorf("time = %q not in expected millisecond UTC format: %v", timeStr, err)
	}
}

func TestAuditLoggerAppendsMultipleEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	al, err := NewAuditLogger(path)
	if err != nil {
		t.Fatalf("NewAuditLogger() error = %v", err)
	}
	defer al.Close()

	for i := 0; i < 3; i++ {
		if err := al.Write(Entry{Event: "server.started", Category: "server", Severity: SeverityInfo, Actor: Actor{IP: "127.0.0.1"}, Result: ResultSuccess}); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("len(lines) = %d, want 3", len(lines))
	}
}

func TestMaskToken(t *testing.T) {
	got := MaskToken("tok_abc12345verylongsecret")
	want := "tok_abc1..."
	if got != want {
		t.Errorf("MaskToken() = %q, want %q", got, want)
	}
	if got := MaskToken("short"); got != "short..." {
		t.Errorf("MaskToken(short) = %q, want short...", got)
	}
}

func TestFormatCEF(t *testing.T) {
	got := FormatCEF("shortner", "shortner", "1.0", 5, "auth-fail", "Authentication failure", map[string]string{"src": "1.2.3.4"})
	if !strings.HasPrefix(got, "CEF:0|shortner|shortner|1.0|auth-fail|Authentication failure|5|") {
		t.Errorf("FormatCEF() = %q, unexpected prefix", got)
	}
	if !strings.Contains(got, "src=1.2.3.4") {
		t.Errorf("FormatCEF() = %q, missing extension field", got)
	}
}
