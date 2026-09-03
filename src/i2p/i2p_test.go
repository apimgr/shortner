package i2p

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/config"
)

// Dirs path helpers should join onto the config/data/log roots exactly as
// AI.md PART 31.2 "Storage Locations" specifies.
func TestDirsPaths(t *testing.T) {
	d := Dirs{Config: "/cfg", Data: "/data", Log: "/log"}

	cases := map[string]string{
		"ConfigPath":   filepath.Join("/cfg", "i2p"),
		"TunnelsPath":  filepath.Join("/cfg", "i2p", "tunnels.conf"),
		"DataPath":     filepath.Join("/data", "i2p"),
		"SitePath":     filepath.Join("/data", "i2p", "site"),
		"KeysPath":     filepath.Join("/data", "i2p", "site", "site-keys.dat"),
		"HostnamePath": filepath.Join("/data", "i2p", "site", "hostname"),
		"LogPath":      filepath.Join("/log", "i2pd.log"),
	}

	got := map[string]string{
		"ConfigPath":   d.ConfigPath(),
		"TunnelsPath":  d.TunnelsPath(),
		"DataPath":     d.DataPath(),
		"SitePath":     d.SitePath(),
		"KeysPath":     d.KeysPath(),
		"HostnamePath": d.HostnamePath(),
		"LogPath":      d.LogPath(),
	}

	for name, want := range cases {
		if got[name] != want {
			t.Errorf("%s = %q, want %q", name, got[name], want)
		}
	}
}

// ReadEepsiteAddress must return the trimmed hostname when the file exists
// and an empty string, never an error, when it does not.
func TestReadEepsiteAddress(t *testing.T) {
	dir := t.TempDir()

	if got := ReadEepsiteAddress(dir); got != "" {
		t.Errorf("no hostname file: got %q, want empty", got)
	}

	sitePath := filepath.Join(dir, "i2p", "site")
	if err := os.MkdirAll(sitePath, 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	addr := "abcdefghijklmnop.b32.i2p"
	if err := os.WriteFile(filepath.Join(sitePath, "hostname"), []byte(addr+"\n"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if got := ReadEepsiteAddress(dir); got != addr {
		t.Errorf("got %q, want %q", got, addr)
	}
}

// ResolveBinary must find an explicit configured path, fail loudly on a
// configured path that does not exist (never silently fall back), and fall
// back to PATH lookup when nothing is configured.
func TestResolveBinary(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "i2pd")
	if err := os.WriteFile(real, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	t.Run("explicit path exists", func(t *testing.T) {
		got, err := ResolveBinary(config.I2P{Binary: real})
		if err != nil {
			t.Fatalf("ResolveBinary: %v", err)
		}
		if got != real {
			t.Errorf("got %q, want %q", got, real)
		}
	})

	t.Run("explicit path missing is an error not a fallback", func(t *testing.T) {
		missing := filepath.Join(dir, "does-not-exist")
		_, err := ResolveBinary(config.I2P{Binary: missing})
		if err == nil {
			t.Fatal("expected an error for a missing configured binary")
		}
	})

	t.Run("nothing on PATH and no common install", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("PATH manipulation differs on windows")
		}
		empty := t.TempDir()
		t.Setenv("PATH", empty)
		_, err := ResolveBinary(config.I2P{})
		if err == nil {
			t.Fatal("expected an error when no i2pd binary is anywhere")
		}
	})

	t.Run("found via PATH", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("PATH manipulation differs on windows")
		}
		t.Setenv("PATH", dir)
		got, err := ResolveBinary(config.I2P{})
		if err != nil {
			t.Fatalf("ResolveBinary: %v", err)
		}
		if got != real {
			t.Errorf("got %q, want %q", got, real)
		}
	})
}

// SAMReachable must report true against a listener that accepts connections
// and false against an address nothing is listening on.
func TestSAMReachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	if !SAMReachable(ln.Addr().String()) {
		t.Error("expected the listening address to be reachable")
	}

	// Bind and immediately close to obtain a port nothing is listening on.
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	deadAddr := closed.Addr().String()
	closed.Close()

	if SAMReachable(deadAddr) {
		t.Error("expected a closed port to be unreachable")
	}
}

