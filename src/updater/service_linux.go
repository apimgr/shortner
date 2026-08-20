//go:build linux

package updater

import "os/exec"

// restartService restarts the service on Linux: systemd where present,
// otherwise the generic `service` wrapper used by runit/s6/OpenRC
// installations (AI.md PART 22 "Service-Aware Update").
func restartService() error {
	if _, err := exec.LookPath("systemctl"); err == nil {
		return exec.Command("systemctl", "restart", serviceName).Run()
	}
	return exec.Command("service", serviceName, "restart").Run()
}
