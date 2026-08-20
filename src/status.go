// --status command handling. See AI.md PART 8 "--status Exit Codes".
// The check is deliberately local and dependency-free — the PID file plus
// what the running server has already persisted on disk — so it stays
// usable as the container HEALTHCHECK (AI.md PART 26) even when the HTTP
// listener is saturated or bound somewhere this process cannot reach.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/apimgr/shortner/src/common/color"
	"github.com/apimgr/shortner/src/common/pidfile"
	"github.com/apimgr/shortner/src/common/version"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/i2p"
	"github.com/apimgr/shortner/src/paths"
	"github.com/apimgr/shortner/src/tor"
	"github.com/apimgr/shortner/src/updater"
)

// runStatus checks the PID file and prints/returns the server's running
// state, plus any pending update the last check recorded. Exit 0 when
// healthy (running), 1 when not.
func runStatus(binaryName string, p paths.Paths) int {
	running, pid, err := pidfile.CheckPIDFile(p.PIDFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}

	okIcon, failIcon := "", ""
	if color.EmojiEnabled() {
		okIcon, failIcon = "✅ ", "❌ "
	}

	code := 1
	if running {
		fmt.Println(okIcon + cliTF("cli.running_pid", map[string]string{
			"project_name": binaryName,
			"pid":          strconv.Itoa(pid),
		}))
		code = 0
	} else {
		fmt.Println(failIcon + cliTF("cli.not_running", map[string]string{
			"project_name": binaryName,
		}))
	}

	printOverlayStatus(p)
	printPendingUpdate(p)
	return code
}

// printOverlayStatus prints the AI.md PART 31 "CLI" status block for both
// overlay networks. --status runs in its own short-lived process, so it
// cannot ask the live managers anything — it reads the addresses the
// providers persisted under {data_dir}, which exist only once a provider
// actually published an address.
func printOverlayStatus(p paths.Paths) {
	if onion := tor.ReadOnionAddress(p.Data); onion != "" {
		fmt.Println()
		fmt.Println("Tor Hidden Service: Connected")
		fmt.Println("  Address: " + onion)
	}

	// AI.md PART 31.2 "CLI" enumerates the I2P Eepsite values as
	// "Running (provider) / Disabled / No Provider / Error", so the line is
	// always printed once the config is readable. An unreadable config is
	// the one case that prints nothing — guessing "Disabled" there would
	// report a state this process cannot actually observe.
	cfg, err := config.Load(p.ConfigFile, filepath.Join(p.DB, "server.db"))
	if err != nil {
		return
	}
	fmt.Println()
	if !cfg.Server.I2P.Enabled {
		fmt.Println("I2P Eepsite: Disabled")
		return
	}
	address := i2p.ReadEepsiteAddress(p.Data)
	if address == "" {
		fmt.Println("I2P Eepsite: No Provider")
		return
	}
	provider, _ := i2p.ResolveProvider(cfg.Server.I2P)
	fmt.Println("I2P Eepsite: Running (" + provider.Name() + ")")
	fmt.Println("  Address: " + address)
}

// printPendingUpdate surfaces the "update available" notice in --status
// output, per AI.md PART 22 "Surfacing rules". It reads only the cached
// state written by `--update check` and the update_check task — a status
// check never makes a network call — and stays silent when nothing is
// pending, since update status is operator-only information.
func printPendingUpdate(p paths.Paths) {
	state := updater.LoadState(updater.StatePath(p.Data))
	if state.AvailableVersion == "" || state.AvailableVersion == version.String() {
		return
	}
	fmt.Println(cliTF("cli.update_available", map[string]string{
		"current": version.String(),
		"latest":  state.AvailableVersion,
		"channel": state.Branch,
		"checked": state.CheckedAt.Format("2006-01-02T15:04:05Z"),
	}))
}
