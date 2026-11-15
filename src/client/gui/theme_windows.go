//go:build gui && windows

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
	out, err := exec.CommandContext(ctx, "reg", "query", `HKCU\Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, "/v", "AppsUseLightTheme").Output()
	return err != nil || strings.Contains(strings.ToLower(string(out)), "0x0")
}
