package i2p

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/config"
)

// Manager methods must be nil-receiver safe: a Manager that was never
// constructed (e.g. I2P support compiled in but never wired up) behaves
// exactly like one whose eepsite is off.
func TestManagerNilReceiver(t *testing.T) {
	var m *Manager

	if m.Enabled() {
		t.Error("Enabled on nil should be false")
	}
	if m.Running() {
		t.Error("Running on nil should be false")
	}
	if m.Healthy() {
		t.Error("Healthy on nil should be false")
	}
	if got := m.EepsiteAddress(); got != "" {
		t.Errorf("EepsiteAddress on nil = %q, want empty", got)
	}
	if got := m.ProviderName(); got != "none" {
		t.Errorf("ProviderName on nil = %q, want %q", got, "none")
	}
	if m.Provider() != ProviderNone {
		t.Error("Provider on nil should be ProviderNone")
	}
	if m.VirtualPort() != 0 {
		t.Error("VirtualPort on nil should be 0")
	}
	if err := m.Close(); err != nil {
		t.Errorf("Close on nil should be nil, got %v", err)
	}
	if err := m.Start(); err != ErrDisabled {
		t.Errorf("Start on nil = %v, want ErrDisabled", err)
	}
}

// Start must return ErrDisabled and start no monitor goroutine or write any
// file when server.i2p.enabled is false, per AI.md PART 31.2's opt-in rule.
func TestManagerStartDisabled(t *testing.T) {
	dir := t.TempDir()
	dirs := Dirs{
		Config: filepath.Join(dir, "config"),
		Data:   filepath.Join(dir, "data"),
		Log:    filepath.Join(dir, "log"),
	}

	var logged []string
	logf := func(format string, args ...any) {
		logged = append(logged, format)
	}
	served := 0
	serve := func(*Service) { served++ }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := NewManager(ctx, config.I2P{Enabled: false}, dirs, serve, logf)
	if err := m.Start(); err != ErrDisabled {
		t.Fatalf("Start = %v, want ErrDisabled", err)
	}
	if m.Enabled() {
		t.Error("Enabled should be false")
	}
	if m.Running() {
		t.Error("Running should be false")
	}
	if served != 0 {
		t.Errorf("serve hook called %d times, want 0", served)
	}
	if len(logged) != 0 {
		t.Errorf("logf called %d times, want 0", len(logged))
	}
	if _, err := os.Stat(dirs.ConfigPath()); !os.IsNotExist(err) {
		t.Error("Start must not create directories while disabled")
	}
	if m.ProviderName() != "none" {
		t.Errorf("ProviderName = %q, want %q", m.ProviderName(), "none")
	}
	if m.VirtualPort() != config.DefaultI2P().VirtualPort {
		t.Errorf("VirtualPort = %d, want the configured default when nothing is running", m.VirtualPort())
	}
}

