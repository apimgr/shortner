package db

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
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
	if !errors.Is(err, ErrNotFound) {
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

// SetGraceSecret retains a previous key until its specified UTC expiry.
func SetGraceSecret(ctx context.Context, sqlDB *sql.DB, name, value string, expires time.Time) error {
	_, err := sqlDB.ExecContext(ctx, `INSERT INTO app_secret_grace(name,value,expires_at) VALUES(?,?,?) ON CONFLICT(name) DO UPDATE SET value=excluded.value,expires_at=excluded.expires_at`, name, value, expires.UTC().Unix())
	return HandleQueryError(err)
}

// GetGraceSecret returns an unexpired previous key and removes expired rows.
func GetGraceSecret(ctx context.Context, sqlDB *sql.DB, name string) (string, error) {
	_, _ = sqlDB.ExecContext(ctx, `DELETE FROM app_secret_grace WHERE expires_at <= strftime('%s','now')`)
	var value string
	err := sqlDB.QueryRowContext(ctx, `SELECT value FROM app_secret_grace WHERE name=? AND expires_at > strftime('%s','now')`, name).Scan(&value)
	return value, HandleQueryError(err)
}

// ReencryptStoredData atomically re-seals every persisted owner/linked token
// copy and security-report body under the replacement AES key.
func ReencryptStoredData(ctx context.Context, sqlDB *sql.DB, oldKey, newKey []byte) error {
	if len(oldKey) != 32 || len(newKey) != 32 || bytes.Equal(oldKey, newKey) {
		return security.ErrSealKeySize
	}
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id,token_enc FROM api_tokens WHERE token_enc <> ''`)
	if err != nil {
		return HandleQueryError(err)
	}
	type sealedRow struct {
		id    int64
		value string
	}
	var tokens []sealedRow
	for rows.Next() {
		var row sealedRow
		if err := rows.Scan(&row.id, &row.value); err != nil {
			rows.Close()
			return err
		}
		tokens = append(tokens, row)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, row := range tokens {
		plain, err := security.Open(oldKey, row.value)
		if err != nil {
			return fmt.Errorf("db: decrypt stored token: %w", err)
		}
		sealed, err := security.Seal(newKey, plain)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE api_tokens SET token_enc=? WHERE id=?`, sealed, row.id); err != nil {
			return HandleQueryError(err)
		}
	}
	rows, err = tx.QueryContext(ctx, `SELECT tracking_id,sealed FROM security_reports`)
	if err != nil {
		return HandleQueryError(err)
	}
	var reports []struct{ id, value string }
	for rows.Next() {
		var row struct{ id, value string }
		if err := rows.Scan(&row.id, &row.value); err != nil {
			rows.Close()
			return err
		}
		reports = append(reports, row)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, row := range reports {
		plain, err := security.Open(oldKey, row.value)
		if err != nil {
			return fmt.Errorf("db: decrypt security report: %w", err)
		}
		sealed, err := security.Seal(newKey, plain)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE security_reports SET sealed=? WHERE tracking_id=?`, sealed, row.id); err != nil {
			return HandleQueryError(err)
		}
	}
	return tx.Commit()
}
