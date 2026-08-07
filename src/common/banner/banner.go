// Package banner prints the server's startup banner, responsive to
// terminal size and TERM=dumb/NO_COLOR. See AI.md PART 7 "Banner Package".
package banner

import (
	"fmt"
	"strings"

	"github.com/apimgr/shortner/src/common/display"
	"github.com/apimgr/shortner/src/common/terminal"
)

// BannerConfig describes the values shown in the startup banner.
type BannerConfig struct {
	AppName string
	Version string
	// AppMode is "production" or "development" (see src/mode).
	AppMode string
	Debug   bool
	URLs    []string
}

// PrintStartupBanner prints the startup banner, choosing a renderer based
// on the current terminal size breakpoint.
func PrintStartupBanner(cfg BannerConfig) {
	size := terminal.GetTerminalSize()

	switch {
	case size.Mode >= terminal.SizeModeStandard:
		printStartupBannerFull(cfg, size)
	case size.Mode >= terminal.SizeModeCompact:
		printStartupBannerCompact(cfg)
	case size.Mode >= terminal.SizeModeMinimal:
		printStartupBannerMinimal(cfg)
	default:
		printStartupBannerMicro(cfg)
	}
}

// useBoxDrawing reports whether Unicode box-drawing borders may be used.
// TERM=dumb falls back to plain ASCII (`+--+`) per AI.md PART 7
// "TERM=dumb Handling".
func useBoxDrawing() bool {
	env := display.DetectDisplayEnv()
	return !env.IsDumbTerminal()
}

// printStartupBannerFull renders the banner for SizeModeStandard and above:
// a bordered box with app name, version, mode, debug flag, and every URL.
func printStartupBannerFull(cfg BannerConfig, size terminal.TerminalSize) {
	width := size.Cols - 2
	if width > 78 {
		width = 78
	}
	if width < 20 {
		width = 20
	}

	if useBoxDrawing() {
		fmt.Println("┌" + strings.Repeat("─", width) + "┐")
		printBoxLine(width, fmt.Sprintf("%s %s", cfg.AppName, cfg.Version))
		printBoxLine(width, fmt.Sprintf("Mode: %s", modeLabel(cfg)))
		for _, u := range cfg.URLs {
			printBoxLine(width, "URL: "+u)
		}
		fmt.Println("└" + strings.Repeat("─", width) + "┘")
		return
	}

	fmt.Println("+" + strings.Repeat("-", width) + "+")
	printBoxLine(width, fmt.Sprintf("%s %s", cfg.AppName, cfg.Version))
	printBoxLine(width, fmt.Sprintf("Mode: %s", modeLabel(cfg)))
	for _, u := range cfg.URLs {
		printBoxLine(width, "URL: "+u)
	}
	fmt.Println("+" + strings.Repeat("-", width) + "+")
}

// printBoxLine prints one line padded to width, framed with the vertical
// bar appropriate for the box-drawing style already in use.
func printBoxLine(width int, text string) {
	bar := "│"
	if !useBoxDrawing() {
		bar = "|"
	}
	if len(text) > width-2 {
		text = text[:width-2]
	}
	fmt.Printf("%s %-*s%s\n", bar, width-2, text, bar)
}

// printStartupBannerCompact renders the banner for SizeModeCompact: a
// simple bordered block without padding calculations.
func printStartupBannerCompact(cfg BannerConfig) {
	rule := strings.Repeat("-", 40)
	fmt.Println(rule)
	fmt.Printf("%s %s (%s)\n", cfg.AppName, cfg.Version, modeLabel(cfg))
	for _, u := range cfg.URLs {
		fmt.Println("URL: " + u)
	}
	fmt.Println(rule)
}

// printStartupBannerMinimal renders the banner for SizeModeMinimal: no
// borders, one line per fact.
func printStartupBannerMinimal(cfg BannerConfig) {
	fmt.Printf("%s %s\n", cfg.AppName, cfg.Version)
	fmt.Println("Mode: " + modeLabel(cfg))
	for _, u := range cfg.URLs {
		fmt.Println(u)
	}
}

// printStartupBannerMicro renders the banner for SizeModeMicro: a single
// terse line, since the terminal is too small for anything else.
func printStartupBannerMicro(cfg BannerConfig) {
	url := ""
	if len(cfg.URLs) > 0 {
		url = " " + cfg.URLs[0]
	}
	fmt.Printf("%s %s%s\n", cfg.AppName, cfg.Version, url)
}

// modeLabel returns the app mode with a debug suffix if enabled.
func modeLabel(cfg BannerConfig) string {
	if cfg.Debug {
		return cfg.AppMode + " [debug]"
	}
	return cfg.AppMode
}
