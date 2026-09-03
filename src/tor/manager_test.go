package tor

import (
	"path/filepath"
	"testing"

	"github.com/apimgr/shortner/src/config"
)

// A nil *Manager must behave exactly like a Manager on a host with no tor
// binary: every accessor answers the "off" value and Close is a no-op,
// never a panic. Call sites throughout the server rely on this.
func TestManagerNilReceiver(t *testing.T) {
	var m *Manager
	if m.Enabled() {
		t.Error("Enabled() on nil Manager should be false")
	}
	if m.Running() {
		t.Error("Running() on nil Manager should be false")
	}
	if m.Healthy() {
		t.Error("Healthy() on nil Manager should be false")
	}
	if got := m.OnionAddress(); got != "" {
		t.Errorf("OnionAddress() on nil Manager = %q, want empty", got)
	}
	if got := m.VirtualPort(); got != 0 {
		t.Errorf("VirtualPort() on nil Manager = %d, want 0", got)
	}
	if err := m.Close(); err != nil {
		t.Errorf("Close() on nil Manager should not error, got %v", err)
	}
	client := m.GetHTTPClient(true)
	if client == nil {
		t.Fatal("GetHTTPClient() on nil Manager returned nil")
	}
}

// newTestManager builds a Manager against a temp-dir Dirs and a config that
// points at a binary path guaranteed not to exist, so Start always fails
// without ever touching a real Tor process or the network.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	base := t.TempDir()
	dirs := Dirs{
		Config: filepath.Join(base, "config"),
		Data:   filepath.Join(base, "data"),
		Log:    filepath.Join(base, "log"),
	}
	cfg := config.Tor{Binary: filepath.Join(base, "no-such-tor-binary")}
	return NewManager(t.Context(), cfg, dirs, nil, nil)
}

// A Manager whose configured binary does not exist must report Start
// failing, Enabled() false, and must remain otherwise usable — mirroring
// AI.md PART 31.1's "a missing binary is INFO, not an error" rule at the
// manager layer.
func TestManagerStartMissingBinary(t *testing.T) {
	m := newTestManager(t)
	defer func() { _ = m.Close() }()

	if err := m.Start(); err == nil {
		t.Fatal("expected Start() to fail with no resolvable tor binary")
	}
	if m.Enabled() {
		t.Error("Enabled() should be false with no resolvable tor binary")
	}
	if m.Running() {
		t.Error("Running() should be false after a failed Start()")
	}
	if got := m.OnionAddress(); got != "" {
		t.Errorf("OnionAddress() = %q, want empty after a failed Start()", got)
	}
	if m.Healthy() {
		t.Error("Healthy() should be false with no running service")
	}
}

// Restart on a manager that never had a running service must behave like a
// fresh Start: it fails the same way and does not panic on the nil
// stopLocked path.
func TestManagerRestartWithoutPriorStart(t *testing.T) {
	m := newTestManager(t)
	defer func() { _ = m.Close() }()

	if err := m.Restart(); err == nil {
		t.Fatal("expected Restart() to fail with no resolvable tor binary")
	}
}

// UpdateConfig must apply the new config even when the resulting start
// fails, so a later successful ResolveBinary would use the updated value.
func TestManagerUpdateConfig(t *testing.T) {
	m := newTestManager(t)
	defer func() { _ = m.Close() }()

	newCfg := config.Tor{Binary: filepath.Join(t.TempDir(), "still-missing")}
	if err := m.UpdateConfig(newCfg); err == nil {
		t.Fatal("expected UpdateConfig() to fail with no resolvable tor binary")
	}
	m.mu.Lock()
	got := m.config.Binary
	m.mu.Unlock()
	if got != newCfg.Binary {
		t.Errorf("manager config.Binary = %q, want %q", got, newCfg.Binary)
	}
}

// VirtualPort must fall back to the normalized config default when no
// service is running, since the operator-visible port must still be
// reportable before Tor ever starts.
func TestManagerVirtualPortFallback(t *testing.T) {
	base := t.TempDir()
	dirs := Dirs{
		Config: filepath.Join(base, "config"),
		Data:   filepath.Join(base, "data"),
		Log:    filepath.Join(base, "log"),
	}
	cfg := config.Tor{VirtualPort: 8080}
	m := NewManager(t.Context(), cfg, dirs, nil, nil)
	defer func() { _ = m.Close() }()

	if got := m.VirtualPort(); got != 8080 {
		t.Errorf("VirtualPort() = %d, want 8080 from config with no service running", got)
	}
}

// Close must be safe to call twice: the monitor goroutine's context is
// cancelled once, and a second Close on an already-nil service must not
// panic or error.
func TestManagerCloseIdempotent(t *testing.T) {
	m := newTestManager(t)
	if err := m.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

// The serve and logf hooks passed to NewManager must both tolerate nil
// without the manager panicking on a failed start.
func TestManagerNilHooks(t *testing.T) {
	base := t.TempDir()
	dirs := Dirs{
		Config: filepath.Join(base, "config"),
		Data:   filepath.Join(base, "data"),
		Log:    filepath.Join(base, "log"),
	}
	cfg := config.Tor{Binary: filepath.Join(base, "missing")}
	m := NewManager(t.Context(), cfg, dirs, nil, nil)
	defer func() { _ = m.Close() }()
	if err := m.Start(); err == nil {
		t.Fatal("expected Start() to fail")
	}
}

// GetHTTPClient with no running service must fall back to a direct client
// rather than nil or a client wired to a dead dialer.
func TestManagerGetHTTPClientNoService(t *testing.T) {
	m := newTestManager(t)
	defer func() { _ = m.Close() }()

	client := m.GetHTTPClient(true)
	if client == nil {
		t.Fatal("GetHTTPClient() returned nil")
	}
	if client.Transport != nil {
		t.Error("GetHTTPClient() with no service should return the plain default transport")
	}
}
