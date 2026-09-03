package updater

// serviceName is the frozen `{internal_name}` used for the systemd unit,
// the rc.d script, and the Windows service (AI.md PART 24). plistName is
// the derived macOS bundle id, `io.github.{project_org}.{internal_name}`.
const (
	serviceName = "shortner"
	plistName   = "io.github." + repoOwner + "." + serviceName
)

// Restart brings the freshly installed binary into service from inside the
// running process, per AI.md PART 22 "Update Flow" step 5. A supervised
// process is handed back to its service manager (which starts the new
// binary); an unsupervised one re-executes itself in place.
func Restart() error {
	if UnderServiceManager() {
		return RestartService()
	}
	return restartSelf()
}

// RestartService restarts the installed service through the platform's
// service manager, per AI.md PART 22 "Service-Aware Update". It is also
// the path a CLI `--update yes` takes when it updated the binary of a
// server that is running as a separate process.
func RestartService() error {
	return restartService()
}

// UnderServiceManager reports whether this process was started by the
// platform's service manager rather than run directly from a shell.
func UnderServiceManager() bool {
	return underServiceManager()
}
