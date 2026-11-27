// Command shortner is the self-hosted URL shortener server. See AI.md for
// the full specification and IDEA.md for project-specific business logic.
package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/blocklist"
	"github.com/apimgr/shortner/src/cache"
	"github.com/apimgr/shortner/src/certmgr"
	"github.com/apimgr/shortner/src/common/banner"
	"github.com/apimgr/shortner/src/common/color"
	"github.com/apimgr/shortner/src/common/netutil"
	"github.com/apimgr/shortner/src/common/pidfile"
	"github.com/apimgr/shortner/src/common/version"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/cve"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/fqdn"
	"github.com/apimgr/shortner/src/geoip"
	"github.com/apimgr/shortner/src/i2p"
	"github.com/apimgr/shortner/src/metrics"
	"github.com/apimgr/shortner/src/mode"
	"github.com/apimgr/shortner/src/notify"
	"github.com/apimgr/shortner/src/paths"
	"github.com/apimgr/shortner/src/pgp"
	"github.com/apimgr/shortner/src/scheduler"
	"github.com/apimgr/shortner/src/security"
	"github.com/apimgr/shortner/src/server"
	"github.com/apimgr/shortner/src/service"
	"github.com/apimgr/shortner/src/signal"
	"github.com/apimgr/shortner/src/ssl"
	"github.com/apimgr/shortner/src/tor"
	"github.com/apimgr/shortner/src/updater"
)

func parseLegacySecretExpiry(value string) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC(), nil
	}
	unix, err := strconv.ParseInt(value, 10, 64)
	if err != nil || unix <= 0 {
		return time.Time{}, fmt.Errorf("invalid expiry")
	}
	return time.Unix(unix, 0).UTC(), nil
}

