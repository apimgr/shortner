// --maintenance command handling. See AI.md PART 8 "Server Binary
// Commands". Backup/restore, operator token management, PGP management,
// and update are dispatched to their PART-specific implementations; other
// actions are dispatched to their PART-specific implementations.
package main

import (
	"crypto/subtle"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/paths"
	"github.com/apimgr/shortner/src/service"
)

// maintenanceHelp is the --maintenance --help text.
const maintenanceHelp = `Usage: %s --maintenance COMMAND [ARG]

Perform server maintenance operations.

Commands:
  backup [FILE]         Create a backup archive (--include-ssl, --include-data)
  backup list           List available backup archives
  backup delete FILE    Delete one backup archive
  restore FILE         Restore from a backup archive
  update                Update the binary to the latest release
  mode {production|development|debug}
                         Set and persist the application mode
  setup                 Reset server configuration to defaults
  pgp [ACTION]          Manage the security-report PGP key
  security list|show|update
                         Manage coordinated-disclosure reports
  secret rotate NAME    Rotate installation_secret or encryption_key
  token list|revoke <PREFIX>
                         List masked owner tokens or revoke a token
  data export|delete --owner PREFIX [--output FILE|--yes]
                         Export or delete an owner's data
  compliance report     Print the operator-wide compliance summary
  --help                 Show this help
`

// maintenanceOptions carries the resolved runtime state the implemented
// maintenance actions need. Paths come from the startup resolution so
// backup/restore use exactly the directories the running server would.
type maintenanceOptions struct {
	paths paths.Paths
	// arg is the optional positional argument: the backup filename for
	// `backup`, the archive to restore for `restore`.
	arg string
	// args is every positional argument, needed by the `update` alias,
	// whose own subcommand takes an argument of its own
	// (`--maintenance update branch beta`).
	args        []string
	includeSSL  bool
	includeData bool
}

// argAt returns args[i], or "" when there is no such positional argument.
func argAt(args []string, i int) string {
	if i >= len(args) {
		return ""
	}
	return args[i]
}

// runMaintenance dispatches --maintenance COMMAND [ARG] and returns the
// process exit code.
func runMaintenance(binaryName, command string, opts maintenanceOptions) int {
	switch command {
	case "", "help", "--help", "-h":
		fmt.Printf(maintenanceHelp, binaryName)
		return 0
	case "backup":
		if action := argAt(opts.args, 0); action == "list" {
			return runBackupList(binaryName, opts.paths)
		} else if action == "delete" {
			return runBackupDelete(binaryName, opts.paths, argAt(opts.args, 1))
		}
		return runBackupCreate(binaryName, opts.paths, opts.arg, opts.includeSSL, opts.includeData)
	case "restore":
		return runBackupRestore(binaryName, opts.paths, opts.arg)
	case "pgp":
		return runPGP(binaryName, opts.paths, opts.args)
	case "token":
		return runTokenMaintenance(binaryName, opts.paths, opts.args)
	case "update":
		// AI.md PART 22: `--maintenance update [cmd]` is an alias for
		// `--update [cmd]`, with the same subcommands and the same
		// default (`yes`) when none is given.
		return runUpdate(binaryName, opts.paths, argAt(opts.args, 0), argAt(opts.args, 1))
	case "mode":
		return runModeMaintenance(binaryName, opts.paths, argAt(opts.args, 0))
	case "setup":
		return runSetupMaintenance(binaryName, opts.paths)
	case "secret":
		return runSecretMaintenance(binaryName, opts.paths, opts.args)
	case "security":
		return runSecurityReportMaintenance(binaryName, opts.paths, opts.args)
	case "data":
		return runDataMaintenance(binaryName, opts.paths, opts.args)
	case "compliance":
		if argAt(opts.args, 0) != "report" || len(opts.args) != 1 {
			fmt.Fprintf(os.Stderr, "%s: usage: --maintenance compliance report\n", binaryName)
			return 2
		}
		return runComplianceReport(binaryName, opts.paths)
	}

	fmt.Fprintf(os.Stderr, "%s: unknown --maintenance command %q (run '%s --maintenance --help')\n", binaryName, command, binaryName)
	return 1
}

func runModeMaintenance(binaryName string, p paths.Paths, value string) int {
	if value != "production" && value != "development" && value != "debug" {
		fmt.Fprintf(os.Stderr, "%s: mode must be production, development, or debug\n", binaryName)
		return 2
	}
	cfg, err := config.Load(p.ConfigFile, filepath.Join(p.DB, "server.db"))
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	if err := authorizeModeMaintenance(cfg.Server.Token); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	fmt.Fprintln(os.Stderr, "Warning: changing mode can relax security controls.")
	cfg.Server.Mode = value
	if err := config.Save(p.ConfigFile, cfg); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	fmt.Printf("Application mode set to %s. Restart the server to apply it.\n", value)
	return 0
}

func authorizeModeMaintenance(operatorToken string) error {
	if service.IsElevated() {
		return nil
	}
	current, err := user.Current()
	if err != nil || current.Username != paths.InternalName {
		return fmt.Errorf("mode changes require root/administrator or the service account")
	}
	entered, err := promptPassword("Enter operator token: ")
	if err != nil || operatorToken == "" || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(entered)), []byte(operatorToken)) != 1 {
		return fmt.Errorf("operator authorization failed")
	}
	return nil
}

func runSetupMaintenance(binaryName string, p paths.Paths) int {
	if err := paths.EnsureDir(p.DB); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	cfg, err := config.Load(p.ConfigFile, filepath.Join(p.DB, "server.db"))
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	sqlDB, err := db.Open(filepath.Join(p.DB, "server.db"), db.DefaultPool(), nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	defer sqlDB.Close()
	var resources int64
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM links`).Scan(&resources); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	if resources > 0 && !service.IsElevated() {
		fmt.Fprintln(os.Stderr, "Setup already completed. Edit server.yml directly and restart the server, or run setup as root.")
		return 1
	}
	if service.IsElevated() {
		answer, promptErr := promptLine("This resets server.yml to defaults. Continue? [y/N]: ")
		if promptErr != nil || (answer != "y" && answer != "Y" && answer != "yes" && answer != "YES") {
			fmt.Fprintln(os.Stderr, "Setup cancelled.")
			return 1
		}
	}
	defaults := config.Default(filepath.Join(p.DB, "server.db"))
	defaults.Server.Token = cfg.Server.Token
	defaults.Server.Security.EncryptionKey = cfg.Server.Security.EncryptionKey
	if defaults.Server.Token == "" {
		if _, err := config.EnsureToken(defaults); err != nil {
			fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
			return 1
		}
	}
	if _, err := config.EnsureEncryptionKey(defaults); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	if err := config.Save(p.ConfigFile, defaults); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	fmt.Println("Server configuration reset to defaults.")
	return 0
}

func authorizeMaintenance(binaryName, operatorToken string) error {
	if service.IsElevated() {
		return nil
	}
	entered, err := promptPassword("Enter operator token: ")
	if err != nil {
		return fmt.Errorf("operator authorization failed")
	}
	if operatorToken == "" || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(entered)), []byte(operatorToken)) != 1 {
		return fmt.Errorf("operator authorization failed")
	}
	return nil
}
