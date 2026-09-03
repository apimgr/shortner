// --maintenance pgp handling. See AI.md PART 11 "GPG Keypair Management":
// one project-level OpenPGP keypair, managed only through the existing
// --maintenance dispatcher, authorized like every other sensitive
// operation (operator token OR root). There is deliberately no web UI and
// no admin API route for any of this.
package main

import (
	"bufio"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/paths"
	"github.com/apimgr/shortner/src/pgp"
)

// pgpHelp is the `--maintenance pgp` usage text.
const pgpHelp = `Usage: %s --maintenance pgp ACTION [ARG]

Manage the project OpenPGP keypair (AI.md PART 11).

Actions:
  generate              Generate the Ed25519/Curve25519 keypair and publish it
  rotate                Generate a replacement key, signed by the current key
  publish               Re-publish the public key to the configured keyservers
  export public [PATH]  Write the public key to PATH (stdout when omitted)
  export private PATH   Write the decrypted private key to PATH (mode 0600)
  import FILE           Import an ASCII-armored private key from FILE
  delete                Delete both keys and stop advertising them
`

// pgpExportInterval is the AI.md PART 11 rate limit on private-key
// exports: "rate-limited to 1 per hour per operator".
const pgpExportInterval = time.Hour

// pgpEnv is the resolved state every pgp action needs.
type pgpEnv struct {
	paths         paths.Paths
	cfg           *config.Config
	sqlDB         *sql.DB
	store         *pgp.Store
	audit         *applog.AuditLogger
	installSecret string
}

// newPGPEnv loads configuration, opens the database, and resolves the
// installation_secret the private key is sealed under.
func newPGPEnv(p paths.Paths) (*pgpEnv, error) {
	cfg, err := config.Load(p.ConfigFile, filepath.Join(p.DB, "server.db"))
	if err != nil {
		return nil, err
	}

	sqlDB, err := db.Open(cfg.Server.Database.URL, db.DefaultPool(), nil)
	if err != nil {
		return nil, err
	}

	secret, err := db.EnsureSecret(context.Background(), sqlDB, db.SecretInstallation)
	if err != nil {
		sqlDB.Close()
		return nil, err
	}

	env := &pgpEnv{
		paths:         p,
		cfg:           cfg,
		sqlDB:         sqlDB,
		store:         pgp.NewStore(p.Config),
		installSecret: secret,
	}
	if logger, err := applog.NewAuditLogger(filepath.Join(p.Logs, "audit.log")); err == nil {
		env.audit = logger
	}
	return env, nil
}

// close releases the database handle and audit log.
func (e *pgpEnv) close() {
	if e.audit != nil {
		_ = e.audit.Close()
	}
	if e.sqlDB != nil {
		_ = e.sqlDB.Close()
	}
}

// identityEmail resolves the address used in the keypair's user id, in the
// same order the security pages resolve the published security contact:
// the security.txt contact override, then the RFC 2142 security@ role
// mailbox, then security@{fqdn}.
func (e *pgpEnv) identityEmail() string {
	if c := strings.TrimSpace(e.cfg.Web.Security.Contact); c != "" {
		return strings.TrimPrefix(c, "mailto:")
	}
	if c := strings.TrimSpace(e.cfg.Server.Contact.Security.Email); c != "" {
		return c
	}
	if host := pgpFallbackHost(e.cfg); host != "" {
		return "security@" + host
	}
	return ""
}

// pgpFallbackHost derives a hostname for the fallback security address
// from the configured base URL, falling back to the machine's own name.
func pgpFallbackHost(cfg *config.Config) string {
	if base := strings.TrimSpace(cfg.Server.BaseURL); base != "" {
		trimmed := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://"), "/")
		if idx := strings.IndexAny(trimmed, "/:"); idx > 0 {
			trimmed = trimmed[:idx]
		}
		if trimmed != "" {
			return trimmed
		}
	}
	host, err := os.Hostname()
	if err != nil {
		return ""
	}
	return host
}

