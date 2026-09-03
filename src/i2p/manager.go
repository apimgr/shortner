package i2p

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/apimgr/shortner/src/config"
)

// monitorInterval is how often the manager checks the provider, per AI.md
// PART 31.2 "I2P Monitoring".
const monitorInterval = 30 * time.Second

// Manager owns the eepsite lifecycle for the whole server: start, monitored
// restart, config reload and destination regeneration.
//
// Every method is safe for concurrent use and nil-receiver safe, and none
// of them is ever fatal to the server — a Manager whose eepsite never
// started behaves exactly like one on a host where I2P is switched off.
type Manager struct {
	mu      sync.Mutex
	service *Service
	config  config.I2P
	dirs    Dirs
	ctx     context.Context
	cancel  context.CancelFunc
	// serve is called with each newly started service so the HTTP server can
	// begin serving its backend listener. A restart produces a new listener
	// on a new port, so this fires on every successful start.
	serve func(*Service)
	// logf receives lifecycle messages. AI.md PART 31.2 makes a missing
	// provider a warning, never an error.
	logf func(format string, args ...any)
	// lastErr is the most recent start/restart failure, surfaced through
	// Err() so /server/healthz can render AI.md PART 13's
	// "error:{short message}" status vocabulary instead of a bare "error"
	// with no explanation. ErrDisabled is never recorded here — it is the
	// normal opt-out state, not a failure.
	lastErr error
}

// NewManager creates an I2P manager. serve is invoked for every service
// that starts successfully; logf receives lifecycle messages. Both may be
// nil.
func NewManager(ctx context.Context, cfg config.I2P, dirs Dirs, serve func(*Service), logf func(string, ...any)) *Manager {
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

// Start brings the eepsite up and begins monitoring it. When I2P is not
// enabled it returns ErrDisabled and starts no monitor, so an opt-out
// install runs no I2P code at all beyond this check.
func (m *Manager) Start() error {
	if m == nil {
		return ErrDisabled
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.config.Enabled {
		return ErrDisabled
	}
	if err := m.startLocked(); err != nil {
		return err
	}
	go m.monitor()
	return nil
}

// startLocked starts the provider and publishes the new service to the
// serve hook. The caller must hold m.mu.
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
	m.log("I2P: %s (%s)", service.EepsiteAddress(), service.Provider().Name())
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

// Restart stops and starts the provider with the current configuration.
func (m *Manager) Restart() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
	return m.startLocked()
}

// UpdateConfig applies new I2P settings. Disabling I2P stops the eepsite
// and returns without starting anything, which is how the opt-in switch
// stays honest at runtime.
func (m *Manager) UpdateConfig(cfg config.I2P) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	wasEnabled := m.config.Enabled
	m.config = cfg
	if !cfg.Enabled {
		m.stopLocked()
		return nil
	}
	m.stopLocked()
	if err := m.startLocked(); err != nil {
		return err
	}
	if !wasEnabled {
		go m.monitor()
	}
	return nil
}

// RegenerateAddress deletes the persisted destination and restarts, which
// mints a brand new .b32.i2p. The old address is unrecoverable afterwards.
func (m *Manager) RegenerateAddress() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
	for _, path := range []string{m.dirs.KeysPath(), m.dirs.HostnamePath()} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	if err := m.startLocked(); err != nil {
		return "", err
	}
	return m.service.EepsiteAddress(), nil
}

// EepsiteAddress returns the live .b32.i2p address, or an empty string when
// the eepsite is not running.
func (m *Manager) EepsiteAddress() string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.service.EepsiteAddress()
}

// Enabled reports whether I2P is switched on in the configuration.
func (m *Manager) Enabled() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.config.Enabled
}

// Running reports whether an eepsite is currently published.
func (m *Manager) Running() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.service != nil
}

// Provider returns the provider currently backing the eepsite.
func (m *Manager) Provider() Provider {
	if m == nil {
		return ProviderNone
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.service.Provider()
}

// ProviderName returns the provider's name for the health endpoint's
// features.i2p.provider field: "i2pd", "sam", or "none" while nothing is
// published (AI.md PART 13).
func (m *Manager) ProviderName() string {
	return m.Provider().Name()
}

// Healthy reports whether the provider still responds. It is the probe the
// i2p_health scheduler task uses. service is nil-receiver safe (see
// (*Service).Healthy), but checked explicitly here too so this stays
// correct even if that guard is ever narrowed by a future change.
func (m *Manager) Healthy() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	service := m.service
	m.mu.Unlock()
	if service == nil {
		return false
	}
	return service.Healthy()
}

// Err returns the most recent start/restart failure's message, or an empty
// string when the last attempt succeeded, none has run yet, or I2P is
// simply disabled (ErrDisabled is never recorded as a failure).
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

// VirtualPort returns the port the eepsite is published on.
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

// Close stops the provider and ends monitoring.
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

// monitor checks the provider every 30 seconds and restarts it when it
// stops responding, per AI.md PART 31.2 "I2P Monitoring". A restart failure
// is logged and retried on the next tick.
func (m *Manager) monitor() {
	ticker := time.NewTicker(monitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.mu.Lock()
			if m.service == nil || m.service.Healthy() {
				m.mu.Unlock()
				continue
			}
			m.log("I2P: provider unresponsive, restarting")
			m.stopLocked()
			if err := m.startLocked(); err != nil {
				m.log("I2P: restart failed: %v", err)
			}
			m.mu.Unlock()
		}
	}
}
