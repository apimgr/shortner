// --update command handling. See AI.md PART 8 "Server Binary Commands".
// The actual self-update actions depend on PART 22, tracked in
// TODO.AI.md. This file implements the full flag parsing, dispatch, and
// --help text now, and reports each action as honestly not yet available.
package main

import (
	"fmt"
	"os"
)

// updateHelp is the --update --help text.
const updateHelp = `Usage: %s --update [COMMAND]

Check for and perform self-updates.

Commands:
  (none)                 Check for updates (same as 'check')
  check                   Check for updates without installing
  yes                     Download and install the latest update
  branch {stable|beta|daily}
                           Switch the update channel
  --help                  Show this help
`

// runUpdate dispatches --update [COMMAND] [ARG] and returns the process
// exit code.
func runUpdate(binaryName, command, arg string) int {
	switch command {
	case "help", "--help", "-h":
		fmt.Printf(updateHelp, binaryName)
		return 0
	case "", "check", "yes":
		fmt.Fprintf(os.Stderr, "%s: --update %s is not yet available (see TODO.AI.md, AI.md PART 22)\n", binaryName, defaultLabel(command))
		return 1
	case "branch":
		switch arg {
		case "stable", "beta", "daily":
			fmt.Fprintf(os.Stderr, "%s: --update branch %s is not yet available (see TODO.AI.md, AI.md PART 22)\n", binaryName, arg)
			return 1
		default:
			fmt.Fprintf(os.Stderr, "%s: --update branch requires stable, beta, or daily\n", binaryName)
			return 1
		}
	default:
		fmt.Fprintf(os.Stderr, "%s: unknown --update command %q (run '%s --update --help')\n", binaryName, command, binaryName)
		return 1
	}
}

// defaultLabel returns "check" for the bare --update case, so the error
// message reads naturally either way.
func defaultLabel(command string) string {
	if command == "" {
		return "check"
	}
	return command
}
