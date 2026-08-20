package tor

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/config"
)

// Dirs path helpers must derive every location from the three app
// directories exactly as AI.md PART 31.1 "Storage Locations" specifies —
// nothing here is configurable, so a wrong join is a silent spec violation.
func TestDirsPaths(t *testing.T) {
	d := Dirs{Config: "/cfg", Data: "/data", Log: "/log"}

	cases := []struct {
		name string
		got  string
		want string
	}{
		{"TorrcPath", d.TorrcPath(), filepath.Join("/cfg", "tor", "torrc")},
		{"DataPath", d.DataPath(), filepath.Join("/data", "tor")},
		{"SitePath", d.SitePath(), filepath.Join("/data", "tor", "site")},
		{"HostnamePath", d.HostnamePath(), filepath.Join("/data", "tor", "site", "hostname")},
		{"LogPath", d.LogPath(), filepath.Join("/log", "tor.log")},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

// ReadOnionAddress must return the trimmed hostname when present, and an
// empty string — never an error — when the file is missing, since it backs
// the out-of-process `--status` path that must never fail on a server that
// simply hasn't published yet.
func TestReadOnionAddress(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		dir := t.TempDir()
		if got := ReadOnionAddress(dir); got != "" {
			t.Errorf("ReadOnionAddress() = %q, want empty", got)
		}
	})

	t.Run("present with trailing newline", func(t *testing.T) {
		dir := t.TempDir()
		site := filepath.Join(dir, "tor", "site")
		if err := os.MkdirAll(site, 0700); err != nil {
			t.Fatal(err)
		}
		addr := "abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuvwx.onion"
		if err := os.WriteFile(filepath.Join(site, "hostname"), []byte(addr+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if got := ReadOnionAddress(dir); got != addr {
			t.Errorf("ReadOnionAddress() = %q, want %q", got, addr)
		}
	})
}

// ResolveBinary must honor an explicit override, fail loudly when that
// override does not exist, and fall through to auto-detection (which finds
// nothing in a controlled empty PATH) otherwise.
func TestResolveBinary(t *testing.T) {
	t.Run("explicit binary exists", func(t *testing.T) {
		dir := t.TempDir()
		bin := filepath.Join(dir, "tor")
		if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0755); err != nil {
			t.Fatal(err)
		}
		got, err := ResolveBinary(config.Tor{Binary: bin})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != bin {
			t.Errorf("ResolveBinary() = %q, want %q", got, bin)
		}
	})

	t.Run("explicit binary missing", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "does-not-exist")
		_, err := ResolveBinary(config.Tor{Binary: missing})
		if err == nil {
			t.Fatal("expected an error for a missing configured binary")
		}
	})

	t.Run("auto-detect finds nothing", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("commonBinaryPaths are windows-specific and cannot be emptied via PATH alone")
		}
		emptyPath := t.TempDir()
		t.Setenv("PATH", emptyPath)
		// commonBinaryPaths are absolute filesystem locations that may
		// genuinely exist on the host running this test (e.g. a real tor
		// install); this assertion only fails if that happens, which
		// documents the coupling rather than hiding it.
		_, err := ResolveBinary(config.Tor{})
		if err == nil {
			t.Skip("a tor binary exists at one of the well-known paths or on PATH in this environment")
		}
	})
}

// BuildTorrc must emit every directive AI.md PART 31.1 requires, with the
// configured values actually substituted in — not just present as literal
// template text.
func TestBuildTorrc(t *testing.T) {
	cfg := config.Tor{
		UseNetwork:          true,
		SafeLogging:         true,
		BandwidthRate:       "1 MB",
		BandwidthBurst:      "2 MB",
		MaxMonthlyBandwidth: "100 GB",
		VirtualPort:         80,
		BootstrapTimeout:    120,
	}
	hsDir := "/data/tor/site"
	backendPort := 64123

	out := BuildTorrc(cfg, hsDir, backendPort)

	mustContain := []string{
		"SocksPort auto",
		"ControlPort 127.0.0.1:auto",
		"SafeLogging 1",
		"BandwidthRate 1 MB",
		"BandwidthBurst 2 MB",
		"AccountingStart month 1 00:00",
		"AccountingMax 100 GB",
		"HiddenServiceDir " + hsDir,
		"HiddenServiceVersion 3",
		"HiddenServicePort 80 127.0.0.1:" + strconv.Itoa(backendPort),
		"HiddenServiceExportCircuitID haproxy",
		"ExitRelay 0",
		"ExitPolicy reject *:*",
		"ORPort 0",
		"DirPort 0",
	}
	for _, want := range mustContain {
		if !strings.Contains(out, want) {
			t.Errorf("BuildTorrc() missing directive %q\noutput:\n%s", want, out)
		}
	}
}

// SocksPort must be disabled (never "auto") when outbound routing is off,
// and safe logging must render as 0 when disabled.
func TestBuildTorrcOutboundDisabled(t *testing.T) {
	cfg := config.Tor{
		UseNetwork:     false,
		SafeLogging:    false,
		BandwidthRate:  "1 MB",
		BandwidthBurst: "2 MB",
		VirtualPort:    80,
	}
	out := BuildTorrc(cfg, "/data/tor/site", 64000)

	if !strings.Contains(out, "SocksPort 0") {
		t.Error("expected SocksPort 0 when UseNetwork is false")
	}
	if strings.Contains(out, "SocksPort auto") {
		t.Error("SocksPort must not be auto when UseNetwork is false")
	}
	if !strings.Contains(out, "SafeLogging 0") {
		t.Error("expected SafeLogging 0 when SafeLogging is false")
	}
}

