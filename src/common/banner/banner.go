// Package banner prints the server's startup banner, responsive to
// terminal size and TERM=dumb/NO_COLOR. See AI.md PART 7 "Banner Package".
package banner

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/apimgr/shortner/src/common/display"
	"github.com/apimgr/shortner/src/common/i18n"
	"github.com/apimgr/shortner/src/common/terminal"
)

var outputMu sync.Mutex

const (
	defaultTimezone       = "America/New_York"
	startupDateTimeLayout = "January 02, 2006 at 15:04:05 MST"
)

// FormatStartupDateTime formats a startup timestamp using TZ, defaulting to
// America/New_York, as required for user-facing timestamps in AI.md.
func FormatStartupDateTime(value time.Time) string {
	timezone := strings.TrimSpace(os.Getenv("TZ"))
	if timezone == "" {
		timezone = defaultTimezone
	}

	location, err := time.LoadLocation(timezone)
	if err != nil {
		location, err = time.LoadLocation(defaultTimezone)
	}
	if err != nil {
		location = time.UTC
	}

	return value.In(location).Format(startupDateTimeLayout)
}

// BannerConfig describes the values shown in the startup banner.
type BannerConfig struct {
	AppName string
	Version string
	// AppMode is "production" or "development" (see src/mode).
	AppMode string
	Debug   bool
	URLs    []string
	// StartupDateTime is the human-readable server start time.
	StartupDateTime string
	// Setup contains the first-run setup block, when present.
	Setup []string
	// Lang is the resolved output language (AI.md PART 30). An empty or
	// unsupported value falls back to English, so a zero BannerConfig
	// still prints a correct banner.
	Lang string
}

// label returns a translated banner label in the banner's language.
func label(cfg BannerConfig, key string) string {
	return i18n.Translate(cfg.Lang, key)
}

// PrintStartupBanner prints the startup banner, choosing a renderer based
// on the current terminal size breakpoint.
func PrintStartupBanner(cfg BannerConfig) {
	outputMu.Lock()
	defer outputMu.Unlock()
	_, _ = os.Stdout.Write([]byte(StartupBanner(cfg)))
}

// StartupBanner renders the complete banner into one string so startup can
// write it atomically before asynchronous feature logging begins.
func StartupBanner(cfg BannerConfig) string {
	size := terminal.GetTerminalSize()

	switch {
	case size.Mode >= terminal.SizeModeStandard:
		return startupBannerFull(cfg, size)
	case size.Mode >= terminal.SizeModeCompact:
		return startupBannerCompact(cfg)
	case size.Mode >= terminal.SizeModeMinimal:
		return startupBannerMinimal(cfg)
	default:
		return startupBannerMicro(cfg)
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
	_, _ = os.Stdout.Write([]byte(startupBannerFull(cfg, size)))
}

func startupBannerFull(cfg BannerConfig, size terminal.TerminalSize) string {
	var out strings.Builder
	width := size.Cols - 2
	if width > 78 {
		width = 78
	}
	if width < 20 {
		width = 20
	}

	if useBoxDrawing() {
		fmt.Fprintln(&out, "┌"+strings.Repeat("─", width)+"┐")
		writeBoxLine(&out, width, fmt.Sprintf("%s · %s", cfg.AppName, cfg.Version))
		writeBoxLine(&out, width, label(cfg, "cli.mode_label")+": "+modeLabel(cfg))
		for _, u := range cfg.URLs {
			writeBoxLine(&out, width, label(cfg, "cli.url_label")+": "+u)
		}
		if cfg.StartupDateTime != "" {
			writeBoxLine(&out, width, label(cfg, "cli.server_started_label")+" "+cfg.StartupDateTime)
		}
		for _, line := range cfg.Setup {
			writeBoxLine(&out, width, line)
		}
		fmt.Fprintln(&out, "└"+strings.Repeat("─", width)+"┘")
		return out.String()
	}

	fmt.Fprintln(&out, "+"+strings.Repeat("-", width)+"+")
	writeBoxLine(&out, width, fmt.Sprintf("%s · %s", cfg.AppName, cfg.Version))
	writeBoxLine(&out, width, label(cfg, "cli.mode_label")+": "+modeLabel(cfg))
	for _, u := range cfg.URLs {
		writeBoxLine(&out, width, label(cfg, "cli.url_label")+": "+u)
	}
	if cfg.StartupDateTime != "" {
		writeBoxLine(&out, width, label(cfg, "cli.server_started_label")+" "+cfg.StartupDateTime)
	}
	for _, line := range cfg.Setup {
		writeBoxLine(&out, width, line)
	}
	fmt.Fprintln(&out, "+"+strings.Repeat("-", width)+"+")
	return out.String()
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

func writeBoxLine(out *strings.Builder, width int, text string) {
	bar := "│"
	if !useBoxDrawing() {
		bar = "|"
	}
	if len(text) > width-2 {
		text = text[:width-2]
	}
	fmt.Fprintf(out, "%s %-*s%s\n", bar, width-2, text, bar)
}

// printStartupBannerCompact renders the banner for SizeModeCompact: a
// simple bordered block without padding calculations.
func printStartupBannerCompact(cfg BannerConfig) {
	_, _ = os.Stdout.Write([]byte(startupBannerCompact(cfg)))
}

func startupBannerCompact(cfg BannerConfig) string {
	var out strings.Builder
	rule := strings.Repeat("-", 40)
	fmt.Fprintln(&out, rule)
	fmt.Fprintf(&out, "%s · %s (%s)\n", cfg.AppName, cfg.Version, modeLabel(cfg))
	for _, u := range cfg.URLs {
		fmt.Fprintln(&out, label(cfg, "cli.url_label")+": "+u)
	}
	if cfg.StartupDateTime != "" {
		fmt.Fprintln(&out, label(cfg, "cli.started_label")+": "+cfg.StartupDateTime)
	}
	for _, line := range cfg.Setup {
		fmt.Fprintln(&out, line)
	}
	fmt.Fprintln(&out, rule)
	return out.String()
}

// printStartupBannerMinimal renders the banner for SizeModeMinimal: no
// borders, one line per fact.
func printStartupBannerMinimal(cfg BannerConfig) {
	_, _ = os.Stdout.Write([]byte(startupBannerMinimal(cfg)))
}

func startupBannerMinimal(cfg BannerConfig) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s · %s\n", cfg.AppName, cfg.Version)
	fmt.Fprintln(&out, label(cfg, "cli.mode_label")+": "+modeLabel(cfg))
	for _, u := range cfg.URLs {
		fmt.Fprintln(&out, u)
	}
	for _, line := range cfg.Setup {
		fmt.Fprintln(&out, line)
	}
	return out.String()
}

// printStartupBannerMicro renders the banner for SizeModeMicro: a single
// terse line, since the terminal is too small for anything else.
func printStartupBannerMicro(cfg BannerConfig) {
	_, _ = os.Stdout.Write([]byte(startupBannerMicro(cfg)))
}

func startupBannerMicro(cfg BannerConfig) string {
	url := ""
	if len(cfg.URLs) > 0 {
		url = " " + cfg.URLs[0]
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%s · %s%s\n", cfg.AppName, cfg.Version, url)
	for _, line := range cfg.Setup {
		fmt.Fprintln(&out, line)
	}
	return out.String()
}

// modeLabel returns the app mode with a debug suffix if enabled.
func modeLabel(cfg BannerConfig) string {
	if cfg.Debug {
		return cfg.AppMode + " [debug]"
	}
	return cfg.AppMode
}