// ResolveProvider must prefer i2pd over SAM, fall through to SAM when no
// i2pd binary exists, and land on ProviderNone when neither is available.
func TestResolveProvider(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH manipulation differs on windows")
	}

	t.Run("i2pd present wins over SAM", func(t *testing.T) {
		dir := t.TempDir()
		real := filepath.Join(dir, "i2pd")
		if err := os.WriteFile(real, []byte("#!/bin/sh\n"), 0755); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		provider, binary := ResolveProvider(config.I2P{Binary: real, SAMAddress: "127.0.0.1:1"})
		if provider != ProviderI2PD {
			t.Errorf("provider = %v, want ProviderI2PD", provider)
		}
		if binary != real {
			t.Errorf("binary = %q, want %q", binary, real)
		}
	})

	t.Run("SAM reachable when no i2pd", func(t *testing.T) {
		empty := t.TempDir()
		t.Setenv("PATH", empty)

		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("Listen: %v", err)
		}
		defer ln.Close()
		go func() {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				_ = conn.Close()
			}
		}()

		provider, _ := ResolveProvider(config.I2P{SAMAddress: ln.Addr().String()})
		if provider != ProviderSAM {
			t.Errorf("provider = %v, want ProviderSAM", provider)
		}
	})

	t.Run("nothing available", func(t *testing.T) {
		empty := t.TempDir()
		t.Setenv("PATH", empty)
		provider, _ := ResolveProvider(config.I2P{SAMAddress: "127.0.0.1:1"})
		if provider != ProviderNone {
			t.Errorf("provider = %v, want ProviderNone", provider)
		}
	})
}

// Provider.Name must return the exact strings the health endpoint and the
// config/status surfaces depend on (AI.md PART 13).
func TestProviderName(t *testing.T) {
	cases := map[Provider]string{
		ProviderNone: "none",
		ProviderI2PD: "i2pd",
		ProviderSAM:  "sam",
		Provider(99): "none",
	}
	for provider, want := range cases {
		if got := provider.Name(); got != want {
			t.Errorf("Provider(%d).Name() = %q, want %q", provider, got, want)
		}
	}
}

// TunnelsConf must carry every value it was given through into the rendered
// text, per AI.md PART 31.2 "tunnels.conf Generation".
func TestTunnelsConf(t *testing.T) {
	cfg := config.I2P{
		InboundLength:    4,
		OutboundLength:   5,
		InboundQuantity:  6,
		OutboundQuantity: 7,
		SignatureType:    7,
	}
	out := TunnelsConf(cfg, "/data/i2p/site/site-keys.dat", 4242)

	for _, want := range []string{
		"host = 127.0.0.1",
		"port = 4242",
		"keys = /data/i2p/site/site-keys.dat",
		"inbound.length = 4",
		"outbound.length = 5",
		"inbound.quantity = 6",
		"outbound.quantity = 7",
		"signaturetype = 7",
		"type = server",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("TunnelsConf output missing %q; got:\n%s", want, out)
		}
	}
}

// B32Address must be the lowercase, unpadded base32 of the SHA-256 of the
// destination, suffixed with .b32.i2p, computed independently here.
func TestB32Address(t *testing.T) {
	destination := []byte("a fake but nonempty destination blob")
	sum := sha256.Sum256(destination)
	want := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:])) + ".b32.i2p"

	got := B32Address(destination)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if !strings.HasSuffix(got, ".b32.i2p") {
		t.Errorf("missing .b32.i2p suffix: %q", got)
	}
	if got != strings.ToLower(got) {
		t.Errorf("address is not lowercase: %q", got)
	}
	if strings.Contains(got, "=") {
		t.Errorf("address should be unpadded, got %q", got)
	}
}

