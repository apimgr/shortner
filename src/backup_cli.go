// --maintenance backup / --maintenance restore handling. See AI.md PART 21
// "BACKUP & RESTORE": archive creation with optional AES-256-GCM
// encryption, the mandatory verification suite, retention, and the
// authorization/password rules a CLI restore must satisfy.
package main

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/backup"
	"github.com/apimgr/shortner/src/common/version"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/paths"
	"github.com/apimgr/shortner/src/scheduler"
)

// backupEnv is the resolved state both backup subcommands need: paths from
// the startup resolution and the loaded configuration.
type backupEnv struct {
	paths  paths.Paths
	cfg    *config.Config
	audit  backup.Auditor
	dbPath string
}

// promptPassword reads a password from the terminal without echoing it.
// AI.md PART 21 is explicit that there is no password flag — "passwords on
// the command line leak via shell history and process lists".
var promptPassword = func(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("a terminal is required to enter the backup password")
	}
	fmt.Fprint(os.Stderr, prompt)
	pw, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return string(pw), nil
}

// promptLine reads one plain line of input (confirmations, operator token).
var promptLine = func(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	var line string
	if _, err := fmt.Fscanln(os.Stdin, &line); err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// newBackupEnv loads the configuration and opens the audit log for a
// backup/restore subcommand. The audit log is best-effort: failing to open
// it must not block an operator's backup.
func newBackupEnv(p paths.Paths) (*backupEnv, error) {
	dbPath := filepath.Join(p.DB, "server.db")
	cfg, err := config.Load(p.ConfigFile, dbPath)
	if err != nil {
		return nil, err
	}
	env := &backupEnv{paths: p, cfg: cfg, dbPath: dbPath}
	if logger, err := applog.NewAuditLogger(filepath.Join(p.Logs, "audit.log")); err == nil {
		env.audit = logger
	}
	return env, nil
}

// wantsEncryption reports whether this backup should be encrypted, per the
// AI.md PART 21 "Backup Encryption" matrix: compliance mode forces it, and
// otherwise the `server.backup.encryption.enabled` toggle decides.
func (e *backupEnv) wantsEncryption() bool {
	return e.cfg.Server.Compliance.Enabled || e.cfg.Server.Backup.Encryption.Enabled
}

// resolvePassword returns the backup password, preferring the configured
// one (which is what unattended scheduled backups use) and otherwise
// prompting interactively.
func (e *backupEnv) resolvePassword(prompt string) (string, error) {
	if pw := e.cfg.Server.Backup.EncryptionPassword; pw != "" {
		return pw, nil
	}
	return promptPassword(prompt)
}

// runBackupCreate implements `--maintenance backup [FILE]`.
func runBackupCreate(binaryName string, p paths.Paths, name string, includeSSL, includeData bool) int {
	env, err := newBackupEnv(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}

	encrypt := env.wantsEncryption()
	var password string
	if encrypt {
		password, err = env.resolvePassword("Enter backup password: ")
		if err != nil {
			fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
			return 1
		}
		if password == "" {
			fmt.Fprintln(os.Stderr, binaryName+": backup password must not be empty")
			return 1
		}
	} else if env.cfg.Server.Compliance.Enabled {
		fmt.Fprintln(os.Stderr, binaryName+": "+backup.ErrComplianceEncryptionRequired.Error())
		return 1
	} else {
		// AI.md PART 21 "Warning Shown if Encryption Not Enabled".
		fmt.Fprintln(os.Stderr, binaryName+": WARNING: "+backup.UnencryptedWarning)
	}

	opts := backup.Options{
		Dir:         p.Backup,
		Prefix:      paths.ProjectName,
		Kind:        backup.KindManual,
		Name:        name,
		ConfigFile:  p.ConfigFile,
		DBPath:      env.dbPath,
		ConfigDir:   p.Config,
		DataDir:     p.Data,
		IncludeSSL:  includeSSL,
		IncludeData: includeData,
		Encrypt:     encrypt,
		Password:    password,
		Compliance:  env.cfg.Server.Compliance.Enabled,
		CreatedBy:   "operator",
		AppVersion:  version.String(),
	}

	fmt.Println("Creating backup...")
	res, err := backup.Create(opts)
	if err != nil {
		backup.AuditFailure(env.audit, name, err)
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	backup.AuditCreated(env.audit, res)
	fmt.Printf("Verifying backup integrity... OK\nBackup created: %s (%d bytes, encrypted=%t)\n",
		res.Path, res.Size, res.Encrypted)

	// AI.md PART 21 "Backup Cleanup Logic" — retention runs after every
	// successful, verified backup, never before.
	if deleted := applyRetention(env, p); len(deleted) > 0 {
		fmt.Printf("Retention: removed %d old backup(s)\n", len(deleted))
	}
	return 0
}

// applyRetention runs the retention sweep and audits what it deleted.
func applyRetention(env *backupEnv, p paths.Paths) []backup.File {
	deps := scheduler.BackupDeps{
		Dir:        p.Backup,
		Prefix:     paths.ProjectName,
		Cfg:        env.cfg.Server.Backup,
		Compliance: env.cfg.Server.Compliance.Enabled,
	}
	status, err := backup.Disk(p.Backup)
	if err != nil {
		return nil
	}
	deleted, err := backup.Apply(p.Backup, paths.ProjectName, deps.Policy(status))
	if err != nil || len(deleted) == 0 {
		return nil
	}
	remaining, _ := backup.List(p.Backup, paths.ProjectName)
	backup.AuditRetentionCleanup(env.audit, deleted, len(remaining), "retention policy applied after verified backup")
	return deleted
}

// runBackupRestore implements `--maintenance restore FILE`, enforcing the
// AI.md PART 21 "Restore Authorization" table and password prompt.
func runBackupRestore(binaryName string, p paths.Paths, file string) int {
	if file == "" {
		fmt.Fprintf(os.Stderr, "%s: --maintenance restore requires a backup file\n", binaryName)
		return 1
	}

	env, err := newBackupEnv(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}

	path := file
	if !filepath.IsAbs(path) {
		if _, statErr := os.Stat(path); statErr != nil {
			path = filepath.Join(p.Backup, file)
		}
	}

	if code := authorizeRestore(binaryName, env, p); code != 0 {
		return code
	}

	var password string
	if backup.IsEncryptedName(path) {
		password, err = promptPassword("Enter backup password: ")
		if err != nil {
			fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
			return 1
		}
	}

	fmt.Print("Verifying backup integrity... ")
	res, err := backup.Restore(backup.RestoreOptions{
		Path:       path,
		Password:   password,
		ConfigFile: p.ConfigFile,
		DBPath:     env.dbPath,
		ConfigDir:  p.Config,
		DataDir:    p.Data,
		AppVersion: version.String(),
	})
	if err != nil {
		fmt.Println("FAILED")
		backup.AuditFailure(env.audit, filepath.Base(path), err)
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	fmt.Println("OK")

	for _, w := range res.Warnings {
		fmt.Fprintln(os.Stderr, binaryName+": WARNING: "+w)
	}
	backup.AuditRestored(env.audit, filepath.Base(path), res)
	fmt.Printf("Restored %d file(s) from %s\n", len(res.Restored), filepath.Base(path))
	return 0
}

// authorizeRestore applies the AI.md PART 21 "Restore Authorization" table:
// an empty database restores freely, root restores after confirmation, the
// service user must supply the operator token, and anyone else is denied.
func authorizeRestore(binaryName string, env *backupEnv, p paths.Paths) int {
	auth := backup.Authorize(backup.AuthEnv{
		DatabaseEmpty:  backup.DatabaseEmpty(env.dbPath),
		IsRoot:         paths.IsPrivileged(),
		ConfigReadable: backup.ConfigReadable(p.ConfigFile),
	})

	switch auth {
	case backup.AuthAllowed:
		return 0
	case backup.AuthConfirm:
		answer, err := promptLine("This overwrites the current configuration and database. Continue? [y/N]: ")
		if err != nil || !strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "yes") {
			fmt.Fprintln(os.Stderr, binaryName+": restore cancelled")
			return 1
		}
		return 0
	case backup.AuthToken:
		token, err := promptPassword("Enter operator token: ")
		if err != nil {
			fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
			return 1
		}
		if env.cfg.Server.Token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(env.cfg.Server.Token)) != 1 {
			fmt.Fprintln(os.Stderr, binaryName+": invalid operator token")
			return 1
		}
		return 0
	default:
		fmt.Fprintln(os.Stderr, binaryName+": "+backup.ErrRestoreDenied.Error())
		return 1
	}
}
