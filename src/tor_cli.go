// The `tor` subcommand: AI.md PART 31.1 "Tor CLI Commands" — status,
// validate, restart, regenerate, vanity start/apply and import-keys. The
// hidden service itself is owned by the running server (src/tor), so the
// commands that change the on-disk identity refuse to run while the server
// is up rather than fighting it for the key files.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/apimgr/shortner/src/common/pidfile"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/paths"
	"github.com/apimgr/shortner/src/tor"
)

// torHelp is the `tor --help` text, mirroring AI.md PART 31.1's command
// table.
const torHelp = `Tor hidden service management:

status                                - Show hidden service status and address
validate                              - Validate the Tor configuration
restart                               - Restart the hidden service
regenerate                            - Generate a brand new .onion address
                                        THE OLD ADDRESS IS LOST FOREVER

vanity start <prefix>                 - Search for an address starting with
                                        <prefix> (base32: a-z and 2-7 only)
vanity apply <address>                - Install a previously found address

import-keys <path>                    - Import an existing hs_ed25519_secret_key

The hidden service runs inside the server process, so regenerate, vanity
apply and import-keys require the server to be stopped.

Examples:
  %[1]s tor status
  %[1]s tor vanity start shrt
  %[1]s tor import-keys /backup/hs_ed25519_secret_key
`

// vanitySearchTimeout bounds an interactive vanity search so the command
// always terminates on its own; Ctrl-C stops it sooner.
const vanitySearchTimeout = 30 * time.Minute

// runTor dispatches `tor [COMMAND] [ARGS...]` and returns the process exit
// code: 0 on success, 1 on failure, 2 on a usage error.
func runTor(binaryName string, p paths.Paths, args []string) int {
	command := ""
	rest := args
	if len(args) > 0 {
		command = args[0]
		rest = args[1:]
	}

	switch command {
	case "", "help", "--help", "-h":
		fmt.Printf(torHelp, binaryName)
		return 0
	case "status":
		return runTorStatus(p)
	case "validate":
		return runTorValidate(p)
	case "restart":
		return runTorRestart(binaryName, p)
	case "regenerate":
		return runTorRegenerate(binaryName, p)
	case "vanity":
		return runTorVanity(binaryName, p, rest)
	case "import-keys":
		if len(rest) == 0 {
			fmt.Fprintf(os.Stderr, "%s: tor import-keys requires a path to hs_ed25519_secret_key\n", binaryName)
			return 2
		}
		return runTorImportKeys(binaryName, p, rest[0])
	default:
		fmt.Fprintf(os.Stderr, "%s: unknown tor command %q (run '%s tor --help')\n", binaryName, command, binaryName)
		return 2
	}
}

// torDirs builds the src/tor directory set from the resolved paths.
func torDirs(p paths.Paths) tor.Dirs {
	return tor.Dirs{Config: p.Config, Data: p.Data, Log: p.Logs}
}

// torCLIConfig loads server.yml and returns the Tor section. A missing or
// unreadable config falls back to the defaults so `tor status` still works
// before first run.
func torCLIConfig(p paths.Paths) config.Tor {
	cfg, err := config.Load(p.ConfigFile, filepath.Join(p.DB, "server.db"))
	if err != nil {
		return config.DefaultTor()
	}
	return cfg.Server.Tor
}

// serverRunning reports whether the server process holds the PID file.
func serverRunning(p paths.Paths) bool {
	running, _, err := pidfile.CheckPIDFile(p.PIDFile)
	return err == nil && running
}

// requireStopped enforces the stopped-server gate for the commands that
// rewrite the hidden service identity on disk.
func requireStopped(binaryName string, p paths.Paths, action string) bool {
	if !serverRunning(p) {
		return true
	}
	fmt.Fprintf(os.Stderr, "%s: cannot %s while the server is running\n", binaryName, action)
	fmt.Fprintf(os.Stderr, "Stop it first: %s --service stop\n", binaryName)
	return false
}

// confirmOverlay asks a destructive-action question and reports whether the
// operator explicitly agreed. Anything other than yes cancels.
func confirmOverlay(prompt string) bool {
	answer, err := promptLine(prompt)
	if err != nil {
		return false
	}
	return strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes")
}

