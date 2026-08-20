package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/paths"
	"github.com/apimgr/shortner/src/tor"
)

// torTestPaths builds a Paths whose every directory lives under a fresh
// t.TempDir(), so tor_cli.go never touches anything outside the test's own
// sandbox.
func torTestPaths(t *testing.T) paths.Paths {
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

// writeTorConfig persists a server.yml whose server.tor block is cfg, so
// torCLIConfig (which always loads from disk) sees it.
func writeTorConfig(t *testing.T, p paths.Paths, cfg config.Tor) {
	t.Helper()
	full := config.Default(filepath.Join(p.DB, "server.db"))
	full.Server.Tor = cfg
	if err := config.Save(p.ConfigFile, full); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
}

// dummyFile creates an arbitrary existing regular file and returns its
// path. tor.ResolveBinary only calls os.Stat on an explicit cfg.Binary, so
// this is enough to make it resolve deterministically without a real tor
// binary anywhere on the test host.
func dummyFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-tor")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	return path
}

// validSecretKeyFile returns a well-formed hs_ed25519_secret_key payload
// tor.ValidateSecretKeyFile accepts.
func validSecretKeyFile(t *testing.T) []byte {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return tor.SecretKeyFile(priv)
}

func TestRunTorHelp(t *testing.T) {
	for _, args := range [][]string{nil, {"help"}, {"--help"}, {"-h"}} {
		out, _, code := captureOutput(t, func() int { return runTor("shortner", torTestPaths(t), args) })
		if code != 0 {
			t.Errorf("runTor(%v) code = %d, want 0", args, code)
		}
		if !strings.Contains(out, "Tor hidden service management:") {
			t.Errorf("runTor(%v) stdout = %q, want the tor help text", args, out)
		}
	}
}

func TestRunTorUnknownCommand(t *testing.T) {
	_, stderr, code := captureOutput(t, func() int { return runTor("shortner", torTestPaths(t), []string{"bogus"}) })
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr, `unknown tor command "bogus"`) {
		t.Errorf("stderr = %q, want unknown-command message", stderr)
	}
}

func TestRunTorImportKeysMissingArg(t *testing.T) {
	_, stderr, code := captureOutput(t, func() int { return runTor("shortner", torTestPaths(t), []string{"import-keys"}) })
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "requires a path") {
		t.Errorf("stderr = %q, want the missing-path message", stderr)
	}
}

// TestRunTorStatusHostnamePresence proves `tor status` differs in exactly
// the address-related lines depending on whether a hidden service has ever
// been established on disk.
func TestRunTorStatusHostnamePresence(t *testing.T) {
	t.Run("no hostname", func(t *testing.T) {
		p := torTestPaths(t)
		out, _, code := captureOutput(t, func() int { return runTorStatus(p) })
		if code != 0 {
			t.Errorf("code = %d, want 0", code)
		}
		if strings.Contains(out, "Address:") {
			t.Errorf("stdout = %q, want no Address line with nothing persisted", out)
		}
	})

	t.Run("persisted hostname", func(t *testing.T) {
		p := torTestPaths(t)
		site := tor.Dirs{Config: p.Config, Data: p.Data, Log: p.Logs}.SitePath()
		if err := os.MkdirAll(site, 0o700); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := os.WriteFile(filepath.Join(site, "hostname"), []byte("abcdefghijklmnop.onion\n"), 0o600); err != nil {
			t.Fatalf("setup: %v", err)
		}

		out, _, code := captureOutput(t, func() int { return runTorStatus(p) })
		if code != 0 {
			t.Errorf("code = %d, want 0", code)
		}
		if !strings.Contains(out, "Address: abcdefghijklmnop.onion") {
			t.Errorf("stdout = %q, want the persisted address", out)
		}
	})
}

// TestRunTorValidateBinaryResolution exercises both the success and
// failure branch of the binary check deterministically, regardless of
// whether the test host happens to have a real tor binary installed.
func TestRunTorValidateBinaryResolution(t *testing.T) {
	t.Run("binary resolves", func(t *testing.T) {
		p := torTestPaths(t)
		cfg := config.DefaultTor()
		cfg.Binary = dummyFile(t)
		writeTorConfig(t, p, cfg)

		out, _, code := captureOutput(t, func() int { return runTorValidate(p) })
		if code != 0 {
			t.Errorf("code = %d, want 0; stdout = %q", code, out)
		}
		if !strings.Contains(out, "✓ tor binary:") {
			t.Errorf("stdout = %q, want a binary success line", out)
		}
	})

	t.Run("binary missing", func(t *testing.T) {
		p := torTestPaths(t)
		cfg := config.DefaultTor()
		cfg.Binary = filepath.Join(p.Config, "no-such-tor-binary")
		writeTorConfig(t, p, cfg)

		out, _, code := captureOutput(t, func() int { return runTorValidate(p) })
		if code != 1 {
			t.Errorf("code = %d, want 1; stdout = %q", code, out)
		}
		if !strings.Contains(out, "✗ tor binary:") {
			t.Errorf("stdout = %q, want a binary failure line", out)
		}
	})
}

