//go:build !gui || (!linux && !darwin && !windows)

package cmd

func runGUI(*runner) (int, bool) { return ExitOK, false }