// UpdateConfig disabling I2P must stop any running service and return
// without starting anything, keeping the opt-in switch honest at runtime.
func TestManagerUpdateConfigDisable(t *testing.T) {
	dir := t.TempDir()
	dirs := Dirs{
		Config: filepath.Join(dir, "config"),
		Data:   filepath.Join(dir, "data"),
		Log:    filepath.Join(dir, "log"),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := NewManager(ctx, config.I2P{Enabled: false}, dirs, nil, nil)

	// Directly install a fake running service to simulate a prior start,
	// without depending on a real i2pd/SAM provider.
	fakeListener := mustListen(t)
	defer fakeListener.Close()
	m.mu.Lock()
	m.service = &Service{provider: ProviderI2PD, backend: fakeListener, address: "fake.b32.i2p"}
	m.mu.Unlock()

	if !m.Running() {
		t.Fatal("expected Running=true before UpdateConfig")
	}

	if err := m.UpdateConfig(config.I2P{Enabled: false}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	if m.Running() {
		t.Error("expected Running=false after disabling via UpdateConfig")
	}
	if m.Enabled() {
		t.Error("expected Enabled=false after UpdateConfig")
	}
}

// Close must stop a running service, release its backend listener, and be
// safe to call without a service ever having started.
func TestManagerClose(t *testing.T) {
	dir := t.TempDir()
	dirs := Dirs{
		Config: filepath.Join(dir, "config"),
		Data:   filepath.Join(dir, "data"),
		Log:    filepath.Join(dir, "log"),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := NewManager(ctx, config.I2P{Enabled: false}, dirs, nil, nil)
	if err := m.Close(); err != nil {
		t.Fatalf("Close on a never-started manager: %v", err)
	}

	fakeListener := mustListen(t)
	m.mu.Lock()
	m.service = &Service{provider: ProviderI2PD, backend: fakeListener}
	m.mu.Unlock()

	ctx2, cancel2 := context.WithCancel(context.Background())
	m2 := NewManager(ctx2, config.I2P{Enabled: false}, dirs, nil, nil)
	m2.mu.Lock()
	m2.service = &Service{provider: ProviderI2PD, backend: fakeListener}
	m2.mu.Unlock()

	if err := m2.Close(); err != nil {
		t.Fatalf("Close with a running service: %v", err)
	}
	if m2.Running() {
		t.Error("expected Running=false after Close")
	}
	cancel2()

	// Closing a listener a second time (via the leftover m) must not panic;
	// Service.Close is idempotent by nature since m.service is nilled.
	_ = m
}

// Healthy on a Manager with no running service must be false rather than
// panic, matching Service.Healthy's own nil-receiver safety.
func TestManagerHealthyNoService(t *testing.T) {
	dir := t.TempDir()
	dirs := Dirs{Config: dir, Data: dir, Log: dir}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := NewManager(ctx, config.I2P{Enabled: false}, dirs, nil, nil)
	if m.Healthy() {
		t.Error("expected Healthy=false with no running service")
	}
}

// VirtualPort must report the running service's actual port when one is
// running, and the normalized configured default otherwise.
func TestManagerVirtualPort(t *testing.T) {
	dir := t.TempDir()
	dirs := Dirs{Config: dir, Data: dir, Log: dir}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := NewManager(ctx, config.I2P{Enabled: false, VirtualPort: 8080}, dirs, nil, nil)
	if got := m.VirtualPort(); got != 8080 {
		t.Errorf("VirtualPort with no service = %d, want 8080", got)
	}

	fakeListener := mustListen(t)
	defer fakeListener.Close()
	m.mu.Lock()
	m.service = &Service{provider: ProviderI2PD, backend: fakeListener, virtualPort: 9999}
	m.mu.Unlock()

	if got := m.VirtualPort(); got != 9999 {
		t.Errorf("VirtualPort with a running service = %d, want 9999", got)
	}
}

// mustListen returns a loopback listener for tests that need a real
// net.Listener to stand in for a Service's backend without touching any
// I2P provider.
func mustListen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	return ln
}

// TestManagerFullLifecycleWithFakeSAM drives Start, Restart,
// RegenerateAddress, and Close against a fake SAM bridge (no real router or
// i2pd anywhere), covering startLocked/log/Start/Restart/RegenerateAddress
// end to end.
func TestManagerFullLifecycleWithFakeSAM(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH manipulation differs on windows")
	}
	// Make sure no real i2pd binary is found, so ResolveProvider picks SAM.
	empty := t.TempDir()
	t.Setenv("PATH", empty)

	ln := fakeSAMServerMulti(t)
	defer ln.Close()

	dir := t.TempDir()
	dirs := Dirs{
		Config: filepath.Join(dir, "config"),
		Data:   filepath.Join(dir, "data"),
		Log:    filepath.Join(dir, "log"),
	}

	var served []*Service
	serve := func(s *Service) { served = append(served, s) }
	var logged []string
	logf := func(format string, args ...any) { logged = append(logged, format) }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := config.I2P{Enabled: true, SAMAddress: ln.Addr().String()}
	m := NewManager(ctx, cfg, dirs, serve, logf)

	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !m.Running() {
		t.Fatal("expected Running=true after Start")
	}
	if m.ProviderName() != "sam" {
		t.Errorf("ProviderName = %q, want %q", m.ProviderName(), "sam")
	}
	if addr := m.EepsiteAddress(); addr == "" || !strings.HasSuffix(addr, ".b32.i2p") {
		t.Errorf("EepsiteAddress = %q, want a .b32.i2p address", addr)
	}
	if len(served) != 1 {
		t.Errorf("serve hook called %d times, want 1", len(served))
	}
	if len(logged) == 0 {
		t.Error("expected at least one lifecycle log line")
	}
	if !m.Healthy() {
		t.Error("expected Healthy=true with a live SAM control connection")
	}

	if err := m.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if !m.Running() {
		t.Error("expected Running=true after Restart")
	}
	if len(served) != 2 {
		t.Errorf("serve hook called %d times after Restart, want 2", len(served))
	}

	addr, err := m.RegenerateAddress()
	if err != nil {
		t.Fatalf("RegenerateAddress: %v", err)
	}
	if addr == "" || !strings.HasSuffix(addr, ".b32.i2p") {
		t.Errorf("RegenerateAddress returned %q, want a .b32.i2p address", addr)
	}
	if len(served) != 3 {
		t.Errorf("serve hook called %d times after RegenerateAddress, want 3", len(served))
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if m.Running() {
		t.Error("expected Running=false after Close")
	}
}
