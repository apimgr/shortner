package tor

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/apimgr/shortner/src/config"
)

// monitorInterval is how often the manager pings Tor's control connection,
// per AI.md PART 31.1 "Tor Process Monitoring".
const monitorInterval = 30 * time.Second

// Manager owns the Tor process lifecycle for the whole server: start,
// monitored restart, config reload, address regeneration and key import,
// per AI.md PART 31.1 "Tor Process Lifecycle".
//
// Every method is safe for concurrent use, and none of them is ever fatal
// to the server — a Manager whose Tor never started behaves exactly like
// one on a host with no tor binary: it reports an empty address and hands
// out direct HTTP clients.
type Manager struct {
	mu      sync.Mutex
	service *Service
	config  config.Tor
	dirs    Dirs
	ctx     context.Context
	cancel  context.CancelFunc
	// serve is called with each newly started service so the HTTP server
	// can begin serving its backend listener. A restart produces a new
	// listener on a new port, so this fires on every successful start,
	// not only the first.
	serve func(*Service)
	// logf receives lifecycle messages. AI.md PART 31.1 "Tor Logging
	// Levels" makes all of them informational or warnings, never errors.
	logf func(format string, args ...any)
	// lastErr is the most recent start/restart failure, surfaced through
	// Err() so /server/healthz can render AI.md PART 13's
	// "error:{short message}" status vocabulary instead of a bare "error"
	// with no explanation.
	lastErr error
}

// NewManager creates a Tor manager. serve is invoked for every service
// that starts successfully so the caller can serve its backend listener;
// logf receives lifecycle messages. Both may be nil.
func NewManager(ctx context.Context, cfg config.Tor, dirs Dirs, serve func(*Service), logf func(string, ...any)) *Manager {
	mctx, cancel := context.WithCancel(ctx)
	return &Manager{
		config: cfg,
		dirs:   dirs,
		ctx:    mctx,
		cancel: cancel,
		serve:  serve,
		logf:   logf,
	}
}

// log emits a lifecycle message when a logger was supplied.
func (m *Manager) log(format string, args ...any) {
	if m.logf != nil {
		m.logf(format, args...)
	}
}

// Start brings the hidden service up and begins monitoring it. A missing
// tor binary is reported through the returned error but leaves the manager
// perfectly usable — the caller treats it as INFO and continues.
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.startLocked(); err != nil {
		return err
	}
	go m.monitor()
	return nil
}

// startLocked starts Tor and publishes the new service to the serve hook.
// The caller must hold m.mu.
func (m *Manager) startLocked() error {
	service, err := Start(m.ctx, m.config, m.dirs)
	if err != nil {
		m.lastErr = err
		return err
	}
	m.lastErr = nil
	m.service = service
	if m.serve != nil {
		m.serve(service)
	}
	m.log("Tor: %s", service.OnionAddress())
	return nil
}

// stopLocked shuts down the running service, if any. The caller must hold
// m.mu.
func (m *Manager) stopLocked() {
	if m.service != nil {
		_ = m.service.Close()
		m.service = nil
	}
}

// Restart stops and starts Tor with the current configuration. It is the
// recovery path for a crashed or unresponsive process and the operator's
// `tor restart` command.
func (m *Manager) Restart() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
	return m.startLocked()
}

// UpdateConfig applies new Tor settings and restarts. torrc is regenerated
// from the new config with a freshly allocated backend port as part of the
// start, so nothing needs to be written here.
func (m *Manager) UpdateConfig(cfg config.Tor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config = cfg
	m.stopLocked()
	return m.startLocked()
}

// RegenerateAddress deletes the hidden service keys and restarts, which
// makes Tor mint a brand new .onion identity. The old address is
// unrecoverable afterwards.
func (m *Manager) RegenerateAddress() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
	if err := os.RemoveAll(m.dirs.SitePath()); err != nil {
		return "", err
	}
	if err := m.startLocked(); err != nil {
		return "", err
	}
	return m.service.OnionAddress(), nil
}

// ApplyKeys installs an existing v3 secret key as the hidden service
// identity and restarts. Any public key or hostname left over from the
// previous identity is removed first so Tor cannot load a mismatched pair.
func (m *Manager) ApplyKeys(secretKey []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()

	site := m.dirs.SitePath()
	if err := os.MkdirAll(site, 0700); err != nil {
		return "", err
	}
	for _, stale := range []string{"hs_ed25519_public_key", "hostname"} {
		if err := os.Remove(filepath.Join(site, stale)); err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	if err := writeSecret(filepath.Join(site, "hs_ed25519_secret_key"), secretKey); err != nil {
		return "", err
	}
	if err := m.startLocked(); err != nil {
		return "", err
	}
	return m.service.OnionAddress(), nil
}

// OnionAddress returns the live .onion address, or an empty string when
// Tor is not running.
func (m *Manager) OnionAddress() string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.service.OnionAddress()
}

// Enabled reports whether the hidden service is switched on. AI.md
// PART 31.1 gives Tor no opt-out toggle: it is enabled whenever a usable
// tor binary was found, so this answers "is a binary available", not "did
// the operator ask for it".
func (m *Manager) Enabled() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	binary, err := ResolveBinary(m.config)
	return err == nil && binary != ""
}

// Running reports whether a hidden service is currently published.
func (m *Manager) Running() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.service != nil
}

// Healthy reports whether the Tor control connection still answers. It is
// the probe the tor_health scheduler task uses.
func (m *Manager) Healthy() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	service := m.service
	m.mu.Unlock()
	return service.Healthy()
}

// Err returns the most recent start/restart failure's message, or an empty
// string when the last attempt succeeded (or none has run yet).
func (m *Manager) Err() string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastErr == nil {
		return ""
	}
	return m.lastErr.Error()
}

// VirtualPort returns the port the onion address is published on.
func (m *Manager) VirtualPort() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.service != nil {
		return m.service.VirtualPort()
	}
	return m.config.Normalized().VirtualPort
}

// GetHTTPClient returns an HTTP client, routed through Tor when useTor is
// set and outbound Tor is available. With Tor down it falls back to a
// direct client rather than failing the caller's request.
func (m *Manager) GetHTTPClient(useTor bool) *http.Client {
	if m == nil {
		return &http.Client{Timeout: 30 * time.Second}
	}
	m.mu.Lock()
	service := m.service
	m.mu.Unlock()
	return service.GetHTTPClient(useTor)
}

// Close stops Tor and ends monitoring.
func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.service != nil {
		err := m.service.Close()
		m.service = nil
		return err
	}
	return nil
}

// monitor pings Tor every 30 seconds and restarts it when the control
// connection stops answering, per AI.md PART 31.1 "Tor Process
// Monitoring". A restart failure is logged and retried on the next tick —
// the loop never gives up while the server is running.
func (m *Manager) monitor() {
	ticker := time.NewTicker(monitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.mu.Lock()
			if m.service == nil {
				m.mu.Unlock()
				continue
			}
			if m.service.Healthy() {
				m.mu.Unlock()
				continue
			}
			m.log("Tor: connection lost, reconnecting")
			m.stopLocked()
			if err := m.startLocked(); err != nil {
				m.log("Tor: restart failed: %v", err)
			}
			m.mu.Unlock()
		}
	}
}