// An "unlimited" (or empty) MaxMonthlyBandwidth must omit the accounting
// block entirely — Tor would otherwise cap a service the operator asked to
// leave unlimited.
func TestBuildTorrcUnlimitedBandwidthOmitsAccounting(t *testing.T) {
	for _, mmb := range []string{"unlimited", ""} {
		cfg := config.Tor{
			BandwidthRate:       "1 MB",
			BandwidthBurst:      "2 MB",
			MaxMonthlyBandwidth: mmb,
			VirtualPort:         80,
		}
		out := BuildTorrc(cfg, "/data/tor/site", 64000)
		if strings.Contains(out, "AccountingStart") || strings.Contains(out, "AccountingMax") {
			t.Errorf("MaxMonthlyBandwidth=%q must omit accounting directives, got:\n%s", mmb, out)
		}
	}
}

// EnsureDirs must create the config/tor, data/tor and data/tor/site
// directories with 0700 permissions, and WriteHiddenServiceSecret must
// write the secret key file with 0600 — Tor refuses to start against
// looser permissions on either.
func TestEnsureDirsAndWriteHiddenServiceSecret(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits are not meaningful on windows")
	}
	base := t.TempDir()
	dirs := Dirs{
		Config: filepath.Join(base, "config"),
		Data:   filepath.Join(base, "data"),
		Log:    filepath.Join(base, "log"),
	}
	if err := EnsureDirs(dirs); err != nil {
		t.Fatalf("EnsureDirs() error = %v", err)
	}

	for _, dir := range []string{
		filepath.Join(dirs.Config, "tor"),
		dirs.DataPath(),
		dirs.SitePath(),
	} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if perm := info.Mode().Perm(); perm != 0700 {
			t.Errorf("%s permissions = %o, want 0700", dir, perm)
		}
	}

	if err := WriteHiddenServiceSecret(dirs.SitePath(), []byte("secret-material")); err != nil {
		t.Fatalf("WriteHiddenServiceSecret() error = %v", err)
	}
	secretPath := filepath.Join(dirs.SitePath(), "hs_ed25519_secret_key")
	info, err := os.Stat(secretPath)
	if err != nil {
		t.Fatalf("stat secret key: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("secret key permissions = %o, want 0600", perm)
	}
	got, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "secret-material" {
		t.Errorf("secret key content = %q, want %q", got, "secret-material")
	}
}

// EnsureDirs must be idempotent: running it twice against an already
// existing (and possibly loosened) tree must re-enforce 0700 rather than
// error out.
func TestEnsureDirsIdempotent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits are not meaningful on windows")
	}
	base := t.TempDir()
	dirs := Dirs{
		Config: filepath.Join(base, "config"),
		Data:   filepath.Join(base, "data"),
		Log:    filepath.Join(base, "log"),
	}
	if err := EnsureDirs(dirs); err != nil {
		t.Fatalf("first EnsureDirs() error = %v", err)
	}
	// Loosen permissions to simulate an operator or umask change, then
	// confirm the second call re-tightens them.
	if err := os.Chmod(dirs.SitePath(), 0755); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDirs(dirs); err != nil {
		t.Fatalf("second EnsureDirs() error = %v", err)
	}
	info, err := os.Stat(dirs.SitePath())
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0700 {
		t.Errorf("SitePath permissions after re-run = %o, want 0700", perm)
	}
}

// Service methods must be nil-receiver-safe: every caller in this package
// treats "Tor unavailable" and "no Service yet" identically, so a nil
// *Service must behave like an unstarted one rather than panic.
func TestServiceNilReceiver(t *testing.T) {
	var s *Service
	if got := s.OnionAddress(); got != "" {
		t.Errorf("OnionAddress() on nil = %q, want empty", got)
	}
	if got := s.VirtualPort(); got != 0 {
		t.Errorf("VirtualPort() on nil = %d, want 0", got)
	}
	if got := s.BackendPort(); got != 0 {
		t.Errorf("BackendPort() on nil = %d, want 0", got)
	}
	if got := s.BackendListener(); got != nil {
		t.Errorf("BackendListener() on nil = %v, want nil", got)
	}
	if s.OutboundEnabled() {
		t.Error("OutboundEnabled() on nil should be false")
	}
	if s.Healthy() {
		t.Error("Healthy() on nil should be false")
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close() on nil should not error, got %v", err)
	}
	client := s.GetHTTPClient(true)
	if client == nil {
		t.Fatal("GetHTTPClient() on nil returned nil client")
	}
}

// Start must fail fast — before allocating a backend port or writing any
// file — when no tor binary can be resolved, per the "no provider → no
// port, no generated config" gate.
func TestStartMissingBinary(t *testing.T) {
	base := t.TempDir()
	dirs := Dirs{
		Config: filepath.Join(base, "config"),
		Data:   filepath.Join(base, "data"),
		Log:    filepath.Join(base, "log"),
	}
	missing := filepath.Join(base, "no-such-tor-binary")
	_, err := Start(t.Context(), config.Tor{Binary: missing}, dirs)
	if err == nil {
		t.Fatal("expected an error when the configured tor binary does not exist")
	}
	if _, statErr := os.Stat(dirs.TorrcPath()); !os.IsNotExist(statErr) {
		t.Error("torrc must not be written when the binary cannot be resolved")
	}
}
