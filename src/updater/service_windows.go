//go:build windows

package updater

import (
	"os/exec"
	"time"
)

// restartService restarts the Windows service through the SCM. A stop
// failure is ignored: the service may simply not be running, and the
// start below is what actually has to succeed.
func restartService() error {
	_ = exec.Command("sc", "stop", serviceName).Run()

	time.Sleep(2 * time.Second)

	return exec.Command("sc", "start", serviceName).Run()
}
