//go:build gui && (linux || darwin || windows)

package setup

import (
	"context"

	"github.com/apimgr/shortner/src/client/gui"
)

func runGUISetup(ctx context.Context, opts Options) (Result, error) {
	result, err := gui.RunSetup(ctx, gui.SetupOptions{
		CurrentServer: opts.CurrentServer,
		CurrentToken:  opts.CurrentToken,
		UserAgent:     opts.UserAgent,
		APIVersion:    opts.APIVersion,
	})
	if err != nil {
		if err == gui.ErrSetupCancelled {
			return Result{}, ErrSetupCancelled
		}
		return Result{}, err
	}
	return Result{Server: result.Server, Token: result.Token}, nil
}
