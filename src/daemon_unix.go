//go:build !windows

// Daemonize (Unix): fork by re-exec with a _DAEMON_CHILD marker env var,
// then setsid to detach from the controlling terminal. See AI.md PART 8
// "Daemonization (--daemon flag)".
package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// Daemonize forks the process and detaches from the controlling terminal.
// The parent prints the child PID and exits 0; the child returns nil and
// continues normal startup.
func Daemonize() error {
	// Already running under an init system (PPID 1) — nothing to do.
	if os.Getppid() == 1 {
		return nil
	}
	// We are the re-exec'd child — continue execution.
	if os.Getenv("_DAEMON_CHILD") != "" {
		return nil
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("daemonize: resolve executable path: %w", err)
	}

	args := filterDaemonFlag(os.Args[1:])
	cmd := exec.Command(execPath, args...)
	cmd.Env = append(os.Environ(), "_DAEMON_CHILD=1")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{
		// New session, detaches from the controlling terminal.
		Setsid: true,
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("daemonize: start background process: %w", err)
	}

	fmt.Printf("Daemon started with PID %d\n", cmd.Process.Pid)
	os.Exit(0)
	return nil
}