// runPGP dispatches `--maintenance pgp ACTION [ARG]` and returns the
// process exit code.
func runPGP(binaryName string, p paths.Paths, args []string) int {
	action := argAt(args, 0)
	switch action {
	case "", "help", "--help", "-h":
		fmt.Printf(pgpHelp, binaryName)
		return 0
	}

	env, err := newPGPEnv(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	defer env.close()

	switch action {
	case "generate":
		return pgpGenerate(binaryName, env)
	case "rotate":
		return pgpRotate(binaryName, env)
	case "publish":
		return pgpPublish(binaryName, env)
	case "export":
		return pgpExport(binaryName, env, argAt(args, 1), argAt(args, 2))
	case "import":
		return pgpImport(binaryName, env, argAt(args, 1))
	case "delete":
		return pgpDelete(binaryName, env)
	default:
		fmt.Fprintf(os.Stderr, "%s: unknown --maintenance pgp action %q (run '%s --maintenance pgp --help')\n",
			binaryName, action, binaryName)
		return 1
	}
}

// pgpGenerate implements `--maintenance pgp generate`.
func pgpGenerate(binaryName string, env *pgpEnv) int {
	if env.store.HasKeypair() {
		fmt.Fprintf(os.Stderr, "%s: a keypair already exists at %s (use 'pgp rotate' to replace it)\n",
			binaryName, env.store.PublicKeyPath())
		return 1
	}

	email := env.identityEmail()
	if email == "" {
		fmt.Fprintf(os.Stderr, "%s: no security contact address is configured (set server.contact.security.email)\n", binaryName)
		return 1
	}

	now := time.Now().UTC()
	fmt.Println("Generating Ed25519 signing key with a Curve25519 encryption subkey...")
	key, err := pgp.Generate(appName(env.cfg), email, now)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}

	if err := env.store.Write(key, env.installSecret); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}

	record := db.PGPKey{
		Fingerprint: key.Fingerprint,
		CreatedAt:   key.CreatedAt,
		ExpiresAt:   key.ExpiresAt,
	}
	if err := db.UpsertPGPKey(context.Background(), env.sqlDB, record); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}

	pgpAudit(env, "security.pgp_key_generated", applog.SeverityInfo, applog.ResultSuccess, key.Fingerprint, "", nil)
	fmt.Printf("Identity:    %s\nFingerprint: %s\nExpires:     %s\nPublic key:  %s\nPrivate key: %s (encrypted)\n",
		pgp.Identity(appName(env.cfg), email), key.Fingerprint,
		key.ExpiresAt.Format(time.RFC3339), env.store.PublicKeyPath(), env.store.PrivateKeyPath())

	// AI.md PART 11: publication is "triggered automatically on
	// generate/rotate".
	publishStored(env, key.Fingerprint)
	return 0
}

// pgpRotate implements `--maintenance pgp rotate`.
func pgpRotate(binaryName string, env *pgpEnv) int {
	old, err := env.store.ReadPrivateKey(env.installSecret)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}

	email := env.identityEmail()
	if email == "" {
		fmt.Fprintf(os.Stderr, "%s: no security contact address is configured (set server.contact.security.email)\n", binaryName)
		return 1
	}

	now := time.Now().UTC()
	key, err := pgp.Generate(appName(env.cfg), email, now)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}

	// AI.md PART 11 "Rotate": the new public key is signed by the old key
	// so anyone holding the retired key can verify the replacement.
	if err := pgp.SignPublicKeyOf(key, old, now); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}

	if err := env.store.Retire(now); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	if err := env.store.Write(key, env.installSecret); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}

	record := db.PGPKey{
		Fingerprint:   key.Fingerprint,
		CreatedAt:     key.CreatedAt,
		ExpiresAt:     key.ExpiresAt,
		LastRotatedAt: now,
	}
	if err := db.UpsertPGPKey(context.Background(), env.sqlDB, record); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}

	pgpAudit(env, "security.pgp_key_rotated", applog.SeverityInfo, applog.ResultSuccess, key.Fingerprint,
		"", map[string]any{"previous_fingerprint": old.Fingerprint})
	fmt.Printf("Rotated %s -> %s\nPrevious key remains valid for %d days.\n",
		old.Fingerprint, key.Fingerprint, pgp.PreviousKeyGraceDays)

	publishStored(env, key.Fingerprint)
	return 0
}

