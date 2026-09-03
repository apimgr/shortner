// Package backup implements AI.md PART 21 "BACKUP & RESTORE": archive
// creation (tar.gz, optionally AES-256-GCM encrypted under an Argon2id-
// derived key), the mandatory post-creation verification suite, the
// retention/disk-pressure sweep, and authorized restore.
//
// Everything here is filesystem- and password-driven; nothing in this
// package reads the operator token or writes the config file directly —
// callers hand it resolved paths and an already-decided password so the
// same code serves the CLI (`--maintenance backup`), the scheduler
// (`backup_daily`/`backup_hourly`), and future API/WebUI callers
// identically.
package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrComplianceEncryptionRequired is returned when compliance mode is on
// but no backup password is available, per AI.md PART 21 "Compliance Mode
// Enforcement": "Backups will NOT run unless encryption password is set".
var ErrComplianceEncryptionRequired = errors.New(
	"backup: compliance mode requires an encrypted backup — set server.backup.encryption_password in server.yml")

// UnencryptedWarning is the operator warning AI.md PART 21 requires on the
// first backup and at startup while encryption is unconfigured ("BACKUP
// ENCRYPTION NOT CONFIGURED").
const UnencryptedWarning = "backup encryption is not configured — backups are NOT encrypted; " +
	"anyone with access to a backup file can read all data including the server configuration " +
	"(set server.backup.encryption_password in server.yml)"

// Options describes one backup to create.
type Options struct {
	// Dir is the resolved backup directory. AI.md PART 21 requires this to
	// be the directory cached at startup — never re-resolved per run.
	Dir string
	// Prefix is {project_name}, the leading component of every generated
	// backup filename.
	Prefix string
	// Kind selects the filename shape and retention treatment.
	Kind Kind
	// Name overrides the generated filename. It may include a directory,
	// in which case Dir is ignored for placement. Only meaningful for
	// KindManual (`--maintenance backup [filename]`).
	Name string

	ConfigFile string
	DBPath     string
	ConfigDir  string
	DataDir    string

	IncludeSSL  bool
	IncludeData bool

	// Encrypt requests an AES-256-GCM archive; Password is the operator-
	// supplied secret it is keyed from. Password is never persisted, never
	// logged, and never written into the archive.
	Encrypt  bool
	Password string
	// Compliance mirrors server.compliance.enabled and hard-blocks
	// unencrypted backups.
	Compliance bool

	CreatedBy  string
	AppVersion string
	// Now is the archive timestamp; zero means time.Now().UTC().
	Now time.Time
	// Base is the full backup an incremental diffs against. Nil produces a
	// self-contained incremental (every file included), which is what the
	// very first run must do.
	Base *Manifest
}

// Result describes a created, verified backup.
type Result struct {
	Path      string
	Name      string
	Size      int64
	Encrypted bool
	Manifest  Manifest
}

// Create builds, writes, and verifies one backup archive, per AI.md
// PART 21 "Backup Command" and "Verification". A backup that fails any
// verification check is deleted before the error is returned — PART 21:
// "If ANY check fails: delete the failed backup immediately".
func Create(opts Options) (*Result, error) {
	opts = opts.normalized()

	if opts.Compliance && !opts.Encrypt {
		return nil, ErrComplianceEncryptionRequired
	}
	if opts.Encrypt && opts.Password == "" {
		return nil, ErrPasswordRequired
	}
	if opts.Prefix == "" {
		return nil, errors.New("backup: project name prefix is required")
	}
	if opts.Dir == "" && opts.Name == "" {
		return nil, errors.New("backup: backup directory is required")
	}

	files, err := collectSources(opts)
	if err != nil {
		return nil, err
	}
	if opts.Kind.Incremental() && opts.Base != nil {
		files = changedSince(files, opts.Base.Files)
	}

	included := filesMap(files)
	manifest := Manifest{
		Version:    ManifestVersion,
		CreatedAt:  opts.Now,
		CreatedBy:  opts.CreatedBy,
		AppVersion: opts.AppVersion,
		Contents:   contentsOf(files),
		Encrypted:  opts.Encrypt,
		Checksum:   checksumOf(included),
		Kind:       opts.Kind,
		Files:      included,
	}
	if opts.Encrypt {
		manifest.EncryptionMethod = EncryptionMethod
	}
	if opts.Kind.Incremental() && opts.Base != nil {
		manifest.BaseChecksum = opts.Base.Checksum
	}

	target := opts.targetPath()
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return nil, fmt.Errorf("backup: create backup dir: %w", err)
	}

	if err := writeBackupFile(target, manifest, files, opts); err != nil {
		os.Remove(target)
		return nil, err
	}

	info, err := os.Stat(target)
	if err != nil {
		os.Remove(target)
		return nil, fmt.Errorf("backup: stat %s: %w", target, err)
	}

	if err := Verify(target, opts.Password); err != nil {
		os.Remove(target)
		return nil, err
	}

	return &Result{
		Path:      target,
		Name:      filepath.Base(target),
		Size:      info.Size(),
		Encrypted: opts.Encrypt,
		Manifest:  manifest,
	}, nil
}

