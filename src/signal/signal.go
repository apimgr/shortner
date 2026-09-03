// Package signal implements OS signal handling and graceful-shutdown
// hooks shared by the server binary. See AI.md PART 8 "Signal Handling &
// Graceful Shutdown".
//
// The HTTP server, database, and Tor child process this package's
// eventual callers manage don't exist yet (PART 9+ — see TODO.AI.md).
// Register, RegisterLogReopen, and RegisterStatusDump let those
// subsystems attach real shutdown/rotation/status logic once they exist,
// without this package depending on any of them. Start installs the OS
// signal handlers and returns immediately (non-blocking) — it does not
// invent a "wait forever" main loop, since there is nothing yet that
// needs one; that arrives with the HTTP server.
package signal

import "sync"

var (
	mu            sync.Mutex
	shutdownHooks []func()
	reopenHooks   []func()
	statusHooks   []func()
)

// Register adds a hook run during graceful shutdown (SIGTERM/SIGINT/
// SIGQUIT/SIGRTMIN+3 on Unix, os.Interrupt on Windows). Hooks run in
// registration order.
func Register(fn func()) {
	mu.Lock()
	defer mu.Unlock()
	shutdownHooks = append(shutdownHooks, fn)
}

// RegisterLogReopen adds a hook run on SIGUSR1 (log rotation). Unix only —
// Windows never delivers SIGUSR1, so the hook simply never fires there.
func RegisterLogReopen(fn func()) {
	mu.Lock()
	defer mu.Unlock()
	reopenHooks = append(reopenHooks, fn)
}

// RegisterStatusDump adds a hook run on SIGUSR2 (status dump). Unix only —
// Windows never delivers SIGUSR2, so the hook simply never fires there.
func RegisterStatusDump(fn func()) {
	mu.Lock()
	defer mu.Unlock()
	statusHooks = append(statusHooks, fn)
}

// runShutdownHooks runs every registered shutdown hook, in order.
func runShutdownHooks() {
	mu.Lock()
	hooks := append([]func(){}, shutdownHooks...)
	mu.Unlock()
	for _, fn := range hooks {
		fn()
	}
}

// runReopenHooks runs every registered log-reopen hook, in order.
func runReopenHooks() {
	mu.Lock()
	hooks := append([]func(){}, reopenHooks...)
	mu.Unlock()
	for _, fn := range hooks {
		fn()
	}
}

// runStatusHooks runs every registered status-dump hook, in order.
func runStatusHooks() {
	mu.Lock()
	hooks := append([]func(){}, statusHooks...)
	mu.Unlock()
	for _, fn := range hooks {
		fn()
	}
}