// DestinationFromKeys must extract the destination from a valid-shaped keys
// blob (384-byte key block + a 3-byte certificate header) and reject a blob
// that is too short to even contain the fixed key block.
func TestDestinationFromKeys(t *testing.T) {
	t.Run("valid shape, zero-length certificate", func(t *testing.T) {
		data := make([]byte, 387)
		if _, err := rand.Read(data[:384]); err != nil {
			t.Fatalf("rand.Read: %v", err)
		}
		// Certificate type + zero-length payload (bytes 385-386 are the
		// big-endian length).
		data[384] = 0
		data[385] = 0
		data[386] = 0

		dest, err := DestinationFromKeys(data)
		if err != nil {
			t.Fatalf("DestinationFromKeys: %v", err)
		}
		if len(dest) != 387 {
			t.Errorf("len(dest) = %d, want 387", len(dest))
		}
	})

	t.Run("valid shape, nonzero-length certificate", func(t *testing.T) {
		data := make([]byte, 384+3+5)
		data[384] = 0
		data[385] = 0
		data[386] = 5
		dest, err := DestinationFromKeys(data)
		if err != nil {
			t.Fatalf("DestinationFromKeys: %v", err)
		}
		if len(dest) != 384+3+5 {
			t.Errorf("len(dest) = %d, want %d", len(dest), 384+3+5)
		}
	})

	t.Run("too short", func(t *testing.T) {
		if _, err := DestinationFromKeys(make([]byte, 10)); err == nil {
			t.Error("expected an error for a too-short keys file")
		}
	})

	t.Run("truncated mid-certificate", func(t *testing.T) {
		data := make([]byte, 384+3+2)
		data[386] = 5
		if _, err := DestinationFromKeys(data); err == nil {
			t.Error("expected an error for a truncated certificate")
		}
	})
}

// EnsureDirs must create every I2P directory at 0700, and writeSecret must
// create files at 0600 - the permissions AI.md PART 31.2 "Runtime Directory
// Handling" requires for material that must not be world-readable.
func TestEnsureDirsAndWriteSecretPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits do not apply on windows")
	}
	dir := t.TempDir()
	dirs := Dirs{Config: filepath.Join(dir, "config"), Data: filepath.Join(dir, "data"), Log: filepath.Join(dir, "log")}

	if err := EnsureDirs(dirs); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	for _, path := range []string{dirs.ConfigPath(), dirs.DataPath(), dirs.SitePath()} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(%s): %v", path, err)
		}
		if perm := info.Mode().Perm(); perm != 0700 {
			t.Errorf("%s perm = %o, want 0700", path, perm)
		}
	}

	secretPath := dirs.HostnamePath()
	if err := writeSecret(secretPath, []byte("test\n")); err != nil {
		t.Fatalf("writeSecret: %v", err)
	}
	info, err := os.Stat(secretPath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("secret file perm = %o, want 0600", perm)
	}
	data, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "test\n" {
		t.Errorf("content = %q, want %q", data, "test\n")
	}
}