// runTorStatus prints the hidden service state from on-disk data, which is
// what an out-of-process command can observe without an IPC channel.
func runTorStatus(p paths.Paths) int {
	cfg := torCLIConfig(p).Normalized()
	address := tor.ReadOnionAddress(p.Data)
	running := serverRunning(p)

	binary, binErr := tor.ResolveBinary(cfg)
	switch {
	case binErr != nil:
		fmt.Println("Tor Hidden Service: No Tor Binary")
	case address == "" && running:
		fmt.Println("Tor Hidden Service: Starting")
	case address == "":
		fmt.Println("Tor Hidden Service: Not Started")
	case running:
		fmt.Println("Tor Hidden Service: Connected")
	default:
		fmt.Println("Tor Hidden Service: Stopped")
	}
	if address != "" {
		fmt.Printf("  Address: %s\n", address)
		fmt.Printf("  Virtual Port: %d\n", cfg.VirtualPort)
	}
	if binErr == nil {
		fmt.Printf("  Binary: %s\n", binary)
	}
	fmt.Printf("  Torrc: %s\n", torDirs(p).TorrcPath())
	fmt.Printf("  Hidden Service Dir: %s\n", torDirs(p).SitePath())
	if staged := tor.ListVanity(p.Data); len(staged) > 0 {
		fmt.Printf("  Staged Vanity Addresses: %s\n", strings.Join(staged, ", "))
	}
	return 0
}

// runTorValidate checks the configuration the server would start Tor with:
// the binary resolves, the settings are in range, and the directories are
// creatable.
func runTorValidate(p paths.Paths) int {
	raw := torCLIConfig(p)
	failed := false

	binary, err := tor.ResolveBinary(raw)
	if err != nil {
		fmt.Printf("✗ tor binary: %v\n", err)
		failed = true
	} else {
		fmt.Printf("✓ tor binary: %s\n", binary)
	}

	warnings := config.ValidateTor(raw)
	if len(warnings) == 0 {
		fmt.Println("✓ server.tor settings are all in range")
	}
	for _, w := range warnings {
		fmt.Printf("! %s\n", w)
	}

	if err := tor.EnsureDirs(torDirs(p)); err != nil {
		fmt.Printf("✗ directories: %v\n", err)
		failed = true
	} else {
		fmt.Printf("✓ directories: %s, %s\n", filepath.Join(p.Config, "tor"), torDirs(p).DataPath())
	}

	if failed {
		return 1
	}
	return 0
}

// runTorRestart reports how to restart the hidden service. Tor is a child
// of the server process and the server ignores SIGHUP by design (see
// src/signal), so there is no signal an external command could send to
// recycle it — restarting the server is the real answer.
func runTorRestart(binaryName string, p paths.Paths) int {
	if serverRunning(p) {
		fmt.Fprintf(os.Stderr, "%s: the hidden service runs inside the server process\n", binaryName)
		fmt.Fprintf(os.Stderr, "Restart it with: %s --service restart\n", binaryName)
		return 1
	}
	fmt.Println("Tor Hidden Service: Stopped")
	fmt.Printf("It starts automatically with the server: %s --service start\n", binaryName)
	return 0
}

// runTorRegenerate destroys the current identity so the next server start
// mints a new one. It confirms first, because the old address cannot be
// recovered.
func runTorRegenerate(binaryName string, p paths.Paths) int {
	if !requireStopped(binaryName, p, "regenerate the .onion address") {
		return 1
	}
	current := tor.ReadOnionAddress(p.Data)
	if current == "" {
		fmt.Println("No .onion address exists yet; one is generated on the next start.")
		return 0
	}
	fmt.Printf("Current address: %s\n", current)
	if !confirmOverlay("This permanently destroys the current .onion address. Continue? [y/N] ") {
		fmt.Println("Cancelled.")
		return 1
	}
	if err := os.RemoveAll(torDirs(p).SitePath()); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", binaryName, err)
		return 1
	}
	fmt.Println("Hidden service keys deleted. A new address is generated on the next start.")
	return 0
}

