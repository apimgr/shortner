//go:build !gui || (!linux && !darwin && !windows)

package setup

import "context"

func runGUISetup(ctx context.Context, opts Options) (Result, error) {
	return runTUI(ctx, opts)
}
