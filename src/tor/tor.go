// Package tor runs and owns a dedicated Tor process that publishes this
// server as a v3 hidden service, per AI.md PART 31.1 "Tor Hidden Service".
//
// The hidden service is always enabled when a `tor` binary is present —
// there is no enable/disable toggle. A missing binary is INFO, not an
// error, and every runtime failure is a warning: the server never fails to
// start because of Tor.
//
// The onion identity is created and persisted by Tor itself under
// HiddenServiceDir; this package never writes the key except when the
// operator explicitly imports or applies one. torrc is derived state and is
// regenerated on every start, because the backend port changes each run.
package tor

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	bine "github.com/cretz/bine/tor"
	"github.com/pires/go-proxyproto"

	"github.com/apimgr/shortner/src/common/netutil"
	"github.com/apimgr/shortner/src/config"
)

// Dirs are the app-owned directories Tor lives under. They are never
// configurable: AI.md PART 31.1 "Storage Locations" derives them from the
// application's own config/data/log directories.
type Dirs struct {
	// Config is {config_dir}; torrc is written to {Config}/tor/torrc.
	Config string
	// Data is {data_dir}; Tor's DataDir is {Data}/tor and the hidden
	// service keys live in {Data}/tor/site.
	Data string
	// Log is {log_dir}; Tor's own log is {Log}/tor.log.
	Log string
}

// TorrcPath returns the generated torrc location.
func (d Dirs) TorrcPath() string {
	return filepath.Join(d.Config, "tor", "torrc")
}

// DataPath returns Tor's isolated DataDir.
func (d Dirs) DataPath() string {
	return filepath.Join(d.Data, "tor")
}

// SitePath returns the HiddenServiceDir where Tor persists the v3 key and
// the hostname file.
func (d Dirs) SitePath() string {
	return filepath.Join(d.Data, "tor", "site")
}

// HostnamePath returns the file Tor writes the .onion address into.
func (d Dirs) HostnamePath() string {
	return filepath.Join(d.Data, "tor", "site", "hostname")
}

// LogPath returns Tor's own log file.
func (d Dirs) LogPath() string {
	return filepath.Join(d.Log, "tor.log")
}

