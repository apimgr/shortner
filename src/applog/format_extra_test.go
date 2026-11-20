package applog

import (
	"strings"
	"testing"
	"time"
)

func TestFormatNginxAndAccessJSON(t *testing.T) {
	stamp := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	entry := AccessLogEntry{
		IP:        "192.0.2.1",
		Time:      stamp,
		Method:    "GET",
		Path:      "/safe\nforged",
		Status:    200,
		Size:      17,
		UserAgent: "client\"name",
	}

	nginx := FormatNginx(entry)
	for _, want := range []string{"192.0.2.1", "GET /safe forged HTTP/1.1", "200 17"} {
		if !strings.Contains(nginx, want) {
			t.Errorf("FormatNginx() missing %q in %q", want, nginx)
		}
	}

	accessJSON := FormatAccessJSON(entry)
	for _, want := range []string{`"time":"2026-01-02T03:04:05Z"`, `"path":"/safe\nforged"`, `"ua":"client\"name"`} {
		if !strings.Contains(accessJSON, want) {
			t.Errorf("FormatAccessJSON() missing %q in %q", want, accessJSON)
		}
	}
}

func TestLevelString(t *testing.T) {
	for level, want := range map[Level]string{
		LevelDebug: "DEBUG",
		LevelInfo:  "INFO",
		LevelWarn:  "WARN",
		LevelError: "ERROR",
		Level(99):  "UNKNOWN",
	} {
		if got := level.String(); got != want {
			t.Errorf("Level(%d).String() = %q, want %q", level, got, want)
		}
	}
}
