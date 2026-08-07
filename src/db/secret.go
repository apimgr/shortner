package db

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/apimgr/shortner/src/security"
)

// Well-known app_secrets row names, per AI.md PART 11 "Cryptographic Keys":
// project-level secrets stored in the database rather than server.yml.
const (
	SecretInstallation  = "installation_secret"
	SecretCookieSigning = "cookie_signing_key"
	SecretCSRFToken     = "csrf_token_secret"
)

// secretByteLen is the raw byte length generated for each app secret
// before base64 encoding, per AI.md PART 11 "Cryptographic Keys"
// (256-bit / 32-byte secrets).
const secretByteLen = 32

// GetSecret returns the base64-encoded value stored under name. Returns
// ErrNotFound if it has not been created yet.
func GetSecret(ctx context.Context, sqlDB *sql.DB, name string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var value string
	err := sqlDB.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name = ?`, name).Scan(&value)
	if err != nil {
		return "", HandleQueryError(err)
	}
	return value, nil
}

// SetSecret inserts or replaces the value stored under name, stamping
// rotated_at when replacing an existing row.
func SetSecret(ctx context.Context, sqlDB *sql.DB, name, value string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	_, err := sqlDB.ExecContext(ctx,
		`INSERT INTO app_secrets (name, value) VALUES (?, ?)
		 ON CONFLICT(name) DO UPDATE SET value = excluded.value, rotated_at = strftime('%s', 'now')`,
		name, value)
	if err != nil {
		return HandleQueryError(err)
	}
	return nil
}

// EnsureSecret returns the existing value for name, generating and
// persisting a fresh cryptographically random one on first run if it does
// not exist yet, per AI.md PART 11 "Cryptographic Keys": secrets are
// generated once at first startup and persisted thereafter.
func EnsureSecret(ctx context.Context, sqlDB *sql.DB, name string) (string, error) {
	value, err := GetSecret(ctx, sqlDB, name)
	if err == nil {
		return value, nil
	}
	if err != ErrNotFound {
		return "", err
	}

	raw, genErr := security.GenerateSecret(secretByteLen)
	if genErr != nil {
		return "", genErr
	}
	encoded := base64.RawStdEncoding.EncodeToString(raw)

	if err := SetSecret(ctx, sqlDB, name, encoded); err != nil {
		return "", fmt.Errorf("db: ensure secret %s: %w", name, err)
	}
	return encoded, nil
}

// EnsureCoreSecrets generates (if missing) and returns the three
// project-level secrets AI.md PART 11 requires at startup:
// installation_secret, cookie_signing_key, csrf_token_secret.
func EnsureCoreSecrets(ctx context.Context, sqlDB *sql.DB) (installation, cookieSigning, csrfToken string, err error) {
	installation, err = EnsureSecret(ctx, sqlDB, SecretInstallation)
	if err != nil {
		return "", "", "", err
	}
	cookieSigning, err = EnsureSecret(ctx, sqlDB, SecretCookieSigning)
	if err != nil {
		return "", "", "", err
	}
	csrfToken, err = EnsureSecret(ctx, sqlDB, SecretCSRFToken)
	if err != nil {
		return "", "", "", err
	}
	return installation, cookieSigning, csrfToken, nil
}
