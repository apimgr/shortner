// --maintenance command handling. See AI.md PART 8 "Server Binary
// Commands". `backup` and `restore` are implemented (AI.md PART 21, see
// backup_cli.go); the remaining actions depend on self-update (PART 22) and
// the token/config/compliance groundwork in PART 11/12 — all tracked in
// TODO.AI.md — and report themselves as honestly not yet available.
package main

import (
	"fmt"
	"os"

	"github.com/apimgr/shortner/src/paths"
)

// maintenanceHelp is the --maintenance --help text.
const maintenanceHelp = `Usage: %s --maintenance COMMAND [ARG]

Perform server maintenance operations.

Commands:
  backup [FILE]        Create a backup archive (--include-ssl, --include-data)
  restore FILE         Restore from a backup archive
  update                Update the binary to the latest release
  mode {production|development}
                         Set and persist the application mode
  setup                 Reset server configuration to defaults
  pgp                   Manage the backup encryption PGP key
  secret [NAME]         Manage stored secrets
  token [ACTION]         Manage the operator token
  data [ACTION]          Manage stored application data
  compliance [ACTION]    Data-compliance operations (export/erase)
  --help                 Show this help
`

// maintenanceReadDeps maps each --maintenance action to the AI.md PART(s)
// implementing its real behavior, for the honest "not yet available"
// message.
var maintenanceReadDeps = map[string]string{
	"data":       "PART 21",
	"compliance": "PART 21",
	"update":     "PART 22",
	"mode":       "PART 11, 12",
	"setup":      "PART 11, 12",
	"pgp":        "PART 11, 21",
	"secret":     "PART 11",
	"token":      "PART 11",
}

// maintenanceOptions carries the resolved runtime state the implemented
// maintenance actions need. Paths come from the startup resolution so
// backup/restore use exactly the directories the running server would.
type maintenanceOptions struct {
	paths paths.Paths
	// arg is the optional positional argument: the backup filename for
	// `backup`, the archive to restore for `restore`.
	arg         string
	includeSSL  bool
	includeData bool
}

// runMaintenance dispatches --maintenance COMMAND [ARG] and returns the
// process exit code.
func runMaintenance(binaryName, command string, opts maintenanceOptions) int {
	switch command {
	case "", "help", "--help", "-h":
		fmt.Printf(maintenanceHelp, binaryName)
		return 0
	case "backup":
		return runBackupCreate(binaryName, opts.paths, opts.arg, opts.includeSSL, opts.includeData)
	case "restore":
		return runBackupRestore(binaryName, opts.paths, opts.arg)
	}

	part, ok := maintenanceReadDeps[command]
	if !ok {
		fmt.Fprintf(os.Stderr, "%s: unknown --maintenance command %q (run '%s --maintenance --help')\n", binaryName, command, binaryName)
		return 1
	}
	fmt.Fprintf(os.Stderr, "%s: --maintenance %s is not yet available (see TODO.AI.md, AI.md %s)\n", binaryName, command, part)
	return 1
}
