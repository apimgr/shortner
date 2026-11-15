package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/paths"
)

func runTokenMaintenance(binaryName string, resolved paths.Paths, args []string) int {
	action := argAt(args, 0)
	if action != "list" && action != "revoke" {
		fmt.Fprintf(os.Stderr, "%s: token action must be 'list' or 'revoke'\n", binaryName)
		return 2
	}
	if action == "revoke" && argAt(args, 1) == "" {
		fmt.Fprintf(os.Stderr, "%s: token revoke requires a token prefix\n", binaryName)
		return 2
	}
	cfg, err := config.Load(resolved.ConfigFile, filepath.Join(resolved.DB, "server.db"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", binaryName, err)
		return 1
	}
	for _, warning := range config.Validate(cfg) {
		fmt.Fprintf(os.Stderr, "%s: warning: %s\n", binaryName, warning)
	}
	sqlDB, err := db.Open(cfg.Server.Database.URL, db.DefaultPool(), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", binaryName, err)
		return 1
	}
	defer sqlDB.Close()
	ctx := context.Background()
	if action == "revoke" {
		token, err := db.GetTokenByPrefix(ctx, sqlDB, argAt(args, 1))
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: could not find token: %v\n", binaryName, err)
			return 1
		}
		audit, err := applog.NewAuditLogger(filepath.Join(resolved.Logs, "audit.log"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: could not open audit log: %v\n", binaryName, err)
			return 1
		}
		defer audit.Close()
		if err := db.RevokeToken(ctx, sqlDB, argAt(args, 1), "operator"); err != nil {
			fmt.Fprintf(os.Stderr, "%s: could not revoke token: %v\n", binaryName, err)
			return 1
		}
		if err := audit.Write(applog.Entry{Event: "token.revoked", Category: "token", Severity: applog.SeverityInfo, Target: &applog.Target{Type: "api_token", ID: token.TokenPrefix}, Details: map[string]any{"owner_id": token.OwnerID, "token_prefix": token.TokenPrefix}, Result: applog.ResultSuccess}); err != nil {
			fmt.Fprintf(os.Stderr, "%s: token revoked but audit write failed: %v\n", binaryName, err)
			return 1
		}
		fmt.Fprintf(os.Stdout, "Revoked token %s\n", argAt(args, 1))
		return 0
	}
	tokens, err := db.ListAllTokens(ctx, sqlDB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: could not list tokens: %v\n", binaryName, err)
		return 1
	}
	fmt.Fprintln(os.Stdout, "prefix\tkind\towner\tname\tcreated\tlast_used\tstatus")
	for _, token := range tokens {
		name := token.Name
		if name == "" {
			name = "-"
		}
		fmt.Fprintf(os.Stdout, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", token.TokenPrefix, token.Kind, token.OwnerID, name, token.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"), tokenLastUsed(token.LastUsedAt), token.Status())
	}
	return 0
}

func tokenLastUsed(value *time.Time) string {
	if value == nil {
		return "-"
	}
	return value.Format("2006-01-02T15:04:05Z")
}
