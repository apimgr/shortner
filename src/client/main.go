// Command shortner-cli is the required companion client for the shortner
// server. It provides full terminal access to every server feature, plus an
// interactive TUI when started with no command. See AI.md PART 32.
package main

import (
	"os"

	"github.com/apimgr/shortner/src/client/cmd"
)

// main runs the client and exits with the code the command returned.
func main() {
	os.Exit(cmd.Run(os.Args[1:], cmd.IO{
		In:  os.Stdin,
		Out: os.Stdout,
		Err: os.Stderr,
	}, cmd.BinaryName()))
}