// ReadOnionAddress returns the persisted .onion address without starting
// or contacting Tor. It exists so out-of-process callers — notably
// `--status`, which only inspects on-disk state — can report the address of
// a server they are not part of. An absent or unreadable file simply means
// "no address yet" and is not an error.
func ReadOnionAddress(dataDir string) string {
	b, err := os.ReadFile(filepath.Join(dataDir, "tor", "site", "hostname"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ResolveBinary locates the tor executable: an explicit cfg.Binary override
// wins, then the common install locations, then $PATH. An error means Tor
// is simply not available, and no port is allocated and no file is written.
func ResolveBinary(cfg config.Tor) (string, error) {
	if cfg.Binary != "" {
		if _, err := os.Stat(cfg.Binary); err == nil {
			return cfg.Binary, nil
		}
		return "", fmt.Errorf("configured tor binary not found: %s", cfg.Binary)
	}
	for _, p := range commonBinaryPaths() {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	if p, err := exec.LookPath("tor"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("tor binary not found - hidden service disabled")
}

// commonBinaryPaths lists the well-known tor install locations, checked
// before $PATH, per AI.md PART 31.1 "Tor Process Management".
func commonBinaryPaths() []string {
	if runtime.GOOS == "windows" {
		return []string{
			`C:\Program Files\Tor\tor.exe`,
			`C:\Program Files (x86)\Tor\tor.exe`,
		}
	}
	return []string{
		"/usr/bin/tor",
		"/usr/sbin/tor",
		"/usr/local/bin/tor",
		"/opt/homebrew/bin/tor",
	}
}

// Service is one running hidden service: the Tor process, the address it
// published, and the dedicated PROXY-protocol loopback listener Tor
// forwards rendezvous connections to.
type Service struct {
	tor          *bine.Tor
	onionAddress string
	backendPort  int
	backend      net.Listener
	dialer       *bine.Dialer
	virtualPort  int
}

// OnionAddress returns the full .onion address, including the suffix.
func (s *Service) OnionAddress() string {
	if s == nil {
		return ""
	}
	return s.onionAddress
}

// VirtualPort returns the port the onion address is published on.
func (s *Service) VirtualPort() int {
	if s == nil {
		return 0
	}
	return s.virtualPort
}

// BackendPort returns the dedicated loopback port Tor forwards to.
func (s *Service) BackendPort() int {
	if s == nil {
		return 0
	}
	return s.backendPort
}

// BackendListener returns the PROXY-protocol-aware loopback listener the
// HTTP server must serve. Every connection it accepts came from this
// server's own Tor process, never from the clearnet.
func (s *Service) BackendListener() net.Listener {
	if s == nil {
		return nil
	}
	return s.backend
}

// OutboundEnabled reports whether outbound requests can be routed through
// Tor, which requires `use_network: true` and a successfully built dialer.
func (s *Service) OutboundEnabled() bool {
	return s != nil && s.dialer != nil
}

// GetHTTPClient returns an HTTP client, routed through Tor when useTor is
// set and an outbound dialer exists. Tor is slower, so the Tor client gets
// the longer timeout.
func (s *Service) GetHTTPClient(useTor bool) *http.Client {
	if s == nil || !useTor || s.dialer == nil {
		return &http.Client{Timeout: 30 * time.Second}
	}
	return &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			DialContext: s.dialer.DialContext,
		},
	}
}

// Healthy reports whether the Tor control connection still answers. It is
// the liveness probe behind both the monitor loop and the tor_health
// scheduler task.
func (s *Service) Healthy() bool {
	if s == nil || s.tor == nil || s.tor.Control == nil {
		return false
	}
	_, err := s.tor.Control.GetInfo("version")
	return err == nil
}

// Close stops the Tor process and releases the backend listener.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	if s.backend != nil {
		_ = s.backend.Close()
		s.backend = nil
	}
	if s.tor != nil {
		err := s.tor.Close()
		s.tor = nil
		return err
	}
	return nil
}

// Start launches a Tor process owned by this server binary and publishes
// the hidden service, per AI.md PART 31.1 "Tor Process Management".
//
// The tor binary is resolved first: with no binary the function returns
// before allocating a port or writing any file, which is the "no provider →
// no port, no generated config" gate. Only then is the dedicated
// PROXY-protocol loopback listener bound (64000-64999, fresh every run,
// never persisted) and torrc regenerated to match it.
func Start(ctx context.Context, cfg config.Tor, dirs Dirs) (*Service, error) {
	cfg = cfg.Normalized()

	binary, err := ResolveBinary(cfg)
	if err != nil {
		return nil, err
	}

	if err := EnsureDirs(dirs); err != nil {
		return nil, fmt.Errorf("failed to create tor directories: %w", err)
	}

	// Binding the listener is how the port is allocated: the port cannot
	// be stolen between an availability probe and the bind, and torrc is
	// written from the port actually held.
	raw, backendPort, err := netutil.ListenLoopback()
	if err != nil {
		return nil, fmt.Errorf("failed to allocate tor backend port: %w", err)
	}
	// Tor prepends a HAProxy PROXY v1 header carrying the circuit ID to
	// every backend connection, so this listener — and only this listener,
	// never the clearnet one — parses it.
	backend := &proxyproto.Listener{Listener: raw}

	svc := &Service{
		backendPort: backendPort,
		backend:     backend,
		virtualPort: cfg.VirtualPort,
	}

	torrc := BuildTorrc(cfg, dirs.SitePath(), backendPort)
	if err := writeSecret(dirs.TorrcPath(), []byte(torrc)); err != nil {
		_ = backend.Close()
		return nil, fmt.Errorf("failed to write torrc: %w", err)
	}

	t, err := bine.Start(ctx, &bine.StartConf{
		TorrcFile: dirs.TorrcPath(),
		DataDir:   dirs.DataPath(),
		// torrc owns SocksPort: auto when outbound is enabled, 0 when not.
		NoAutoSocksPort: true,
		ExePath:         binary,
	})
	if err != nil {
		_ = backend.Close()
		return nil, fmt.Errorf("failed to start dedicated tor: %w", err)
	}
	svc.tor = t

	bootstrapCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.BootstrapTimeout)*time.Second)
	defer cancel()
	if err := t.EnableNetwork(bootstrapCtx, true); err != nil {
		_ = svc.Close()
		return nil, fmt.Errorf("failed to enable tor network: %w", err)
	}

	// Tor generated (or loaded) the v3 key during bootstrap and wrote the
	// address into the hostname file — there is no ADD_ONION and no manual
	// key handling anywhere in this package.
	onion, err := os.ReadFile(dirs.HostnamePath())
	if err != nil {
		_ = svc.Close()
		return nil, fmt.Errorf("failed to read onion hostname: %w", err)
	}
	svc.onionAddress = strings.TrimSpace(string(onion))

	if cfg.UseNetwork {
		// A dialer failure costs outbound anonymity only; the hidden
		// service is unaffected, so it is never fatal.
		if dialer, derr := t.Dialer(ctx, nil); derr == nil {
			svc.dialer = dialer
		}
	}

	return svc, nil
}

