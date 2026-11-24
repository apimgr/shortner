package main

import (
	"context"
	"os"
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

func TestSecurityReportExportWritesCiphertextWithRestrictedMode(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "server.db"), db.DefaultPool(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	report := db.SecurityReport{TrackingID: "sec_0123456789abcdef", ReceivedAt: time.Now().UTC(), Severity: "low", Component: "api", Sealed: "ENCRYPTED-MESSAGE"}
	if err := db.InsertSecurityReport(context.Background(), store, report); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "report.asc")
	if code := runSecurityReportExport("shortner", paths.Paths{Logs: t.TempDir()}, store, report.TrackingID, output); code != 0 {
		t.Fatalf("export code = %d", code)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ENCRYPTED-MESSAGE" {
		t.Fatalf("output = %q", data)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}
