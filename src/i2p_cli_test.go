package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/i2p"
	"github.com/apimgr/shortner/src/paths"
)

// i2pTestPaths builds a Paths whose every directory lives under a fresh
// t.TempDir(), so i2p_cli.go never touches anything outside the test's own
// sandbox.
func i2pTestPaths(t *testing.T) paths.Paths {
	t.Helper()
	base := t.TempDir()
	return paths.Paths{
		Config:     filepath.Join(base, "config"),
		ConfigFile: filepath.Join(base, "config", "server.yml"),
		Data:       filepath.Join(base, "data"),
		Logs:       filepath.Join(base, "logs"),
		DB:         filepath.Join(base, "db"),
		PIDFile:    filepath.Join(base, "run", "shortner.pid"),
	}
}

// writeI2PConfig persists a server.yml whose server.i2p block is cfg, so
// i2pCLIConfig (which always loads from disk) sees it.
func writeI2PConfig(t *testing.T, p paths.Paths, cfg config.I2P) {
	t.Helper()
	full := config.Default(filepath.Join(p.DB, "server.db"))
	full.Server.I2P = cfg
	if err := config.Save(p.ConfigFile, full); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
}

// i2pDummyFile creates an arbitrary existing regular file and returns its
// path. i2p.ResolveBinary only calls os.Stat on an explicit cfg.Binary, so
// this is enough to force a deterministic ProviderI2PD resolution without
// a real i2pd binary anywhere on the test host.
func i2pDummyFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-i2pd")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	return path
}

func TestRunI2PHelp(t *testing.T) {
	for _, args := range [][]string{nil, {"help"}, {"--help"}, {"-h"}} {
		out, _, code := captureOutput(t, func() int { return runI2P("shortner", i2pTestPaths(t), args) })
		if code != 0 {
			t.Errorf("runI2P(%v) code = %d, want 0", args, code)
		}
		if !strings.Contains(out, "I2P eepsite management:") {
			t.Errorf("runI2P(%v) stdout = %q, want the i2p help text", args, out)
		}
	}
}

func TestRunI2PUnknownCommand(t *testing.T) {
	_, stderr, code := captureOutput(t, func() int { return runI2P("shortner", i2pTestPaths(t), []string{"bogus"}) })
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr, `unknown i2p command "bogus"`) {
		t.Errorf("stderr = %q, want unknown-command message", stderr)
	}
}

// TestRunI2PStatusHostnamePresence proves `i2p status` differs in exactly
// the address-related lines depending on whether an eepsite address has
// ever been established on disk.
func TestRunI2PStatusHostnamePresence(t *testing.T) {
	t.Run("no hostname", func(t *testing.T) {
		p := i2pTestPaths(t)
		out, _, code := captureOutput(t, func() int { return runI2PStatus(p) })
		if code != 0 {
			t.Errorf("code = %d, want 0", code)
		}
		if !strings.Contains(out, "I2P Eepsite: Disabled") {
			t.Errorf("stdout = %q, want the disabled line (default config is opt-out)", out)
		}
		if strings.Contains(out, "Address:") {
			t.Errorf("stdout = %q, want no Address line with nothing persisted", out)
		}
	})

	t.Run("persisted hostname", func(t *testing.T) {
		p := i2pTestPaths(t)
		site := i2p.Dirs{Config: p.Config, Data: p.Data, Log: p.Logs}.SitePath()
		if err := os.MkdirAll(site, 0o700); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := os.WriteFile(filepath.Join(site, "hostname"), []byte("abcdefghijklmnop.b32.i2p\n"), 0o600); err != nil {
			t.Fatalf("setup: %v", err)
		}

		out, _, code := captureOutput(t, func() int { return runI2PStatus(p) })
		if code != 0 {
			t.Errorf("code = %d, want 0", code)
		}
		if !strings.Contains(out, "Address: abcdefghijklmnop.b32.i2p") {
			t.Errorf("stdout = %q, want the persisted address", out)
		}
	})
}