// BuildTorrc renders the torrc for cfg, per AI.md PART 31.1 "Tor
// Configuration Optimizations".
//
// The hidden service is declared here through the HiddenServiceDir block —
// never through ADD_ONION — so Tor owns key generation and persistence.
// SocksPort and ControlPort use Tor's own "auto" runtime detection, so the
// default 9050/9051 ports are never used and no port is ever persisted.
func BuildTorrc(cfg config.Tor, hsDir string, backendPort int) string {
	cfg = cfg.Normalized()

	socksConfig := "SocksPort 0"
	if cfg.UseNetwork {
		socksConfig = "SocksPort auto"
	}

	safeLogging := "1"
	if !cfg.SafeLogging {
		safeLogging = "0"
	}

	accountingConfig := ""
	if cfg.MaxMonthlyBandwidth != "" && cfg.MaxMonthlyBandwidth != "unlimited" {
		accountingConfig = fmt.Sprintf("\n# Monthly bandwidth limit\nAccountingStart month 1 00:00\nAccountingMax %s", cfg.MaxMonthlyBandwidth)
	}

	return fmt.Sprintf(`
# ============================================================
# Tor Configuration - Generated by server binary
# Regenerated on every start: the backend port changes each run
# Manual edits are overwritten - change server.tor.* in server.yml instead
# ============================================================

# SOCKS port for outbound connections (0 = disabled, auto = runtime port)
# NEVER uses default port 9050 - runtime detection only
%s

# Control connection
# NEVER uses default port 9051 - uses runtime localhost port on all OSes
ControlPort 127.0.0.1:auto

# Security Hardening
SafeLogging %s

# Circuit limits
MaxCircuitDirtiness 600

# Bandwidth limits per second (from config)
BandwidthRate %s
BandwidthBurst %s
%s

# Disable unused features - not a relay or exit
ExitRelay 0
ExitPolicy reject *:*
ORPort 0
DirPort 0

# Guard-discovery-attack defense (vanguards-lite) - built into Tor >= 0.4.7; keep enabled, never disable
VanguardsLiteEnabled 1

# Hidden service optimizations
HiddenServiceSingleHopMode 0

# Faster startup
FetchDirInfoEarly 1
FetchDirInfoExtraEarly 1

# Reduce memory usage
DisableDebuggerAttachment 1

# ============================================================
# Hidden Service (v3) - Tor generates and persists the key + hostname here
# ============================================================
HiddenServiceDir %s
HiddenServiceVersion 3
HiddenServicePort %d 127.0.0.1:%d
# Export per-rendezvous-circuit ID via HAProxy PROXY protocol (opaque token, not an IP)
HiddenServiceExportCircuitID haproxy
`, socksConfig, safeLogging, cfg.BandwidthRate, cfg.BandwidthBurst, accountingConfig, hsDir, cfg.VirtualPort, backendPort)
}

// EnsureDirs creates every Tor directory with 0700 and the app's own
// uid/gid, enforcing them even when the directory already existed, per
// AI.md PART 31.1 "Runtime Directory Handling". It is idempotent and must
// run before any Tor file is written.
func EnsureDirs(dirs Dirs) error {
	for _, dir := range []string{
		filepath.Join(dirs.Config, "tor"),
		dirs.DataPath(),
		dirs.SitePath(),
	} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("create tor dir %s: %w", dir, err)
		}
		if err := os.Chmod(dir, 0700); err != nil {
			return fmt.Errorf("chmod tor dir %s: %w", dir, err)
		}
		if err := chownSelf(dir); err != nil {
			return fmt.Errorf("chown tor dir %s: %w", dir, err)
		}
	}
	return nil
}

// writeSecret (over)writes a Tor file with 0600 and the app's uid/gid,
// creating the parent directory first. Used for torrc and for operator-
// supplied keys.
func writeSecret(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create parent dir: %w", err)
	}
	if err := os.WriteFile(path, content, 0600); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		return fmt.Errorf("chmod file: %w", err)
	}
	return chownSelf(path)
}

// WriteHiddenServiceSecret installs an ed25519 v3 secret key into a hidden
// service directory with the 0600 permissions Tor insists on. It is how the
// `tor import-keys` and `tor vanity apply` commands hand an identity to the
// server without starting Tor themselves.
func WriteHiddenServiceSecret(siteDir string, secret []byte) error {
	return writeSecret(filepath.Join(siteDir, "hs_ed25519_secret_key"), secret)
}

// chownSelf sets path's owner to the process's own uid/gid. Windows has no
// chown and inherits ACLs from the user profile instead, so it is skipped
// there rather than reported as a failure.
func chownSelf(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	return os.Chown(path, os.Getuid(), os.Getgid())
}
