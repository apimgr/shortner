// Daemonization support for the --daemon flag. See AI.md PART 8
// "Daemonization (--daemon flag)". Platform-specific Daemonize() lives in
// daemon_unix.go and daemon_windows.go.
package main

// filterDaemonFlag removes the daemon flag from args before re-exec, to
// prevent an infinite fork loop. AI.md PART 8 "Daemonization" shows this
// filtering only "--daemon" and "-d", but main.go registers the flag as
// fs.Bool("daemon", ...) with no "-d" alias — Go's flag package accepts
// both "-daemon" and "--daemon" as equivalent spellings of that same
// flag (single vs. double dash is not significant to flag), so a caller
// using "-daemon" would survive the AI.md-literal filter unstripped and
// the re-exec'd child would daemonize again, looping. "-daemon" is
// filtered here as a deliberate, documented deviation from AI.md's
// example to close that fork loop; "-d" is kept for forward-compat in
// case a short alias is registered later.
func filterDaemonFlag(args []string) []string {
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		if arg != "--daemon" && arg != "-daemon" && arg != "-d" {
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
