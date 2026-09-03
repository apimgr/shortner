// Package cmd implements the shortner-cli command line, its flag parsing,
// display-mode detection, and every project command. See AI.md PART 32.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/client/api"
	clientconfig "github.com/apimgr/shortner/src/client/config"
	"github.com/apimgr/shortner/src/client/paths"
	"github.com/apimgr/shortner/src/client/setup"
	"github.com/apimgr/shortner/src/client/tui"
	"github.com/apimgr/shortner/src/common/color"
	"github.com/apimgr/shortner/src/common/display"
	"github.com/apimgr/shortner/src/common/i18n"
	"github.com/apimgr/shortner/src/common/version"
)

// ProjectName is the compiled project name. AI.md PART 32 requires internal
// identifiers (User-Agent, config paths) to use this constant and never the
// possibly-renamed binary basename.
const ProjectName = "shortner"

// InternalOrg and InternalName are the frozen on-disk identifiers from
// IDEA.md "## Project variables".
const (
	InternalOrg  = "apimgr"
	InternalName = "shortner"
)

// Exit codes from AI.md PART 32 "Exit Codes".
const (
	ExitOK           = 0
	ExitError        = 1
	ExitConfig       = 2
	ExitConnection   = 3
	ExitAuth         = 4
	ExitNotFound     = 5
	ExitUsage        = 64
	ExitInterrupted  = 130
	defaultAPIPrefix = "v1"
)

// IO bundles the streams a run reads from and writes to, so every command is
// testable without touching the process streams.
type IO struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// runner carries everything a command needs.
type runner struct {
	io         IO
	binaryName string
	lang       string
	flags      Flags
	cfg        clientconfig.Config
	paths      paths.Paths
	client     *api.Client
	printer    *Printer
	debug      bool
}

// Run executes the client with the given argv (excluding the program name)
// and returns the process exit code.
func Run(args []string, streams IO, binaryName string) int {
	flags, err := ParseFlags(args)
	if err != nil {
		fmt.Fprintf(streams.Err, "%s: %v\n", binaryName, err)
		fmt.Fprintf(streams.Err, "Run '%s --help' for usage.\n", binaryName)
		return ExitUsage
	}

	// --help and --version are answered before any config, network, or TUI
	// work, and never escalate privileges.
	lang := i18n.CLILanguage(flags.Lang, "")
	if flags.Help {
		printHelp(streams.Out, binaryName, lang)
		return ExitOK
	}
	if flags.Version {
		printVersion(streams.Out, binaryName, lang)
		return ExitOK
	}
	if flags.ShellSet {
		command, shellArg := shellArgs(flags)
		return runShell(streams.Out, streams.Err, binaryName, command, shellArg)
	}

	r := &runner{io: streams, binaryName: binaryName, flags: flags}
	if code := r.loadConfig(); code != ExitOK {
		return code
	}
	return r.dispatch()
}

// shellArgs splits `--shell CMD [SHELL]` into its command and optional shell
// name. The shell name arrives as the first positional argument.
func shellArgs(flags Flags) (command, shellArg string) {
	command = flags.Shell
	if len(flags.Args) > 0 {
		shellArg = flags.Args[0]
	}
	return command, shellArg
}