// TestRunTorRestartNotRunning exercises the only serverRunning() branch
// this environment can reach deterministically (no PID file at all); the
// "server is running" refusal needs a live process holding the PID file
// under this binary's own name, which the test binary can never be — see
// the final report for this documented gap.
func TestRunTorRestartNotRunning(t *testing.T) {
	p := torTestPaths(t)
	out, _, code := captureOutput(t, func() int { return runTorRestart("shortner", p) })
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !strings.Contains(out, "Tor Hidden Service: Stopped") {
		t.Errorf("stdout = %q, want the stopped message", out)
	}
	if !strings.Contains(out, "shortner --service start") {
		t.Errorf("stdout = %q, want the restart hint", out)
	}
}

func TestRunTorRegenerateNoAddress(t *testing.T) {
	p := torTestPaths(t)
	out, _, code := captureOutput(t, func() int { return runTorRegenerate("shortner", p) })
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !strings.Contains(out, "No .onion address exists yet") {
		t.Errorf("stdout = %q, want the no-address message", out)
	}
}

// seedOnionAddress writes a hidden service hostname file so regenerate/
// import-keys have an existing identity to reason about.
func seedOnionAddress(t *testing.T, p paths.Paths, address string) {
	t.Helper()
	site := tor.Dirs{Config: p.Config, Data: p.Data, Log: p.Logs}.SitePath()
	if err := os.MkdirAll(site, 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(site, "hostname"), []byte(address+"\n"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
}

// withPromptAnswer overrides the package-level promptLine for the duration
// of the test, mirroring the pattern in service_test.go.
func withPromptAnswer(t *testing.T, answer string) {
	t.Helper()
	original := promptLine
	t.Cleanup(func() { promptLine = original })
	promptLine = func(string) (string, error) { return answer, nil }
}

func TestRunTorRegenerateCancelled(t *testing.T) {
	p := torTestPaths(t)
	seedOnionAddress(t, p, "existingaddress1234.onion")
	withPromptAnswer(t, "n")

	out, _, code := captureOutput(t, func() int { return runTorRegenerate("shortner", p) })
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(out, "Cancelled.") {
		t.Errorf("stdout = %q, want Cancelled.", out)
	}
	if _, err := os.Stat(tor.Dirs{Config: p.Config, Data: p.Data, Log: p.Logs}.SitePath()); err != nil {
		t.Errorf("hidden service dir was removed despite cancellation: %v", err)
	}
}

func TestRunTorRegenerateConfirmed(t *testing.T) {
	p := torTestPaths(t)
	seedOnionAddress(t, p, "existingaddress1234.onion")
	withPromptAnswer(t, "y")

	out, _, code := captureOutput(t, func() int { return runTorRegenerate("shortner", p) })
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !strings.Contains(out, "deleted") {
		t.Errorf("stdout = %q, want the deletion confirmation", out)
	}
	if _, err := os.Stat(tor.Dirs{Config: p.Config, Data: p.Data, Log: p.Logs}.SitePath()); !os.IsNotExist(err) {
		t.Errorf("hidden service dir still exists after confirmed regeneration: %v", err)
	}
}

func TestRunTorVanityDispatch(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    int
		wantErr string
	}{
		{"no subcommand", nil, 2, "unknown vanity command"},
		{"unknown subcommand", []string{"bogus"}, 2, "unknown vanity command"},
		{"start missing prefix", []string{"start"}, 2, "requires a prefix"},
		{"apply missing address", []string{"apply"}, 2, "requires an address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, code := captureOutput(t, func() int { return runTorVanity("shortner", torTestPaths(t), tt.args) })
			if code != tt.want {
				t.Errorf("code = %d, want %d", code, tt.want)
			}
			if !strings.Contains(stderr, tt.wantErr) {
				t.Errorf("stderr = %q, want to contain %q", stderr, tt.wantErr)
			}
		})
	}
}

// TestRunTorVanityStartInvalidPrefix proves an unsearchable prefix (base32
// excludes 0/1/8/9) is rejected before any search goroutine is spawned.
func TestRunTorVanityStartInvalidPrefix(t *testing.T) {
	_, stderr, code := captureOutput(t, func() int { return runTorVanityStart("shortner", torTestPaths(t), "019") })
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "cannot appear in an onion address") {
		t.Errorf("stderr = %q, want the invalid-character message", stderr)
	}
}