func initializeRemoteDatabase(ctx context.Context, store db.Handle, tokenKey []byte, configFile, logDir, binaryName string) error {
	if err := db.MigrateLegacyGraceSecrets(ctx, store, tokenKey); err != nil {
		return fmt.Errorf("secret rotation migration failed: %w", err)
	}
	legacyAppSecrets, err := config.ReadLegacyAppSecrets(configFile)
	if err != nil {
		return err
	}
	if value, ok := legacyAppSecrets[db.SecretInstallation]; ok {
		if _, err := db.GetSecret(ctx, store, db.SecretInstallation); errors.Is(err, db.ErrNotFound) {
			if err := db.SetSecret(ctx, store, db.SecretInstallation, value); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	for _, name := range []string{db.SecretInstallation, "encryption_key"} {
		legacyName := name + "_previous"
		value, ok := legacyAppSecrets[legacyName]
		if !ok {
			continue
		}
		active, err := db.HasPreviousSecret(ctx, store, name)
		if err != nil {
			return err
		}
		if active {
			continue
		}
		expires, err := parseLegacySecretExpiry(legacyAppSecrets[legacyName+"_expires"])
		if err != nil {
			return fmt.Errorf("legacy secret expiry is invalid")
		}
		if err := db.SetGraceSecret(ctx, store, name, value, expires, tokenKey); err != nil {
			return err
		}
	}
	if len(legacyAppSecrets) > 0 {
		if err := config.RemoveLegacyAppSecrets(configFile); err != nil {
			return err
		}
	}
	if previousEncoded, err := db.GetPreviousSecret(ctx, store, "encryption_key", tokenKey); err == nil {
		previousKey, decodeErr := base64.StdEncoding.DecodeString(previousEncoded)
		if decodeErr != nil || len(previousKey) != 32 {
			return fmt.Errorf("previous encryption key is invalid")
		}
		if err := db.ReencryptStoredData(ctx, store, previousKey, tokenKey); err != nil {
			logStartupDecryptFailure(logDir, err)
			return fmt.Errorf("unfinished encryption-key rotation failed: %w", err)
		}
	} else if !errors.Is(err, db.ErrNotFound) {
		return fmt.Errorf("previous encryption key could not be loaded: %w", err)
	}
	if skipped, err := db.BackfillTokenLast4(ctx, store, tokenKey); err != nil {
		return fmt.Errorf("token metadata migration failed: %w", err)
	} else if skipped > 0 {
		fmt.Fprintf(os.Stderr, "%s: warning: %d legacy token suffixes cannot be recovered until those tokens are presented\n", binaryName, skipped)
	}
	return nil
}

func loadProjectSecretKeys(ctx context.Context, sqlDB db.Handle) (*security.SecretKeyring, error) {
	read := func(name string) ([]byte, []byte, error) {
		encoded, err := db.GetSecret(ctx, sqlDB, name)
		if err != nil {
			return nil, nil, err
		}
		current, err := base64.RawStdEncoding.DecodeString(encoded)
		if err != nil || len(current) != 32 {
			return nil, nil, fmt.Errorf("db: secret %s is invalid", name)
		}
		previousEncoded, err := db.GetPreviousSecret(ctx, sqlDB, name, nil)
		if err != nil && !errors.Is(err, db.ErrNotFound) {
			return nil, nil, err
		}
		var previous []byte
		if err == nil {
			previous, err = base64.RawStdEncoding.DecodeString(previousEncoded)
			if err != nil || len(previous) != 32 {
				return nil, nil, fmt.Errorf("db: previous secret %s is invalid", name)
			}
		}
		return current, previous, nil
	}
	cookieCurrent, cookiePrevious, err := read(db.SecretCookieSigning)
	if err != nil {
		return nil, err
	}
	csrfCurrent, csrfPrevious, err := read(db.SecretCSRFToken)
	if err != nil {
		return nil, err
	}
	return security.NewSecretKeyring(cookieCurrent, cookiePrevious, csrfCurrent, csrfPrevious), nil
}

func maintainProjectSecrets(ctx context.Context, sqlDB db.Handle, audit *applog.AuditLogger, keyring *security.SecretKeyring, now time.Time) error {
	for _, secret := range []struct {
		name  string
		age   time.Duration
		event string
	}{
		{db.SecretCookieSigning, 90 * 24 * time.Hour, "security.cookie_signing_key_rotated"},
		{db.SecretCSRFToken, 180 * 24 * time.Hour, "security.csrf_token_secret_rotated"},
	} {
		rotated, err := db.RotateSecretDue(ctx, sqlDB, secret.name, secret.age, 7*24*time.Hour, now)
		if err != nil {
			return err
		}
		if rotated && audit != nil {
			if err := audit.Write(applog.Entry{Event: secret.event, Category: "security", Severity: applog.SeverityInfo, Details: map[string]any{"reason": "scheduled rotation"}, Result: applog.ResultSuccess}); err != nil {
				return err
			}
		}
	}
	return keyring.Refresh(func() ([]byte, []byte, []byte, []byte, error) {
		keys, err := loadProjectSecretKeys(ctx, sqlDB)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		cookieCurrent, cookiePrevious := keys.CookieKeys()
		csrfCurrent, csrfPrevious := keys.CSRFKeys()
		return cookieCurrent, cookiePrevious, csrfCurrent, csrfPrevious, nil
	})
}

func logStartupDecryptFailure(logDir string, err error) {
	var failure *db.DecryptFailure
	if !errors.As(err, &failure) {
		return
	}
	audit, openErr := applog.NewAuditLogger(filepath.Join(logDir, "audit.log"))
	if openErr != nil {
		return
	}
	_ = writeDecryptFailure(audit, err)
	_ = audit.Close()
}

// internalName is the frozen on-disk project identifier (IDEA.md
// "internal_name"), used as the dev-TLD project name in fqdn.IsDevTLD.
const internalName = "shortner"

const defaultListenAddress = "[::]"

func main() {
	os.Exit(run(os.Args[1:]))
}

// startHTTPServer runs srv.Start(), blocking until Shutdown is called (see
// AI.md PART 8 "Signal Handling & Graceful Shutdown" — the signal package
// calls os.Exit(0) directly from its handler goroutine after running the
// shutdown hooks, so control never actually returns here in production).
// Tests override this variable to skip the blocking call and exercise only
// the startup sequence above it.
var startHTTPServer = func(srv *server.Server) error {
	return srv.Start()
}

var createServiceAccount = service.CreateServiceAccount
var setRuntimeOwnership = service.SetRuntimeOwnership
var dropPrivileges = service.DropPrivileges
var bindPrivilegedPorts = bindRootPorts
var downloadGeoIP = geoip.Download
var ipv4FallbackWarning sync.Once

func run(args []string) int {
	binaryName := filepath.Base(os.Args[0])

	fs := flag.NewFlagSet(binaryName, flag.ContinueOnError)
	fs.SetOutput(os.Stdout)

	showHelp := fs.Bool("help", false, "Show help")
	showVersion := fs.Bool("version", false, "Show version")
	modeFlag := fs.String("mode", "", "Application mode: production|development|dev|debug")
	debugFlag := fs.Bool("debug", false, "Enable debug diagnostics")
	i2pFlag := fs.Bool("i2p", false, "Enable the I2P eepsite")
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
	maintenanceFlag := fs.String("maintenance", "", "Maintenance operations: backup, restore, update, mode, setup, pgp, security, secret, token, data, compliance, scheduler, email, tor, --help")
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

	// AI.md PART 30 "CLI/Agent/Server Output Translation": resolve the
	// output language before anything is printed. The config file is not
	// readable yet at this point, so only --lang and the LANG/LC_ALL
	// environment participate here; setCLILanguage runs again once the
	// config file has been loaded so server.i18n.default_language can
	// take effect for the long-running server.
	setCLILanguage(*langFlag, "")

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
	// 16+ territory.
	_ = color.Enabled(forceColor)

	// The shared container probe controls daemon/PID behavior. Only Docker
	// uses /config and /data; Incus/LXC keeps its native OS path layout.
	inContainer := pidfile.IsContainer()
	dockerPaths := paths.IsDocker()
	p := paths.Resolve(dockerPaths)

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
	p.DB = paths.GetDatabaseDir(p.Data, dockerPaths)
	if err := config.EnforcePrivatePaths(p.Config, p.Data); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}

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
	// I2P keeps its documented positional operator command.
	if a := fs.Args(); len(a) > 0 && a[0] == "i2p" {
		return runI2P(binaryName, p, a[1:])
	}
	if flagWasSet(fs, "update") {
		return runUpdate(binaryName, p, *updateFlag, firstArg(fs.Args()))
	}

	serviceUID := os.Getuid()
	if paths.IsPrivileged() {
		serviceUID, err = createServiceAccount(serviceParams(p))
		if err != nil {
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
	if paths.IsPrivileged() {
		if err := setRuntimeOwnership(serviceParams(p), serviceUID); err != nil {
			fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
			return 1
		}
	} else if err := service.EnforceRuntimeModes(serviceParams(p), false); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	portSetting := *port
	portWasGenerated := false
	var earlyPortWarning string
	earlyAddress, configPort, configDaemonize, peekErr := config.PeekServerStartupSettings(p.ConfigFile)
	if peekErr != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+peekErr.Error())
		return 1
	}
	if earlyAddress != "" && !config.ValidServerAddress(earlyAddress) {
		earlyAddress = ""
	}
	listenAddress := selectListenAddress(*address, os.Getenv("LISTEN"), earlyAddress, defaultListenAddress)
	if !config.ValidServerAddress(listenAddress) {
		fmt.Fprintf(os.Stderr, "%s: invalid listen address %q\n", binaryName, listenAddress)
		return 1
	}
	resolvedListenAddress, usedIPv4Fallback, addressErr := netutil.ResolveListenAddress(listenAddress)
	if addressErr != nil {
		fmt.Fprintf(os.Stderr, "%s: resolve listen address %q: %v\n", binaryName, listenAddress, addressErr)
		return 1
	}
	listenAddress = resolvedListenAddress
	if usedIPv4Fallback {
		warnIPv4Fallback(binaryName)
	}
	if portSetting == "" {
		portSetting = os.Getenv("PORT")
	}
	if portSetting == "" {
		portSetting = configPort
		if portSetting != "" && !validPortSetting(portSetting) {
			earlyPortWarning = fmt.Sprintf("invalid server.port %q", portSetting)
			portSetting = ""
		}
	}
	if portSetting == "" {
		randomPort, portErr := netutil.GetRandomAvailablePort()
		if portErr != nil {
			fmt.Fprintln(os.Stderr, binaryName+": "+portErr.Error())
			return 1
		}
		portSetting = strconv.Itoa(randomPort)
		portWasGenerated = true
	}
	if !validPortSetting(portSetting) {
		fmt.Fprintf(os.Stderr, "%s: invalid listen port %q\n", binaryName, portSetting)
		return 1
	}
	if !paths.IsPrivileged() {
		if err := validateUnprivilegedPortSetting(portSetting); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", binaryName, err)
			return 1
		}
	}
	if shouldDaemonizeForManager(detectServiceManager(), false, *daemonFlag, configDaemonize) && !inContainer {
		if err := Daemonize(); err != nil {
			fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
			return 1
		}
	}
	var privilegedListeners map[string]net.Listener
	if paths.IsPrivileged() && !flagWasSet(fs, "scheduler") {
		privilegedListeners, err = bindPrivilegedPorts(listenAddress, portSetting)
		if err != nil {
			fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
			return 1
		}
		defer func() {
			for _, listener := range privilegedListeners {
				_ = listener.Close()
			}
		}()
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
	areaMigrationErr := config.MigrateAreaFiles(p.ConfigFile)
	secretMigrationErr := config.MigrateSecretsFiles(p.ConfigFile)
	if migrationErr := errors.Join(secretMigrationErr, areaMigrationErr); migrationErr != nil {
		fmt.Fprintln(os.Stderr, binaryName+": warning: configuration migration skipped; using the merged configuration: "+migrationErr.Error())
	}

	cfg, err := config.Load(p.ConfigFile, filepath.Join(p.DB, "server.db"))
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	if !cfg.Server.I2P.EnabledExplicit && !flagWasSet(fs, "i2p") {
		cfg.Server.I2P.Enabled = inContainer
	}
	for _, warning := range config.ApplyConnectionEnvironment(cfg) {
		fmt.Fprintln(os.Stderr, binaryName+": warning: "+warning)
	}
	for _, warning := range config.ApplyI2PEnv(cfg) {
		fmt.Fprintln(os.Stderr, binaryName+": warning: "+warning)
	}
	if flagWasSet(fs, "i2p") {
		cfg.Server.I2P.Enabled = *i2pFlag
	}
	if cfg.Data.SecurityDir == "" {
		cfg.Data.SecurityDir = filepath.Join(p.Data, "security")
	}
	if earlyPortWarning != "" {
		cfg.Server.Port = portSetting
	}
	// Invalid config values never fail startup — they are replaced with
	// their framework default and warned about. See AI.md PART 12 "Config
	// Validation Rule".
	for _, warning := range config.Validate(cfg) {
		fmt.Fprintln(os.Stderr, binaryName+": warning: "+warning)
	}
	if !config.DatabaseConnectionURLValid(cfg) {
		fmt.Fprintln(os.Stderr, binaryName+": database connection failed")
		return 1
	}
	if err := config.ValidateContactCaptcha(cfg); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	if earlyPortWarning != "" {
		fmt.Fprintf(os.Stderr, "%s: warning: %s, using random port %s\n", binaryName, earlyPortWarning, portSetting)
	}
	if portWasGenerated {
		cfg.Server.Port = portSetting
	}
	effectiveMode := cfg.Server.Mode
	if envMode := os.Getenv("MODE"); envMode != "" {
		effectiveMode = envMode
	}
	if *modeFlag != "" {
		effectiveMode = *modeFlag
	}
	mode.SetAppMode(effectiveMode)
	if debugEnv, set := os.LookupEnv("DEBUG"); set && debugEnv != "" {
		mode.SetDebugEnabled(config.IsTruthy(debugEnv))
	}
	if flagWasSet(fs, "debug") {
		mode.SetDebugEnabled(*debugFlag)
	}

	// The config file's default language now participates in AI.md PART
	// 30's priority chain; an explicit --lang still wins over it.
	setCLILanguage(*langFlag, cfg.Server.I18N.DefaultLanguage)

	generated, err := config.EnsureToken(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	keyVersionWasMissing := cfg.Server.Security.EncryptionKey != "" && cfg.Server.Security.EncryptionKeyVersion == 0
	// server.security.encryption_key is the canonical at-rest AES-256-GCM
	// key (AI.md PART 11 "Cryptographic Keys"). It is stored in secrets.yml
	// and generated on first run alongside the operator token.
	keyGenerated, err := config.EnsureEncryptionKey(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	metricsTokenWasEmpty := map[string]bool{
		"server.metrics.auth.tokens.prometheus": !config.ValidMetricsToken(cfg.Server.Metrics.Auth.Tokens.Prometheus),
		"server.metrics.auth.tokens.grafana":    !config.ValidMetricsToken(cfg.Server.Metrics.Auth.Tokens.Grafana),
		"server.metrics.auth.tokens.loki":       !config.ValidMetricsToken(cfg.Server.Metrics.Auth.Tokens.Loki),
	}
	metricsTokensGenerated, err := config.EnsureMetricsTokens(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	webhookSecretsGenerated, err := config.EnsureWebhookSecrets(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	dnsCredentialsChanged, err := config.SealDNSCredentials(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	if *port != "" {
		cfg.Server.Port = *port
	} else if v := os.Getenv("PORT"); v != "" {
		cfg.Server.Port = v
	}
	if *baseURL != "" {
		cfg.Server.BaseURL = *baseURL
	}
	cfg.Server.Listen = listenAddress

	if paths.IsPrivileged() {
		if err := setRuntimeOwnership(serviceParams(p), serviceUID); err != nil {
			fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
			return 1
		}
		if err := dropPrivileges(serviceUID); err != nil {
			fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
			return 1
		}
	}
	if generated || keyGenerated || keyVersionWasMissing || metricsTokensGenerated || len(webhookSecretsGenerated) > 0 || portWasGenerated || dnsCredentialsChanged || config.HasSeededConnection(cfg) {
		if err := config.Save(p.ConfigFile, cfg); err != nil {
			fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
			return 1
		}
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

	var sqlDB *db.Store
	recoveryInterval, _ := config.ParseDuration(cfg.Server.Maintenance.SelfHealing.RetryInterval, 30*time.Second)
	maxRecoveryAttempts := cfg.Server.Maintenance.SelfHealing.MaxAttempts
	if cfg.Server.Database.Cache.Enabled {
		cacheCfg := cfg.Server.Database.Cache
		if cacheCfg.Path == "" {
			cacheCfg.Path = filepath.Join(p.Data, "db", "cache.db")
		}
		cacheCfg.Path = strings.ReplaceAll(cacheCfg.Path, "${DATA_DIR}", p.Data)
		if !filepath.IsAbs(cacheCfg.Path) {
			cacheCfg.Path = filepath.Join(p.Data, cacheCfg.Path)
		}
		if mkdirErr := os.MkdirAll(filepath.Dir(cacheCfg.Path), 0o700); mkdirErr != nil {
			err = mkdirErr
		} else {
			interval, _ := config.ParseDuration(cacheCfg.SyncInterval, 30*time.Second)
			maxAge, _ := config.ParseDuration(cacheCfg.MaxAge, time.Hour)
			sqlDB, err = db.OpenWithCacheRecovery(cfg.Server.Database.URL, db.DefaultPool(), appMetrics, cacheCfg.Path, interval, maxAge, recoveryInterval, maxRecoveryAttempts, cacheCfg.OfflineMode, cfg.Server.Database.Token)
		}
		if err != nil {
			sqlDB, err = db.Open(cfg.Server.Database.URL, db.DefaultPool(), appMetrics, cfg.Server.Database.Token)
		}
	} else {
		sqlDB, err = db.Open(cfg.Server.Database.URL, db.DefaultPool(), appMetrics, cfg.Server.Database.Token)
	}
	if err != nil {
		sqlDB, err = db.OpenInMaintenanceWithPolicy(
			cfg.Server.Database.URL, db.DefaultPool(), appMetrics,
			filepath.Join(p.Cache, "db-maintenance.db"), recoveryInterval, maxRecoveryAttempts, cfg.Server.Database.Token,
		)
		if err != nil {
			fmt.Fprintln(os.Stderr, binaryName+": database maintenance store could not be opened")
			return 1
		}
	}
	defer func() { _ = sqlDB.Close() }()
	offlineDatabase := sqlDB.Offline()
	tokenKey, err := config.DecodeEncryptionKey(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": token encryption key is invalid")
		return 1
	}
	ctx := context.Background()
	if !offlineDatabase {
		if err := initializeRemoteDatabase(ctx, sqlDB, tokenKey, p.ConfigFile, p.Logs, binaryName); err != nil {
			_ = sqlDB.Close()
			sqlDB, err = db.OpenInMaintenanceWithPolicy(
				cfg.Server.Database.URL, db.DefaultPool(), appMetrics,
				filepath.Join(p.Cache, "db-maintenance.db"), recoveryInterval, maxRecoveryAttempts, cfg.Server.Database.Token,
			)
			if err != nil {
				fmt.Fprintln(os.Stderr, binaryName+": database maintenance store could not be opened")
				return 1
			}
			offlineDatabase = true
		}
	}
	if offlineDatabase {
		fmt.Fprintln(os.Stderr, binaryName+": database connection failed; entering maintenance mode")
	}

	stopMetrics := func() {}
	var metricsCtx context.Context
	var cancelMetrics context.CancelFunc
	if appMetrics != nil {
		metricsCtx, cancelMetrics = context.WithCancel(context.Background())
		stopMetrics = cancelMetrics
		appMetrics.StartCollector(metricsCtx, p.Data, sqlDB.Read(metricsCtx), cfg.Server.Metrics.IncludeSystem, cfg.Server.Metrics.IncludeRuntime)
	}

	accessLog, err := applog.Open(filepath.Join(p.Logs, "access.log"), applog.LevelInfo)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	defer accessLog.Close()
	sqlDB.SetBusyObserver(func(wait time.Duration, caller string) {
		if wait <= time.Second {
			return
		}
		_ = accessLog.WriteLine(applog.LevelWarn, fmt.Sprintf("db.busy wait_ms=%d caller=%s", wait.Milliseconds(), caller))
	})
	accessPolicy, _ := cfg.Server.Logs.EffectiveLogKind("access")
	if err := accessLog.SetRotationPolicy(accessPolicy.Rotate, accessPolicy.Keep, accessPolicy.Compress); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}

	schedulerLog, err := applog.Open(filepath.Join(p.Logs, "scheduler.log"), applog.LevelInfo)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	defer schedulerLog.Close()
	serverPolicy, _ := cfg.Server.Logs.EffectiveLogKind("server")
	if err := schedulerLog.SetRotationPolicy(serverPolicy.Rotate, serverPolicy.Keep, serverPolicy.Compress); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}

	var appCache cache.Cache = cache.NewMemory()
	cacheHealth := func(context.Context) string { return "ok" }
	var closeCache func()
	cacheType := strings.ToLower(strings.TrimSpace(cfg.Server.Cache.Type))
	switch cacheType {
	case "redis", "valkey":
		cacheTimeout, parseErr := time.ParseDuration(cfg.Server.Cache.Timeout)
		if parseErr != nil || cacheTimeout <= 0 {
			cacheTimeout = 5 * time.Second
		}
		cacheCtx, cancelCache := context.WithTimeout(context.Background(), cacheTimeout)
		redisCache, cacheErr := cache.NewRedis(cacheCtx, cache.RedisOptions{
			URL: cfg.Server.Cache.URL, Host: cfg.Server.Cache.Host, Port: cfg.Server.Cache.Port,
			Username: cfg.Server.Cache.Username, Password: cfg.Server.Cache.Password, DB: cfg.Server.Cache.DB,
			TLS: cfg.Server.Cache.TLS, TLSSkipVerify: cfg.Server.Cache.TLSSkipVerify,
			TLSCA: cfg.Server.Cache.TLSCA, TLSCert: cfg.Server.Cache.TLSCert, TLSKey: cfg.Server.Cache.TLSKey,
			PoolSize: cfg.Server.Cache.PoolSize, MinIdle: cfg.Server.Cache.MinIdle,
			Timeout: cacheTimeout, Prefix: cfg.Server.Cache.Prefix, ConfigDir: p.Config,
		})
		cancelCache()
		if cacheErr != nil {
			cacheHealth = func(context.Context) string { return "error" }
			_ = accessLog.WriteLine(applog.LevelWarn, "cache: external cache unavailable; using in-memory cache\n")
		} else {
			appCache = redisCache
			closeCache = func() { _ = redisCache.Close() }
			cacheHealth = func(parent context.Context) string {
				ctx, cancel := context.WithTimeout(parent, time.Second)
				defer cancel()
				if redisCache.Check(ctx) != nil {
					return "error"
				}
				return "ok"
			}
		}
	case "none":
		appCache = nil
	}
	_, redisCacheMetrics := appCache.(*cache.Redis)
	if appMetrics != nil && redisCacheMetrics {
		if metricsCtx == nil {
			var cancelCacheMetrics context.CancelFunc
			metricsCtx, cancelCacheMetrics = context.WithCancel(context.Background())
			previousStopMetrics := stopMetrics
			stopMetrics = func() { cancelCacheMetrics(); previousStopMetrics() }
		}
		appMetrics.StartCacheCollector(metricsCtx, appCache, "rate_limit")
	}

	// Audit log (AI.md PART 11 "Audit Log Format"), consumed by the PART 21
	// backup events.
	auditLog, err := applog.NewAuditLogger(filepath.Join(p.Logs, "audit.log"))
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	defer auditLog.Close()
	auditPolicy, _ := cfg.Server.Logs.EffectiveLogKind("audit")
	if err := auditLog.SetRotationPolicy(auditPolicy.Rotate, auditPolicy.Keep, auditPolicy.Compress); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	auditLog.SetMirror(func(entry applog.Entry) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		mirror := db.AuditMirrorEntry{
			AuditID: entry.ID, Timestamp: entry.Time.Unix(), Level: string(entry.Severity),
			Category: entry.Category, Action: entry.Event, ActorIP: entry.Actor.IP,
			Details: entry.Details, Success: entry.Result == applog.ResultSuccess,
		}
		if entry.Target != nil {
			mirror.TargetType, mirror.TargetID = entry.Target.Type, entry.Target.ID
		}
		return db.InsertAuditMirror(ctx, sqlDB, mirror)
	})
	for key, wasGenerated := range map[string]bool{
		"server.token":                          generated,
		"server.security.encryption_key":        keyGenerated,
		"server.metrics.auth.tokens.prometheus": metricsTokensGenerated && metricsTokenWasEmpty["server.metrics.auth.tokens.prometheus"],
		"server.metrics.auth.tokens.grafana":    metricsTokensGenerated && metricsTokenWasEmpty["server.metrics.auth.tokens.grafana"],
		"server.metrics.auth.tokens.loki":       metricsTokensGenerated && metricsTokenWasEmpty["server.metrics.auth.tokens.loki"],
	} {
		if wasGenerated {
			_ = auditLog.Write(applog.Entry{Event: "config.secret_generated", Category: "config", Severity: applog.SeverityInfo, Details: map[string]any{"key": key}, Result: applog.ResultSuccess})
		}
	}
	for _, key := range webhookSecretsGenerated {
		_ = auditLog.Write(applog.Entry{Event: "config.secret_generated", Category: "config", Severity: applog.SeverityInfo, Details: map[string]any{"key": key}, Result: applog.ResultSuccess})
	}

	// installation_secret (AI.md PART 11 "Cryptographic Keys") backs the
	// rotating {security_id} published in security.txt. A failure here is
	// not fatal: the server still serves, security.txt simply omits the
	// contact-form line rather than publishing a forgeable id.
	installSecret := ""
	previousInstallSecret := ""
	secretKeys := security.NewSecretKeyring(nil, nil, nil, nil)
	if !offlineDatabase {
		installSecret, _, _, err = db.EnsureCoreSecrets(context.Background(), sqlDB)
		if err != nil {
			fmt.Fprintln(os.Stderr, binaryName+": core secret initialization failed: "+err.Error())
			return 1
		}
		if err := maintainProjectSecrets(context.Background(), sqlDB, auditLog, secretKeys, time.Now().UTC()); err != nil {
			fmt.Fprintln(os.Stderr, binaryName+": secret maintenance failed: "+err.Error())
			return 1
		}
		if installSecret != "" {
			if err := syncDNSCredentialState(sqlDB, cfg, installSecret); err != nil {
				fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
				return 1
			}
		}
		previousInstallSecret, _ = db.GetGraceSecret(context.Background(), sqlDB, db.SecretInstallation)
	}
	if !offlineDatabase && previousInstallSecret != "" {
		store := pgp.NewStore(p.Config)
		if store.HasKeypair() {
			if _, err := store.ReadPrivateKey(installSecret); err != nil {
				private, previousErr := store.ReadPrivateKey(previousInstallSecret)
				if previousErr != nil {
					_ = auditLog.Write(applog.Entry{Event: "security.decrypt_failed", Category: "security", Severity: applog.SeverityError, Details: map[string]any{"table": "pgp_keys", "row_id": "project"}, Result: applog.ResultFailure})
					fmt.Fprintln(os.Stderr, binaryName+": previous installation key could not decrypt the PGP private key")
					return 1
				}
				if err := store.Write(private, installSecret); err != nil {
					fmt.Fprintln(os.Stderr, binaryName+": PGP key rotation recovery failed: "+err.Error())
					return 1
				}
			}
		}
	}
	sqlDB.SetRecoveryHook(func(recoveryCtx context.Context) error {
		if err := initializeRemoteDatabase(recoveryCtx, sqlDB, tokenKey, p.ConfigFile, p.Logs, binaryName); err != nil {
			return err
		}
		install, _, _, err := db.EnsureCoreSecrets(recoveryCtx, sqlDB)
		if err != nil {
			return err
		}
		if err := maintainProjectSecrets(recoveryCtx, sqlDB, auditLog, secretKeys, time.Now().UTC()); err != nil {
			return err
		}
		keys, err := loadProjectSecretKeys(recoveryCtx, sqlDB)
		if err != nil {
			return err
		}
		cookie, previousCookie := keys.CookieKeys()
		csrf, previousCSRF := keys.CSRFKeys()
		secretKeys.Replace(cookie, previousCookie, csrf, previousCSRF)
		if previousInstall, err := db.GetGraceSecret(recoveryCtx, sqlDB, db.SecretInstallation); err == nil && previousInstall != "" {
			store := pgp.NewStore(p.Config)
			if store.HasKeypair() {
				if _, err := store.ReadPrivateKey(install); err != nil {
					private, previousErr := store.ReadPrivateKey(previousInstall)
					if previousErr != nil {
						_ = auditLog.Write(applog.Entry{Event: "security.decrypt_failed", Category: "security", Severity: applog.SeverityError, Details: map[string]any{"table": "pgp_keys", "row_id": "project"}, Result: applog.ResultFailure})
						return errors.New("previous installation key could not decrypt the PGP private key")
					}
					if err := store.Write(private, install); err != nil {
						return fmt.Errorf("PGP key rotation recovery failed: %w", err)
					}
				}
			}
		} else if err != nil && !errors.Is(err, db.ErrNotFound) {
			return err
		}
		return syncDNSCredentialState(sqlDB, cfg, install)
	})

	// GeoIP (AI.md PART 19): opens whatever MMDB files already exist in
	// cfg.Server.GeoIP.Dir (none on a genuinely first run) and, if enabled,
	// kicks off a background download so first run still works with zero
	// config instead of blocking startup on a network fetch.
	geoManager := geoip.Open(cfg.Server.GeoIP.Dir, cfg.Server.GeoIP.Enabled, cfg.Server.GeoIP.Databases)
	if cfg.Server.GeoIP.Enabled {
		geoDownloadCtx, cancelGeoDownload := context.WithCancel(context.Background())
		geoDownloadDone := make(chan struct{})
		defer func() {
			cancelGeoDownload()
			<-geoDownloadDone
		}()
		go func() {
			defer close(geoDownloadDone)
			ctx, cancel := context.WithTimeout(geoDownloadCtx, 5*time.Minute)
			defer cancel()
			if err := downloadGeoIP(ctx, cfg.Server.GeoIP.Dir, cfg.Server.GeoIP.Databases); err != nil {
				_ = accessLog.WriteLine(applog.LevelError, "geoip: initial download failed: "+err.Error())
				return
			}
			geoManager.Reload()
		}()
	}
	blocklistManager, err := blocklist.Open(filepath.Join(p.Data, "security", "blocklists"), cfg.Server.Security.Blocklists, cfg.Server.TrustedProxies.Additional...)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	blocklistCtx, cancelBlocklistUpdate := context.WithCancel(context.Background())
	blocklistDone := make(chan struct{})
	defer func() { cancelBlocklistUpdate(); <-blocklistDone }()
	go func() {
		defer close(blocklistDone)
		ctx, cancel := context.WithTimeout(blocklistCtx, 5*time.Minute)
		defer cancel()
		if err := blocklistManager.Update(ctx); err != nil {
			_ = accessLog.WriteLine(applog.LevelWarn, "blocklist: initial update incomplete: "+err.Error()+"\n")
		}
	}()

	fqdnHost := fqdn.GetFQDN(internalName)

	// Email notifications (AI.md PART 17). Built before the scheduler and
	// the HTTP server because both raise events through it.
	notifier := newNotifier(cfg, p, fqdnHost)
	startupNotifications(binaryName, notifier, cfg, p, fqdnHost, accessLog)
	var maintenanceMu sync.Mutex
	var maintenanceSince time.Time
	sqlDB.SetMaintenanceHook(func(offline bool, status db.RecoveryStatus) {
		event := "server.maintenance_recovered"
		severity := applog.SeverityInfo
		result := applog.ResultSuccess
		message := "Server recovered from database maintenance mode"
		if offline {
			maintenanceMu.Lock()
			maintenanceSince = status.Since
			maintenanceMu.Unlock()
			event = "server.maintenance_entered"
			severity = applog.SeverityWarn
			result = applog.ResultFailure
			message = "Database connection failed; server entered maintenance mode"
		}
		_ = auditLog.Write(applog.Entry{Event: event, Category: "server", Severity: severity, Details: map[string]any{"reason": "database_connection", "attempts": status.Attempts}, Result: result})
		if (offline && cfg.Server.Maintenance.Notify.OnEnter) || (!offline && cfg.Server.Maintenance.Notify.OnExit) {
			since := status.Since
			if !offline {
				maintenanceMu.Lock()
				since = maintenanceSince
				maintenanceMu.Unlock()
			}
			vars := map[string]string{"since": since.Format(time.RFC3339), "error": message}
			if err := notifier.Send(notify.EventDatabaseError, vars); err != nil && !errors.Is(err, notify.ErrDisabled) {
				_ = accessLog.WriteLine(applog.LevelWarn, "database maintenance notification failed: "+err.Error())
			}
		}
	})
	sqlDB.SetRecoveryExhaustedHook(func(attempts uint64) {
		_ = auditLog.Write(applog.Entry{Event: "server.self_healing_exhausted", Category: "server", Severity: applog.SeverityError, Details: map[string]any{"reason": "database_connection", "attempts": attempts}, Result: applog.ResultFailure})
		if err := notifier.Send(notify.EventDatabaseError, map[string]string{"since": sqlDB.RecoveryStatus().Since.Format(time.RFC3339), "error": "Database recovery retries exhausted; operator action is required"}); err != nil && !errors.Is(err, notify.ErrDisabled) {
			_ = accessLog.WriteLine(applog.LevelWarn, "database recovery exhaustion notification failed: "+err.Error())
		}
	})

	// TLS (AI.md PART 15 "Built-in Let's Encrypt Support"): initialize
	// managed certificate providers before the scheduler so ssl_renewal can
	// invoke the same provider used by the HTTPS listener.
	scheme := "http"
	tlsConfig, httpChallengeHandler, tlsErr := ssl.BuildTLSConfig(ssl.Options{
		Config:       cfg,
		ConfigDir:    p.Config,
		InternalName: internalName,
		Database:     sqlDB,
		OnRenewed: func(host string, notAfter time.Time) {
			if err := sendSSLRenewed(notifier, host, notAfter); err != nil {
				fmt.Fprintln(os.Stderr, "shortner: WARN: send SSL renewal notification:", err)
			}
		},
	})
	if tlsErr != nil {
		fmt.Fprintln(os.Stderr, binaryName+": ERROR: "+tlsErr.Error())
		if tlsConfig != nil {
			scheme = "https"
		}
	} else if tlsConfig != nil {
		scheme = "https"
	}

	// Overlay networks (AI.md PART 31): built before the scheduler and the
	// HTTP server because the tor_health/i2p_health tasks and
	// /server/healthz both report on them. Nothing is started here — see
	// startOverlays below, after the HTTP server exists.
	overlays := &overlayRef{}
	torManager, i2pManager := newOverlayManagers(context.Background(), cfg, p, overlays, func(format string, args ...any) {
		// AI.md PART 31.1 "Logging Rules": the bootstrap success line
		// (`Tor: {onion_address}`) is printed once, on the console, and
		// mirrored into the access log for later inspection.
		line := fmt.Sprintf(format, args...)
		fmt.Println(line)
		_ = accessLog.WriteLine(applog.LevelInfo, line)
	})
	if appMetrics != nil {
		appMetrics.StartTorCollector(context.Background(), torManager)
	}

	sched, err := newBuiltinScheduler(cfg, sqlDB, schedulerLog, accessLog, p, fqdnHost, geoManager, blocklistManager, auditLog, notifier, torManager, i2pManager, tlsConfig, func(ctx context.Context) error {
		return maintainProjectSecrets(ctx, sqlDB, auditLog, secretKeys, time.Now().UTC())
	})
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

	srv := server.New(server.Options{
		Config:               cfg,
		DB:                   sqlDB,
		Store:                sqlDB,
		DataDir:              p.Data,
		CacheDir:             p.Cache,
		AccessLog:            accessLog,
		Version:              version.Version,
		CommitID:             version.CommitID,
		BuildDate:            version.BuildDate,
		StartTime:            time.Now(),
		TLSConfig:            tlsConfig,
		BoundListeners:       privilegedListeners,
		HTTPChallengeHandler: httpChallengeHandler,
		GeoIP:                geoManager,
		Blocklists:           blocklistManager,
		Metrics:              appMetrics,

		ConfigDir:             p.Config,
		Cache:                 appCache,
		CacheHealth:           cacheHealth,
		Scheduler:             sched,
		ContactConfigPath:     filepath.Join(p.Config, "server.yml"),
		AuditLog:              auditLog,
		InstallSecret:         installSecret,
		PreviousInstallSecret: previousInstallSecret,
		SecretKeys:            secretKeys,
		Notifier:              notifier,
		Tor:                   torManager,
		I2P:                   i2pManager,
	})
	overlays.set(srv)
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
	cveOptions := cve.Options{SecurityDir: cfg.Data.SecurityDir, NVDSource: cfg.Data.CVE.Source, APIKey: cfg.Data.CVE.APIKey, TrivyEnabled: cfg.Data.Trivy.Enabled, TrivySource: cfg.Data.Trivy.Source}
	if cve.NeedsInitialUpdate(cveOptions) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		updateDone := make(chan struct{})
		defer func() {
			cancel()
			<-updateDone
		}()
		go func() {
			defer close(updateDone)
			if err := sched.RunNow(ctx, "cve_update"); err != nil {
				_ = accessLog.WriteLine(applog.LevelWarn, "cve: initial security database update failed: "+err.Error()+"\n")
			}
		}()
	}

	pidPath := p.PIDFile
	signal.Register(func() { srv.Shutdown() })
	// AI.md PART 31: the overlay providers are stopped right after the
	// listeners are drained, so no onion or eepsite address stays published
	// for a service that is going away.
	signal.Register(func() { stopOverlays(overlays, torManager, i2pManager) })
	signal.Register(func() { _ = sched.Stop() })
	// AI.md PART 17 `shutdown` event: sent after the listener is closed and
	// the scheduler is stopped, while the logs and SMTP connection are
	// still usable.
	signal.Register(func() { _ = notifier.Send(notify.EventShutdown, nil) })
	signal.Register(func() { _ = geoManager.Close() })
	if closeCache != nil {
		signal.Register(closeCache)
	}
	signal.Register(stopMetrics)
	signal.Register(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := sqlDB.Checkpoint(ctx); err != nil {
			_ = accessLog.WriteLine(applog.LevelError, "db: WAL checkpoint failed: "+err.Error())
		}
		_ = sqlDB.Close()
	})
	signal.Register(func() { accessLog.Close() })
	signal.Register(func() { schedulerLog.Close() })
	signal.Register(func() { pidfile.RemovePIDFile(pidPath) })

	// Non-blocking: installs OS signal handlers and returns immediately;
	// the shutdown hooks registered above run when a signal arrives.
	signal.Start()

	host := cfg.Server.Listen
	if host == "0.0.0.0" || host == "" {
		host = "localhost"
	}
	banner.PrintStartupBanner(banner.BannerConfig{
		AppName:         binaryName,
		Version:         version.Version,
		AppMode:         mode.GetCurrentAppMode().String(),
		Debug:           mode.IsDebugEnabled(),
		URLs:            startupURLsForConfiguredPorts(cfg, p, scheme, host),
		StartupDateTime: banner.FormatStartupDateTime(time.Now()),
		Lang:            cliLang,
		Setup:           firstRunSetup(generated, cfg.Server.Token, p.ConfigFile),
	})
	// Start overlays after the single banner write so their asynchronous
	// startup messages cannot split the banner.
	go startOverlays(binaryName, torManager, i2pManager)

	// Start blocks until Shutdown is called by the signal hook above.
	if err := startHTTPServer(srv); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	return 0
}

func selectListenAddress(flagAddress, envAddress, configAddress, defaultAddress string) string {
	for _, address := range []string{flagAddress, envAddress, configAddress, defaultAddress} {
		if address = strings.TrimSpace(address); address != "" {
			return address
		}
	}
	return defaultListenAddress
}

func firstRunSetup(firstRun bool, token, configFile string) []string {
	if !firstRun {
		return nil
	}
	return []string{
		"First run setup",
		"Operator token (save this value): " + token,
		"Configuration: " + configFile,
	}
}

func startupURLsForConfiguredPorts(cfg *config.Config, p paths.Paths, scheme, host string) []string {
	parts := strings.Split(strings.TrimSpace(cfg.Server.Port), ",")
	if len(parts) != 2 {
		return startupURLs(cfg, p, scheme, host)
	}
	first, firstErr := strconv.Atoi(strings.TrimSpace(parts[0]))
	second, secondErr := strconv.Atoi(strings.TrimSpace(parts[1]))
	if firstErr != nil || secondErr != nil || first < 0 || first > 65535 || second < 0 || second > 65535 {
		return startupURLs(cfg, p, scheme, host)
	}
	base := *cfg
	base.Server.Port = strconv.Itoa(first)
	urls := startupURLs(&base, p, "http", host)
	if scheme == "https" {
		address := net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(second))
		urls = append(urls, fmt.Sprintf("https://%s%s", address, cfg.Server.BaseURL))
	}
	return urls
}

