// --service command handling. See AI.md PART 23 "PRIVILEGE ESCALATION &
// SERVICE" for the install/uninstall/disable logic, the confirmation
// prompt, and the "Service Help Output" block reproduced below, and
// PART 24 "SERVICE SUPPORT" for the per-init-system templates. The
// platform work itself lives in src/service.
package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/apimgr/shortner/src/common/pidfile"
	"github.com/apimgr/shortner/src/paths"
	"github.com/apimgr/shortner/src/service"
)

// projectOrg is the user-facing organization from IDEA.md "Project
// variables"; it backs the systemd Documentation= URL and the derived
// macOS plist name (io.github.{project_org}.{internal_name}).
const projectOrg = "apimgr"

// appName is the human-readable application name from IDEA.md.
const serviceAppName = "Shortner"

// detectServiceManager returns the service manager currently supervising
// this process, used to decide daemonization behavior (see
// shouldDaemonize). It answers "who started me", which is a different
// question from src/service's Detect(), which answers "what would I
// install into".
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

// serviceHelpHeader is the fixed part of the --service --help output,
// transcribed from AI.md PART 23 "Service Help Output". The "Current
// status:" block below it is filled in from the live service state.
const serviceHelpHeader = `Service management commands:

start                                 - Start the service
stop                                  - Stop the service
restart                               - Restart the service
reload                                - Reload configuration without restart

--install                              - Install, enable, and start service
--disable                              - Stop and disable service (keeps data)
--uninstall                            - Stop, disable, and remove everything (keeps binary)

Current status:
`

// serviceParams builds the src/service parameters from the resolved
// runtime paths and the frozen project identifiers.
func serviceParams(p paths.Paths) service.Params {
	binary := p.Binary
	if exe, err := os.Executable(); err == nil && exe != "" {
		binary = exe
	}
	return service.Params{
		InternalName: paths.InternalName,
		InternalOrg:  paths.InternalOrg,
		ProjectName:  paths.ProjectName,
		ProjectOrg:   projectOrg,
		AppName:      serviceAppName,
		BinaryPath:   binary,
		ConfigDir:    p.Config,
		DataDir:      p.Data,
		CacheDir:     p.Cache,
		LogDir:       p.Logs,
		BackupDir:    p.Backup,
		PIDFile:      p.PIDFile,
		// PART 23 "Service Installation Logic" step 2: a system service
		// when root/admin, otherwise the user-level fallback.
		UserLevel: !service.IsElevated(),
	}
}

// printServiceHelp writes the PART 23 help block followed by the real
// detected status.
func printServiceHelp(p paths.Paths) {
	fmt.Print(serviceHelpHeader)

	params := serviceParams(p)
	mgr, err := service.New(params)
	if err != nil {
		fmt.Println("  Service:    not installed")
		fmt.Println("  State:      stopped")
		fmt.Println("  Auto-start: disabled")
		fmt.Printf("\n%s\n", err)
		return
	}

	st := mgr.Status()
	installed := "not installed"
	if st.Installed {
		installed = "installed"
	}
	autoStart := "disabled"
	if st.AutoStart {
		autoStart = "enabled"
	}
	fmt.Printf("  Service:    %s\n", installed)
	fmt.Printf("  State:      %s\n", st.State)
	fmt.Printf("  Auto-start: %s\n", autoStart)
	if st.PID > 0 {
		fmt.Printf("  PID:        %d\n", st.PID)
	}
	fmt.Printf("\nService manager: %s\n", mgr.Name())
}

// serviceNeedsPrivilege reports whether the string is a real --service
// command. Every one of them writes to system state and therefore needs
// root/Administrator unless it is a user-level install (AI.md PART 5
// "Commands Requiring Escalation"); --help is handled before this point.
func serviceNeedsPrivilege(command string) bool {
	switch command {
	case "--install", "--uninstall", "--disable", "start", "stop", "restart", "reload":
		return true
	default:
		return false
	}
}

// ensureServicePrivilege implements PART 5 "Smart escalation flow": do
// nothing when already elevated, inform (never prompt) when the user
// cannot escalate, and otherwise ask before re-executing elevated.
// It returns true when the caller should continue in this process.
func ensureServicePrivilege(binaryName string, userLevel bool) (bool, int) {
	if service.IsElevated() || userLevel {
		return true, 0
	}
	action := "Service management"

	if !service.CanEscalate() {
		fmt.Fprintf(os.Stderr, "%s: %s\n", binaryName, (&service.NoEscalationError{Action: action}).Error())
		return false, 1
	}

	fmt.Printf("%s requires administrator privileges.\n", action)
	answer, err := promptLine("Re-run with elevated privileges? [Y/n]: ")
	if err != nil && answer == "" {
		fmt.Fprintf(os.Stderr, "%s: escalation declined\n", binaryName)
		return false, 1
	}
	if answer != "" && !strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "yes") {
		fmt.Fprintf(os.Stderr, "%s: escalation declined\n", binaryName)
		return false, 1
	}

	if err := service.ExecElevated(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %s\n", binaryName, err)
		return false, 1
	}
	return false, 0
}

