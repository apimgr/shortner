// --update command handling. See AI.md PART 22 "UPDATE COMMAND" for the
// subcommands, channel semantics, and exit codes, and AI.md PART 8
// "Server Binary Commands" for where this sits in the startup sequence.
// The self-update machinery itself lives in src/updater.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/apimgr/shortner/src/common/pidfile"
	"github.com/apimgr/shortner/src/common/version"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/paths"
	"github.com/apimgr/shortner/src/updater"
)

// updateCheckTimeout bounds a manual update check/download so a hung
// mirror cannot leave an operator's terminal blocked indefinitely.
const updateCheckTimeout = 15 * time.Minute

// updateHelp is the --update --help text, matching AI.md PART 22 "Update
// Help Output". The trailing "Current:" block is filled in by
// printUpdateHelp.
const updateHelp = `Update management:

check                                 - Check for available updates
                                        Compares current version with latest release

yes                                   - Download and install update
                                        Downloads latest release, replaces binary, restarts

branch <name>                         - Switch update branch
  stable                              - Stable releases (default)
  beta                                - Beta/preview releases
  daily                               - Daily builds (development)

Examples:
  %[1]s --update check
  %[1]s --update yes
  %[1]s --update branch beta

Current:
`

// The updater entry points are indirected through package variables so
// tests can drive the check/install flows without a network call or a real
// binary replacement.
var (
	checkUpdate   = updater.CheckForUpdate
	installUpdate = updater.DoUpdate
	restartServer = updater.RestartService
)

// runUpdate dispatches --update [COMMAND] [ARG] and returns the process
// exit code: 0 for a successful update or "no update available", 1 for an
// error (AI.md PART 22 "Exit Codes").
func runUpdate(binaryName string, p paths.Paths, command, arg string) int {
	switch command {
	case "help", "--help", "-h":
		printUpdateHelp(binaryName, p)
		return 0
	case "check":
		return runUpdateCheck(binaryName, p)
	case "", "yes":
		// A bare `--update` is `--update yes` (AI.md PART 22 "Commands").
		return runUpdateInstall(binaryName, p)
	case "branch":
		return runUpdateBranch(binaryName, p, arg)
	default:
		fmt.Fprintf(os.Stderr, "%s: unknown --update command %q (run '%s --update --help')\n", binaryName, command, binaryName)
		return 1
	}
}

// loadUpdateConfig loads server.yml for the update subcommands. The
// channel lives in the config file, which AI.md PART 22 makes the single
// source of truth for it.
func loadUpdateConfig(p paths.Paths) (*config.Config, error) {
	return config.Load(p.ConfigFile, filepath.Join(p.DB, "server.db"))
}

// printUpdateHelp prints the update help and its "Current:" block. The
// "Latest" line is filled from the cached update state written by
// `--update check` and the update_check task — help output never makes a
// network call of its own, so it still works offline.
func printUpdateHelp(binaryName string, p paths.Paths) {
	fmt.Printf(updateHelp, binaryName)

	branch := updater.BranchStable
	if cfg, err := loadUpdateConfig(p); err == nil && cfg.Server.Update.Branch != "" {
		branch = cfg.Server.Update.Branch
	}
	fmt.Printf("  Version:  %s\n", version.String())
	fmt.Printf("  Branch:   %s\n", branch)

	state := updater.LoadState(updater.StatePath(p.Data))
	if state.AvailableVersion != "" && state.AvailableVersion != version.String() {
		fmt.Printf("  Latest:   %s\n", state.AvailableVersion)
	}
}

// checkForUpdate runs the channel check an operator asked for. The defer
// window is deliberately not applied: AI.md PART 22 "Defer Semantics"
// gates the scheduled task only — an explicit operator action always sees
// the true latest release.
func checkForUpdate(p paths.Paths) (*updater.Release, string, error) {
	cfg, err := loadUpdateConfig(p)
	if err != nil {
		return nil, "", err
	}
	branch := cfg.Server.Update.Branch
	if !updater.ValidBranch(branch) {
		branch = updater.BranchStable
	}

	ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
	defer cancel()

	release, err := checkUpdate(ctx, version.String(), branch, version.Epoch())
	if err != nil {
		return nil, branch, err
	}
	return release, branch, nil
}

// recordUpdateState caches what the check found so `--status` and
// `--update --help` can report it without repeating the network call.
func recordUpdateState(p paths.Paths, branch string, release *updater.Release) {
	state := updater.LoadState(updater.StatePath(p.Data))
	state.Branch = branch
	state.CheckedAt = time.Now().UTC()
	if release == nil {
		state.AvailableVersion = ""
	} else {
		state.AvailableVersion = release.Version()
		state.NotifiedKey = release.Key()
	}
	_ = updater.SaveState(updater.StatePath(p.Data), state)
}

// runUpdateCheck implements `--update check`: report whether a newer
// release exists, without installing anything and without needing
// privileges.
func runUpdateCheck(binaryName string, p paths.Paths) int {
	release, branch, err := checkForUpdate(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	recordUpdateState(p, branch, release)

	fmt.Printf("Current version: %s\n", version.String())
	fmt.Printf("Update channel:  %s\n", branch)
	if release == nil {
		fmt.Println("No updates available (already current).")
		return 0
	}
	fmt.Printf("Update available: %s\n", release.Version())
	fmt.Printf("Run '%s --update yes' to install it.\n", binaryName)
	return 0
}

// runUpdateInstall implements `--update yes`: check, download, verify,
// replace the binary, and restart the running service.
func runUpdateInstall(binaryName string, p paths.Paths) int {
	release, branch, err := checkForUpdate(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	recordUpdateState(p, branch, release)

	if release == nil {
		fmt.Printf("%s %s is already current (channel %s).\n", binaryName, version.String(), branch)
		return 0
	}

	fmt.Printf("Downloading %s (%s)...\n", release.Version(), updater.BinaryName())
	ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
	defer cancel()
	if err := installUpdate(ctx, release); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	fmt.Printf("Verified checksum and installed %s.\n", release.Version())

	// The new binary is only live once whatever is running the old one is
	// restarted. This process is the CLI, not the server, so re-executing
	// here would just re-run `--update`.
	running, _, err := pidfile.CheckPIDFile(p.PIDFile)
	if err != nil || !running {
		fmt.Println("Start the server to run the new version.")
		return 0
	}
	fmt.Println("Restarting the running service...")
	if err := restartServer(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: binary updated but the service restart failed: %v\n", binaryName, err)
		fmt.Fprintln(os.Stderr, binaryName+": restart the service manually to run the new version")
		return 1
	}
	fmt.Println("Service restarted.")
	return 0
}

// runUpdateBranch implements `--update branch {stable|beta|daily}`,
// persisting the channel to server.yml — per AI.md PART 22 the config file
// is the single source of truth and there is no CLI-side state.
func runUpdateBranch(binaryName string, p paths.Paths, name string) int {
	if !updater.ValidBranch(name) {
		fmt.Fprintf(os.Stderr, "%s: --update branch requires stable, beta, or daily\n", binaryName)
		return 1
	}

	cfg, err := loadUpdateConfig(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	if cfg.Server.Update.Branch == name {
		fmt.Printf("Update branch is already %s.\n", name)
		return 0
	}

	cfg.Server.Update.Branch = name
	if err := config.Save(p.ConfigFile, cfg); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	fmt.Printf("Update branch set to %s (%s).\n", name, p.ConfigFile)
	return 0
}