func bindRootPorts(address, setting string) (map[string]net.Listener, error) {
	if !paths.IsPrivileged() {
		return nil, nil
	}
	listeners := make(map[string]net.Listener)
	for _, part := range strings.Split(strings.TrimSpace(setting), ",") {
		port, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return nil, fmt.Errorf("invalid listen port %q: %w", part, err)
		}
		if port >= 1024 {
			continue
		}
		addr := net.JoinHostPort(strings.Trim(address, "[]"), strconv.Itoa(port))
		listener, fallback, err := netutil.ListenTCP(addr)
		if err != nil {
			for _, opened := range listeners {
				_ = opened.Close()
			}
			return nil, fmt.Errorf("bind privileged port %s: %w", addr, err)
		}
		if fallback {
			warnIPv4Fallback(filepath.Base(os.Args[0]))
		}
		listeners[addr] = listener
	}
	return listeners, nil
}

func warnIPv4Fallback(binaryName string) {
	ipv4FallbackWarning.Do(func() {
		fmt.Fprintf(os.Stderr, "%s: warning: IPv6 is unavailable; listening on IPv4 address 0.0.0.0\n", binaryName)
	})
}

func validPortSetting(setting string) bool {
	parts := strings.Split(strings.TrimSpace(setting), ",")
	if len(parts) < 1 || len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		port, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || port < 1 || port > 65535 {
			return false
		}
	}
	return true
}

