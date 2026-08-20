// --status command handling. See AI.md PART 8 "--status Exit Codes".
// There is no HTTP server yet (PART 9+, tracked in TODO.AI.md), so this
// is a real, working, PID-file-only health check — deeper health (e.g.
// querying /server/healthz) arrives once the server exists.
package main

import (
	"fmt"
	"os"

	"github.com/apimgr/shortner/src/common/color"
	"github.com/apimgr/shortner/src/common/pidfile"
	"github.com/apimgr/shortner/src/common/version"
	"github.com/apimgr/shortner/src/paths"
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
		fmt.Printf("%s%s is running (PID %d)\n", okIcon, binaryName, pid)
		code = 0
	} else {
		fmt.Printf("%s%s is not running\n", failIcon, binaryName)
	}

	printPendingUpdate(p)
	return code
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
	fmt.Printf("Update available: %s -> %s (channel %s, checked %s)\n",
		version.String(), state.AvailableVersion, state.Branch,
		state.CheckedAt.Format("2006-01-02T15:04:05Z"))
}
