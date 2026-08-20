// Command shortner is the self-hosted URL shortener server. See AI.md for
// the full specification and IDEA.md for project-specific business logic.
package main

import (
	"context"
	"crypto/tls"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/backup"
	"github.com/apimgr/shortner/src/certmgr"
	"github.com/apimgr/shortner/src/common/banner"
	"github.com/apimgr/shortner/src/common/color"
	"github.com/apimgr/shortner/src/common/pidfile"
	"github.com/apimgr/shortner/src/common/version"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/fqdn"
	"github.com/apimgr/shortner/src/geoip"
	"github.com/apimgr/shortner/src/httpserver"
	"github.com/apimgr/shortner/src/metrics"
	"github.com/apimgr/shortner/src/mode"
	"github.com/apimgr/shortner/src/notify"
	"github.com/apimgr/shortner/src/paths"
	"github.com/apimgr/shortner/src/scheduler"
	"github.com/apimgr/shortner/src/signal"
	"github.com/apimgr/shortner/src/updater"
)

// internalName is the frozen on-disk project identifier (IDEA.md
// "internal_name"), used as the dev-TLD project name in fqdn.IsDevTLD.
const internalName = "shortner"

func main() {
	os.Exit(run(os.Args[1:]))
}

// startHTTPServer runs srv.Start(), blocking until Shutdown is called (see
// AI.md PART 8 "Signal Handling & Graceful Shutdown" — the signal package
// calls os.Exit(0) directly from its handler goroutine after running the
// shutdown hooks, so control never actually returns here in production).
// Tests override this variable to skip the blocking call and exercise only
// the startup sequence above it.
var startHTTPServer = func(srv *httpserver.Server) error {
	return srv.Start()
}

