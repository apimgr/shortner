package backup

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Check names the individual verification steps AI.md PART 21
// "Verification" requires after every backup and before every restore.
type Check string

// The verification checks, in the order PART 21's table lists them.
const (
	CheckFileExists   Check = "file exists"
	CheckSizeNonZero  Check = "size > 0"
	CheckDecrypt      Check = "decrypt test"
	CheckFormat       Check = "format valid"
	CheckManifest     Check = "manifest readable"
	CheckChecksum     Check = "checksum valid"
	CheckExtraction   Check = "content extraction"
	CheckDBIntegrity  Check = "database integrity"
	CheckVersionMatch Check = "version compatible"
)

// VerificationError reports which PART 21 check failed. Callers log the
// check name in the `backup.verification_failed` audit event.
type VerificationError struct {
	Path  string
	Check Check
	Err   error
}

func (e *VerificationError) Error() string {
	return fmt.Sprintf("backup: verification failed for %s (%s): %v", filepath.Base(e.Path), e.Check, e.Err)
}

func (e *VerificationError) Unwrap() error { return e.Err }

// failed wraps err as a VerificationError for check.
func failed(path string, check Check, err error) error {
	return &VerificationError{Path: path, Check: check, Err: err}
}

// Verify runs every AI.md PART 21 "Verification" check against the backup
// at path. All checks are fatal: the first failure returns a
// VerificationError naming the check, and the caller deletes the file.
//
// Verification is a real extraction into a temporary directory, including
// opening the extracted server.db and running SQLite's own integrity
// check, because PART 21 demands "backups must be 100% working" rather
// than merely well-formed.
func Verify(path, password string) error {
	info, err := os.Stat(path)
	if err != nil {
		return failed(path, CheckFileExists, err)
	}
	if info.Size() == 0 {
		return failed(path, CheckSizeNonZero, errors.New("backup file is empty"))
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return failed(path, CheckFileExists, err)
	}

	if IsEncryptedName(path) {
		raw, err = Decrypt(raw, password)
		if err != nil {
			return failed(path, CheckDecrypt, err)
		}
	}

	tmpDir, err := os.MkdirTemp("", "shortner-verify-")
	if err != nil {
		return failed(path, CheckExtraction, err)
	}
	defer os.RemoveAll(tmpDir)

	manifest, digests, err := extractArchive(raw, tmpDir)
	if err != nil {
		check := CheckExtraction
		if errors.Is(err, errUnsafeEntry) || errors.Is(err, errInvalidFormat) {
			check = CheckFormat
		}
		return failed(path, check, err)
	}
	if manifest.Version == "" {
		return failed(path, CheckManifest, errors.New("manifest has no version"))
	}

	if got := checksumOf(digests); got != manifest.Checksum {
		return failed(path, CheckChecksum, fmt.Errorf("extracted contents hash to %s, manifest says %s", got, manifest.Checksum))
	}
	for name, want := range manifest.Files {
		got, ok := digests[name]
		if !ok {
			return failed(path, CheckExtraction, fmt.Errorf("manifest lists %s but the archive does not contain it", name))
		}
		if got != want {
			return failed(path, CheckChecksum, fmt.Errorf("%s hashes to %s, manifest says %s", name, got, want))
		}
	}

	dbFile := filepath.Join(tmpDir, "server.db")
	if _, err := os.Stat(dbFile); err == nil {
		if err := checkSQLiteIntegrity(dbFile); err != nil {
			return failed(path, CheckDBIntegrity, err)
		}
	} else if !manifest.Kind.Incremental() {
		// server.db is an "Always" content per PART 21 "Backup Contents";
		// only an incremental may legitimately omit it (unchanged since
		// the base full backup).
		return failed(path, CheckExtraction, errors.New("archive does not contain server.db"))
	}

	return nil
}

// checkSQLiteIntegrity opens the extracted database read-only and runs
// PRAGMA integrity_check, per AI.md PART 21 "Database integrity — verify
// SQLite/dump is valid".
func checkSQLiteIntegrity(dbFile string) error {
	handle, err := sql.Open("sqlite", "file:"+dbFile+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return fmt.Errorf("open extracted database: %w", err)
	}
	defer handle.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var result string
	if err := handle.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("integrity_check: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("integrity_check reported %q", result)
	}
	return nil
}

// VerifyForRestore runs Verify plus AI.md PART 21 "Restore Verification"'s
// version-compatibility step, returning the manifest and any non-fatal
// warnings. A version mismatch is a warning, never a failure: PART 21
// "Version mismatch | Warning shown, schema updates applied if needed".
func VerifyForRestore(path, password, appVersion string) (*Manifest, []string, error) {
	if err := Verify(path, password); err != nil {
		return nil, nil, err
	}
	manifest, err := LoadManifest(path, password)
	if err != nil {
		return nil, nil, failed(path, CheckManifest, err)
	}

	var warnings []string
	if manifest.AppVersion != "" && appVersion != "" && manifest.AppVersion != appVersion {
		warnings = append(warnings, fmt.Sprintf(
			"backup was created by version %s, this server is version %s — schema updates will be applied if needed",
			manifest.AppVersion, appVersion))
	}
	return manifest, warnings, nil
}

// limitedBuffer is a bytes.Buffer that refuses to grow past max, so an
// oversized --include-data backup fails with a clear error instead of
// driving the host out of memory while the archive is built for
// encryption.
type limitedBuffer struct {
	buf bytes.Buffer
	max int64
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.max > 0 && int64(b.buf.Len()+len(p)) > b.max {
		return 0, fmt.Errorf("backup: archive exceeds %d byte in-memory limit for encryption", b.max)
	}
	return b.buf.Write(p)
}

func (b *limitedBuffer) Bytes() []byte { return b.buf.Bytes() }

func (b *limitedBuffer) Reset() { b.buf.Reset() }