// runService dispatches --service COMMAND and returns the process exit
// code.
func runService(binaryName string, p paths.Paths, command string) int {
	switch command {
	case "", "help", "--help", "-h", "status":
		printServiceHelp(p)
		return 0
	}

	// Reject an unknown verb before touching the host, so a typo never
	// triggers an escalation prompt or an init-system probe.
	if !serviceNeedsPrivilege(command) {
		fmt.Fprintf(os.Stderr, "%s: unknown --service command %q (run '%s --service --help')\n", binaryName, command, binaryName)
		return 1
	}

	params := serviceParams(p)

	// A user-level fallback install never needs escalation; a system one
	// always does.
	proceed, code := ensureServicePrivilege(binaryName, params.UserLevel)
	if !proceed {
		return code
	}

	// PART 23 "Service Uninstall Logic": confirm before anything else
	// happens — the prompt precedes even the init-system probe.
	if command == "--uninstall" && !confirmUninstall(binaryName) {
		return 1
	}

	mgr, err := service.New(params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %s\n", binaryName, err)
		return 1
	}

	switch command {
	case "start":
		return serviceAction(binaryName, "started", mgr.Start)
	case "stop":
		return serviceAction(binaryName, "stopped", mgr.Stop)
	case "restart":
		return serviceAction(binaryName, "restarted", mgr.Restart)
	case "reload":
		return serviceAction(binaryName, "reloaded", mgr.Reload)
	case "--install":
		return runServiceInstall(binaryName, mgr, params)
	case "--disable":
		return runServiceDisable(binaryName, mgr)
	case "--uninstall":
		return runServiceUninstall(binaryName, mgr, params)
	}
	return 1
}

// serviceAction runs one service-manager verb and reports the outcome.
func serviceAction(binaryName, pastTense string, action func() error) int {
	if err := action(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %s\n", binaryName, err)
		return 1
	}
	fmt.Printf("Service %s.\n", pastTense)
	return 0
}

// runServiceInstall performs PART 23 "Service Installation Logic": write
// the service file, enable auto-start, and start the service. User,
// group, directory, and permission setup deliberately do NOT happen here
// — the server does that during normal startup.
func runServiceInstall(binaryName string, mgr service.Manager, params service.Params) int {
	if err := service.Install(mgr); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %s\n", binaryName, err)
		return 1
	}

	scope := "system"
	if params.UserLevel {
		scope = "user"
	}
	fmt.Printf("Installed %s %s service (%s).\n", scope, mgr.Name(), params.InternalName)
	for _, file := range mgr.Files() {
		fmt.Printf("  %s\n", file)
	}
	fmt.Println("Service enabled and started.")
	return 0
}

// runServiceDisable performs PART 23 "Service Disable Logic": stop and
// disable only — the service file, config, data, and user all stay.
func runServiceDisable(binaryName string, mgr service.Manager) int {
	if err := service.Disable(mgr); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %s\n", binaryName, err)
		return 1
	}
	fmt.Println("Service stopped and disabled. Data, configuration, and the service file were kept.")
	fmt.Println("Re-enable with: --service --install")
	return 0
}

// confirmUninstall asks the mandatory PART 23 confirmation question and
// reports whether the operator agreed. Anything other than an explicit
// yes cancels.
func confirmUninstall(binaryName string) bool {
	answer, err := promptLine("This will delete ALL data, configs, and the system user. Continue? [y/N]: ")
	if err != nil || (!strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "yes")) {
		fmt.Fprintf(os.Stderr, "%s: uninstall cancelled\n", binaryName)
		return false
	}
	return true
}

// runServiceUninstall performs PART 23 "Service Uninstall Logic" steps
// 1-6. The confirmation gate has already been passed by this point.
func runServiceUninstall(binaryName string, mgr service.Manager, params service.Params) int {
	result, err := service.Uninstall(mgr, params)
	for _, path := range result.RemovedPaths {
		fmt.Printf("Removed %s\n", path)
	}
	if result.RemovedUser {
		fmt.Printf("Removed system user and group %s\n", result.UserName)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %s\n", binaryName, err)
		return 1
	}

	fmt.Printf("Service uninstalled. Delete binary manually: rm %s\n", params.BinaryPath)
	return 0
}