// normalized fills in Options defaults without mutating the caller's copy.
func (o Options) normalized() Options {
	if o.Now.IsZero() {
		o.Now = time.Now().UTC()
	}
	o.Now = o.Now.UTC()
	if o.CreatedBy == "" {
		o.CreatedBy = "operator"
	}
	if o.AppVersion == "" {
		o.AppVersion = "unknown"
	}
	if o.Kind == "" {
		o.Kind = KindFull
	}
	return o
}

// targetPath resolves the absolute output path for these options.
func (o Options) targetPath() string {
	name := o.Name
	if name == "" {
		return filepath.Join(o.Dir, FileName(o.Prefix, o.Kind, o.Now, o.Encrypt))
	}
	name = NormalizeName(name, o.Encrypt)
	if filepath.Dir(name) != "." {
		return name
	}
	return filepath.Join(o.Dir, name)
}

// FileName returns the AI.md PART 21 "Backup Format" filename for a
// backup of the given kind: dated fulls, dated+timestamped manual
// backups, and the fixed-name daily/hourly incrementals.
func FileName(prefix string, kind Kind, t time.Time, encrypted bool) string {
	var name string
	switch kind {
	case KindDaily:
		name = prefix + "-daily.tar.gz"
	case KindHourly:
		name = prefix + "-hourly.tar.gz"
	case KindManual:
		name = prefix + "_backup_" + t.UTC().Format("2006-01-02_150405") + ".tar.gz"
	default:
		name = prefix + "_backup_" + t.UTC().Format("2006-01-02") + ".tar.gz"
	}
	if encrypted {
		name += EncryptedSuffix
	}
	return name
}

// NormalizeName forces an operator-supplied filename to carry the correct
// extension pair for its encryption state, so `--maintenance backup mybk`
// still produces a file a later `--maintenance restore` recognizes.
func NormalizeName(name string, encrypted bool) string {
	name = strings.TrimSuffix(name, EncryptedSuffix)
	if !strings.HasSuffix(name, ".tar.gz") {
		name = strings.TrimSuffix(name, ".tgz")
		name += ".tar.gz"
	}
	if encrypted {
		name += EncryptedSuffix
	}
	return name
}

// changedSince returns the subset of files whose content digest differs
// from (or is absent from) base — the "changes since full" an incremental
// carries, per AI.md PART 21 "Backup Files Created".
func changedSince(files []sourceFile, base map[string]string) []sourceFile {
	var changed []sourceFile
	for _, f := range files {
		if base[f.Name] != f.Digest {
			changed = append(changed, f)
		}
	}
	return changed
}

// writeBackupFile renders the archive and places it at target atomically
// (temp file in the same directory, then rename), so a crash or full disk
// can never leave a half-written file that looks like a valid backup.
//
// When encryption is on the tar.gz is built entirely in memory and only
// the ciphertext is written, per AI.md PART 21 "How Encryption Works"
// step 5: "Unencrypted archive never touches disk".
func writeBackupFile(target string, manifest Manifest, files []sourceFile, opts Options) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, filepath.Base(target)+".tmp-*")
	if err != nil {
		return fmt.Errorf("backup: create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("backup: chmod %s: %w", tmpName, err)
	}

	if opts.Encrypt {
		var buf limitedBuffer
		buf.max = maxArchiveBytes
		if err := writeArchive(&buf, manifest, files); err != nil {
			tmp.Close()
			return err
		}
		sealed, err := Encrypt(buf.Bytes(), opts.Password)
		buf.Reset()
		if err != nil {
			tmp.Close()
			return err
		}
		if _, err := tmp.Write(sealed); err != nil {
			tmp.Close()
			return fmt.Errorf("backup: write %s: %w", tmpName, err)
		}
	} else if err := writeArchive(tmp, manifest, files); err != nil {
		tmp.Close()
		return err
	}

	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("backup: sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("backup: close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("backup: place %s: %w", target, err)
	}
	return nil
}

// LoadManifest reads (and decrypts, if needed) the manifest of an existing
// backup file without extracting its payload.
func LoadManifest(path, password string) (*Manifest, error) {
	raw, err := readBackupPayload(path, password)
	if err != nil {
		return nil, err
	}
	m, _, err := extractArchive(raw, "")
	if err != nil {
		return nil, err
	}
	return m, nil
}

// readBackupPayload reads path and returns the plain .tar.gz bytes,
// decrypting first when the filename marks it encrypted.
func readBackupPayload(path, password string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("backup: open %s: %w", path, err)
	}
	defer f.Close()

	raw, err := readAllLimited(f, maxArchiveBytes)
	if err != nil {
		return nil, fmt.Errorf("backup: read %s: %w", path, err)
	}
	if !IsEncryptedName(path) {
		return raw, nil
	}
	return Decrypt(raw, password)
}