func TestRunI2PValidateDisabled(t *testing.T) {
	p := i2pTestPaths(t)
	out, _, code := captureOutput(t, func() int { return runI2PValidate(p) })
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !strings.Contains(out, "server.i2p.enabled is false") {
		t.Errorf("stdout = %q, want the opt-out message", out)
	}
}

func TestRunI2PValidateEnabledNoProvider(t *testing.T) {
	p := i2pTestPaths(t)
	cfg := config.DefaultI2P()
	cfg.Enabled = true
	cfg.Binary = filepath.Join(p.Config, "no-such-i2pd-binary")
	// A privileged, almost certainly unbound port refuses instantly rather
	// than waiting out SAMReachable's 3s dial timeout.
	cfg.SAMAddress = "127.0.0.1:1"
	writeI2PConfig(t, p, cfg)

	out, _, code := captureOutput(t, func() int { return runI2PValidate(p) })
	if code != 1 {
		t.Errorf("code = %d, want 1; stdout = %q", code, out)
	}
	if !strings.Contains(out, "✗ provider:") {
		t.Errorf("stdout = %q, want the no-provider failure line", out)
	}
}

func TestRunI2PValidateEnabledWithProvider(t *testing.T) {
	p := i2pTestPaths(t)
	cfg := config.DefaultI2P()
	cfg.Enabled = true
	cfg.Binary = i2pDummyFile(t)
	writeI2PConfig(t, p, cfg)

	out, _, code := captureOutput(t, func() int { return runI2PValidate(p) })
	if code != 0 {
		t.Errorf("code = %d, want 0; stdout = %q", code, out)
	}
	if !strings.Contains(out, "✓ provider: i2pd") {
		t.Errorf("stdout = %q, want the i2pd provider success line", out)
	}
}

func TestRunI2PRegenerateNoAddress(t *testing.T) {
	p := i2pTestPaths(t)
	out, _, code := captureOutput(t, func() int { return runI2PRegenerate("shortner", p) })
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !strings.Contains(out, "No .b32.i2p address exists yet") {
		t.Errorf("stdout = %q, want the no-address message", out)
	}
}

// seedEepsiteAddress writes the eepsite hostname (and a placeholder keys
// file) so regenerate has an existing identity to destroy.
func seedEepsiteAddress(t *testing.T, p paths.Paths, address string) {
	t.Helper()
	dirs := i2p.Dirs{Config: p.Config, Data: p.Data, Log: p.Logs}
	if err := os.MkdirAll(dirs.SitePath(), 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(dirs.HostnamePath(), []byte(address+"\n"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(dirs.KeysPath(), []byte("placeholder-destination-key"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
}

func TestRunI2PRegenerateCancelled(t *testing.T) {
	p := i2pTestPaths(t)
	seedEepsiteAddress(t, p, "existingaddress1234.b32.i2p")
	withPromptAnswer(t, "n")

	out, _, code := captureOutput(t, func() int { return runI2PRegenerate("shortner", p) })
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(out, "Cancelled.") {
		t.Errorf("stdout = %q, want Cancelled.", out)
	}
	if address := i2p.ReadEepsiteAddress(p.Data); address != "existingaddress1234.b32.i2p" {
		t.Errorf("address = %q, want the original address to survive a cancelled regeneration", address)
	}
}

func TestRunI2PRegenerateConfirmed(t *testing.T) {
	p := i2pTestPaths(t)
	seedEepsiteAddress(t, p, "existingaddress1234.b32.i2p")
	withPromptAnswer(t, "y")

	out, _, code := captureOutput(t, func() int { return runI2PRegenerate("shortner", p) })
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !strings.Contains(out, "Destination key deleted.") {
		t.Errorf("stdout = %q, want the deletion confirmation", out)
	}
	if address := i2p.ReadEepsiteAddress(p.Data); address != "" {
		t.Errorf("address = %q, want the hostname file removed", address)
	}
	dirs := i2p.Dirs{Config: p.Config, Data: p.Data, Log: p.Logs}
	if _, err := os.Stat(dirs.KeysPath()); !os.IsNotExist(err) {
		t.Errorf("expected the destination key file removed, stat err = %v", err)
	}
}