func validateUnprivilegedPortSetting(setting string) error {
	if !validPortSetting(setting) {
		return fmt.Errorf("invalid listen port %q", setting)
	}
	for _, part := range strings.Split(setting, ",") {
		port, _ := strconv.Atoi(strings.TrimSpace(part))
		if port < 1024 {
			return fmt.Errorf("unprivileged starts require ports >= 1024 (got %d)", port)
		}
	}
	return nil
}

func syncDNSCredentialState(sqlDB db.Handle, cfg *config.Config, installSecret string) error {
	credentials, err := config.DNSCredentialValues(cfg)
	if err != nil {
		return err
	}
	fingerprint := certmgr.DNSConfigFingerprint(cfg.Server.TLS.LetsEncrypt.DNSProvider, credentials, installSecret)
	ctx := context.Background()
	previous, err := db.GetSecret(ctx, sqlDB, db.SecretDNSFingerprint)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return fmt.Errorf("DNS credential state: read fingerprint")
	}
	if err != nil || previous != fingerprint {
		if err := db.DeleteSecret(ctx, sqlDB, db.SecretDNSValidatedAt); err != nil {
			return fmt.Errorf("DNS credential state: clear validation timestamp")
		}
	}
	if err := db.SetSecret(ctx, sqlDB, db.SecretDNSFingerprint, fingerprint); err != nil {
		return fmt.Errorf("DNS credential state: store fingerprint")
	}
	return nil
}