// pgpPublish implements `--maintenance pgp publish`.
func pgpPublish(binaryName string, env *pgpEnv) int {
	if !env.store.HasKeypair() {
		fmt.Fprintln(os.Stderr, binaryName+": "+pgp.ErrNoKeypair.Error())
		return 1
	}
	key, err := env.store.ReadPrivateKey(env.installSecret)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	if !publishStored(env, key.Fingerprint) {
		return 1
	}
	return 0
}

// publishStored submits the public key to every configured keyserver and
// records the outcome in both the on-disk state file and the DB row. It
// reports whether every submission succeeded.
func publishStored(env *pgpEnv, fingerprint string) bool {
	servers := env.cfg.Web.Security.Keyservers
	if len(servers) == 0 {
		fmt.Println("No keyservers configured (web.security.keyservers is empty); skipping publication.")
		return true
	}

	armored, err := env.store.ReadPublicKey()
	if err != nil {
		fmt.Fprintln(os.Stderr, "publish: "+err.Error())
		return false
	}

	fmt.Printf("Publishing %s to %d keyserver(s)...\n", fingerprint, len(servers))
	results := pgp.Publish(context.Background(), nil, servers, armored, time.Now)

	ok := true
	published := map[string]time.Time{}
	for _, res := range results {
		if res.OK() {
			published[res.Keyserver] = res.At.UTC()
			fmt.Printf("  %s: published\n", res.Keyserver)
			continue
		}
		ok = false
		fmt.Fprintf(os.Stderr, "  %s: failed after %d attempt(s): %s\n", res.Keyserver, res.Attempts, res.Err)
	}

	if err := env.store.SaveKeyserverState(fingerprint, results); err != nil {
		fmt.Fprintln(os.Stderr, "publish: "+err.Error())
		ok = false
	}

	if len(published) > 0 {
		if record, err := db.GetPGPKey(context.Background(), env.sqlDB, fingerprint); err == nil {
			if record.KeyserversPublished == nil {
				record.KeyserversPublished = map[string]time.Time{}
			}
			for server, at := range published {
				record.KeyserversPublished[server] = at
			}
			if err := db.UpsertPGPKey(context.Background(), env.sqlDB, *record); err != nil {
				fmt.Fprintln(os.Stderr, "publish: "+err.Error())
				ok = false
			}
		}
	}

	result := applog.ResultSuccess
	if !ok {
		result = applog.ResultFailure
	}
	pgpAudit(env, "security.pgp_key_published", applog.SeverityInfo, result, fingerprint, "",
		map[string]any{"keyservers": len(servers), "published": len(published)})
	return ok
}

// pgpExport implements `--maintenance pgp export {public|private} [PATH]`.
func pgpExport(binaryName string, env *pgpEnv, kind, path string) int {
	switch kind {
	case "public":
		return pgpExportPublic(binaryName, env, path)
	case "private":
		return pgpExportPrivate(binaryName, env, path)
	default:
		fmt.Fprintf(os.Stderr, "%s: --maintenance pgp export requires 'public' or 'private'\n", binaryName)
		return 1
	}
}

// pgpExportPublic writes the public key to path, or to stdout when the
// path is omitted.
func pgpExportPublic(binaryName string, env *pgpEnv, path string) int {
	armored, err := env.store.ReadPublicKey()
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	if path == "" {
		fmt.Print(armored)
		return 0
	}
	if err := os.WriteFile(path, []byte(armored), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	fmt.Printf("Public key written to %s\n", path)
	return 0
}

// pgpExportPrivate implements AI.md PART 11's sensitive-operation flow for
// the private key: operator authorization, a typed reason, an audit entry
// carrying the operator IP, a 0600 output file, and a one-per-hour limit.
func pgpExportPrivate(binaryName string, env *pgpEnv, path string) int {
	if path == "" {
		fmt.Fprintf(os.Stderr, "%s: --maintenance pgp export private requires an output path\n", binaryName)
		return 1
	}

	operator := pgpOperator()
	last, err := pgpLastExport(env.store, operator)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	if wait := pgpExportInterval - time.Since(last); !last.IsZero() && wait > 0 {
		fmt.Fprintf(os.Stderr, "%s: private-key export is limited to one per hour per operator (try again in %s)\n",
			binaryName, wait.Round(time.Second))
		return 1
	}

	if code := authorizePGPSensitive(binaryName, env); code != 0 {
		return code
	}

	reason, err := promptText("Reason for exporting the private key: ")
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	if reason == "" {
		fmt.Fprintf(os.Stderr, "%s: a reason is required for a private-key export\n", binaryName)
		return 1
	}

	key, err := env.store.ReadPrivateKey(env.installSecret)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	armored, err := key.ArmorPrivate()
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	if err := os.WriteFile(path, []byte(armored), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}

	if err := pgpRecordExport(env.store, operator, time.Now().UTC()); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
	}

	pgpAudit(env, "security.private_key_exported", applog.SeverityCritical, applog.ResultSuccess,
		key.Fingerprint, reason, map[string]any{"operator": operator, "destination": path})
	fmt.Printf("Private key written to %s (mode 0600)\nProtect this file: anyone holding it can decrypt every security report.\n", path)
	return 0
}

