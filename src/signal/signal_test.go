package signal

import "testing"

// resetHooks saves and clears the package-level hook slices, returning a
// restore function so tests don't leak state into each other or into
// Start(), which is exercised in the platform-specific test files.
func resetHooks(t *testing.T) {
	t.Helper()
	mu.Lock()
	savedShutdown := shutdownHooks
	savedReopen := reopenHooks
	savedStatus := statusHooks
	shutdownHooks = nil
	reopenHooks = nil
	statusHooks = nil
	mu.Unlock()

	t.Cleanup(func() {
		mu.Lock()
		shutdownHooks = savedShutdown
		reopenHooks = savedReopen
		statusHooks = savedStatus
		mu.Unlock()
	})
}

func TestRegisterAndRunShutdownHooks(t *testing.T) {
	resetHooks(t)

	var order []int
	Register(func() { order = append(order, 1) })
	Register(func() { order = append(order, 2) })
	Register(func() { order = append(order, 3) })

	runShutdownHooks()

	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Errorf("hooks ran in order %v, want [1 2 3]", order)
	}
}

func TestRegisterLogReopenAndRun(t *testing.T) {
	resetHooks(t)

	called := false
	RegisterLogReopen(func() { called = true })
	runReopenHooks()

	if !called {
		t.Error("log-reopen hook was not called")
	}
}

func TestRegisterStatusDumpAndRun(t *testing.T) {
	resetHooks(t)

	called := false
	RegisterStatusDump(func() { called = true })
	runStatusHooks()

	if !called {
		t.Error("status-dump hook was not called")
	}
}

func TestRunShutdownHooksWithNoneRegisteredIsNoOp(t *testing.T) {
	resetHooks(t)
	// Must not panic when nothing is registered.
	runShutdownHooks()
	runReopenHooks()
	runStatusHooks()
}

func TestHooksAreIndependent(t *testing.T) {
	resetHooks(t)

	var shutdownCalled, reopenCalled, statusCalled bool
	Register(func() { shutdownCalled = true })
	RegisterLogReopen(func() { reopenCalled = true })
	RegisterStatusDump(func() { statusCalled = true })

	runReopenHooks()
	if reopenCalled != true || shutdownCalled || statusCalled {
		t.Errorf("running reopen hooks only should not trigger others: reopen=%v shutdown=%v status=%v",
			reopenCalled, shutdownCalled, statusCalled)
	}
}
