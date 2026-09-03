//go:build darwin

package updater

import "os/exec"

// restartService restarts the launchd job on macOS. `kickstart -k` kills
// the running instance and starts it fresh from the replaced binary.
func restartService() error {
	return exec.Command("launchctl", "kickstart", "-k", "system/"+plistName).Run()
}