// runTorVanity dispatches `tor vanity start|apply`.
func runTorVanity(binaryName string, p paths.Paths, args []string) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	arg := ""
	if len(args) > 1 {
		arg = args[1]
	}
	switch sub {
	case "start":
		if arg == "" {
			fmt.Fprintf(os.Stderr, "%s: tor vanity start requires a prefix\n", binaryName)
			return 2
		}
		return runTorVanityStart(binaryName, p, arg)
	case "apply":
		if arg == "" {
			fmt.Fprintf(os.Stderr, "%s: tor vanity apply requires an address\n", binaryName)
			return 2
		}
		return runTorVanityApply(binaryName, p, arg)
	default:
		fmt.Fprintf(os.Stderr, "%s: unknown vanity command %q (expected start or apply)\n", binaryName, sub)
		return 2
	}
}

// runTorVanityStart searches for an address with the requested prefix and
// stages the result. The search runs happily while the server is up:
// nothing is installed until `vanity apply`.
func runTorVanityStart(binaryName string, p paths.Paths, prefix string) int {
	if err := tor.ValidVanityPrefix(prefix); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", binaryName, err)
		return 2
	}
	fmt.Printf("Searching for an address starting with %q (Ctrl-C to stop)...\n", strings.ToLower(prefix))

	ctx, cancel := context.WithTimeout(context.Background(), vanitySearchTimeout)
	defer cancel()
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	started := time.Now()
	res, err := tor.SearchVanity(ctx, prefix)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", binaryName, err)
		return 1
	}
	dir, err := tor.SaveVanity(p.Data, res)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", binaryName, err)
		return 1
	}
	fmt.Printf("Found %s after %d keys in %s\n", res.Address, res.Tried, time.Since(started).Round(time.Second))
	fmt.Printf("Saved to %s\n", dir)
	fmt.Printf("Apply it with: %s tor vanity apply %s\n", binaryName, res.Address)
	return 0
}

// runTorVanityApply installs a staged vanity identity as the live hidden
// service identity.
func runTorVanityApply(binaryName string, p paths.Paths, address string) int {
	if !requireStopped(binaryName, p, "apply a vanity address") {
		return 1
	}
	address = strings.ToLower(strings.TrimSpace(address))
	if !strings.HasSuffix(address, ".onion") {
		address += ".onion"
	}
	secret, err := os.ReadFile(filepath.Join(tor.VanityDir(p.Data, address), "hs_ed25519_secret_key"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: no staged vanity address %q (run '%s tor status' to list them)\n", binaryName, address, binaryName)
		return 1
	}
	if err := installHiddenServiceKey(p, secret); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", binaryName, err)
		return 1
	}
	fmt.Printf("Applied %s. It becomes active on the next server start.\n", address)
	return 0
}

// runTorImportKeys installs an existing hs_ed25519_secret_key, validating
// it first so a malformed file never leaves Tor unable to start.
func runTorImportKeys(binaryName string, p paths.Paths, path string) int {
	if !requireStopped(binaryName, p, "import hidden service keys") {
		return 1
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", binaryName, err)
		return 1
	}
	if err := tor.ValidateSecretKeyFile(data); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %s is not a valid hs_ed25519_secret_key: %v\n", binaryName, path, err)
		return 1
	}
	if current := tor.ReadOnionAddress(p.Data); current != "" {
		fmt.Printf("Current address: %s\n", current)
		if !confirmOverlay("Importing replaces it permanently. Continue? [y/N] ") {
			fmt.Println("Cancelled.")
			return 1
		}
	}
	if err := installHiddenServiceKey(p, data); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", binaryName, err)
		return 1
	}
	fmt.Println("Keys imported. The address becomes active on the next server start.")
	return 0
}

// installHiddenServiceKey writes a secret key into the hidden service
// directory, clearing the stale public key and hostname so Tor regenerates
// them from the new key instead of loading a mismatched pair.
func installHiddenServiceKey(p paths.Paths, secret []byte) error {
	dirs := torDirs(p)
	if err := tor.EnsureDirs(dirs); err != nil {
		return err
	}
	site := dirs.SitePath()
	for _, stale := range []string{"hs_ed25519_public_key", "hostname"} {
		if err := os.Remove(filepath.Join(site, stale)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return tor.WriteHiddenServiceSecret(site, secret)
}
