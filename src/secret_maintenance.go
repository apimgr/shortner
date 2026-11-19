package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/paths"
	"github.com/apimgr/shortner/src/pgp"
)

func runSecretMaintenance(binaryName string, p paths.Paths, args []string) int {
	if len(args) != 2 || args[0] != "rotate" {
		fmt.Fprintf(os.Stderr, "Usage: %s --maintenance secret rotate {installation_secret|encryption_key}\n", binaryName)
		return 2
	}
	name := args[1]
	if name != db.SecretInstallation && name != "encryption_key" {
		fmt.Fprintf(os.Stderr, "%s: secret %q cannot be manually rotated\n", binaryName, name)
		return 2
	}
	for _, dir := range []string{p.DB, p.Logs} {
		if err := paths.EnsureDir(dir); err != nil {
			return maintenanceError(binaryName, err)
		}
	}
	cfg, err := config.Load(p.ConfigFile, filepath.Join(p.DB, "server.db"))
	if err != nil {
		return maintenanceError(binaryName, err)
	}
	if err := authorizeMaintenance(binaryName, cfg.Server.Token); err != nil {
		return maintenanceError(binaryName, err)
	}
	answer, err := promptLine("Type ROTATE " + name + " to confirm: ")
	if err != nil || strings.TrimSpace(answer) != "ROTATE "+name {
		fmt.Fprintln(os.Stderr, "Secret rotation cancelled.")
		return 1
	}
	sqlDB, err := db.Open(filepath.Join(p.DB, "server.db"), db.DefaultPool(), nil)
	if err != nil {
		return maintenanceError(binaryName, err)
	}
	defer sqlDB.Close()
	audit, err := applog.NewAuditLogger(filepath.Join(p.Logs, "audit.log"))
	if err != nil {
		return maintenanceError(binaryName, err)
	}
	defer audit.Close()
	if name == db.SecretInstallation {
		err = rotateInstallationSecret(sqlDB, p.Config, audit)
	} else {
		err = rotateEncryptionKey(sqlDB, p.ConfigFile, cfg, audit)
	}
	if err != nil {
		return maintenanceError(binaryName, err)
	}
	fmt.Printf("Secret %s rotated successfully. Restart the server to apply it.\n", name)
	return 0
}

func rotateInstallationSecret(sqlDB db.Handle, configDir string, audit *applog.AuditLogger) error {
	ctx := context.Background()
	oldSecret, err := db.GetSecret(ctx, sqlDB, db.SecretInstallation)
	if err != nil {
		return err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	newSecret := base64.RawStdEncoding.EncodeToString(raw)
	store := pgp.NewStore(configDir)
	var private *pgp.Keypair
	if store.HasKeypair() {
		private, err = store.ReadPrivateKey(oldSecret)
		if err != nil {
			return fmt.Errorf("decrypt existing PGP private key: %w", err)
		}
	}
	now := time.Now().UTC()
	if err := db.SetGraceSecret(ctx, sqlDB, db.SecretInstallation, oldSecret, now.Add(7*24*time.Hour)); err != nil {
		return err
	}
	if err := db.SetSecret(ctx, sqlDB, db.SecretInstallation, newSecret); err != nil {
		return err
	}
	if private != nil {
		if err := store.Write(private, newSecret); err != nil {
			_ = db.SetSecret(ctx, sqlDB, db.SecretInstallation, oldSecret)
			return err
		}
	}
	return audit.Write(applog.Entry{Event: "security.installation_secret_rotated", Category: "security", Severity: applog.SeverityWarn, Details: map[string]any{"operator_reason": "manual rotation"}, Result: applog.ResultSuccess})
}

func rotateEncryptionKey(sqlDB db.Handle, configPath string, cfg *config.Config, audit *applog.AuditLogger) error {
	oldKey, err := config.DecodeEncryptionKey(cfg)
	if err != nil {
		return err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	newEncoded := base64.StdEncoding.EncodeToString(raw)
	newKey := raw
	oldEncoded := cfg.Server.Security.EncryptionKey
	ctx := context.Background()
	if err := db.SetGraceSecret(ctx, sqlDB, "encryption_key", oldEncoded, time.Now().UTC().Add(30*24*time.Hour)); err != nil {
		return err
	}
	if err := db.ReencryptStoredData(ctx, sqlDB, oldKey, newKey); err != nil {
		return err
	}
	if err := config.ReencryptDNSCredentials(cfg, oldKey, newKey); err != nil {
		_ = db.ReencryptStoredData(ctx, sqlDB, newKey, oldKey)
		return err
	}
	cfg.Server.Security.EncryptionKey = newEncoded
	if err := config.Save(configPath, cfg); err != nil {
		_ = db.ReencryptStoredData(ctx, sqlDB, newKey, oldKey)
		return err
	}
	return audit.Write(applog.Entry{Event: "security.encryption_key_rotated", Category: "security", Severity: applog.SeverityWarn, Details: map[string]any{"operator_reason": "manual rotation"}, Result: applog.ResultSuccess})
}

func maintenanceError(binaryName string, err error) int {
	fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
	return 1
}