func sendSSLRenewed(notifier *notify.Notifier, host string, notAfter time.Time) error {
	if notifier == nil {
		return nil
	}
	validUntil := notAfter.UTC()
	days := int(validUntil.Sub(time.Now().UTC()).Hours() / 24)
	if days < 0 {
		days = 0
	}
	return notifier.Send(notify.EventSSLRenewed, map[string]string{
		"fqdn":        host,
		"expires_in":  fmt.Sprintf("%d days", days),
		"expiry_date": validUntil.Format(time.RFC3339),
		"valid_until": validUntil.Format(time.RFC3339),
	})
}

func portIncludes(setting string, wanted int) bool {
	for _, part := range strings.Split(setting, ",") {
		port, err := strconv.Atoi(strings.TrimSpace(part))
		if err == nil && port == wanted {
			return true
		}
	}
	return false
}

// newBuiltinScheduler builds a scheduler.Scheduler with every AI.md
// PART 18 "Built-in Tasks (Required)" registered, using cfg's per-task
// schedule/enabled overrides. It does not start the scheduler.
func newBuiltinScheduler(cfg *config.Config, sqlDB db.Handle, schedulerLog, accessLog *applog.Logger, p paths.Paths, host string, geoManager *geoip.Manager, blocklistManager *blocklist.Manager, auditLog *applog.AuditLogger, notifier *notify.Notifier, torManager *tor.Manager, i2pManager *i2p.Manager, tlsConfig *tls.Config, maintainSecrets func(context.Context) error) (*scheduler.Scheduler, error) {
	sched, err := scheduler.New(sqlDB, schedulerLog, cfg.Server.Scheduler.Timezone, cfg.Server.Scheduler.CatchUpWindow)
	if err != nil {
		return nil, err
	}
	dbProbe, _ := sqlDB.(interface{ Probe(context.Context) error })

	deps := scheduler.Deps{
		DB:                      sqlDB,
		DBProbe:                 dbProbe,
		DatabaseError:           cfg.Server.Notifications.DatabaseError,
		Tokens:                  cfg.Server.Tokens,
		Logs:                    []*applog.Logger{schedulerLog, accessLog},
		AuditLog:                auditLog,
		DiskPaths:               []string{p.Data, p.Logs, p.Backup},
		TempDir:                 filepath.Join(os.TempDir(), "apimgr"),
		TempPrefix:              paths.InternalName + "-",
		MaintenanceCleanup:      cfg.Server.Maintenance.Cleanup,
		DiskLowEnabled:          cfg.Server.Notifications.DiskLow.Enabled,
		DiskLowThresholdPercent: cfg.Server.Notifications.DiskLow.ThresholdPercent,
		DiskCheckInterval:       cfg.Server.Notifications.DiskLow.CheckInterval,
		DiskLowRepeat: func() time.Duration {
			d, _ := config.ParseDuration(cfg.Server.Notifications.DiskLow.Repeat, 24*time.Hour)
			return d
		}(),
		Logger:                      schedulerLog,
		MaintainSecrets:             maintainSecrets,
		TLSEnabled:                  cfg.Server.TLS.Enabled || portIncludes(cfg.Server.Port, 443),
		ConfigDir:                   p.Config,
		FQDN:                        host,
		DevTLD:                      fqdn.IsDevTLD(host, internalName),
		TLSConfig:                   tlsConfig,
		CVEOptions:                  cve.Options{SecurityDir: cfg.Data.SecurityDir, NVDSource: cfg.Data.CVE.Source, APIKey: cfg.Data.CVE.APIKey, TrivyEnabled: cfg.Data.Trivy.Enabled, TrivySource: cfg.Data.Trivy.Source},
		SecurityReportRetentionDays: cfg.Server.Security.Reports.RetentionDays,
		PGPStore:                    pgp.NewStore(p.Config),
		Keyservers:                  cfg.Web.Security.Keyservers,
		GeoIP:                       geoManager,
		GeoIPCfg:                    cfg.Server.GeoIP,
		Blocklists:                  blocklistManager,
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
		// AI.md PART 18/31: tor_health and i2p_health probe the live
		// managers every 10 minutes and restart a provider that stopped
		// answering. i2p_health is disabled by default because I2P is.
		Tor: torManager,
		I2P: i2pManager,
	}
	ctx := context.Background()
	registered := scheduler.BuiltinTasks(cfg.Server.Scheduler, deps)
	taskIDs := make([]string, 0, len(registered))
	for _, t := range registered {
		if err := sched.Register(ctx, t); err != nil {
			return nil, err
		}
		taskIDs = append(taskIDs, t.ID)
	}
	if schedulerLog != nil {
		_ = schedulerLog.WriteLine(applog.LevelInfo, "scheduler: registered tasks: "+strings.Join(taskIDs, ", ")+"\n")
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
