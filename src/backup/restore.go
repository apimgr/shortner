package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/db"
	"gopkg.in/yaml.v3"
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

	handle, err := db.OpenReadOnlySQLite(dbPath, 1)
	if err != nil {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var count int
	err = handle.Read(ctx).QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'").Scan(&count)
	closeErr := handle.Close()
	if err != nil || closeErr != nil {
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
	return f.Close() == nil
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
	Limits     ArchiveLimits
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
func Restore(opts RestoreOptions) (result *RestoreResult, resultErr error) {
	limits := opts.Limits
	if limits.MaxArchiveSize == 0 {
		limits.MaxArchiveSize = 1 << 30
	}
	if limits.MaxExpandedSize == 0 {
		limits.MaxExpandedSize = 4 << 30
	}
	if limits.MaxFiles == 0 {
		limits.MaxFiles = 10000
	}
	if limits.MaxFileSize == 0 {
		limits.MaxFileSize = 512 << 20
	}
	if limits.MaxRatio == 0 {
		limits.MaxRatio = 100
	}
	info, statErr := os.Stat(opts.Path)
	if statErr != nil {
		return nil, statErr
	}
	if info.Size() > limits.MaxArchiveSize {
		return nil, fmt.Errorf("backup: archive exceeds maximum size %d", limits.MaxArchiveSize)
	}
	manifest, warnings, err := VerifyForRestoreWithLimits(opts.Path, opts.Password, opts.AppVersion, limits)
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
	defer func() {
		if err := os.RemoveAll(stage); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("backup: remove restore staging dir: %w", err))
		}
	}()

	usage := &expansionUsage{}
	if manifest.Kind.Incremental() {
		basePath := filepath.Join(filepath.Dir(opts.Path), filepath.Base(manifest.BaseBackup))
		baseInfo, statErr := os.Stat(basePath)
		if statErr != nil {
			return nil, fmt.Errorf("backup: incremental base missing: %w", statErr)
		}
		if baseInfo.Size() > limits.MaxArchiveSize {
			return nil, fmt.Errorf("backup: base archive exceeds maximum size %d", limits.MaxArchiveSize)
		}
		if err := verifyWithLimits(basePath, opts.Password, limits); err != nil {
			return nil, fmt.Errorf("backup: invalid incremental base: %w", err)
		}
		baseRaw, err := readBackupPayload(basePath, opts.Password)
		if err != nil {
			return nil, err
		}
		if _, _, err := extractArchiveLimited(baseRaw, stage, limits, usage); err != nil {
			return nil, err
		}
	}
	if _, _, err := extractArchiveLimited(raw, stage, limits, usage); err != nil {
		return nil, err
	}
	if err := rewriteExternalTLSPaths(stage, opts.ConfigDir); err != nil {
		return nil, err
	}

	var names []string
	deletedSet := make(map[string]bool, len(manifest.Deleted))
	for _, name := range manifest.Deleted {
		deletedSet[name] = true
	}
	if err := filepath.WalkDir(stage, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Clean(filePath) == filepath.Clean(stage) {
			return nil
		}
		rel, err := filepath.Rel(stage, filePath)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if name != ManifestName && !deletedSet[name] {
			names = append(names, name)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("backup: enumerate staged restore: %w", err)
	}
	sort.Strings(names)

	var restored []string
	deleted := append([]string(nil), manifest.Deleted...)
	sort.Strings(deleted)
	for _, name := range deleted {
		dest, err := opts.destinationFor(name)
		if err != nil {
			return nil, err
		}
		if dest == "" {
			continue
		}
		if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("backup: delete restored path %s: %w", name, err)
		}
	}
	for _, name := range names {
		dest, err := opts.destinationFor(name)
		if err != nil {
			return nil, err
		}
		if dest == "" {
			continue
		}
		mode := os.FileMode(0)
		if strings.HasPrefix(name, "ssl-external/key/") {
			mode = 0o600
		}
		if err := placeFile(filepath.Join(stage, filepath.FromSlash(name)), dest, mode); err != nil {
			return nil, err
		}
		restored = append(restored, name)
	}

	return &RestoreResult{Manifest: *manifest, Warnings: warnings, Restored: restored}, nil
}

func rewriteExternalTLSPaths(stage, configDir string) error {
	if configDir == "" {
		return nil
	}
	serverPath := filepath.Join(stage, "server.yml")
	data, err := os.ReadFile(serverPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("backup: parse restored server.yml: %w", err)
	}
	if len(root.Content) == 0 {
		return nil
	}
	server := yamlMapValue(root.Content[0], "server")
	ssl := yamlMapValue(server, "ssl")
	changed := false
	for _, field := range []string{"cert", "key"} {
		node := yamlMapValue(ssl, field)
		if node == nil || node.Kind != yaml.ScalarNode || strings.TrimSpace(node.Value) == "" {
			continue
		}
		archiveName := path.Join("ssl-external", field, filepath.Base(node.Value))
		if _, err := os.Stat(filepath.Join(stage, filepath.FromSlash(archiveName))); err == nil {
			node.Value = filepath.Join(configDir, "ssl", field, filepath.Base(node.Value))
			changed = true
		}
	}
	if !changed {
		return nil
	}
	var output strings.Builder
	if err := yaml.NewEncoder(&output).Encode(&root); err != nil {
		return err
	}
	return os.WriteFile(serverPath, []byte(output.String()), 0o600)
}

func yamlMapValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
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
	case "secrets.yml":
		if o.ConfigDir == "" {
			return "", nil
		}
		return filepath.Join(o.ConfigDir, "secrets.yml"), nil
	case "server.db":
		return o.DBPath, nil
	}

	top, rest, found := strings.Cut(clean, "/")
	if !found || rest == "" {
		return "", nil
	}
	rel := filepath.FromSlash(path.Clean(rest))

	switch top {
	case "template", "theme", "ssl", "ssl-external", "yaml.d", "html", "assets":
		if o.ConfigDir == "" {
			return "", nil
		}
		if top == "ssl-external" {
			return filepath.Join(o.ConfigDir, "ssl", rel), nil
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
func placeFile(src, dest string, requiredMode os.FileMode) (resultErr error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return fmt.Errorf("backup: create dir for %s: %w", dest, err)
	}

	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("backup: open staged %s: %w", filepath.Base(src), err)
	}
	defer func() {
		if err := in.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("backup: close staged %s: %w", filepath.Base(src), err))
		}
	}()

	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("backup: stat staged %s: %w", filepath.Base(src), err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".restore-*")
	if err != nil {
		return fmt.Errorf("backup: create temp file for %s: %w", dest, err)
	}
	tmpName := tmp.Name()
	tmpClosed := false
	defer func() {
		if !tmpClosed {
			tmpClosed = true
			resultErr = errors.Join(resultErr, tmp.Close())
		}
		if err := os.Remove(tmpName); err != nil && !errors.Is(err, os.ErrNotExist) {
			resultErr = errors.Join(resultErr, fmt.Errorf("backup: remove temporary restore file: %w", err))
		}
	}()

	mode := info.Mode().Perm()
	if requiredMode != 0 {
		mode = requiredMode
	}
	if err := tmp.Chmod(mode); err != nil {
		return fmt.Errorf("backup: chmod %s: %w", tmpName, err)
	}
	if _, err := io.Copy(tmp, in); err != nil {
		return fmt.Errorf("backup: write %s: %w", dest, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("backup: sync %s: %w", dest, err)
	}
	tmpClosed = true
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("backup: close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("backup: replace %s: %w", dest, err)
	}
	return nil
}
