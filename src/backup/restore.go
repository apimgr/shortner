package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Authorization is the outcome of AI.md PART 21 "Restore Authorization".
type Authorization string

// Restore authorization outcomes.
const (
	// AuthAllowed: the database is empty (first run) — nothing to protect.
	AuthAllowed Authorization = "allowed"
	// AuthConfirm: running as root — allowed after an explicit
	// confirmation.
	AuthConfirm Authorization = "confirm"
	// AuthToken: running as the service user — the operator token
	// (server.token) must be supplied.
	AuthToken Authorization = "token"
	// AuthDenied: any other user.
	AuthDenied Authorization = "denied"
)

// ErrRestoreDenied is returned when the caller is not permitted to
// restore, per AI.md PART 21 "Restore Authorization" -> "Random user:
// Denied".
var ErrRestoreDenied = errors.New("backup: restore denied — run as root or as the service user with the operator token")

// AuthEnv is the observable state AI.md PART 21's authorization table
// branches on.
type AuthEnv struct {
	// DatabaseEmpty is true on a first-run server with nothing to protect.
	DatabaseEmpty bool
	// IsRoot is true when the process holds administrative privilege.
	IsRoot bool
	// ConfigReadable is true when this account can read server.yml. That
	// is the portable stand-in for "running as the service user": the
	// service account is precisely the one the config file is readable by,
	// and a random user cannot read it.
	ConfigReadable bool
}

// Authorize implements AI.md PART 21 "Restore Authorization" in table
// order: empty database wins, then root, then the service user, then
// denial.
func Authorize(env AuthEnv) Authorization {
	switch {
	case env.DatabaseEmpty:
		return AuthAllowed
	case env.IsRoot:
		return AuthConfirm
	case env.ConfigReadable:
		return AuthToken
	default:
		return AuthDenied
	}
}

// DatabaseEmpty reports whether the SQLite database at dbPath is a
// first-run database — absent, zero-length, or carrying no application
// tables. An unreadable or unopenable database is reported as not empty:
// refusing to treat an unknown database as disposable is the safe default
// for a destructive operation.
func DatabaseEmpty(dbPath string) bool {
	info, err := os.Stat(dbPath)
	if err != nil {
		return os.IsNotExist(err)
	}
	if info.Size() == 0 {
		return true
	}

	handle, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return false
	}
	defer handle.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var count int
	err = handle.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'").Scan(&count)
	if err != nil {
		return false
	}
	return count == 0
}

// ConfigReadable reports whether the current process can read the config
// file — see AuthEnv.ConfigReadable.
func ConfigReadable(configFile string) bool {
	f, err := os.Open(configFile)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// RestoreOptions describes one restore.
type RestoreOptions struct {
	// Path is the backup archive to restore.
	Path string
	// Password is required when Path names an encrypted archive.
	Password string

	ConfigFile string
	DBPath     string
	ConfigDir  string
	DataDir    string

	AppVersion string
}

// RestoreResult reports what a completed restore did.
type RestoreResult struct {
	Manifest Manifest
	// Warnings carries non-fatal findings, currently the PART 21 version-
	// mismatch warning.
	Warnings []string
	// Restored lists the archive-relative names written to disk.
	Restored []string
}

// Restore verifies and then applies a backup archive, per AI.md PART 21
// "Restore Command". Every verification check must pass before anything is
// written ("Only proceed with restore if ALL verification checks pass");
// each destination file is then replaced atomically so a failure part-way
// through cannot leave a truncated server.yml or a torn database.
//
// Authorization is the caller's responsibility — see Authorize — because
// only the caller knows whether the operator confirmed or supplied a
// valid token.
func Restore(opts RestoreOptions) (*RestoreResult, error) {
	manifest, warnings, err := VerifyForRestore(opts.Path, opts.Password, opts.AppVersion)
	if err != nil {
		return nil, err
	}

	raw, err := readBackupPayload(opts.Path, opts.Password)
	if err != nil {
		return nil, err
	}

	stage, err := os.MkdirTemp(filepath.Dir(opts.ConfigFile), ".restore-")
	if err != nil {
		return nil, fmt.Errorf("backup: create staging dir: %w", err)
	}
	defer os.RemoveAll(stage)

	if _, _, err := extractArchive(raw, stage); err != nil {
		return nil, err
	}

	names := make([]string, 0, len(manifest.Files))
	for name := range manifest.Files {
		names = append(names, name)
	}
	sort.Strings(names)

	var restored []string
	for _, name := range names {
		dest, err := opts.destinationFor(name)
		if err != nil {
			return nil, err
		}
		if dest == "" {
			continue
		}
		if err := placeFile(filepath.Join(stage, filepath.FromSlash(name)), dest); err != nil {
			return nil, err
		}
		restored = append(restored, name)
	}

	return &RestoreResult{Manifest: *manifest, Warnings: warnings, Restored: restored}, nil
}

// destinationFor maps an archive-relative name back to the live path it
// was captured from, mirroring collectSources. Unknown top-level entries
// are skipped rather than guessed at.
func (o RestoreOptions) destinationFor(name string) (string, error) {
	clean, err := safeArchiveName(name)
	if err != nil {
		return "", err
	}

	switch clean {
	case "server.yml":
		return o.ConfigFile, nil
	case "server.db":
		return o.DBPath, nil
	}

	top, rest, found := strings.Cut(clean, "/")
	if !found || rest == "" {
		return "", nil
	}
	rel := filepath.FromSlash(path.Clean(rest))

	switch top {
	case "template", "theme", "ssl":
		if o.ConfigDir == "" {
			return "", nil
		}
		return filepath.Join(o.ConfigDir, top, rel), nil
	case "data":
		if o.DataDir == "" {
			return "", nil
		}
		return filepath.Join(o.DataDir, rel), nil
	default:
		return "", nil
	}
}

// placeFile moves src over dest atomically, preserving src's permissions
// and creating dest's parent directories.
func placeFile(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return fmt.Errorf("backup: create dir for %s: %w", dest, err)
	}

	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("backup: open staged %s: %w", filepath.Base(src), err)
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("backup: stat staged %s: %w", filepath.Base(src), err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".restore-*")
	if err != nil {
		return fmt.Errorf("backup: create temp file for %s: %w", dest, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return fmt.Errorf("backup: chmod %s: %w", tmpName, err)
	}
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return fmt.Errorf("backup: write %s: %w", dest, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("backup: sync %s: %w", dest, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("backup: close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("backup: replace %s: %w", dest, err)
	}
	return nil
}
