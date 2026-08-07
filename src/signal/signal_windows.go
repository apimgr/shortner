//go:build windows

// Package signal (Windows signal table). See AI.md PART 8 "Signal
// Handling & Graceful Shutdown" — Windows only supports os.Interrupt
// (Ctrl+C, Ctrl+Break); SIGHUP/SIGUSR1/SIGUSR2/SIGQUIT do not exist.
package signal

import (
	"log"
	"os"
	"os/signal"
)

// Start installs the Windows signal handler and returns immediately:
// os.Interrupt runs the shutdown hooks and exits.
func Start() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)

	go func() {
		for sig := range sigChan {
			log.Printf("received %v, starting graceful shutdown", sig)
			runShutdownHooks()
			os.Exit(0)
		}
	}()
}

// KillProcess terminates pid. Windows has no graceful signal — Kill()
// calls TerminateProcess regardless of the graceful argument.
func KillProcess(pid int, graceful bool) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}