// TestRunTorVanityStartAndApply runs a real 1-character search (fast: a
// 1-in-32 hit rate) end to end, then applies the result as the live
// identity.
func TestRunTorVanityStartAndApply(t *testing.T) {
	p := torTestPaths(t)

	out, _, code := captureOutput(t, func() int { return runTorVanityStart("shortner", p, "a") })
	if code != 0 {
		t.Fatalf("vanity start code = %d, want 0; stdout = %q", code, out)
	}
	if !strings.Contains(out, "Found") || !strings.Contains(out, "Saved to") {
		t.Errorf("stdout = %q, want the found/saved messages", out)
	}

	var address string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Found ") {
			fields := strings.Fields(line)
			address = fields[1]
		}
	}
	if address == "" {
		t.Fatalf("could not parse the found address out of %q", out)
	}
	if !strings.HasPrefix(strings.ToLower(address), "a") {
		t.Errorf("address = %q, want to start with the requested prefix", address)
	}
	staged := filepath.Join(tor.VanityDir(p.Data, address), "hs_ed25519_secret_key")
	if _, err := os.Stat(staged); err != nil {
		t.Errorf("expected staged key at %s: %v", staged, err)
	}

	out, _, code = captureOutput(t, func() int { return runTorVanityApply("shortner", p, address) })
	if code != 0 {
		t.Fatalf("vanity apply code = %d, want 0; stdout = %q", code, out)
	}
	if !strings.Contains(out, "Applied "+address) {
		t.Errorf("stdout = %q, want the applied confirmation", out)
	}
	installed := filepath.Join(tor.Dirs{Config: p.Config, Data: p.Data, Log: p.Logs}.SitePath(), "hs_ed25519_secret_key")
	if _, err := os.Stat(installed); err != nil {
		t.Errorf("expected the applied key at %s: %v", installed, err)
	}
}

func TestRunTorVanityApplyNonexistent(t *testing.T) {
	_, stderr, code := captureOutput(t, func() int { return runTorVanityApply("shortner", torTestPaths(t), "doesnotexist") })
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "no staged vanity address") {
		t.Errorf("stderr = %q, want the honest no-staged-address message", stderr)
	}
}

func TestRunTorImportKeysMissingPath(t *testing.T) {
	p := torTestPaths(t)
	_, stderr, code := captureOutput(t, func() int {
		return runTorImportKeys("shortner", p, filepath.Join(p.Data, "no-such-file"))
	})
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if stderr == "" {
		t.Error("stderr = empty, want the propagated read error")
	}
}

func TestRunTorImportKeysGarbage(t *testing.T) {
	p := torTestPaths(t)
	garbage := filepath.Join(t.TempDir(), "garbage-key")
	if err := os.WriteFile(garbage, []byte("not a key at all"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, stderr, code := captureOutput(t, func() int { return runTorImportKeys("shortner", p, garbage) })
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "is not a valid hs_ed25519_secret_key") {
		t.Errorf("stderr = %q, want the invalid-key message", stderr)
	}
}

func TestRunTorImportKeysValid(t *testing.T) {
	p := torTestPaths(t)
	keyPath := filepath.Join(t.TempDir(), "hs_ed25519_secret_key")
	secret := validSecretKeyFile(t)
	if err := os.WriteFile(keyPath, secret, 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	out, _, code := captureOutput(t, func() int { return runTorImportKeys("shortner", p, keyPath) })
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout = %q", code, out)
	}
	if !strings.Contains(out, "Keys imported.") {
		t.Errorf("stdout = %q, want the import confirmation", out)
	}

	installed, err := os.ReadFile(filepath.Join(tor.Dirs{Config: p.Config, Data: p.Data, Log: p.Logs}.SitePath(), "hs_ed25519_secret_key"))
	if err != nil {
		t.Fatalf("expected the installed key to exist: %v", err)
	}
	if string(installed) != string(secret) {
		t.Error("installed key content does not match the imported file")
	}
}

func TestRunTorImportKeysOverExistingAddress(t *testing.T) {
	newKeyPath := func(t *testing.T) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "hs_ed25519_secret_key")
		if err := os.WriteFile(path, validSecretKeyFile(t), 0o600); err != nil {
			t.Fatalf("setup: %v", err)
		}
		return path
	}

	t.Run("cancelled", func(t *testing.T) {
		p := torTestPaths(t)
		seedOnionAddress(t, p, "existingaddress1234.onion")
		withPromptAnswer(t, "no")

		out, _, code := captureOutput(t, func() int { return runTorImportKeys("shortner", p, newKeyPath(t)) })
		if code != 1 {
			t.Errorf("code = %d, want 1", code)
		}
		if !strings.Contains(out, "Cancelled.") {
			t.Errorf("stdout = %q, want Cancelled.", out)
		}
		address := tor.ReadOnionAddress(p.Data)
		if address != "existingaddress1234.onion" {
			t.Errorf("address = %q, want the original address to survive a cancelled import", address)
		}
	})

	t.Run("confirmed", func(t *testing.T) {
		p := torTestPaths(t)
		seedOnionAddress(t, p, "existingaddress1234.onion")
		withPromptAnswer(t, "y")

		out, _, code := captureOutput(t, func() int { return runTorImportKeys("shortner", p, newKeyPath(t)) })
		if code != 0 {
			t.Fatalf("code = %d, want 0; stdout = %q", code, out)
		}
		if !strings.Contains(out, "Keys imported.") {
			t.Errorf("stdout = %q, want the import confirmation", out)
		}
		// installHiddenServiceKey clears the stale hostname so Tor never
		// starts advertising a hostname that belongs to the old key.
		if address := tor.ReadOnionAddress(p.Data); address != "" {
			t.Errorf("address = %q, want the stale hostname cleared", address)
		}
	})
}
