package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/fqdn"
	"github.com/apimgr/shortner/src/paths"
	"github.com/apimgr/shortner/src/pgp"
	"github.com/apimgr/shortner/src/security"
)

func runSecurityReportMaintenance(binaryName string, p paths.Paths, args []string) int {
	if len(args) == 0 {
		return securityReportUsage(binaryName)
	}
	cfg, err := config.Load(p.ConfigFile, filepath.Join(p.DB, "server.db"))
	if err != nil {
		return securityReportError(binaryName, err)
	}
	if err := authorizeMaintenance(binaryName, cfg.Server.Token); err != nil {
		return securityReportError(binaryName, err)
	}
	if err := paths.EnsureDir(p.DB); err != nil {
		return securityReportError(binaryName, err)
	}
	sqlDB, err := db.Open(filepath.Join(p.DB, "server.db"), db.DefaultPool(), nil)
	if err != nil {
		return securityReportError(binaryName, err)
	}
	defer sqlDB.Close()
	ctx := context.Background()
	switch args[0] {
	case "list":
		status := ""
		if len(args) == 3 && args[1] == "--status" {
			status = args[2]
		} else if len(args) != 1 {
			return securityReportUsage(binaryName)
		}
		if status != "" && !db.IsSecurityReportStatus(status) {
			fmt.Fprintf(os.Stderr, "%s: invalid security report status %q\n", binaryName, status)
			return 2
		}
		reports, err := db.ListSecurityReports(ctx, sqlDB, status)
		if err != nil {
			return securityReportError(binaryName, err)
		}
		fmt.Println("TRACKING ID\tSTATUS\tSEVERITY\tCOMPONENT\tCREATED AT\tUPDATED AT")
		for _, rep := range reports {
			fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\n", rep.TrackingID, rep.Status, rep.Severity, rep.Component, rep.ReceivedAt.Format(time.RFC3339), rep.UpdatedAt.Format(time.RFC3339))
		}
		return 0
	case "show":
		if len(args) != 2 {
			return securityReportUsage(binaryName)
		}
		rep, err := db.GetSecurityReport(ctx, sqlDB, args[1])
		if err != nil {
			return securityReportError(binaryName, err)
		}
		fmt.Printf("Tracking ID: %s\nStatus: %s\nSeverity: %s\nComponent: %s\nCreated at: %s\nUpdated at: %s\nResearcher comment: %s\nDisclosure date: %s\n", rep.TrackingID, rep.Status, rep.Severity, rep.Component, rep.ReceivedAt.Format(time.RFC3339), rep.UpdatedAt.Format(time.RFC3339), rep.Comments, formatDisclosureDate(rep.DisclosureAt))
		return 0
	case "export":
		if len(args) != 2 && len(args) != 4 || (len(args) == 4 && args[2] != "--output") {
			return securityReportUsage(binaryName)
		}
		output := args[3]
		if len(args) == 2 {
			output = args[1] + ".asc"
		}
		return runSecurityReportExport(binaryName, p, sqlDB, args[1], output)
	case "update":
		return runSecurityReportUpdate(binaryName, p, cfg, sqlDB, ctx, args[1:])
	default:
		return securityReportUsage(binaryName)
	}
}

func runSecurityReportExport(binaryName string, p paths.Paths, sqlDB db.Handle, trackingID, output string) int {
	rep, err := db.GetSecurityReport(context.Background(), sqlDB, trackingID)
	if err != nil {
		return securityReportError(binaryName, err)
	}
	message := []byte(rep.Sealed)
	if len(message) == 0 {
		message = rep.BodyEnc
	}
	if len(message) == 0 {
		return securityReportError(binaryName, fmt.Errorf("encrypted report message is unavailable"))
	}
	var dst io.Writer = os.Stdout
	var file *os.File
	if output != "-" {
		file, err = os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return securityReportError(binaryName, err)
		}
		dst = file
	}
	if _, err := dst.Write(message); err != nil {
		if file != nil {
			_ = file.Close()
		}
		return securityReportError(binaryName, err)
	}
	if file != nil {
		if err := file.Close(); err != nil {
			return securityReportError(binaryName, err)
		}
	}
	if err := writeSecurityReportExportAudit(p.Logs, trackingID, time.Now()); err != nil {
		return securityReportError(binaryName, err)
	}
	return 0
}

func writeSecurityReportExportAudit(logDir, trackingID string, now time.Time) error {
	if err := paths.EnsureDir(logDir); err != nil {
		return err
	}
	logger, err := applog.Open(filepath.Join(logDir, "security.log"), applog.LevelInfo)
	if err != nil {
		return err
	}
	defer logger.Close()
	return logger.WriteLine(applog.LevelInfo, fmt.Sprintf("{\"time\":%q,\"event\":\"security.report_exported\",\"tracking_id\":%q}", now.UTC().Format("2006-01-02T15:04:05.000Z"), trackingID))
}

