// --help and --version output. See AI.md PART 8 "Server --help Output".
package main

import (
	"fmt"

	"github.com/apimgr/shortner/src/common/version"
)

// projectDescription is the one-line project description shown in --help,
// from IDEA.md "## Project description".
const projectDescription = "A self-hosted URL shortening service with an API and web interface"

// helpTemplate mirrors AI.md PART 8 "Server --help Output" verbatim,
// substituting the actual binary name (never the frozen project name).
const helpTemplate = `%[1]s %[2]s - %[3]s

Usage:
  %[1]s [flags]

Information:
-h, --help                             - Show help (--help for any command shows its help)
-v, --version                          - Show version
--status                               - Show server status and health

Shell Integration:
--shell completions [SHELL]            - Print shell completions
--shell init [SHELL]                   - Print shell init command
--shell help                           - Show shell help

Server Configuration:
--mode {production|development|debug}  - Application mode (default: production)
--config DIR                           - Config directory
--data DIR                             - Data directory
--cache DIR                            - Cache directory
--log DIR                              - Log directory
--backup DIR                           - Backup directory
--pid FILE                             - PID file path
--address ADDR                         - Listen address (default: 0.0.0.0)
--port PORT                            - Listen port (default: random 64xxx, 80 in container)
--baseurl PATH                         - URL path prefix (default: /)
--daemon                               - Run as daemon (detach from terminal)
--debug                                - Enable debug mode
--color {auto|yes|no}                  - Color output (default: auto)
--lang CODE                            - Language for output (default: auto)

Service Management:
--service CMD                          - Service management (run --service help for details)
--maintenance CMD                      - Maintenance operations (run --maintenance help for details)
--update [CMD]                         - Check/perform updates (run --update help for details)
--include-ssl                          - Include SSL certificates in --maintenance backup
--include-data                         - Include the data directory in --maintenance backup

Run '%[1]s <command> help' for detailed help on any command.
`

// printHelp prints the server --help output for binaryName.
func printHelp(binaryName string) {
	fmt.Printf(helpTemplate, binaryName, version.Version, projectDescription)
}

// printVersion prints the server --version output for binaryName.
func printVersion(binaryName string) {
	fmt.Println(binaryName + " " + version.Full())
}
