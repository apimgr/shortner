// Package i2p implements the AI.md PART 31.2 eepsite: an optional,
// opt-in I2P presence for the server.
//
// Two provider models are supported, resolved in this order: Model A spawns
// a dedicated i2pd child process driven by a regenerated tunnels.conf; Model
// B talks raw SAMv3 to an already-running router. Nothing at all happens —
// no provider probe, no port, no file — unless server.i2p.enabled is true.
//
// The eepsite backend is a dedicated plain loopback listener. Unlike Tor
// there is no PROXY-protocol header to parse, so an I2P request arrives
// with no client identity whatsoever, which is exactly the property the
// network is designed to provide.
package i2p

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/common/netutil"
	"github.com/apimgr/shortner/src/config"
)

// Provider identifies which I2P implementation is backing the eepsite.
type Provider int

const (
	// ProviderNone means no provider is available; the eepsite is off.
	ProviderNone Provider = iota
	// ProviderI2PD is Model A: a dedicated i2pd child process.
	ProviderI2PD
	// ProviderSAM is Model B: an external router's SAMv3 bridge.
	ProviderSAM
)

// Name returns the provider's config/status name.
func (p Provider) Name() string {
	switch p {
	case ProviderI2PD:
		return "i2pd"
	case ProviderSAM:
		return "sam"
	default:
		return "none"
	}
}

// ErrDisabled is returned when the eepsite is asked to start while
// server.i2p.enabled is false. AI.md PART 31.2 makes I2P opt-in, so this is
// the normal, expected outcome on a default install.
var ErrDisabled = errors.New("i2p disabled (opt-in) - eepsite not started")

// i2pBase64 is I2P's base64 alphabet: standard base64 with '-' and '~' in
// place of '+' and '/'.
var i2pBase64 = base64.NewEncoding("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-~")

// b32Encoding is unpadded base32; .b32.i2p addresses are lowercase.
var b32Encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// Dirs is the set of on-disk locations the eepsite uses, from AI.md
// PART 31.2 "Storage Locations". They are passed in rather than resolved
// here so the CLI and the server always agree on them.
type Dirs struct {
	// Config is the server's config directory.
	Config string
	// Data is the server's data directory.
	Data string
	// Log is the server's log directory.
	Log string
}

// ConfigPath is {config_dir}/i2p.
func (d Dirs) ConfigPath() string { return filepath.Join(d.Config, "i2p") }

// TunnelsPath is {config_dir}/i2p/tunnels.conf (Model A only).
func (d Dirs) TunnelsPath() string { return filepath.Join(d.ConfigPath(), "tunnels.conf") }

// DataPath is {data_dir}/i2p.
func (d Dirs) DataPath() string { return filepath.Join(d.Data, "i2p") }

// SitePath is {data_dir}/i2p/site.
func (d Dirs) SitePath() string { return filepath.Join(d.DataPath(), "site") }

// KeysPath is {data_dir}/i2p/site/site-keys.dat, the persisted destination.
func (d Dirs) KeysPath() string { return filepath.Join(d.SitePath(), "site-keys.dat") }

// HostnamePath is where the resolved .b32.i2p address is cached so that an
// out-of-process `--status` can report it without contacting a router.
func (d Dirs) HostnamePath() string { return filepath.Join(d.SitePath(), "hostname") }

// LogPath is {log_dir}/i2pd.log (Model A only).
func (d Dirs) LogPath() string { return filepath.Join(d.Log, "i2pd.log") }

