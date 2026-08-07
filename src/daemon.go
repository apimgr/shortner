// Daemonization support for the --daemon flag. See AI.md PART 8
// "Daemonization (--daemon flag)". Platform-specific Daemonize() lives in
// daemon_unix.go and daemon_windows.go.
package main

// filterDaemonFlag removes --daemon from args before re-exec, to prevent
// an infinite fork loop.
func filterDaemonFlag(args []string) []string {
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		if arg != "--daemon" {
			filtered = append(filtered, arg)
		}
	}
	return filtered
}

// shouldDaemonize decides whether to daemonize, given whether this is a
// --service start invocation, the --daemon flag, and server.yml's
// daemonize setting (not yet part of the config schema — PART 12 — so
// callers currently always pass configDaemonize=false).
func shouldDaemonize(isServiceStart, daemonFlag, configDaemonize bool) bool {
	if isServiceStart {
		switch detectServiceManager() {
		case "sysv", "rcd":
			// These init systems expect the process to background itself.
			return true
		default:
			// systemd, launchd, runit, s6, container, manual: foreground.
			return false
		}
	}
	if daemonFlag {
		return true
	}
	return configDaemonize
}