// splitSAMFields must split on unquoted spaces while keeping a
// double-quoted MESSAGE value (which routinely contains spaces) intact.
func TestSplitSAMFields(t *testing.T) {
	in := `RESULT=I2P_ERROR MESSAGE="some words here" PUB=abc`
	got := splitSAMFields(in)
	want := []string{`RESULT=I2P_ERROR`, `MESSAGE="some words here"`, `PUB=abc`}
	if len(got) != len(want) {
		t.Fatalf("got %d fields %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("field %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// readSAMReply must parse KEY=VALUE fields on success and turn a non-OK
// RESULT into an error carrying the router's MESSAGE.
func TestReadSAMReply(t *testing.T) {
	t.Run("ok reply", func(t *testing.T) {
		reader := bufio.NewReader(strings.NewReader("HELLO REPLY RESULT=OK VERSION=3.3\n"))
		fields, err := readSAMReply(reader, "HELLO REPLY")
		if err != nil {
			t.Fatalf("readSAMReply: %v", err)
		}
		if fields["RESULT"] != "OK" || fields["VERSION"] != "3.3" {
			t.Errorf("fields = %v", fields)
		}
	})

	t.Run("error result with message", func(t *testing.T) {
		reader := bufio.NewReader(strings.NewReader(`DEST REPLY RESULT=I2P_ERROR MESSAGE="router is down"` + "\n"))
		_, err := readSAMReply(reader, "DEST REPLY")
		if err == nil {
			t.Fatal("expected an error for a non-OK RESULT")
		}
		if !strings.Contains(err.Error(), "router is down") {
			t.Errorf("error %q does not carry the router MESSAGE", err)
		}
	})

	t.Run("unexpected reply prefix", func(t *testing.T) {
		reader := bufio.NewReader(strings.NewReader("SOMETHING ELSE RESULT=OK\n"))
		if _, err := readSAMReply(reader, "HELLO REPLY"); err == nil {
			t.Fatal("expected an error for an unexpected reply line")
		}
	})
}

// handleFakeSAMConn drives one connection through the same SAMv3 exchange
// startSAMEepsite performs: HELLO REPLY, DEST REPLY, SESSION STATUS, STREAM
// STATUS. failSession makes SESSION CREATE fail with RESULT=I2P_ERROR so the
// error path is exercised too. The connection is then held open (blocking on
// a read) so a caller's Healthy()/Close() checks have a live control
// connection to exercise, exactly like a real router would leave one open.
func handleFakeSAMConn(conn net.Conn, failSession bool) {
	defer conn.Close()
	reader := bufio.NewReader(conn)

	// HELLO VERSION ...
	if _, err := reader.ReadString('\n'); err != nil {
		return
	}
	if _, err := conn.Write([]byte("HELLO REPLY RESULT=OK VERSION=3.3\n")); err != nil {
		return
	}

	// The client sends DEST GENERATE only the first time (when nothing is
	// persisted on disk yet); on a Restart/RegenerateAddress reconnect it
	// goes straight to SESSION CREATE, so branch on what actually arrives
	// rather than assuming a fixed message order.
	line, err := reader.ReadString('\n')
	if err != nil {
		return
	}
	if strings.HasPrefix(line, "DEST GENERATE") {
		if _, err := conn.Write([]byte(
			"DEST REPLY PUB=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA PRIV=BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB\n",
		)); err != nil {
			return
		}
		// SESSION CREATE ...
		if _, err := reader.ReadString('\n'); err != nil {
			return
		}
	}
	if failSession {
		_, _ = conn.Write([]byte(`SESSION STATUS RESULT=I2P_ERROR MESSAGE="no tunnels available"` + "\n"))
		return
	}
	if _, err := conn.Write([]byte("SESSION STATUS RESULT=OK\n")); err != nil {
		return
	}

	// STREAM FORWARD ...
	if _, err := reader.ReadString('\n'); err != nil {
		return
	}
	if _, err := conn.Write([]byte("STREAM STATUS RESULT=OK\n")); err != nil {
		return
	}

	// Keep the connection open for the caller's Healthy()/Close() checks.
	buf := make([]byte, 1)
	_, _ = conn.Read(buf)
}

// fakeSAMServer accepts exactly one connection and drives it through
// handleFakeSAMConn.
func fakeSAMServer(t *testing.T, failSession bool) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		handleFakeSAMConn(conn, failSession)
	}()
	return ln
}

// fakeSAMServerMulti accepts an unbounded sequence of connections, each
// driven through handleFakeSAMConn, so a test can exercise Manager.Restart
// and Manager.RegenerateAddress (which each dial the bridge again).
func fakeSAMServerMulti(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleFakeSAMConn(conn, false)
		}
	}()
	return ln
}

// startSAMEepsite must complete the full handshake against a fake SAM
// server and derive a .b32.i2p address from the returned PUB destination.
func TestStartSAMEepsiteHappyPath(t *testing.T) {
	ln := fakeSAMServer(t, false)
	defer ln.Close()

	dir := t.TempDir()
	dirs := Dirs{Config: dir, Data: dir, Log: dir}
	if err := EnsureDirs(dirs); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	cfg := config.I2P{SAMAddress: ln.Addr().String()}.Normalized()
	svc := &Service{}
	address, err := startSAMEepsite(cfg, dirs, 4242, svc)
	if err != nil {
		t.Fatalf("startSAMEepsite: %v", err)
	}
	if !strings.HasSuffix(address, ".b32.i2p") {
		t.Errorf("address %q missing .b32.i2p suffix", address)
	}
	if svc.samConn == nil {
		t.Error("expected svc.samConn to be set on success")
	}

	// The destination should now be persisted for the next start.
	if _, err := os.Stat(dirs.KeysPath()); err != nil {
		t.Errorf("expected keys file to be persisted: %v", err)
	}
	_ = svc.Close()
}