func run(args []string) int {
	binaryName := filepath.Base(os.Args[0])

	fs := flag.NewFlagSet(binaryName, flag.ContinueOnError)
	fs.SetOutput(os.Stdout)

	showHelp := fs.Bool("help", false, "Show help")
	showVersion := fs.Bool("version", false, "Show version")
	modeFlag := fs.String("mode", "", "Application mode: production|development|dev|debug")
	debugFlag := fs.Bool("debug", false, "Enable debug diagnostics")
	configDir := fs.String("config", "", "Config directory override")
	dataDir := fs.String("data", "", "Data directory override")
	cacheDir := fs.String("cache", "", "Cache directory override")
	logDir := fs.String("log", "", "Log directory override")
	backupDir := fs.String("backup", "", "Backup directory override")
	pidFile := fs.String("pid", "", "PID file path override")
	address := fs.String("address", "", "Listen address override")
	port := fs.String("port", "", "Listen port override")
	baseURL := fs.String("baseurl", "", "URL path prefix override")
	daemonFlag := fs.Bool("daemon", false, "Run as background daemon")
	colorFlag := fs.String("color", "auto", "Color output: auto, yes, no")
	langFlag := fs.String("lang", "", "Output language (default: auto, from LANG env)")
	statusFlag := fs.Bool("status", false, "Show server status and health")
	shellFlag := fs.String("shell", "", "Shell integration: completions, init, help")
	serviceFlag := fs.String("service", "", "Service management: start, restart, stop, reload, --install, --uninstall, --disable, --help")
	maintenanceFlag := fs.String("maintenance", "", "Maintenance operations: backup, restore, update, mode, setup, pgp, secret, token, data, compliance, --help")
	updateFlag := fs.String("update", "", "Check/perform updates: check, yes, branch, --help")
	// AI.md PART 21 "Backup Contents": SSL certificates and the data
	// directory are opt-in additions to every backup.
	includeSSL := fs.Bool("include-ssl", false, "Include SSL certificates in --maintenance backup")
	includeData := fs.Bool("include-data", false, "Include the data directory in --maintenance backup")
	schedulerFlag := fs.String("scheduler", "", "Scheduler management: list, show <id>, run <id>, enable <id>, disable <id>, history <id>, --help")
	fs.BoolVar(showHelp, "h", false, "Show help")
	fs.BoolVar(showVersion, "v", false, "Show version")

	// --update's entire subcommand is optional per AI.md PART 8
	// ("--update [check|yes|branch ...|--help]"), but flag.FlagSet
	// requires a value for every non-boolean flag; a bare trailing
	// "--update" has nothing left to consume. Default it to "yes", the
	// documented default subcommand (AI.md PART 22 "Commands"), so the
	// flag package's normal parsing handles the rest.
	args = injectDefaultUpdateValue(args)

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *showHelp {
		printHelp(binaryName)
		return 0
	}
	if *showVersion {
		printVersion(binaryName)
		return 0
	}

	// Immediate-exit subcommands (AI.md PART 8 "Server Startup Sequence"
	// PHASE 1-4): none of these touch the filesystem or start the server.
	// PHASE 1 flags (--help, --version, --status, --shell) are handled
	// before the PHASE 2-4 --service/--maintenance/--update subcommands.
	if flagWasSet(fs, "shell") {
		return runShell(binaryName, *shellFlag, firstArg(fs.Args()))
	}

	forceColor, err := color.ParseFlag(*colorFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 2
	}
	// forceColor/EmojiEnabled feed CLI output (see status.go); colored
	// HTML/terminal styling for the eventual HTTP admin surface is PART
	// 16+ territory. --lang is parsed and round-trips here; the i18n
	// lookup it selects is PART 30 (see TODO.AI.md).
	_ = color.Enabled(forceColor)
	_ = langFlag

	mode.FromEnv()
	if *modeFlag != "" {
		mode.SetAppMode(*modeFlag)
	}
	if flagWasSet(fs, "debug") {
		mode.SetDebugEnabled(*debugFlag)
	}

	// Container detection combines the explicit CONTAINER env var with the
	// broader file/env/cgroup heuristics in src/common/pidfile (PART 7),
	// reused here instead of reimplemented.
	inContainer := config.IsTruthy(os.Getenv("CONTAINER")) || pidfile.IsContainer()
	p := paths.Resolve(inContainer)

	// Apply CLI flag / env var overrides (AI.md PART 8 "Environment
	// Variable Fallbacks"), then re-derive the paths that depend on them.
	p.Config = paths.GetConfigDir(*configDir, p.Config)
	p.ConfigFile = filepath.Join(p.Config, "server.yml")
	p.Data = paths.GetDataDir(*dataDir, p.Data)
	p.Cache = paths.GetCacheDir(*cacheDir, p.Cache)
	p.Logs = paths.GetLogDir(*logDir, p.Logs)
	p.LogFile = filepath.Join(p.Logs, "server.log")
	p.Backup = paths.GetBackupDir(*backupDir, p.Data)
	p.PIDFile = paths.GetPIDFile(*pidFile, p.PIDFile)
	p.DB = paths.GetDatabaseDir(p.Data)

	if *statusFlag {
		return runStatus(binaryName, p)
	}

	// PHASE 2-4 subcommands (AI.md PART 8 "Server Startup Sequence"):
	// executed after every PHASE 1 flag, and never start the server.
	if flagWasSet(fs, "service") {
		return runService(binaryName, p, *serviceFlag)
	}
	if flagWasSet(fs, "maintenance") {
		return runMaintenance(binaryName, *maintenanceFlag, maintenanceOptions{
			paths:       p,
			arg:         firstArg(fs.Args()),
			args:        fs.Args(),
			includeSSL:  *includeSSL,
			includeData: *includeData,
		})
	}
	// `email [COMMAND]` is a positional subcommand (AI.md PART 17 uses the
	// literal form `{project_name} email test`), so it is dispatched from
	// the leftover args rather than from a flag.
	if a := fs.Args(); len(a) > 0 && a[0] == "email" {
		return runEmail(binaryName, p, firstArg(a[1:]), firstArg(a[2:]))
	}
	if flagWasSet(fs, "update") {
		return runUpdate(binaryName, p, *updateFlag, firstArg(fs.Args()))
	}

	// --daemon (manual start only; --service start would auto-detect the
	// service manager and ignore this, but --service's real actions are
	// not implemented yet — see service.go).
	if *daemonFlag && !inContainer {
		if err := Daemonize(); err != nil {
			fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
			return 1
		}
	}

	// Directory flags create their directories if missing (AI.md PART 8
	// "Directory Flags" / "Directory Validation Rules").
	for _, dir := range []string{p.Config, p.Data, p.Cache, p.Logs, p.Backup, p.DB} {
		if err := paths.EnsureDir(dir); err != nil {
			fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
			return 1
		}
	}
	if err := paths.EnsurePIDFile(p.PIDFile); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}

	// PID file: written at process start, removed on clean exit. AI.md
	// PART 8 "Server Startup Sequence" steps 11-12 place the PID check and
	// write BEFORE the configuration load (step 13). A no-op inside
	// containers, per "PID File Handling".
	if err := pidfile.WritePIDFile(p.PIDFile); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	defer pidfile.RemovePIDFile(p.PIDFile)

	cfg, err := config.Load(p.ConfigFile, filepath.Join(p.DB, "server.db"))
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	// Invalid config values never fail startup — they are replaced with
	// their framework default and warned about. See AI.md PART 12 "Config
	// Validation Rule".
	for _, warning := range config.Validate(cfg) {
		fmt.Fprintln(os.Stderr, binaryName+": warning: "+warning)
	}

	generated, err := config.EnsureToken(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	// server.security.encryption_key is the canonical at-rest AES-256-GCM
	// key (AI.md PART 11 "Cryptographic Keys"). It lives in server.yml, not
	// in server.db, and is generated on first run alongside the operator
	// token so both land in the same Save below.
	keyGenerated, err := config.EnsureEncryptionKey(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	if generated || keyGenerated {
		if err := config.Save(p.ConfigFile, cfg); err != nil {
			fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
			return 1
		}
	}

	if *address != "" {
		cfg.Server.Listen = *address
	} else if v := os.Getenv("LISTEN"); v != "" {
		cfg.Server.Listen = v
	}
	if *port != "" {
		cfg.Server.Port = *port
	} else if v := os.Getenv("PORT"); v != "" {
		cfg.Server.Port = v
	}
	if *baseURL != "" {
		cfg.Server.BaseURL = *baseURL
	}

	// AI.md PART 17 "Environment Variable Priority": SMTP_* env vars
	// override the config file. Applied before the connection test so the
	// server that gets tested is the one that will actually be used.
	for _, warning := range config.ApplySMTPEnv(cfg) {
		fmt.Fprintln(os.Stderr, binaryName+": warning: "+warning)
	}

	// GeoIP directory defaults to "{data_dir}/security/geoip" per AI.md
	// PART 19 "Configuration" — config.Default can't derive this itself
	// (it only receives a DB file path), so it's resolved here the same
	// way DataDir is.
	if cfg.Server.GeoIP.Dir == "" {
		cfg.Server.GeoIP.Dir = filepath.Join(p.Data, "security", "geoip")
	}

	// Metrics (AI.md PART 20): built before db.Open so the database-driver
	// instrumentation wraps every query from the very first connection.
	var appMetrics *metrics.Metrics
	if cfg.Server.Metrics.Enabled {
		appMetrics = metrics.New(cfg.Server.Metrics)
		appMetrics.InitAppInfo(version.Version, version.CommitID, version.BuildDate)
		appMetrics.StartUptimeUpdater(context.Background())
	}

	sqlDB, err := db.Open(cfg.Server.Database.URL, db.DefaultPool(), appMetrics)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	defer sqlDB.Close()

	if appMetrics != nil {
		appMetrics.StartCollector(context.Background(), p.Data, sqlDB, cfg.Server.Metrics.IncludeSystem, cfg.Server.Metrics.IncludeRuntime)
	}

	accessLog, err := applog.Open(filepath.Join(p.Logs, "access.log"), applog.LevelInfo)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	defer accessLog.Close()

	schedulerLog, err := applog.Open(filepath.Join(p.Logs, "scheduler.log"), applog.LevelInfo)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	defer schedulerLog.Close()

	// Audit log (AI.md PART 11 "Audit Log Format"), consumed by the PART 21
	// backup events.
	auditLog, err := applog.NewAuditLogger(filepath.Join(p.Logs, "audit.log"))
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	defer auditLog.Close()

	// installation_secret (AI.md PART 11 "Cryptographic Keys") backs the
	// rotating {security_id} published in security.txt. A failure here is
	// not fatal: the server still serves, security.txt simply omits the
	// contact-form line rather than publishing a forgeable id.
	installSecret, _, _, err := db.EnsureCoreSecrets(context.Background(), sqlDB)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": warning: "+err.Error())
	}

	// GeoIP (AI.md PART 19): opens whatever MMDB files already exist in
	// cfg.Server.GeoIP.Dir (none on a genuinely first run) and, if enabled,
	// kicks off a background download so first run still works with zero
	// config instead of blocking startup on a network fetch.
	geoManager := geoip.Open(cfg.Server.GeoIP.Dir, cfg.Server.GeoIP.Enabled, cfg.Server.GeoIP.Databases)
	if cfg.Server.GeoIP.Enabled {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			if err := geoip.Download(ctx, cfg.Server.GeoIP.Dir, cfg.Server.GeoIP.Databases); err != nil {
				_ = accessLog.WriteLine(applog.LevelError, "geoip: initial download failed: "+err.Error())
				return
			}
			geoManager.Reload()
		}()
	}

	fqdnHost := fqdn.GetFQDN(internalName)

	// Email notifications (AI.md PART 17). Built before the scheduler and
	// the HTTP server because both raise events through it.
	notifier := newNotifier(cfg, p, fqdnHost)
	startupNotifications(binaryName, notifier, cfg, p, fqdnHost, accessLog)

	sched, err := newBuiltinScheduler(cfg, sqlDB, schedulerLog, accessLog, p, fqdnHost, geoManager, auditLog, notifier)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	sched.SetMetrics(appMetrics)
	sched.SetNotifier(notifier)

	// --scheduler is a PHASE 2-4-style subcommand (AI.md PART 18 "CLI
	// Commands") that reuses server.db directly and never starts the HTTP
	// server, but (unlike --service/--maintenance/--update) it genuinely
	// needs the config-derived task schedules and an open database, so it
	// is dispatched here rather than earlier alongside those flags.
	if flagWasSet(fs, "scheduler") {
		return runSchedulerCLI(binaryName, sched, *schedulerFlag, firstArg(fs.Args()))
	}

	// TLS (AI.md PART 15 "Built-in Let's Encrypt Support"): only attempted
	// for a real, publicly-resolvable FQDN — never for dev-only TLDs
	// (.local, .test, the project's own name, etc.), which can never get a
	// valid public certificate.
	scheme := "http"
	tlsConfig, tlsErr := buildTLSConfig(cfg, p.Config)
	if tlsErr != nil {
		fmt.Fprintln(os.Stderr, binaryName+": warning: "+tlsErr.Error())
	} else if tlsConfig != nil {
		scheme = "https"
	}

	srv := httpserver.New(httpserver.Options{
		Config:    cfg,
		DB:        sqlDB,
		DataDir:   p.Data,
		AccessLog: accessLog,
		Version:   version.Version,
		CommitID:  version.CommitID,
		BuildDate: version.BuildDate,
		StartTime: time.Now(),
		TLSConfig: tlsConfig,
		GeoIP:     geoManager,
		Metrics:   appMetrics,

		ConfigDir:     p.Config,
		AuditLog:      auditLog,
		InstallSecret: installSecret,
		Notifier:      notifier,
	})
	// Shutdown hooks run in registration order, so they are registered in
	// the order AI.md PART 8 "Graceful Shutdown Sequence" prescribes: stop
	// accepting connections and drain in-flight requests (steps 2-4), close
	// database connections (step 5), flush logs (step 6), remove the PID
	// file last (step 9).
	// AI.md PART 11 "IP Block Management" -> "Auto-Release" requires expired
	// temporary IP blocks to be "checked every minute by scheduler". The task
	// is registered here rather than in scheduler.BuiltinTasks because it
	// needs the live HTTP server's in-memory block store, which does not
	// exist when the scheduler is built.
	if err := sched.Register(context.Background(), scheduler.TaskDef{
		ID:       "ip_block_release",
		Name:     "IP Block Auto-Release",
		Schedule: "* * * * *",
		Enabled:  true,
		Run: func(context.Context) error {
			srv.ReleaseExpiredBlocks()
			return nil
		},
	}); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}

	// Scheduler (AI.md PART 18 "Always Running"): started alongside the
	// HTTP server and stopped as part of the same shutdown sequence.
	if err := sched.Start(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}

	pidPath := p.PIDFile
	signal.Register(func() { srv.Shutdown() })
	signal.Register(func() { _ = sched.Stop() })
	// AI.md PART 17 `shutdown` event: sent after the listener is closed and
	// the scheduler is stopped, while the logs and SMTP connection are
	// still usable.
	signal.Register(func() { _ = notifier.Send(notify.EventShutdown, nil) })
	signal.Register(func() { _ = geoManager.Close() })
	signal.Register(func() { sqlDB.Close() })
	signal.Register(func() { accessLog.Close() })
	signal.Register(func() { schedulerLog.Close() })
	signal.Register(func() { pidfile.RemovePIDFile(pidPath) })

	// Non-blocking: installs OS signal handlers and returns immediately;
	// the shutdown hooks registered above run when a signal arrives.
	signal.Start()

	fmt.Println(mode.Banner())
	host := cfg.Server.Listen
	if host == "0.0.0.0" || host == "" {
		host = "localhost"
	}
	banner.PrintStartupBanner(banner.BannerConfig{
		AppName: binaryName,
		Version: version.Version,
		AppMode: mode.GetCurrentAppMode().String(),
		Debug:   mode.IsDebugEnabled(),
		URLs:    []string{fmt.Sprintf("%s://%s:%s%s", scheme, host, cfg.Server.Port, cfg.Server.BaseURL)},
	})

	// Start blocks until Shutdown is called by the signal hook above, then
	// returns nil (http.ErrServerClosed is treated as a clean stop).
	if err := startHTTPServer(srv); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	return 0
}

