//go:build !linux

package i2p

import "os/exec"

func setParentDeathSignal(*exec.Cmd) {}