// loadConfig resolves paths, loads cli.yml, applies environment and flag
// overrides, and builds the API client and printer.
func (r *runner) loadConfig() int {
	p := paths.Resolve(InternalOrg, InternalName, r.flags.Config)
	if err := p.EnsureDirs(); err != nil {
		fmt.Fprintf(r.io.Err, "%s: cannot create client directories: %v\n", r.binaryName, err)
		return ExitConfig
	}

	cfg, err := clientconfig.Load(p.ConfigFile)
	if err != nil {
		fmt.Fprintf(r.io.Err, "%s: %v\n", r.binaryName, err)
		return ExitConfig
	}
	cfg.SetPath(p.ConfigFile)

	// A missing cli.yml is written on first run so the client works with
	// zero configuration, per AI.md PART 32.
	if _, statErr := os.Stat(p.ConfigFile); os.IsNotExist(statErr) {
		if saveErr := cfg.Save(); saveErr != nil {
			fmt.Fprintf(r.io.Err, "%s: cannot write %s: %v\n", r.binaryName, p.ConfigFile, saveErr)
			return ExitConfig
		}
	}

	cfg.ApplyEnv()

	persist := false
	if r.flags.Server != "" {
		value, save, warn := clientconfig.SaveIfEmptyOrInvalid(cfg.Server.Primary, r.flags.Server, clientconfig.ValidateServerURL)
		if warn {
			fmt.Fprintf(r.io.Err, "%s: invalid --server value %q, keeping current configuration\n", r.binaryName, r.flags.Server)
		}
		cfg.Server.Primary = value
		persist = persist || save
	}
	if r.flags.Token != "" {
		value, save, warn := clientconfig.SaveIfEmptyOrInvalid(cfg.Auth.Token, r.flags.Token, clientconfig.ValidateToken)
		if warn {
			fmt.Fprintf(r.io.Err, "%s: invalid --token value, keeping current configuration\n", r.binaryName)
		}
		cfg.Auth.Token = value
		persist = persist || save
	}
	if r.flags.TokenFile != "" {
		cfg.Auth.TokenFile = r.flags.TokenFile
	}
	if r.flags.Output != "" {
		if !clientconfig.ValidateOutputFormat(r.flags.Output) {
			fmt.Fprintf(r.io.Err, "%s: invalid --output value %q (want table, json, yaml, plain, or csv)\n", r.binaryName, r.flags.Output)
			return ExitUsage
		}
		cfg.Output.Format = strings.ToLower(r.flags.Output)
	}
	if r.flags.Quiet {
		cfg.Output.Quiet = true
	}
	if r.flags.Verbose {
		cfg.Output.Verbose = true
	}
	if r.flags.Debug {
		cfg.Debug = true
	}
	if r.flags.Color != "" {
		cfg.Output.Color = strings.ToLower(r.flags.Color)
	}

	if persist {
		if err := cfg.Save(); err != nil {
			fmt.Fprintf(r.io.Err, "%s: cannot save %s: %v\n", r.binaryName, cfg.Path(), err)
		}
	}

	forceColor, err := color.ParseFlag(cfg.Output.Color)
	if err != nil {
		fmt.Fprintf(r.io.Err, "%s: %v\n", r.binaryName, err)
		return ExitUsage
	}

	r.cfg = cfg
	r.paths = p
	r.debug = cfg.Debug
	r.lang = i18n.CLILanguage(r.flags.Lang, cfg.Defaults.Lang)
	r.printer = NewPrinter(r.io.Out, r.io.Err, cfg.Output.Format, color.Enabled(forceColor), cfg.Output.Quiet)
	r.client = api.New(api.Options{
		BaseURL:    cfg.Server.Primary,
		APIVersion: cfg.Server.APIVersion,
		UserAgent:  UserAgent(),
		Token:      cfg.ResolveToken(),
		Timeout:    parseDuration(cfg.Server.Timeout, 30*time.Second),
		Retry:      cfg.Server.Retry,
		RetryDelay: parseDuration(cfg.Server.RetryDelay, time.Second),
	})
	return ExitOK
}

// UserAgent returns the client's User-Agent. It is always built from the
// compiled project name, never from the binary basename.
func UserAgent() string {
	return fmt.Sprintf("%s-cli/%s", ProjectName, version.Version)
}

