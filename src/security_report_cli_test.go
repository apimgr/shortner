package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/paths"
)

func TestSecurityReportUpdateAcceptsSeverity(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "server.db"), db.DefaultPool(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	report := db.SecurityReport{TrackingID: "sec_0123456789abcdef", ReceivedAt: time.Now().UTC(), Severity: "low", Component: "api", Sealed: "ciphertext"}
	if err := db.InsertSecurityReport(context.Background(), store, report); err != nil {
		t.Fatal(err)
	}
	projectPaths := paths.Paths{Logs: t.TempDir()}
	if code := runSecurityReportUpdate("shortner", projectPaths, config.Default(""), store, context.Background(), []string{report.TrackingID, "--severity", "CRITICAL"}); code != 0 {
		t.Fatalf("runSecurityReportUpdate() = %d, want 0", code)
	}
	updated, err := db.GetSecurityReport(context.Background(), store, report.TrackingID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Severity != "critical" {
		t.Fatalf("updated severity = %q, want critical", updated.Severity)
	}
}