func runSecurityReportUpdate(binaryName string, p paths.Paths, cfg *config.Config, sqlDB db.Handle, ctx context.Context, args []string) int {
	if len(args) == 0 {
		return securityReportUsage(binaryName)
	}
	id := args[0]
	update := db.SecurityReportUpdate{}
	for i := 1; i < len(args); i++ {
		arg := args[i]
		value := ""
		if strings.HasPrefix(arg, "--status=") {
			update.Status = stringPointer(strings.TrimPrefix(arg, "--status="))
			continue
		}
		if strings.HasPrefix(arg, "--comment=") {
			update.Comments = stringPointer(strings.TrimPrefix(arg, "--comment="))
			continue
		}
		if strings.HasPrefix(arg, "--disclosure-date=") {
			value = strings.TrimPrefix(arg, "--disclosure-date=")
			date, err := parseSecurityDisclosureDate(value)
			if err != nil {
				return securityReportError(binaryName, err)
			}
			update.DisclosureAt = date
			continue
		}
		if strings.HasPrefix(arg, "--severity=") {
			update.Severity = stringPointer(strings.ToLower(strings.TrimPrefix(arg, "--severity=")))
			continue
		}
		if arg != "--status" && arg != "--comment" && arg != "--disclosure-date" && arg != "--severity" {
			return securityReportUsage(binaryName)
		}
		if i+1 >= len(args) {
			return securityReportUsage(binaryName)
		}
		i++
		switch arg {
		case "--status":
			update.Status = stringPointer(args[i])
		case "--comment":
			update.Comments = stringPointer(args[i])
		case "--disclosure-date":
			date, err := parseSecurityDisclosureDate(args[i])
			if err != nil {
				return securityReportError(binaryName, err)
			}
			update.DisclosureAt = date
		case "--severity":
			update.Severity = stringPointer(strings.ToLower(args[i]))
		}
	}
	now := time.Now().UTC()
	rep, oldStatus, err := db.UpdateSecurityReport(ctx, sqlDB, id, update, now)
	if err != nil {
		return securityReportError(binaryName, err)
	}
	if err := writeSecurityReportAudit(p.Logs, id, oldStatus, rep.Status, now); err != nil {
		return securityReportError(binaryName, err)
	}
	if oldStatus != rep.Status {
		sendSecurityReportStatusNotice(cfg, p, rep)
	}
	fmt.Printf("Security report %s updated. Status: %s.\n", rep.TrackingID, rep.Status)
	return 0
}

func sendSecurityReportStatusNotice(cfg *config.Config, p paths.Paths, rep *db.SecurityReport) {
	if cfg == nil || rep == nil || len(rep.ContactEnc) == 0 {
		return
	}
	key, err := config.DecodeEncryptionKey(cfg)
	if err != nil {
		return
	}
	plain, err := security.Open(key, string(rep.ContactEnc))
	if err != nil {
		return
	}
	var contact struct {
		Email string `json:"email"`
		GPG   string `json:"gpg"`
	}
	if err := json.Unmarshal(plain, &contact); err != nil || strings.TrimSpace(contact.Email) == "" {
		return
	}
	body := fmt.Sprintf("Security report %s status changed to %s.\n", rep.TrackingID, rep.Status)
	if rep.Comments != "" {
		body += "Researcher comment: " + rep.Comments + "\n"
	}
	if !rep.DisclosureAt.IsZero() {
		body += "Expected disclosure date: " + rep.DisclosureAt.UTC().Format("2006-01-02") + "\n"
	}
	if contact.GPG != "" {
		recipients, err := pgp.ParsePublic(contact.GPG)
		if err != nil {
			return
		}
		message, err := pgp.EncryptArmored(recipients, []byte(body))
		if err != nil {
			return
		}
		body = message
	}
	n := newNotifier(cfg, p, fqdn.GetFQDN(internalName))
	if !n.Startup(fqdn.GetFQDN(internalName)).Enabled {
		return
	}
	_ = n.SendRaw([]string{contact.Email}, "Security report status updated - "+rep.TrackingID, body)
}

func stringPointer(value string) *string { return &value }

func securityReportUsage(binaryName string) int {
	fmt.Fprintf(os.Stderr, "Usage: %s --maintenance security list [--status STATUS] | show TRACKING_ID | export TRACKING_ID [--output FILE] | update TRACKING_ID [--status STATUS] [--comment TEXT] [--disclosure-date YYYY-MM-DD] [--severity LEVEL]\n", binaryName)
	return 2
}

func securityReportError(binaryName string, err error) int {
	fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
	return 1
}

func formatDisclosureDate(value time.Time) string {
	if value.IsZero() {
		return "(none)"
	}
	return value.UTC().Format("2006-01-02")
}

func writeSecurityReportAudit(logDir, trackingID, oldStatus, newStatus string, now time.Time) error {
	if err := paths.EnsureDir(logDir); err != nil {
		return err
	}
	logger, err := applog.Open(filepath.Join(logDir, "security.log"), applog.LevelInfo)
	if err != nil {
		return err
	}
	defer logger.Close()
	entry := map[string]string{"time": now.UTC().Format("2006-01-02T15:04:05.000Z"), "event": "security.report_updated", "tracking_id": trackingID, "old_status": oldStatus, "new_status": newStatus}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return logger.WriteLine(applog.LevelInfo, string(data)+"\n")
}

// parseSecurityDisclosureDate accepts only the CLI's documented ISO date shape.
func parseSecurityDisclosureDate(value string) (*time.Time, error) {
	if strings.TrimSpace(value) != value || value == "" {
		return nil, fmt.Errorf("disclosure date must use YYYY-MM-DD")
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return nil, fmt.Errorf("disclosure date must use YYYY-MM-DD")
	}
	return &parsed, nil
}