// parseDuration parses a config duration, falling back to a default.
func parseDuration(value string, fallback time.Duration) time.Duration {
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

// dispatch routes to the selected command, the setup wizard, or the TUI.
func (r *runner) dispatch() int {
	ctx := context.Background()

	if r.flags.UpdateSet {
		return r.runUpdate(ctx)
	}

	command, args := splitCommand(r.flags.Args)

	// AI.md PART 32 step 2: every start checks the server's published client
	// versions, so an outdated client is told to upgrade and one below
	// cli_min_version stops before it makes any further request.
	if command != "help" && clientconfig.ValidateServerURL(r.client.BaseURL()) && !r.updateGate(ctx) {
		return ExitError
	}

	if command == "" {
		return r.runInteractive(ctx)
	}

	switch command {
	case "setup":
		return r.runSetup(ctx)
	case "shorten", "create", "new", "add":
		return r.runShorten(ctx, args)
	case "get", "info", "show":
		return r.runGet(ctx, args)
	case "list", "ls":
		return r.runList(ctx)
	case "update", "edit":
		return r.runUpdateLink(ctx, args)
	case "delete", "rm", "remove":
		return r.runDelete(ctx, args)
	case "stats", "analytics":
		return r.runStats(ctx, args)
	case "health", "status":
		return r.runHealth(ctx)
	case "help":
		printHelp(r.io.Out, r.binaryName, r.lang)
		return ExitOK
	default:
		// Smart argument detection (AI.md PART 32): a bare URL shortens, a
		// bare slug is looked up.
		if looksLikeURL(command) {
			return r.runShorten(ctx, r.flags.Args)
		}
		if len(r.flags.Args) == 1 && looksLikeSlug(command) {
			return r.runGet(ctx, r.flags.Args)
		}
		fmt.Fprintf(r.io.Err, "%s: unknown command %q (run '%s --help')\n", r.binaryName, command, r.binaryName)
		return ExitUsage
	}
}

// splitCommand separates the command word from its arguments.
func splitCommand(args []string) (string, []string) {
	if len(args) == 0 {
		return "", nil
	}
	return args[0], args[1:]
}

// runInteractive launches the TUI, or the setup wizard when the client has
// no server configured yet. A non-terminal invocation with no command is a
// usage error rather than a hang.
func (r *runner) runInteractive(ctx context.Context) int {
	mode := r.displayMode()
	switch mode {
	case "plain":
		fmt.Fprintf(r.io.Err, "%s: no command given and no terminal attached\n", r.binaryName)
		fmt.Fprintf(r.io.Err, "Run '%s --help' for usage.\n", r.binaryName)
		return ExitUsage
	case "gui":
		// The GUI front end lives behind the `gui` build tag (AI.md PART 32
		// "GUI Mode"); the default CGO-free build falls back to the TUI.
		fallthrough
	default:
		if !clientconfig.ValidateServerURL(r.cfg.Server.Primary) {
			if code := r.runSetup(ctx); code != ExitOK {
				return code
			}
		}
		return r.runTUI(ctx)
	}
}

// displayMode resolves the effective display mode. AI.md PART 32 forbids
// UI-mode flags — the only override is display.mode in cli.yml.
func (r *runner) displayMode() string {
	env := display.DetectDisplayEnv()
	switch strings.ToLower(r.cfg.Display.Mode) {
	case "gui":
		if !env.HasDisplay {
			return "plain"
		}
		return "gui"
	case "tui":
		if !env.IsTerminal {
			return "plain"
		}
		return "tui"
	default:
		if !env.IsTerminal {
			return "plain"
		}
		return "tui"
	}
}

// runTUI starts the interactive terminal interface.
func (r *runner) runTUI(ctx context.Context) int {
	err := tui.Run(ctx, tui.Options{
		Client:      r.client,
		Theme:       r.cfg.TUI.Theme,
		Mouse:       r.cfg.TUI.Mouse,
		Unicode:     r.cfg.TUI.Unicode,
		Lang:        r.lang,
		ServerLabel: r.cfg.Server.Primary,
		PageSize:    r.cfg.Defaults.Limit,
	})
	if err != nil {
		return r.fail(err)
	}
	return ExitOK
}

// runSetup runs the first-run configuration wizard.
func (r *runner) runSetup(ctx context.Context) int {
	result, err := setup.Run(ctx, setup.Options{
		In:            r.io.In,
		Out:           r.io.Out,
		Err:           r.io.Err,
		BinaryName:    r.binaryName,
		CurrentServer: r.cfg.Server.Primary,
		CurrentToken:  r.cfg.Auth.Token,
		UserAgent:     UserAgent(),
		APIVersion:    r.cfg.Server.APIVersion,
	})
	if err != nil {
		fmt.Fprintf(r.io.Err, "%s: %v\n", r.binaryName, err)
		return ExitConfig
	}

	r.cfg.Server.Primary = result.Server
	r.cfg.Auth.Token = result.Token
	if err := r.cfg.Save(); err != nil {
		fmt.Fprintf(r.io.Err, "%s: cannot save %s: %v\n", r.binaryName, r.cfg.Path(), err)
		return ExitConfig
	}
	r.client = api.New(api.Options{
		BaseURL:    r.cfg.Server.Primary,
		APIVersion: r.cfg.Server.APIVersion,
		UserAgent:  UserAgent(),
		Token:      r.cfg.ResolveToken(),
		Timeout:    parseDuration(r.cfg.Server.Timeout, 30*time.Second),
		Retry:      r.cfg.Server.Retry,
		RetryDelay: parseDuration(r.cfg.Server.RetryDelay, time.Second),
	})
	r.printer.Message("Configuration saved to %s", r.cfg.Path())
	return ExitOK
}

// fail maps an error to the matching exit code from AI.md PART 32.
func (r *runner) fail(err error) int {
	if err == nil {
		return ExitOK
	}
	r.printer.Error("%v", err)
	switch {
	case errors.Is(err, api.ErrNotFound):
		return ExitNotFound
	case errors.Is(err, api.ErrTokenRevoked), errors.Is(err, api.ErrUnauthorized):
		return ExitAuth
	case errors.Is(err, context.DeadlineExceeded):
		return ExitConnection
	case strings.Contains(err.Error(), "connect to "):
		return ExitConnection
	case strings.Contains(err.Error(), "no server configured"):
		return ExitConfig
	default:
		return ExitError
	}
}

// requireServer reports a config error when no server is configured.
func (r *runner) requireServer() error {
	if clientconfig.ValidateServerURL(r.client.BaseURL()) {
		return nil
	}
	return fmt.Errorf("no server configured: run '%s setup' or pass --server URL", r.binaryName)
}

// looksLikeURL reports whether an argument is a destination URL rather than
// a command or slug.
func looksLikeURL(arg string) bool {
	lower := strings.ToLower(arg)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

// looksLikeSlug reports whether an argument matches the project's short-code
// and custom-slug rules from IDEA.md: 3-20 characters, alphanumeric plus
// hyphens.
func looksLikeSlug(arg string) bool {
	if len(arg) < 3 || len(arg) > 20 {
		return false
	}
	for _, r := range arg {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
		default:
			return false
		}
	}
	return true
}

// BinaryName returns the actual (possibly renamed) binary name for display.
func BinaryName() string {
	name := filepath.Base(os.Args[0])
	if runtime.GOOS == "windows" {
		name = strings.TrimSuffix(name, ".exe")
	}
	return name
}
