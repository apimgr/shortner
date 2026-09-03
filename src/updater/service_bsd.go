//go:build freebsd || openbsd || netbsd

package updater

import "os/exec"

// restartService restarts the rc.d service on the BSDs.
func restartService() error {
	return exec.Command("service", serviceName, "restart").Run()
}
