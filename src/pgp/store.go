package pgp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// File names under `{config_dir}/security/`, per AI.md PART 11 "GPG
// Keypair Management" and "Backup Integration".
const (
	// PublicKeyName is served verbatim at /.well-known/pgp-key.asc.
	PublicKeyName = "pgp.pub.asc"
	// PrivateKeyName holds the AES-256-GCM sealed private key.
	PrivateKeyName = "pgp.priv.asc.enc"
	// PreviousPublicKeyName and PreviousPrivateKeyName hold the retired
	// keypair during the 30-day rotation grace window.
	PreviousPublicKeyName  = "pgp.pub.old.asc"
	PreviousPrivateKeyName = "pgp.priv.old.asc.enc"
	// RotationStateName records when the retired keypair stops being valid.
	RotationStateName = "rotation.state"
	// KeyserverStateName is the per-keyserver publish state AI.md's backup
	// contents table lists, so a restore does not double-submit.
	KeyserverStateName = "keyservers.state"
	// ExportStateName backs the "1 export per hour per operator" limit on
	// `--maintenance pgp export private`.
	ExportStateName = "export.state"
)

// PreviousKeyGraceDays is how long a rotated-out keypair stays usable for
// in-flight reports, per AI.md PART 11 "Rotate": "Old key stays valid for
// 30 days".
const PreviousKeyGraceDays = 30

// dirMode and fileMode are the permissions every file in the security
// directory gets: the directory is owner-only, and so is each key.
const (
	dirMode  os.FileMode = 0o700
	fileMode os.FileMode = 0o600
)

// Store is the `{config_dir}/security/` directory holding the keypair.
type Store struct {
	Dir string
}

// LockShared coordinates report submissions with key rotation across server
// and maintenance processes that use the same config directory.
func (s *Store) LockShared() (func(), error) {
	if err := os.MkdirAll(s.Dir, dirMode); err != nil {
		return nil, fmt.Errorf("pgp: create security directory: %w", err)
	}
	lock := flock.New(filepath.Join(s.Dir, ".keypair.lock"))
	if err := lock.RLock(); err != nil {
		return nil, fmt.Errorf("pgp: lock keypair for reading: %w", err)
	}
	return func() { _ = lock.Unlock() }, nil
}

// LockExclusive prevents submissions while a keypair and encrypted reports
// are being rotated.
func (s *Store) LockExclusive() (func(), error) {
	if err := os.MkdirAll(s.Dir, dirMode); err != nil {
		return nil, fmt.Errorf("pgp: create security directory: %w", err)
	}
	lock := flock.New(filepath.Join(s.Dir, ".keypair.lock"))
	if err := lock.Lock(); err != nil {
		return nil, fmt.Errorf("pgp: lock keypair for update: %w", err)
	}
	return func() { _ = lock.Unlock() }, nil
}

// NewStore returns the store rooted at `{config_dir}/security`.
func NewStore(configDir string) *Store {
	return &Store{Dir: filepath.Join(configDir, "security")}
}

// PublicKeyPath is the path /.well-known/pgp-key.asc serves.
func (s *Store) PublicKeyPath() string { return filepath.Join(s.Dir, PublicKeyName) }

// PrivateKeyPath is the sealed private key's path.
func (s *Store) PrivateKeyPath() string { return filepath.Join(s.Dir, PrivateKeyName) }

// HasKeypair reports whether a public key has been generated.
func (s *Store) HasKeypair() bool {
	info, err := os.Stat(s.PublicKeyPath())
	return err == nil && !info.IsDir()
}

// ReadPublicKey returns the ASCII-armored public key, or ErrNoKeypair when
// none has been generated.
func (s *Store) ReadPublicKey() (string, error) {
	body, err := os.ReadFile(s.PublicKeyPath())
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNoKeypair
	}
	if err != nil {
		return "", fmt.Errorf("pgp: read public key: %w", err)
	}
	return string(body), nil
}

// ReadPrivateKey decrypts and parses the stored private key.
func (s *Store) ReadPrivateKey(installationSecret string) (*Keypair, error) {
	sealed, err := os.ReadFile(s.PrivateKeyPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoKeypair
	}
	if err != nil {
		return nil, fmt.Errorf("pgp: read private key: %w", err)
	}
	armored, err := OpenPrivate(sealed, installationSecret)
	if err != nil {
		return nil, err
	}
	return ParsePrivate(armored)
}

// Write stores a keypair, sealing the private half under
// installationSecret. Both files are written 0600 inside a 0700 directory.
func (s *Store) Write(key *Keypair, installationSecret string) error {
	pub, err := key.ArmorPublic()
	if err != nil {
		return err
	}
	priv, err := key.ArmorPrivate()
	if err != nil {
		return err
	}
	sealed, err := SealPrivate(priv, installationSecret)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, dirMode); err != nil {
		return fmt.Errorf("pgp: create security directory: %w", err)
	}
	if err := writeFile(s.PublicKeyPath(), []byte(pub)); err != nil {
		return err
	}
	return writeFile(s.PrivateKeyPath(), sealed)
}