// buildTLSConfig resolves the server's FQDN and, when cfg.Server.TLS.Enabled
// and the FQDN is not a dev-only TLD, returns a *tls.Config that serves the
// best certificate found by certmgr (falling back to ACME issuance). It
// returns (nil, nil) when TLS is disabled or the FQDN is dev-only — HTTP is
// used in both cases, per AI.md PART 15 "Dev TLD Handling" (never request a
// public certificate for a dev-only TLD).
func buildTLSConfig(cfg *config.Config, configDir string) (*tls.Config, error) {
	if !cfg.Server.TLS.Enabled {
		return nil, nil
	}
	host := fqdn.GetFQDN(internalName)
	if fqdn.IsDevTLD(host, internalName) {
		return nil, fmt.Errorf("tls: %q is a dev-only TLD, skipping certificate issuance", host)
	}
	tlsConfig, _ := certmgr.NewTLSConfig(configDir, host, "")
	return tlsConfig, nil
}

// newBuiltinScheduler builds a scheduler.Scheduler with every AI.md
// PART 18 "Built-in Tasks (Required)" registered, using cfg's per-task
// schedule/enabled overrides. It does not start the scheduler.
func newBuiltinScheduler(cfg *config.Config, sqlDB *sql.DB, schedulerLog, accessLog *applog.Logger, p paths.Paths, host string, geoManager *geoip.Manager, auditLog backup.Auditor, notifier *notify.Notifier) (*scheduler.Scheduler, error) {
	sched, err := scheduler.New(sqlDB, schedulerLog, cfg.Server.Scheduler.Timezone, cfg.Server.Scheduler.CatchUpWindow)
	if err != nil {
		return nil, err
	}

	deps := scheduler.Deps{
		DB:         sqlDB,
		Logs:       []*applog.Logger{schedulerLog, accessLog},
		TLSEnabled: cfg.Server.TLS.Enabled,
		ConfigDir:  p.Config,
		FQDN:       host,
		DevTLD:     fqdn.IsDevTLD(host, internalName),
		GeoIP:      geoManager,
		GeoIPCfg:   cfg.Server.GeoIP,
		// AI.md PART 21: the backup directory is the one resolved at
		// startup and cached here — never re-resolved per run.
		Backup: scheduler.BackupDeps{
			Dir:        p.Backup,
			Prefix:     paths.ProjectName,
			ConfigFile: p.ConfigFile,
			DBPath:     filepath.Join(p.DB, "server.db"),
			ConfigDir:  p.Config,
			DataDir:    p.Data,
			AppVersion: version.String(),
			Cfg:        cfg.Server.Backup,
			Compliance: cfg.Server.Compliance.Enabled,
			Audit:      auditLog,
		},
		// AI.md PART 22: the update_check task compares the embedded
		// build info against the configured channel and records what it
		// found in the data directory's update state file.
		Update: scheduler.UpdateDeps{
			Cfg:            cfg.Server.Update,
			CurrentVersion: version.String(),
			BuildEpoch:     version.Epoch(),
			StatePath:      updater.StatePath(p.Data),
			Log:            schedulerLog,
		},
		// AI.md PART 17: the tasks that own a failure/success email event
		// raise it through this notifier.
		Notifier: notifier,
	}
	ctx := context.Background()
	for _, t := range scheduler.BuiltinTasks(cfg.Server.Scheduler, deps) {
		if err := sched.Register(ctx, t); err != nil {
			return nil, err
		}
	}
	return sched, nil
}

// flagWasSet reports whether name was explicitly passed on the command
// line (as opposed to holding its zero-value default).
func flagWasSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

// firstArg returns positional[0], or "" if there are no positional
// arguments left after flag parsing.
func firstArg(positional []string) string {
	if len(positional) == 0 {
		return ""
	}
	return positional[0]
}

// injectDefaultUpdateValue appends "yes" after a bare trailing "--update"
// so flag.FlagSet always has a value to consume. See the comment at its
// call site in run().
func injectDefaultUpdateValue(args []string) []string {
	if len(args) > 0 && args[len(args)-1] == "--update" {
		out := make([]string, len(args), len(args)+1)
		copy(out, args)
		return append(out, "yes")
	}
	return args
}
