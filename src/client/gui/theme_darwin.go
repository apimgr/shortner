//go:build gui && darwin

package gui

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

func systemThemeIsDark() bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "defaults", "read", "-g", "AppleInterfaceStyle").Output()
	return err == nil && strings.Contains(strings.ToLower(string(out)), "dark")
}