// ReadEepsiteAddress returns the cached .b32.i2p address for a data
// directory, or an empty string when no eepsite has ever been established.
func ReadEepsiteAddress(dataDir string) string {
	data, err := os.ReadFile(filepath.Join(dataDir, "i2p", "site", "hostname"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// commonI2PDPaths lists the well-known i2pd install locations from AI.md
// PART 31.2, in the order they are probed.
func commonI2PDPaths() []string {
	if runtime.GOOS == "windows" {
		return []string{`C:\Program Files\i2pd\i2pd.exe`, `C:\Program Files (x86)\i2pd\i2pd.exe`}
	}
	return []string{"/usr/bin/i2pd", "/usr/sbin/i2pd", "/usr/local/bin/i2pd", "/opt/homebrew/bin/i2pd"}
}

// ResolveBinary finds the i2pd binary. An explicit server.i2p.binary that
// does not exist is an error rather than a silent fallback, so a typo in
// the config is reported instead of quietly selecting a different router.
func ResolveBinary(cfg config.I2P) (string, error) {
	if cfg.Binary != "" {
		if _, err := os.Stat(cfg.Binary); err != nil {
			return "", fmt.Errorf("configured i2pd binary %s not found: %w", cfg.Binary, err)
		}
		return cfg.Binary, nil
	}
	for _, candidate := range commonI2PDPaths() {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	path, err := exec.LookPath("i2pd")
	if err != nil {
		return "", errors.New("i2pd binary not found")
	}
	return path, nil
}

// SAMReachable reports whether a SAMv3 bridge answers at addr.
func SAMReachable(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// ResolveProvider picks the provider the eepsite would use right now,
// i2pd first and the SAM bridge second, exactly as Start does. The CLI uses
// it to report what would happen without starting anything.
func ResolveProvider(cfg config.I2P) (Provider, string) {
	if binary, err := ResolveBinary(cfg); err == nil {
		return ProviderI2PD, binary
	}
	if SAMReachable(cfg.SAMAddress) {
		return ProviderSAM, ""
	}
	return ProviderNone, ""
}

// Service is a running eepsite: its provider, its address, and the plain
// loopback listener the provider forwards connections to.
type Service struct {
	provider    Provider
	address     string
	backendPort int
	backend     net.Listener
	virtualPort int
	i2pd        *exec.Cmd
	samConn     net.Conn
	// exited is closed by the reaper goroutine when the i2pd child process
	// terminates. Waiting in the background both reaps the zombie and gives
	// Healthy a race-free liveness answer without signalling the process.
	exited chan struct{}
}

// EepsiteAddress returns the full .b32.i2p address, or an empty string when
// no eepsite is running.
func (s *Service) EepsiteAddress() string {
	if s == nil {
		return ""
	}
	return s.address
}

// Provider returns the provider backing this eepsite.
func (s *Service) Provider() Provider {
	if s == nil {
		return ProviderNone
	}
	return s.provider
}

// VirtualPort returns the port the eepsite is published on.
func (s *Service) VirtualPort() int {
	if s == nil {
		return 0
	}
	return s.virtualPort
}

// BackendPort returns the loopback port the provider forwards to.
func (s *Service) BackendPort() int {
	if s == nil {
		return 0
	}
	return s.backendPort
}

// BackendListener returns the plain loopback listener the HTTP server
// serves eepsite traffic on.
func (s *Service) BackendListener() net.Listener {
	if s == nil {
		return nil
	}
	return s.backend
}

// Healthy reports whether the provider is still alive: for Model A the i2pd
// child process, for Model B the SAM control connection. It is the probe
// the i2p_health scheduler task and the monitor loop both use.
func (s *Service) Healthy() bool {
	if s == nil {
		return false
	}
	switch s.provider {
	case ProviderI2PD:
		if s.i2pd == nil || s.exited == nil {
			return false
		}
		select {
		case <-s.exited:
			return false
		default:
			return true
		}
	case ProviderSAM:
		if s.samConn == nil {
			return false
		}
		// The router sends nothing on an idle control connection, so a read
		// that times out means the session is alive and any other outcome
		// (EOF, reset) means the router hung up and the session is gone.
		if err := s.samConn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			return false
		}
		_, err := s.samConn.Read(make([]byte, 1))
		_ = s.samConn.SetReadDeadline(time.Time{})
		var netErr net.Error
		return errors.As(err, &netErr) && netErr.Timeout()
	default:
		return false
	}
}

// Close shuts the provider down and releases the backend listener.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	if s.backend != nil {
		_ = s.backend.Close()
		s.backend = nil
	}
	if s.samConn != nil {
		_ = s.samConn.Close()
		s.samConn = nil
	}
	if s.i2pd != nil && s.i2pd.Process != nil {
		err := s.i2pd.Process.Signal(os.Interrupt)
		s.i2pd = nil
		return err
	}
	s.i2pd = nil
	return nil
}

// Start brings the eepsite up. It returns ErrDisabled untouched when I2P is
// opt-out, and resolves the provider BEFORE allocating anything so that a
// host with no router leaves no port bound and no file written.
func Start(ctx context.Context, raw config.I2P, dirs Dirs) (*Service, error) {
	if !raw.Enabled {
		return nil, ErrDisabled
	}
	cfg := raw.Normalized()

	provider, binary := ResolveProvider(cfg)
	if provider == ProviderNone {
		return nil, fmt.Errorf("i2p enabled but no provider available (no i2pd binary, SAM %s unreachable)", cfg.SAMAddress)
	}

	if err := EnsureDirs(dirs); err != nil {
		return nil, fmt.Errorf("failed to create i2p directories: %w", err)
	}

	backend, port, err := netutil.ListenLoopback()
	if err != nil {
		return nil, fmt.Errorf("allocate i2p backend port: %w", err)
	}

	svc := &Service{provider: provider, backend: backend, backendPort: port, virtualPort: cfg.VirtualPort}

	switch provider {
	case ProviderI2PD:
		svc.address, err = startI2PD(ctx, cfg, dirs, binary, port, svc)
	case ProviderSAM:
		svc.address, err = startSAMEepsite(cfg, dirs, port, svc)
	}
	if err != nil {
		_ = svc.Close()
		return nil, err
	}

	if err := writeSecret(dirs.HostnamePath(), []byte(svc.address+"\n")); err != nil {
		_ = svc.Close()
		return nil, err
	}
	return svc, nil
}

// TunnelsConf renders the i2pd server-tunnel definition from AI.md
// PART 31.2 "tunnels.conf Generation". It is derived state, regenerated on
// every start; the identity lives in keysPath, never here.
func TunnelsConf(cfg config.I2P, keysPath string, backendPort int) string {
	return fmt.Sprintf(`[site]
type = server
host = 127.0.0.1
port = %d
keys = %s
inbound.length = %d
outbound.length = %d
inbound.quantity = %d
outbound.quantity = %d
signaturetype = %d
`, backendPort, keysPath,
		cfg.InboundLength, cfg.OutboundLength,
		cfg.InboundQuantity, cfg.OutboundQuantity, cfg.SignatureType)
}

// startI2PD is Model A: regenerate tunnels.conf, spawn a dedicated i2pd,
// and wait for it to persist the destination so the address can be derived.
func startI2PD(ctx context.Context, cfg config.I2P, dirs Dirs, binary string, backendPort int, svc *Service) (string, error) {
	if err := writeSecret(dirs.TunnelsPath(), []byte(TunnelsConf(cfg, dirs.KeysPath(), backendPort))); err != nil {
		return "", fmt.Errorf("failed to write tunnels.conf: %w", err)
	}

	cmd := exec.CommandContext(ctx, binary,
		"--datadir", dirs.DataPath(),
		"--tunconf", dirs.TunnelsPath(),
		"--log", "file",
		"--logfile", dirs.LogPath(),
	)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start i2pd: %w", err)
	}
	svc.i2pd = cmd
	svc.exited = make(chan struct{})
	go func(exited chan struct{}) {
		_ = cmd.Wait()
		close(exited)
	}(svc.exited)

	address, err := waitForI2PDAddress(ctx, dirs.KeysPath(), time.Duration(cfg.BootstrapTimeout)*time.Second)
	if err != nil {
		_ = cmd.Process.Signal(os.Interrupt)
		return "", err
	}
	return address, nil
}

// waitForI2PDAddress polls until i2pd has written a complete destination to
// keysPath, then derives the .b32.i2p address from it. i2pd creates the file
// before it is fully written, so a short-read is treated as "not ready yet"
// rather than as a failure.
func waitForI2PDAddress(ctx context.Context, keysPath string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		if data, err := os.ReadFile(keysPath); err == nil {
			if dest, err := DestinationFromKeys(data); err == nil {
				return B32Address(dest), nil
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("i2pd did not publish a destination within %s", timeout)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// DestinationFromKeys extracts the public Destination from an i2pd keys
// file. The file begins with the destination itself: a 384-byte key block
// followed by a certificate whose 3-byte header carries its own payload
// length, per the I2P common structures specification. Everything after
// that is private key material and is deliberately not touched here.
func DestinationFromKeys(data []byte) ([]byte, error) {
	const keyBlock = 384
	if len(data) < keyBlock+3 {
		return nil, errors.New("i2p keys file is too short to contain a destination")
	}
	certLen := int(data[keyBlock+1])<<8 | int(data[keyBlock+2])
	total := keyBlock + 3 + certLen
	if len(data) < total {
		return nil, errors.New("i2p keys file is truncated mid-certificate")
	}
	return data[:total], nil
}

// B32Address derives the .b32.i2p address from a binary destination:
// unpadded base32 of its SHA-256, lowercased.
func B32Address(destination []byte) string {
	sum := sha256.Sum256(destination)
	return strings.ToLower(b32Encoding.EncodeToString(sum[:])) + ".b32.i2p"
}

// samDestination is a SAMv3 destination: the public destination and the
// private key blob that reproduces it, both in I2P base64.
type samDestination struct {
	pub  string
	priv string
}

// startSAMEepsite is Model B: handshake with the router's SAM bridge, load
// or mint the persisted destination, open a STREAM session, and forward
// incoming eepsite streams to the plain backend port.
func startSAMEepsite(cfg config.I2P, dirs Dirs, backendPort int, svc *Service) (string, error) {
	conn, err := net.DialTimeout("tcp", cfg.SAMAddress, 5*time.Second)
	if err != nil {
		return "", fmt.Errorf("failed to dial SAM %s: %w", cfg.SAMAddress, err)
	}
	reader := bufio.NewReader(conn)

	if _, err := fmt.Fprint(conn, "HELLO VERSION MIN=3.0 MAX=3.3\n"); err != nil {
		_ = conn.Close()
		return "", err
	}
	if _, err := readSAMReply(reader, "HELLO REPLY"); err != nil {
		_ = conn.Close()
		return "", err
	}

	dest, err := loadOrCreateSAMDestination(conn, reader, dirs.KeysPath(), cfg.SignatureType)
	if err != nil {
		_ = conn.Close()
		return "", err
	}

	if _, err := fmt.Fprintf(conn, "SESSION CREATE STYLE=STREAM ID=site DESTINATION=%s "+
		"inbound.length=%d outbound.length=%d inbound.quantity=%d outbound.quantity=%d\n",
		dest.priv, cfg.InboundLength, cfg.OutboundLength, cfg.InboundQuantity, cfg.OutboundQuantity); err != nil {
		_ = conn.Close()
		return "", err
	}
	if _, err := readSAMReply(reader, "SESSION STATUS"); err != nil {
		_ = conn.Close()
		return "", err
	}

	if _, err := fmt.Fprintf(conn, "STREAM FORWARD ID=site PORT=%d HOST=127.0.0.1\n", backendPort); err != nil {
		_ = conn.Close()
		return "", err
	}
	if _, err := readSAMReply(reader, "STREAM STATUS"); err != nil {
		_ = conn.Close()
		return "", err
	}

	// The session lives exactly as long as this control connection, so it is
	// held open for the lifetime of the service.
	svc.samConn = conn

	raw, err := i2pBase64.DecodeString(dest.pub)
	if err != nil {
		_ = conn.Close()
		return "", fmt.Errorf("SAM returned an undecodable destination: %w", err)
	}
	return B32Address(raw), nil
}

// loadOrCreateSAMDestination returns the persisted destination, generating
// and persisting one the first time. The private blob is stored verbatim
// because it is what SESSION CREATE takes; the public half is derived from
// it by the router on every subsequent run.
func loadOrCreateSAMDestination(conn net.Conn, reader *bufio.Reader, keysPath string, signatureType int) (samDestination, error) {
	if data, err := os.ReadFile(keysPath); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) == 2 {
			return samDestination{pub: fields[0], priv: fields[1]}, nil
		}
	}

	if _, err := fmt.Fprintf(conn, "DEST GENERATE SIGNATURE_TYPE=%d\n", signatureType); err != nil {
		return samDestination{}, err
	}
	fields, err := readSAMReply(reader, "DEST REPLY")
	if err != nil {
		return samDestination{}, err
	}
	dest := samDestination{pub: fields["PUB"], priv: fields["PRIV"]}
	if dest.pub == "" || dest.priv == "" {
		return samDestination{}, errors.New("SAM DEST REPLY did not carry PUB and PRIV")
	}
	if err := writeSecret(keysPath, []byte(dest.pub+"\n"+dest.priv+"\n")); err != nil {
		return samDestination{}, err
	}
	return dest, nil
}

// readSAMReply reads one SAM control line, verifies it is the expected
// reply, and returns its KEY=VALUE fields. A RESULT other than OK is turned
// into an error carrying the router's own MESSAGE, which is the only useful
// diagnostic SAM provides.
func readSAMReply(reader *bufio.Reader, expect string) (map[string]string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", expect, err)
	}
	line = strings.TrimRight(line, "\r\n")
	if !strings.HasPrefix(line, expect) {
		return nil, fmt.Errorf("expected %s, got %q", expect, line)
	}

	fields := map[string]string{}
	for _, token := range splitSAMFields(strings.TrimPrefix(line, expect)) {
		key, value, found := strings.Cut(token, "=")
		if !found {
			continue
		}
		fields[key] = strings.Trim(value, `"`)
	}
	if result, ok := fields["RESULT"]; ok && result != "OK" {
		if message := fields["MESSAGE"]; message != "" {
			return nil, fmt.Errorf("%s: %s (%s)", expect, result, message)
		}
		return nil, fmt.Errorf("%s: %s", expect, result)
	}
	return fields, nil
}

// splitSAMFields splits a SAM reply's field list on spaces, keeping
// double-quoted values (MESSAGE="..." routinely contains spaces) intact.
func splitSAMFields(s string) []string {
	var fields []string
	var current strings.Builder
	quoted := false
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
			current.WriteRune(r)
		case r == ' ' && !quoted:
			if current.Len() > 0 {
				fields = append(fields, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		fields = append(fields, current.String())
	}
	return fields
}

// EnsureDirs creates every I2P directory with 0700 and the app's own
// uid/gid before any file is written, per AI.md PART 31.2 "Runtime
// Directory Handling".
func EnsureDirs(dirs Dirs) error {
	for _, dir := range []string{dirs.ConfigPath(), dirs.DataPath(), dirs.SitePath()} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("create i2p dir %s: %w", dir, err)
		}
		if err := os.Chmod(dir, 0700); err != nil {
			return fmt.Errorf("chmod i2p dir %s: %w", dir, err)
		}
		if err := chownSelf(dir); err != nil {
			return fmt.Errorf("chown i2p dir %s: %w", dir, err)
		}
	}
	return nil
}

// writeSecret (over)writes an I2P file with 0600 and the app's uid/gid,
// creating the parent directory first.
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

// chownSelf sets path's owner to the process's own uid/gid. Windows has no
// chown and inherits ACLs from the user profile instead, so it is skipped
// there rather than reported as a failure.
func chownSelf(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	return os.Chown(path, os.Getuid(), os.Getgid())
}
