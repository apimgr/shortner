// --service command handling. See AI.md PART 8 "Server Binary Commands"
// and "Service Manager Detection". The actual install/start/stop actions
// depend on PART 23/24 (systemd/launchd/Windows service unit generation
// and installation), tracked in TODO.AI.md — this file implements the
// full flag parsing, dispatch, and --help text now, and reports each
// action as honestly not yet available.
package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/apimgr/shortner/src/common/pidfile"
)

// detectServiceManager returns the active service manager, used to decide
// daemonization behavior for --service start (see shouldDaemonize) and to
// inform --service --help output.
func detectServiceManager() string {
	if pidfile.IsContainer() {
		return "container"
	}

	ppid := os.Getppid()

	if ppid == 1 {
		if _, err := os.Stat("/run/systemd/system"); err == nil {
			return "systemd"
		}
	}
	if os.Getenv("INVOCATION_ID") != "" {
		return "systemd"
	}

	if runtime.GOOS == "darwin" && ppid == 1 {
		return "launchd"
	}

	if os.Getenv("SVDIR") != "" {
		return "runit"
	}
	if os.Getenv("S6_LOGGING") != "" {
		return "s6"
	}

	if ppid == 1 {
		if _, err := os.Stat("/etc/init.d"); err == nil {
			if _, err := os.Stat("/run/systemd/system"); os.IsNotExist(err) {
				return "sysv"
			}
		}
	}

	if _, err := os.Stat("/etc/rc.subr"); err == nil {
		return "rcd"
	}

	return "manual"
}

// serviceHelp is the --service --help text.
const serviceHelp = `Usage: %s --service COMMAND

Manage the %s system service (systemd, launchd, SysV, or Windows Service,
auto-detected).

Commands:
  start          Start the service
  stop           Stop the service
  restart        Stop then start the service
  reload         Reload configuration without restarting
  --install      Install and enable the service, then start it
  --uninstall    Stop, disable, and remove the service
  --disable      Stop and disable the service (keeps the unit file)
  --help         Show this help

Detected service manager: %s
`

// runService dispatches --service COMMAND and returns the process exit
// code.
func runService(binaryName, command string) int {
	switch command {
	case "", "help", "--help", "-h":
		fmt.Printf(serviceHelp, binaryName, binaryName, detectServiceManager())
		return 0
	case "start", "stop", "restart", "reload", "--install", "--uninstall", "--disable":
		fmt.Fprintf(os.Stderr, "%s: --service %s is not yet available (see TODO.AI.md, AI.md PART 23/24)\n", binaryName, command)
		return 1
	default:
		fmt.Fprintf(os.Stderr, "%s: unknown --service command %q (run '%s --service --help')\n", binaryName, command, binaryName)
		return 1
	}
}