// pgpImport implements `--maintenance pgp import FILE`.
func pgpImport(binaryName string, env *pgpEnv, file string) int {
	if file == "" {
		fmt.Fprintf(os.Stderr, "%s: --maintenance pgp import requires a file\n", binaryName)
		return 1
	}
	if code := authorizePGPSensitive(binaryName, env); code != 0 {
		return code
	}

	raw, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	key, err := pgp.ParsePrivate(string(raw))
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}

	// AI.md PART 11 "Import private key": identity mismatch is a warning
	// the operator may override, never a hard failure — restoring an older
	// instance's key is a legitimate reason for the address to differ.
	expected := env.identityEmail()
	if actual := key.PrimaryEmail(); expected != "" && !strings.EqualFold(actual, expected) {
		fmt.Fprintf(os.Stderr, "WARNING: imported key identity is %q but this server expects %q\n", actual, expected)
		answer, err := promptText("Import it anyway? [y/N]: ")
		if err != nil || !isAffirmative(answer) {
			fmt.Fprintln(os.Stderr, binaryName+": import cancelled")
			return 1
		}
	}

	if err := env.store.Write(key, env.installSecret); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	record := db.PGPKey{
		Fingerprint: key.Fingerprint,
		CreatedAt:   key.CreatedAt,
		ExpiresAt:   key.ExpiresAt,
	}
	if err := db.UpsertPGPKey(context.Background(), env.sqlDB, record); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}

	pgpAudit(env, "security.pgp_key_imported", applog.SeverityWarn, applog.ResultSuccess, key.Fingerprint,
		"", map[string]any{"source": file})
	fmt.Printf("Imported %s (expires %s)\n", key.Fingerprint, key.ExpiresAt.Format(time.RFC3339))
	return 0
}

// pgpDelete implements `--maintenance pgp delete`.
func pgpDelete(binaryName string, env *pgpEnv) int {
	if !env.store.HasKeypair() {
		fmt.Fprintln(os.Stderr, binaryName+": "+pgp.ErrNoKeypair.Error())
		return 1
	}
	if code := authorizePGPSensitive(binaryName, env); code != 0 {
		return code
	}

	fingerprint := ""
	if key, err := env.store.ReadPrivateKey(env.installSecret); err == nil {
		fingerprint = key.Fingerprint
	}

	fmt.Fprintln(os.Stderr, "WARNING: deleting the keypair makes every in-flight encrypted security report permanently un-decryptable.")
	answer, err := promptText("Type DELETE to confirm: ")
	if err != nil || answer != "DELETE" {
		fmt.Fprintln(os.Stderr, binaryName+": delete cancelled")
		return 1
	}

	if err := env.store.Delete(); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	if err := db.RevokeAllPGPKeys(context.Background(), env.sqlDB); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}

	// AI.md PART 11 "Delete": also stop advertising the key. The
	// `Encryption:` line in security.txt is already conditioned on the
	// public key file existing AND this flag, so clearing the flag and
	// removing the file are together enough — security.txt needs no
	// separate edit.
	env.cfg.Web.Security.PublishPGPKey = false
	if err := config.Save(env.paths.ConfigFile, env.cfg); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}

	pgpAudit(env, "security.pgp_key_deleted", applog.SeverityCritical, applog.ResultSuccess, fingerprint, answer, nil)
	fmt.Println("Keypair deleted; web.security.publish_pgp_key set to false.")
	return 0
}

