//go:build linux

package i2p

import (
	"os/exec"
	"syscall"
)

func setParentDeathSignal(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}
