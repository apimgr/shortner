//go:build gui && linux

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
	out, err := exec.CommandContext(ctx, "gsettings", "get", "org.gnome.desktop.interface", "color-scheme").Output()
	return err != nil || strings.Contains(strings.ToLower(string(out)), "dark")
}
