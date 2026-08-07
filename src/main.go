// Command shortner is the self-hosted URL shortener server. See AI.md for
// the full specification and IDEA.md for project-specific business logic.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/mode"
	"github.com/apimgr/shortner/src/paths"
)

// Build info — set via -ldflags at build time. See PART 25 (Makefile).
var (
	Version      = "devel"
	CommitID     = "N/A"
	BuildDate    = "N/A"
	OfficialSite = ""
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("shortner", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)

	showHelp := fs.Bool("help", false, "Show help")
	showVersion := fs.Bool("version", false, "Show version")
	modeFlag := fs.String("mode", "", "Application mode: production|development|dev|debug")
	debugFlag := fs.Bool("debug", false, "Enable debug diagnostics")
	configDir := fs.String("config", "", "Config directory override")
	address := fs.String("address", "", "Listen address override")
	port := fs.String("port", "", "Listen port override")
	baseURL := fs.String("baseurl", "", "URL path prefix override")
	fs.BoolVar(showHelp, "h", false, "Show help")
	fs.BoolVar(showVersion, "v", false, "Show version")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *showHelp {
		fs.PrintDefaults()
		return 0
	}
	if *showVersion {
		fmt.Println("shortner " + Version)
		return 0
	}

	debugPtr := (*bool)(nil)
	if flagWasSet(fs, "debug") {
		debugPtr = debugFlag
	}
	st := mode.Resolve(*modeFlag, debugPtr)

	inContainer := config.IsTruthy(os.Getenv("CONTAINER"))
	p := paths.Resolve(inContainer)

	cfgFile := p.ConfigFile
	if *configDir != "" {
		cfgFile = *configDir + string(os.PathSeparator) + "server.yml"
	}

	cfg, err := config.Load(cfgFile, p.DB+string(os.PathSeparator)+"server.db")
	if err != nil {
		fmt.Fprintln(os.Stderr, "shortner: "+err.Error())
		return 1
	}

	generated, err := config.EnsureToken(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shortner: "+err.Error())
		return 1
	}
	if generated {
		if err := config.Save(cfgFile, cfg); err != nil {
			fmt.Fprintln(os.Stderr, "shortner: "+err.Error())
			return 1
		}
	}

	if *address != "" {
		cfg.Server.Listen = *address
	}
	if *port != "" {
		cfg.Server.Port = *port
	}
	if *baseURL != "" {
		cfg.Server.BaseURL = *baseURL
	}

	fmt.Println(st.Banner())
	fmt.Printf("shortner %s listening on %s:%s%s\n", Version, cfg.Server.Listen, cfg.Server.Port, cfg.Server.BaseURL)

	// HTTP server startup, CLI subcommands (--service, --maintenance,
	// --status, --update, --daemon) and route registration are tracked in
	// TODO.AI.md — PART 7, 8, 12, 23, 24.
	return 0
}

func flagWasSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}