// authorizePGPSensitive applies the AI.md PART 11 authorization rule for
// keypair management — "authorized like other sensitive operations
// (server.token OR root)". Root proceeds directly; anyone else must retype
// the operator token.
func authorizePGPSensitive(binaryName string, env *pgpEnv) int {
	if paths.IsPrivileged() {
		return 0
	}
	if env.cfg.Server.Token == "" {
		fmt.Fprintf(os.Stderr, "%s: this operation requires root or a configured server.token\n", binaryName)
		return 1
	}
	token, err := promptPassword("Enter operator token: ")
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(env.cfg.Server.Token)) != 1 {
		fmt.Fprintln(os.Stderr, binaryName+": invalid operator token")
		return 1
	}
	return 0
}

// promptText reads one full line of input, so an operator can type a
// multi-word reason. promptLine's Fscanln stops at the first space, which
// would silently truncate the audited reason text.
var promptText = func(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// isAffirmative reports whether an answer means yes.
func isAffirmative(answer string) bool {
	return strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes")
}

// pgpOperator identifies the operator for the export rate limit and the
// audit entry: the OS account running the command.
func pgpOperator() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if name := os.Getenv("USER"); name != "" {
		return name
	}
	return "unknown"
}

// pgpOperatorIP is the operator's address for the audit entry. A CLI
// invocation has no HTTP peer, so an SSH session's client address is used
// when there is one and the loopback address otherwise — an honest record
// of where the command came from.
func pgpOperatorIP() string {
	for _, key := range []string{"SSH_CLIENT", "SSH_CONNECTION"} {
		if value := os.Getenv(key); value != "" {
			if host, _, err := net.SplitHostPort(strings.ReplaceAll(strings.Fields(value)[0], " ", "")); err == nil {
				return host
			}
			return strings.Fields(value)[0]
		}
	}
	return "127.0.0.1"
}

// pgpExportState is the persisted per-operator export history backing the
// one-per-hour limit.
type pgpExportState struct {
	Operators map[string]string `json:"operators"`
}

// pgpLastExport returns when operator last exported the private key.
func pgpLastExport(store *pgp.Store, operator string) (time.Time, error) {
	raw, err := os.ReadFile(filepath.Join(store.Dir, pgp.ExportStateName))
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	var state pgpExportState
	if err := json.Unmarshal(raw, &state); err != nil {
		return time.Time{}, nil
	}
	value, ok := state.Operators[operator]
	if !ok {
		return time.Time{}, nil
	}
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, nil
	}
	return at, nil
}

// pgpRecordExport stamps operator's export time.
func pgpRecordExport(store *pgp.Store, operator string, at time.Time) error {
	state := pgpExportState{Operators: map[string]string{}}
	if raw, err := os.ReadFile(filepath.Join(store.Dir, pgp.ExportStateName)); err == nil {
		if err := json.Unmarshal(raw, &state); err != nil || state.Operators == nil {
			state.Operators = map[string]string{}
		}
	}
	state.Operators[operator] = at.UTC().Format(time.RFC3339)
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(store.Dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(store.Dir, pgp.ExportStateName), append(body, '\n'), 0o600)
}

// pgpAudit writes one audit entry for a keypair operation. The fingerprint
// is public information (it is published in security.txt and on the
// keyservers), so it is safe as the audit target id.
func pgpAudit(env *pgpEnv, event string, severity applog.Severity, result applog.Result, fingerprint, reason string, details map[string]any) {
	if env.audit == nil {
		return
	}
	entry := applog.Entry{
		Event:    event,
		Category: "security",
		Severity: severity,
		Actor:    applog.Actor{IP: pgpOperatorIP()},
		Details:  details,
		Reason:   reason,
		Result:   result,
	}
	if fingerprint != "" {
		entry.Target = &applog.Target{Type: "pgp_key", ID: fingerprint}
	}
	_ = env.audit.Write(entry)
}
