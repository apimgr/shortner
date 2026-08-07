//go:build !windows

// Package signal (Unix signal table). See AI.md PART 8 "Signal Handling &
// Graceful Shutdown".
package signal

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

// Start installs the Unix signal handlers and returns immediately: SIGTERM/
// SIGINT/SIGQUIT/SIGRTMIN+3 run the shutdown hooks and exit; SIGUSR1 runs
// the log-reopen hooks; SIGUSR2 runs the status-dump hooks; SIGHUP is
// ignored (config auto-reloads via file watcher, see PART 8 "Smart Config
// Reload").
func Start() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan,
		syscall.SIGTERM,
		syscall.SIGINT,
		syscall.SIGQUIT,
		syscall.SIGUSR1,
		syscall.SIGUSR2,
	)
	// SIGRTMIN+3 (signal 37) is Docker's default STOPSIGNAL.
	signal.Notify(sigChan, syscall.Signal(37))
	signal.Ignore(syscall.SIGHUP)

	go func() {
		for sig := range sigChan {
			switch sig {
			case syscall.SIGUSR1:
				log.Println("received SIGUSR1, reopening logs")
				runReopenHooks()
			case syscall.SIGUSR2:
				log.Println("received SIGUSR2, dumping status")
				runStatusHooks()
			default:
				log.Printf("received %v, starting graceful shutdown", sig)
				runShutdownHooks()
				os.Exit(0)
			}
		}
	}()
}

// KillProcess sends SIGTERM (graceful) or SIGKILL (immediate) to pid.
func KillProcess(pid int, graceful bool) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if graceful {
		return process.Signal(syscall.SIGTERM)
	}
	return process.Signal(syscall.SIGKILL)
}