// startSAMEepsite must surface a SESSION CREATE failure (RESULT=I2P_ERROR)
// as an error rather than a synthesized address.
func TestStartSAMEepsiteSessionError(t *testing.T) {
	ln := fakeSAMServer(t, true)
	defer ln.Close()

	dir := t.TempDir()
	dirs := Dirs{Config: dir, Data: dir, Log: dir}
	if err := EnsureDirs(dirs); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	cfg := config.I2P{SAMAddress: ln.Addr().String()}.Normalized()
	svc := &Service{}
	_, err := startSAMEepsite(cfg, dirs, 4242, svc)
	if err == nil {
		t.Fatal("expected an error when SESSION CREATE fails")
	}
	if !strings.Contains(err.Error(), "no tunnels available") {
		t.Errorf("error %q does not carry the router MESSAGE", err)
	}
}

// loadOrCreateSAMDestination must reuse a destination already persisted on
// disk without talking to the router at all.
func TestLoadOrCreateSAMDestinationReusesPersisted(t *testing.T) {
	dir := t.TempDir()
	keysPath := filepath.Join(dir, "site-keys.dat")
	if err := os.WriteFile(keysPath, []byte("PUBVALUE\nPRIVVALUE\n"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// A nil conn/reader would panic if the function tried to talk to the
	// router, which proves the persisted path never does.
	dest, err := loadOrCreateSAMDestination(nil, nil, keysPath, 7)
	if err != nil {
		t.Fatalf("loadOrCreateSAMDestination: %v", err)
	}
	if dest.pub != "PUBVALUE" || dest.priv != "PRIVVALUE" {
		t.Errorf("dest = %+v, want pub=PUBVALUE priv=PRIVVALUE", dest)
	}
}

// Start must return ErrDisabled untouched, and must not create any file or
// bind any port, when server.i2p.enabled is false.
func TestStartDisabled(t *testing.T) {
	dir := t.TempDir()
	dirs := Dirs{Config: filepath.Join(dir, "config"), Data: filepath.Join(dir, "data"), Log: filepath.Join(dir, "log")}

	svc, err := Start(context.Background(), config.I2P{Enabled: false}, dirs)
	if err != ErrDisabled {
		t.Fatalf("err = %v, want ErrDisabled", err)
	}
	if svc != nil {
		t.Error("expected a nil service")
	}
	if _, statErr := os.Stat(dirs.ConfigPath()); !os.IsNotExist(statErr) {
		t.Error("Start must not create directories while disabled")
	}
}

// Start must fail with no provider available rather than partially
// allocating resources, when enabled but nothing is reachable.
func TestStartNoProvider(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH manipulation differs on windows")
	}
	dir := t.TempDir()
	dirs := Dirs{Config: filepath.Join(dir, "config"), Data: filepath.Join(dir, "data"), Log: filepath.Join(dir, "log")}

	empty := t.TempDir()
	t.Setenv("PATH", empty)

	svc, err := Start(context.Background(), config.I2P{Enabled: true, SAMAddress: "127.0.0.1:1"}, dirs)
	if err == nil {
		t.Fatal("expected an error when no provider is available")
	}
	if svc != nil {
		t.Error("expected a nil service")
	}
}

// Service methods must be nil-receiver safe, matching Manager's contract
// that a never-started eepsite behaves exactly like one that is off.
func TestServiceNilReceiver(t *testing.T) {
	var s *Service
	if s.EepsiteAddress() != "" {
		t.Error("EepsiteAddress on nil should be empty")
	}
	if s.Provider() != ProviderNone {
		t.Error("Provider on nil should be ProviderNone")
	}
	if s.VirtualPort() != 0 {
		t.Error("VirtualPort on nil should be 0")
	}
	if s.BackendPort() != 0 {
		t.Error("BackendPort on nil should be 0")
	}
	if s.BackendListener() != nil {
		t.Error("BackendListener on nil should be nil")
	}
	if s.Healthy() {
		t.Error("Healthy on nil should be false")
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close on nil should be nil, got %v", err)
	}
}

// Healthy must reflect the i2pd child's actual liveness through the exited
// channel, and must return false once the process has terminated.
func TestServiceHealthyI2PD(t *testing.T) {
	svc := &Service{provider: ProviderI2PD, exited: make(chan struct{})}
	// No i2pd process assigned yet: Healthy must not panic and must report
	// false, since there is nothing to be healthy about.
	if svc.Healthy() {
		t.Error("expected Healthy=false with no process assigned")
	}

	svc2 := &Service{provider: ProviderI2PD}
	if svc2.Healthy() {
		t.Error("expected Healthy=false with a nil exited channel")
	}
}
