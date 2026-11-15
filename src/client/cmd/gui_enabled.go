//go:build gui && (linux || darwin || windows)

package cmd

import "github.com/apimgr/shortner/src/client/gui"

func runGUI(r *runner) (int, bool) {
	if err := gui.RunDashboard(r.client, r.cfg.Server.Primary); err != nil {
		r.printer.Error("GUI closed: %v", err)
		return ExitError, true
	}
	return ExitOK, true
}
