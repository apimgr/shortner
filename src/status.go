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
)

// runStatus checks pidPath and prints/returns the server's running state.
// Exit 0 when healthy (running), 1 when not.
func runStatus(binaryName, pidPath string) int {
	running, pid, err := pidfile.CheckPIDFile(pidPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}

	okIcon, failIcon := "", ""
	if color.EmojiEnabled() {
		okIcon, failIcon = "✅ ", "❌ "
	}

	if running {
		fmt.Printf("%s%s is running (PID %d)\n", okIcon, binaryName, pid)
		return 0
	}
	fmt.Printf("%s%s is not running\n", failIcon, binaryName)
	return 1
}