// Retire moves the current keypair aside for the 30-day grace window AI.md
// PART 11 "Rotate" requires, recording the moment it stops being valid.
// Any previously retired keypair is replaced — only one grace window can
// be open at a time.
func (s *Store) Retire(now time.Time) error {
	if !s.HasKeypair() {
		return ErrNoKeypair
	}
	previousPublic := filepath.Join(s.Dir, PreviousPublicKeyName)
	previousPrivate := filepath.Join(s.Dir, PreviousPrivateKeyName)
	statePath := filepath.Join(s.Dir, RotationStateName)
	if raw, err := os.ReadFile(statePath); err == nil {
		expiresAt, parseErr := time.Parse(time.RFC3339, trimLine(string(raw)))
		if parseErr != nil {
			return fmt.Errorf("pgp: parse existing rotation state: %w", parseErr)
		}
		if now.UTC().Before(expiresAt) {
			return fmt.Errorf("pgp: previous key remains in its %d-day grace period", PreviousKeyGraceDays)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("pgp: read existing rotation state: %w", err)
	}
	for _, name := range []string{PreviousPublicKeyName, PreviousPrivateKeyName, RotationStateName} {
		if err := os.Remove(filepath.Join(s.Dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("pgp: remove older retired key %s: %w", name, err)
		}
	}
	if err := os.Rename(s.PublicKeyPath(), previousPublic); err != nil {
		return fmt.Errorf("pgp: retire public key: %w", err)
	}
	if err := os.Rename(s.PrivateKeyPath(), previousPrivate); err != nil {
		if restoreErr := os.Rename(previousPublic, s.PublicKeyPath()); restoreErr != nil {
			return fmt.Errorf("pgp: retire private key: %v (restore public key: %w)", err, restoreErr)
		}
		return fmt.Errorf("pgp: retire private key: %w", err)
	}
	expiry := now.UTC().AddDate(0, 0, PreviousKeyGraceDays).Format(time.RFC3339)
	if err := writeFile(statePath, []byte(expiry+"\n")); err != nil {
		if restoreErr := s.RestorePrevious(); restoreErr != nil {
			return fmt.Errorf("pgp: write rotation state: %v (restore keypair: %w)", err, restoreErr)
		}
		return err
	}
	return nil
}

// PreviousKey returns the retired keypair while its grace window is still
// open, or ErrNoKeypair once the window has closed or nothing is retired.
func (s *Store) PreviousKey(installationSecret string, now time.Time) (*Keypair, error) {
	raw, err := os.ReadFile(filepath.Join(s.Dir, RotationStateName))
	if err != nil {
		return nil, ErrNoKeypair
	}
	expiry, err := time.Parse(time.RFC3339, trimLine(string(raw)))
	if err != nil || now.UTC().After(expiry) {
		return nil, ErrNoKeypair
	}
	sealed, err := os.ReadFile(filepath.Join(s.Dir, PreviousPrivateKeyName))
	if err != nil {
		return nil, ErrNoKeypair
	}
	armored, err := OpenPrivate(sealed, installationSecret)
	if err != nil {
		return nil, err
	}
	return ParsePrivate(armored)
}

// CleanupExpiredRotation removes the retired keypair after its grace period.
// The public key and expiry record are removed with the private key so a stale
// key cannot continue to look active after its private half is gone.
func (s *Store) CleanupExpiredRotation(now time.Time) (bool, error) {
	statePath := filepath.Join(s.Dir, RotationStateName)
	raw, err := os.ReadFile(statePath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("pgp: read rotation state: %w", err)
	}
	expiresAt, err := time.Parse(time.RFC3339, trimLine(string(raw)))
	if err != nil {
		return false, fmt.Errorf("pgp: parse rotation state: %w", err)
	}
	if now.UTC().Before(expiresAt) {
		return false, nil
	}
	for _, name := range []string{PreviousPrivateKeyName, PreviousPublicKeyName, RotationStateName} {
		if err := os.Remove(filepath.Join(s.Dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("pgp: remove expired rotation file %s: %w", name, err)
		}
	}
	return true, nil
}

// RestorePrevious reverses a rotation whose database transaction failed.
func (s *Store) RestorePrevious() error {
	for _, name := range []string{PublicKeyName, PrivateKeyName} {
		if err := os.Remove(filepath.Join(s.Dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("pgp: remove uncommitted key %s: %w", name, err)
		}
	}
	for _, move := range []struct{ from, to string }{
		{filepath.Join(s.Dir, PreviousPublicKeyName), s.PublicKeyPath()},
		{filepath.Join(s.Dir, PreviousPrivateKeyName), s.PrivateKeyPath()},
	} {
		if err := os.Rename(move.from, move.to); err != nil {
			return fmt.Errorf("pgp: restore previous key: %w", err)
		}
	}
	if err := os.Remove(filepath.Join(s.Dir, RotationStateName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("pgp: clear failed rotation state: %w", err)
	}
	return nil
}

// Delete removes both key files and the rotation grace state, per AI.md
// PART 11 "Delete". The keyserver publish state is kept: the public key is
// already out in the world and the record of where it went stays useful.
func (s *Store) Delete() error {
	for _, name := range []string{PublicKeyName, PrivateKeyName, PreviousPublicKeyName, PreviousPrivateKeyName, RotationStateName} {
		if err := os.Remove(filepath.Join(s.Dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("pgp: delete %s: %w", name, err)
		}
	}
	return nil
}

// writeFile writes data with 0600 permissions, creating the parent
// directory when it does not exist yet.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return fmt.Errorf("pgp: create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, fileMode); err != nil {
		return fmt.Errorf("pgp: write %s: %w", path, err)
	}
	return nil
}

// trimLine strips surrounding whitespace and the trailing newline.
func trimLine(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}
