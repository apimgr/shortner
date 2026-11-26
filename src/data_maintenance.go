package main

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
)

type dataExport struct {
	Format int `json:"format"`
	Owner  struct {
		ID         string `json:"id"`
		CreatedAt  string `json:"created_at"`
		LastSeenAt string `json:"last_seen_at"`
	} `json:"owner"`
	Tokens    []exportToken    `json:"tokens"`
	Resources []exportResource `json:"resources"`
}
type exportToken struct {
	Prefix     string  `json:"prefix"`
	Kind       string  `json:"kind"`
	Name       string  `json:"name,omitempty"`
	CreatedAt  string  `json:"created_at"`
	LastUsedAt *string `json:"last_used_at"`
	ExpiresAt  *string `json:"expires_at"`
	Status     string  `json:"status"`
}
type exportResource struct {
	ShortCode      string  `json:"short_code"`
	ShortURL       string  `json:"short_url"`
	DestinationURL string  `json:"destination_url"`
	CreatedAt      string  `json:"created_at"`
	ExpiresAt      *string `json:"expires_at"`
	ClickCount     int64   `json:"click_count"`
}

func runDataMaintenance(binaryName string, p paths.Paths, args []string) (exitCode int) {
	if len(args) < 1 || (args[0] != "export" && args[0] != "delete") {
		return dataUsage(binaryName)
	}
	action := args[0]
	var prefix, output string
	yes := false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--owner":
			i++
			if i >= len(args) || prefix != "" {
				return dataUsage(binaryName)
			}
			prefix = args[i]
		case "--output":
			i++
			if action != "export" || i >= len(args) || output != "" {
				return dataUsage(binaryName)
			}
			output = args[i]
		case "--yes":
			if action != "delete" || yes {
				return dataUsage(binaryName)
			}
			yes = true
		default:
			return dataUsage(binaryName)
		}
	}
	if prefix == "" {
		return dataUsage(binaryName)
	}
	if err := paths.EnsureDir(p.DB); err != nil {
		return maintenanceError(binaryName, err)
	}
	cfg, err := config.Load(p.ConfigFile, filepath.Join(p.DB, "server.db"))
	if err != nil {
		return maintenanceError(binaryName, err)
	}
	if err := authorizeMaintenance(binaryName, cfg.Server.Token); err != nil {
		return maintenanceError(binaryName, err)
	}
	h, err := db.Open(cfg.Server.Database.URL, db.DefaultPool(), nil)
	if err != nil {
		return maintenanceError(binaryName, err)
	}
	defer func() {
		if err := h.Close(); err != nil {
			exitCode = maintenanceError(binaryName, fmt.Errorf("close data maintenance database: %w", err))
		}
	}()
	tok, err := db.GetTokenByPrefix(context.Background(), h, prefix)
	if err != nil {
		return maintenanceError(binaryName, errors.New("owner token prefix not found"))
	}
	if action == "export" {
		return runDataExport(binaryName, p, cfg, h, tok.OwnerID, output)
	}
	return runDataDelete(binaryName, p, cfg, h, tok.OwnerID, yes)
}
func dataUsage(binaryName string) int {
	fmt.Fprintf(os.Stderr, "%s: usage: --maintenance data export|delete --owner <prefix> [--output FILE|--yes]\n", binaryName)
	return 2
}
func runDataExport(binaryName string, p paths.Paths, cfg *config.Config, h db.Handle, ownerID, output string) int {
	ctx := context.Background()
	owner, err := db.GetOwner(ctx, h, ownerID)
	if err != nil {
		return maintenanceError(binaryName, err)
	}
	tokens, err := db.ListOwnerTokens(ctx, h, ownerID)
	if err != nil {
		return maintenanceError(binaryName, err)
	}
	resources, err := ownerLinks(ctx, h, ownerID, cfg)
	if err != nil {
		return maintenanceError(binaryName, err)
	}
	result := dataExport{Format: 1, Resources: resources}
	result.Owner.ID = owner.ID
	result.Owner.CreatedAt = owner.CreatedAt.Format(time.RFC3339)
	result.Owner.LastSeenAt = owner.LastSeenAt.Format(time.RFC3339)
	for _, t := range tokens {
		item := exportToken{Prefix: t.TokenPrefix, Kind: t.Kind, Name: t.Name, CreatedAt: t.CreatedAt.Format(time.RFC3339), Status: t.Status()}
		if t.LastUsedAt != nil {
			v := t.LastUsedAt.Format(time.RFC3339)
			item.LastUsedAt = &v
		}
		if t.ExpiresAt != nil {
			v := t.ExpiresAt.Format(time.RFC3339)
			item.ExpiresAt = &v
		}
		result.Tokens = append(result.Tokens, item)
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return maintenanceError(binaryName, err)
	}
	requestID, err := applog.GenerateULID()
	if err != nil {
		return maintenanceError(binaryName, err)
	}
	auditOwnerID, err := applog.GenerateULID()
	if err != nil {
		return maintenanceError(binaryName, err)
	}
	if err := writeMaintenanceAuditDetails(p, "compliance.data_export_requested", auditOwnerID, "success", map[string]any{"request_id": requestID}); err != nil {
		return maintenanceError(binaryName, err)
	}
	if output == "-" {
		if _, err := fmt.Fprintln(os.Stdout, string(data)); err != nil {
			return maintenanceError(binaryName, err)
		}
		if err := writeMaintenanceAuditDetails(p, "compliance.data_export_completed", auditOwnerID, "success", map[string]any{"request_id": requestID, "file_size": len(data)}); err != nil {
			return maintenanceError(binaryName, err)
		}
		return 0
	}
	if output == "" {
		output = fmt.Sprintf("%s-export-%s-%s.json", paths.ProjectName, ownerID, time.Now().UTC().Format("20060102"))
	}
	f, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return maintenanceError(binaryName, err)
	}
	if _, err = f.Write(append(data, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(output)
		return maintenanceError(binaryName, err)
	}
	if err := os.Chmod(output, 0o600); err != nil {
		return maintenanceError(binaryName, err)
	}
	if err := writeMaintenanceAuditDetails(p, "compliance.data_export_completed", auditOwnerID, "success", map[string]any{"request_id": requestID, "file_size": len(data)}); err != nil {
		return maintenanceError(binaryName, err)
	}
	fmt.Printf("Data export written to %s\n", output)
	return 0
}
func ownerLinks(ctx context.Context, h db.Handle, ownerID string, cfg *config.Config) (out []exportResource, resultErr error) {
	rows, err := h.QueryContext(ctx, `SELECT short_code,destination_url,created_at,expires_at,click_count FROM links WHERE owner_id=? ORDER BY created_at,short_code`, ownerID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			out = nil
			resultErr = errors.Join(resultErr, fmt.Errorf("close owner links rows: %w", err))
		}
	}()
	proto := []string{"http", "https"}[boolIndex(cfg.Server.TLS.Enabled)]
	host := fqdn.GetFQDN(paths.ProjectName)
	base := proto + "://" + host
	if cfg.Server.Port != "" && cfg.Server.Port != "80" && cfg.Server.Port != "443" {
		base += ":" + cfg.Server.Port
	}
	basePath := strings.Trim(strings.TrimSpace(cfg.Server.BaseURL), "/")
	for rows.Next() {
		var r exportResource
		var created int64
		var expires sql.NullInt64
		if err := rows.Scan(&r.ShortCode, &r.DestinationURL, &created, &expires, &r.ClickCount); err != nil {
			return nil, err
		}
		r.CreatedAt = time.Unix(created, 0).UTC().Format(time.RFC3339)
		shortPath := "/" + r.ShortCode
		if basePath != "" {
			shortPath = "/" + basePath + shortPath
		}
		r.ShortURL = base + shortPath
		if expires.Valid {
			v := time.Unix(expires.Int64, 0).UTC().Format(time.RFC3339)
			r.ExpiresAt = &v
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func boolIndex(value bool) int {
	if value {
		return 1
	}
	return 0
}

func runDataDelete(binaryName string, p paths.Paths, cfg *config.Config, h db.Handle, ownerID string, yes bool) int {
	requestID, err := applog.GenerateULID()
	if err != nil {
		return maintenanceError(binaryName, err)
	}
	auditOwnerID, err := applog.GenerateULID()
	if err != nil {
		return maintenanceError(binaryName, err)
	}
	if err := writeMaintenanceAuditDetails(p, "compliance.data_deletion_requested", auditOwnerID, "success", map[string]any{"request_id": requestID}); err != nil {
		return maintenanceError(binaryName, err)
	}
	if !yes {
		answer, err := promptLine("Type owner ID to confirm deletion: ")
		if err != nil || strings.TrimSpace(answer) != ownerID {
			fmt.Fprintln(os.Stderr, "Deletion cancelled.")
			return 1
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	deletedCount := int64(0)
	anonymizedCount := int64(0)
	err = db.WithTransaction(ctx, h, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM owners WHERE id=?`, ownerID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return db.ErrNotFound
		}
		for _, statement := range []string{
			`DELETE FROM clicks WHERE link_id IN (SELECT id FROM links WHERE owner_id=?)`,
			`DELETE FROM api_tokens WHERE owner_id=?`,
			`DELETE FROM links WHERE owner_id=?`,
			`DELETE FROM owners WHERE id=?`,
		} {
			result, err := tx.ExecContext(ctx, statement, ownerID)
			if err != nil {
				return err
			}
			count, err := result.RowsAffected()
			if err != nil {
				return err
			}
			deletedCount += count
		}
		return nil
	})
	if err != nil {
		return maintenanceError(binaryName, err)
	}
	if err := writeMaintenanceAuditDetails(p, "compliance.data_deletion_completed", auditOwnerID, "success", map[string]any{"request_id": requestID, "deleted_count": deletedCount, "anonymized_count": anonymizedCount}); err != nil {
		return maintenanceError(binaryName, err)
	}
	fmt.Printf("Deleted %d items; anonymized %d retained items.\n", deletedCount, anonymizedCount)
	return 0
}

func runComplianceReport(binaryName string, p paths.Paths) (exitCode int) {
	if err := paths.EnsureDir(p.DB); err != nil {
		return maintenanceError(binaryName, err)
	}
	cfg, err := config.Load(p.ConfigFile, filepath.Join(p.DB, "server.db"))
	if err != nil {
		return maintenanceError(binaryName, err)
	}
	if err := authorizeMaintenance(binaryName, cfg.Server.Token); err != nil {
		return maintenanceError(binaryName, err)
	}
	h, err := db.Open(cfg.Server.Database.URL, db.DefaultPool(), nil)
	if err != nil {
		return maintenanceError(binaryName, err)
	}
	defer func() {
		if err := h.Close(); err != nil {
			exitCode = maintenanceError(binaryName, fmt.Errorf("close compliance report database: %w", err))
		}
	}()
	var owners, resources, active, reports int64
	for _, x := range []struct {
		q string
		v *int64
	}{{`SELECT COUNT(*) FROM owners`, &owners}, {`SELECT COUNT(*) FROM links`, &resources}, {`SELECT COUNT(*) FROM api_tokens WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at>strftime('%s','now'))`, &active}, {`SELECT COUNT(*) FROM security_reports WHERE closed_at=0`, &reports}} {
		if err := h.QueryRow(x.q).Scan(x.v); err != nil {
			return maintenanceError(binaryName, err)
		}
	}
	var backup sql.NullInt64
	if err := h.QueryRow(`SELECT MAX(created_at) FROM backups`).Scan(&backup); err != nil {
		return maintenanceError(binaryName, err)
	}
	if err := writeMaintenanceAuditDetails(p, "compliance.report_generated", "", "success", map[string]any{"report_type": "compliance", "generated_by": "operator"}); err != nil {
		return maintenanceError(binaryName, err)
	}
	standards := cfg.Server.Compliance.EnabledStandards()
	if len(standards) == 0 {
		standards = []string{"none"}
	}
	r := cfg.Server.Backup.Retention
	fmt.Printf("Enabled standards: %s\nCompliance mode: %t\nAudit retention: %d years\nPrivacy retention: %s\nBackup retention: max=%d weekly=%d monthly=%d yearly=%d max_total_size=%s\n", strings.Join(standards, ", "), cfg.Server.Compliance.Enabled, cfg.Server.Compliance.AuditRetentionYears(), cfg.Server.Privacy.Retention.Period, r.MaxBackups, r.KeepWeekly, r.KeepMonthly, r.KeepYearly, r.MaxTotalSize)
	fmt.Printf("Owners: %d\nResources: %d\nActive tokens: %d\nOpen security reports: %d\n", owners, resources, active, reports)
	if backup.Valid {
		fmt.Printf("Last backup: %s\n", time.Unix(backup.Int64, 0).UTC().Format(time.RFC3339))
	} else {
		fmt.Println("Last backup: never")
	}
	fmt.Printf("Compliance events (last 30 days): %d\n", countComplianceEvents(p.Logs, time.Now().UTC().Add(-30*24*time.Hour)))
	return 0
}

func countComplianceEvents(logDir string, since time.Time) int {
	files, err := filepath.Glob(filepath.Join(logDir, "audit.log*"))
	if err != nil {
		return 0
	}
	count := 0
	for _, name := range files {
		f, err := os.Open(name)
		if err != nil {
			continue
		}
		var input io.Reader = f
		var gz *gzip.Reader
		if strings.HasSuffix(name, ".gz") {
			gz, err = gzip.NewReader(f)
			if err != nil {
				_ = f.Close()
				continue
			}
			input = gz
		}
		dec := json.NewDecoder(input)
		for {
			var e struct {
				Time  time.Time `json:"time"`
				Event string    `json:"event"`
			}
			if err := dec.Decode(&e); err != nil {
				break
			}
			if strings.HasPrefix(e.Event, "compliance.") && !e.Time.Before(since) {
				count++
			}
		}
		if gz != nil {
			_ = gz.Close()
		}
		_ = f.Close()
	}
	return count
}

func writeMaintenanceAuditDetails(p paths.Paths, event, ownerID, result string, details map[string]any) error {
	if err := paths.EnsureDir(p.Logs); err != nil {
		return err
	}
	a, err := applog.NewAuditLogger(filepath.Join(p.Logs, "audit.log"))
	if err != nil {
		return err
	}
	entry := applog.Entry{Event: event, Category: "compliance", Severity: applog.SeverityInfo, Actor: applog.Actor{}, Details: details, Result: applog.Result(result)}
	if ownerID != "" {
		entry.Target = &applog.Target{Type: "owner", ID: ownerID}
	}
	writeErr := a.Write(entry)
	closeErr := a.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
