// The `i2p` subcommand: AI.md PART 31.2 states the eepsite is "configured
// via server.yml and CLI only. No REST API for I2P configuration". The
// eepsite runs inside the server process, so — like the tor subcommand —
// these commands read on-disk state and the destructive one requires the
// server to be stopped.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/i2p"
	"github.com/apimgr/shortner/src/paths"
)

// i2pHelp is the `i2p --help` text.
const i2pHelp = `I2P eepsite management:

status                                - Show eepsite status and address
validate                              - Validate the I2P configuration
regenerate                            - Generate a brand new .b32.i2p address
                                        THE OLD ADDRESS IS LOST FOREVER

I2P is opt-in: set server.i2p.enabled to true in server.yml. The eepsite
runs inside the server process, so regenerate requires the server stopped.

Examples:
  %[1]s i2p status
  %[1]s i2p validate
`

// runI2P dispatches `i2p [COMMAND]` and returns the process exit code:
// 0 on success, 1 on failure, 2 on a usage error.
func runI2P(binaryName string, p paths.Paths, args []string) int {
	command := ""
	if len(args) > 0 {
		command = args[0]
	}
	switch command {
	case "", "help", "--help", "-h":
		fmt.Printf(i2pHelp, binaryName)
		return 0
	case "status":
		return runI2PStatus(p)
	case "validate":
		return runI2PValidate(p)
	case "regenerate":
		return runI2PRegenerate(binaryName, p)
	default:
		fmt.Fprintf(os.Stderr, "%s: unknown i2p command %q (run '%s i2p --help')\n", binaryName, command, binaryName)
		return 2
	}
}

// i2pDirs builds the src/i2p directory set from the resolved paths.
func i2pDirs(p paths.Paths) i2p.Dirs {
	return i2p.Dirs{Config: p.Config, Data: p.Data, Log: p.Logs}
}

// i2pCLIConfig loads server.yml and returns the I2P section, falling back
// to the (disabled) defaults when there is no readable config yet.
func i2pCLIConfig(p paths.Paths) config.I2P {
	cfg, err := config.Load(p.ConfigFile, filepath.Join(p.DB, "server.db"))
	if err != nil {
		return config.DefaultI2P()
	}
	return cfg.Server.I2P
}

// i2pStatusLine renders the AI.md PART 31.2 status value: Running
// (provider) / Disabled / No Provider / Error.
func i2pStatusLine(cfg config.I2P, address string, running bool) string {
	if !cfg.Enabled {
		return "I2P Eepsite: Disabled"
	}
	provider, _ := i2p.ResolveProvider(cfg.Normalized())
	if provider == i2p.ProviderNone {
		return "I2P Eepsite: No Provider"
	}
	if !running {
		return "I2P Eepsite: Stopped"
	}
	if address == "" {
		return "I2P Eepsite: Error"
	}
	return fmt.Sprintf("I2P Eepsite: Running (%s)", provider.Name())
}

// runI2PStatus prints the eepsite state from on-disk data.
func runI2PStatus(p paths.Paths) int {
	cfg := i2pCLIConfig(p)
	address := i2p.ReadEepsiteAddress(p.Data)
	fmt.Println(i2pStatusLine(cfg, address, serverRunning(p)))
	if address != "" {
		fmt.Printf("  Address: %s\n", address)
		fmt.Printf("  Virtual Port: %d\n", cfg.Normalized().VirtualPort)
	}
	if cfg.Enabled {
		fmt.Printf("  Destination Key: %s\n", i2pDirs(p).KeysPath())
	}
	return 0
}

// runI2PValidate checks the configuration the server would start the
// eepsite with: opt-in state, a reachable provider, settings in range, and
// creatable directories.
func runI2PValidate(p paths.Paths) int {
	raw := i2pCLIConfig(p)
	if !raw.Enabled {
		fmt.Println("! server.i2p.enabled is false — the eepsite is off and nothing else is checked")
		return 0
	}
	cfg := raw.Normalized()
	failed := false

	switch provider, binary := i2p.ResolveProvider(cfg); provider {
	case i2p.ProviderI2PD:
		fmt.Printf("✓ provider: i2pd (%s)\n", binary)
	case i2p.ProviderSAM:
		fmt.Printf("✓ provider: SAM bridge at %s\n", cfg.SAMAddress)
	default:
		fmt.Printf("✗ provider: no i2pd binary and SAM %s is unreachable\n", cfg.SAMAddress)
		failed = true
	}

	warnings := config.ValidateI2P(raw)
	if len(warnings) == 0 {
		fmt.Println("✓ server.i2p settings are all in range")
	}
	for _, w := range warnings {
		fmt.Printf("! %s\n", w)
	}

	if err := i2p.EnsureDirs(i2pDirs(p)); err != nil {
		fmt.Printf("✗ directories: %v\n", err)
		failed = true
	} else {
		fmt.Printf("✓ directories: %s, %s\n", i2pDirs(p).ConfigPath(), i2pDirs(p).DataPath())
	}

	if failed {
		return 1
	}
	return 0
}

// runI2PRegenerate destroys the persisted destination so the next server
// start mints a new one. It confirms first, because the old address cannot
// be recovered.
func runI2PRegenerate(binaryName string, p paths.Paths) int {
	if !requireStopped(binaryName, p, "regenerate the .b32.i2p address") {
		return 1
	}
	current := i2p.ReadEepsiteAddress(p.Data)
	if current == "" {
		fmt.Println("No .b32.i2p address exists yet; one is generated on the next start.")
		return 0
	}
	fmt.Printf("Current address: %s\n", current)
	if !confirmOverlay("This permanently destroys the current .b32.i2p address. Continue? [y/N] ") {
		fmt.Println("Cancelled.")
		return 1
	}
	dirs := i2pDirs(p)
	for _, path := range []string{dirs.KeysPath(), dirs.HostnamePath()} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "%s: %v\n", binaryName, err)
			return 1
		}
	}
	fmt.Println("Destination key deleted. A new address is generated on the next start.")
	return 0
}
