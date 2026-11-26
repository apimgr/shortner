package db

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/security"
)

// Well-known app_secrets row names, per AI.md PART 11 "Cryptographic Keys":
// project-level secrets stored in the database rather than server.yml.
const (
	SecretInstallation   = "installation_secret"
	SecretCookieSigning  = "cookie_signing_key"
	SecretCSRFToken      = "csrf_token_secret"
	SecretDNSValidatedAt = "dns_validated_at"
	SecretDNSFingerprint = "dns_credential_fingerprint"
)

// secretByteLen is the raw byte length generated for each app secret
// before base64 encoding, per AI.md PART 11 "Cryptographic Keys"
// (256-bit / 32-byte secrets).
const secretByteLen = 32

// DecryptFailure identifies a stored row that neither the current nor
// previous encryption key could open, without carrying row contents.
type DecryptFailure struct {
	Table string
	RowID string
	Cause error
}

func (e *DecryptFailure) Error() string {
	return fmt.Sprintf("db: decrypt %s row %s failed: %v", e.Table, e.RowID, e.Cause)
}

func (e *DecryptFailure) Unwrap() error { return e.Cause }

// GetSecret returns the base64-encoded value stored under name. Returns
// ErrNotFound if it has not been created yet.
func GetSecret(ctx context.Context, sqlDB Handle, name string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var value string
	err := sqlDB.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name = ?`, name).Scan(&value)
	if err != nil {
		return "", HandleQueryError(err)
	}
	return value, nil
}

// SetSecret inserts or replaces the value stored under name and advances its
// version so consumers can detect a changed secret.
func SetSecret(ctx context.Context, sqlDB Handle, name, value string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	_, err := sqlDB.ExecContext(ctx,
		`INSERT INTO app_secrets (name, value, version, updated_at) VALUES (?, ?, 1, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		 ON CONFLICT(name) DO UPDATE SET value = excluded.value, version = app_secrets.version + 1, updated_at = excluded.updated_at`,
		name, value)
	if err != nil {
		return HandleQueryError(err)
	}
	return nil
}

// DeleteSecret removes one server-owned app_secrets value.
func DeleteSecret(ctx context.Context, sqlDB Handle, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := sqlDB.ExecContext(ctx, `DELETE FROM app_secrets WHERE name = ?`, name)
	if err != nil {
		return HandleQueryError(err)
	}
	return nil
}

// EnsureSecret returns the existing value for name, generating and
// persisting a fresh cryptographically random one on first run if it does
// not exist yet, per AI.md PART 11 "Cryptographic Keys": secrets are
// generated once at first startup and persisted thereafter.
func EnsureSecret(ctx context.Context, sqlDB Handle, name string) (string, error) {
	var value string
	err := WithTransaction(ctx, sqlDB, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name=?`, name).Scan(&value)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		raw, genErr := security.GenerateSecret(secretByteLen)
		if genErr != nil {
			return genErr
		}
		encoded := base64.RawStdEncoding.EncodeToString(raw)
		if _, err := tx.ExecContext(ctx, `INSERT INTO app_secrets(name,value,version,updated_at) VALUES(?,?,1,strftime('%Y-%m-%dT%H:%M:%fZ','now')) ON CONFLICT(name) DO NOTHING`, name, encoded); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name=?`, name).Scan(&value)
	})
	if err != nil {
		return "", fmt.Errorf("db: ensure secret %s: %w", name, err)
	}
	return value, nil
}

// EnsureCoreSecrets generates (if missing) and returns the three
// project-level secrets AI.md PART 11 requires at startup:
// installation_secret, cookie_signing_key, csrf_token_secret.
func EnsureCoreSecrets(ctx context.Context, sqlDB Handle) (installation, cookieSigning, csrfToken string, err error) {
	values := make(map[string]string, 3)
	err = WithTransaction(ctx, sqlDB, func(tx *sql.Tx) error {
		for _, name := range []string{SecretInstallation, SecretCookieSigning, SecretCSRFToken} {
			var value string
			if scanErr := tx.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name=?`, name).Scan(&value); scanErr != nil {
				if !errors.Is(scanErr, sql.ErrNoRows) {
					return scanErr
				}
				raw, genErr := security.GenerateSecret(secretByteLen)
				if genErr != nil {
					return genErr
				}
				value = base64.RawStdEncoding.EncodeToString(raw)
				if _, insertErr := tx.ExecContext(ctx, `INSERT INTO app_secrets(name,value,version,updated_at) VALUES(?,?,1,strftime('%Y-%m-%dT%H:%M:%fZ','now')) ON CONFLICT(name) DO NOTHING`, name, value); insertErr != nil {
					return insertErr
				}
			}
			if scanErr := tx.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name=?`, name).Scan(&value); scanErr != nil {
				return scanErr
			}
			values[name] = value
		}
		return nil
	})
	if err != nil {
		return "", "", "", fmt.Errorf("db: ensure core secrets: %w", err)
	}
	installation, cookieSigning, csrfToken = values[SecretInstallation], values[SecretCookieSigning], values[SecretCSRFToken]
	return installation, cookieSigning, csrfToken, nil
}

// RotateSecretDue rotates a generated project secret after its configured
// lifetime and retains its previous value for the supplied grace period.
func RotateSecretDue(ctx context.Context, sqlDB Handle, name string, age, grace time.Duration, now time.Time) (bool, error) {
	return rotateSecret(ctx, sqlDB, name, age, grace, now.UTC(), false)
}

// RotateCSRFSecretForAPIKey rotates the CSRF base whenever an API credential
// is regenerated. Existing CSRF values remain available for a short grace.
func RotateCSRFSecretForAPIKey(ctx context.Context, sqlDB Handle) error {
	if _, err := EnsureSecret(ctx, sqlDB, SecretCSRFToken); err != nil {
		return err
	}
	_, err := rotateSecret(ctx, sqlDB, SecretCSRFToken, 0, 7*24*time.Hour, time.Now().UTC(), true)
	return err
}

func RotateCSRFSecretTx(ctx context.Context, tx *sql.Tx) error {
	var value string
	err := tx.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name=?`, SecretCSRFToken).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		raw, genErr := security.GenerateSecret(secretByteLen)
		if genErr != nil {
			return genErr
		}
		value = base64.RawStdEncoding.EncodeToString(raw)
		if _, err := tx.ExecContext(ctx, `INSERT INTO app_secrets(name,value,version,updated_at) VALUES(?,?,1,strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, SecretCSRFToken, value); err != nil {
			return err
		}
	} else if err != nil {
		return HandleQueryError(err)
	}
	raw, err := security.GenerateSecret(secretByteLen)
	if err != nil {
		return err
	}
	_, err = rotateSecretTx(ctx, tx, SecretCSRFToken, 0, 7*24*time.Hour, time.Now().UTC(), true, base64.RawStdEncoding.EncodeToString(raw))
	return err
}
func rotateSecret(ctx context.Context, sqlDB Handle, name string, age, grace time.Duration, now time.Time, force bool) (bool, error) {
	raw, err := security.GenerateSecret(secretByteLen)
	if err != nil {
		return false, err
	}
	newValue := base64.RawStdEncoding.EncodeToString(raw)
	rotated := false
	err = WithTransaction(ctx, sqlDB, func(tx *sql.Tx) error {
		var err error
		rotated, err = rotateSecretTx(ctx, tx, name, age, grace, now, force, newValue)
		return err
	})
	return rotated, err
}

func rotateSecretTx(ctx context.Context, tx *sql.Tx, name string, age, grace time.Duration, now time.Time, force bool, newValue string) (bool, error) {
	rotated := false
	var oldValue, updated string
	if err := tx.QueryRowContext(ctx, `SELECT value,updated_at FROM app_secrets WHERE name=?`, name).Scan(&oldValue, &updated); err != nil {
		return false, HandleQueryError(err)
	}
	last, err := time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return false, fmt.Errorf("db: invalid updated_at for secret %s", name)
	}
	if !force && now.Sub(last) < age {
		return false, nil
	}
	if !force && name != SecretCSRFToken {
		var until string
		err := tx.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name=?`, name+"_previous_until").Scan(&until)
		if err == nil {
			deadline, parseErr := time.Parse(time.RFC3339Nano, until)
			if parseErr != nil {
				return false, fmt.Errorf("db: invalid previous secret expiry for %s", name)
			}
			if deadline.After(now) {
				return false, nil
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return false, HandleQueryError(err)
		}
	}
	until := now.Add(grace).Format(time.RFC3339Nano)
	stamp := now.Format(time.RFC3339Nano)
	for _, row := range []struct{ name, value string }{{name + "_previous", oldValue}, {name + "_previous_until", until}} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO app_secrets(name,value,version,updated_at) VALUES(?,?,1,?) ON CONFLICT(name) DO UPDATE SET value=excluded.value,version=app_secrets.version+1,updated_at=excluded.updated_at`, row.name, row.value, stamp); err != nil {
			return false, HandleQueryError(err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE app_secrets SET value=?,version=version+1,updated_at=? WHERE name=?`, newValue, stamp, name); err != nil {
		return false, HandleQueryError(err)
	}
	rotated = true
	return rotated, nil
}

// SetGraceSecret retains a previous key until its specified UTC expiry.
func SetGraceSecret(ctx context.Context, sqlDB Handle, name, value string, expires time.Time, replacementKey ...[]byte) error {
	var wrapKey []byte
	if len(replacementKey) > 0 {
		wrapKey = replacementKey[0]
	}
	return SetPreviousSecret(ctx, sqlDB, name, []byte(value), expires, wrapKey)
}

// SetPreviousSecret stores a rotation's previous value and expiry in the
// canonical app_secrets rows. The previous AES key must be wrapped under the
// replacement key before it is persisted.
func SetPreviousSecret(ctx context.Context, sqlDB Handle, name string, value []byte, expires time.Time, replacementKey []byte) error {
	stored := string(value)
	if name == "encryption_key" {
		sealed, err := security.Seal(replacementKey, value)
		if err != nil {
			return err
		}
		stored = sealed
	}
	return WithTransaction(ctx, sqlDB, func(tx *sql.Tx) error {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		until := expires.UTC().Format(time.RFC3339Nano)
		for _, row := range []struct{ name, value string }{{name + "_previous", stored}, {name + "_previous_until", until}} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO app_secrets(name,value,version,updated_at) VALUES(?,?,1,?) ON CONFLICT(name) DO UPDATE SET value=excluded.value,version=app_secrets.version+1,updated_at=excluded.updated_at`, row.name, row.value, now); err != nil {
				return HandleQueryError(err)
			}
		}
		return nil
	})
}

// GetPreviousSecret returns an unexpired prior value. The previous AES key is
// unwrapped only with the current key and is never returned as ciphertext.
func GetPreviousSecret(ctx context.Context, sqlDB Handle, name string, currentKey []byte) (string, error) {
	var value, until string
	err := sqlDB.QueryRowContext(ctx, `SELECT p.value,u.value FROM app_secrets p JOIN app_secrets u ON u.name=? WHERE p.name=?`, name+"_previous_until", name+"_previous").Scan(&value, &until)
	if err != nil {
		return "", HandleQueryError(err)
	}
	deadline, err := time.Parse(time.RFC3339Nano, until)
	if err != nil {
		return "", fmt.Errorf("db: invalid previous secret expiry for %s", name)
	}
	if !deadline.After(time.Now().UTC()) {
		return "", ErrNotFound
	}
	if name == "encryption_key" {
		plain, err := security.Open(currentKey, value)
		if err != nil {
			return "", err
		}
		return string(plain), nil
	}
	return value, nil
}

// HasPreviousSecret reports whether an unexpired previous value blocks a
// second rotation.
func HasPreviousSecret(ctx context.Context, sqlDB Handle, name string) (bool, error) {
	var until string
	err := sqlDB.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name=?`, name+"_previous_until").Scan(&until)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, HandleQueryError(err)
	}
	deadline, err := time.Parse(time.RFC3339Nano, until)
	if err != nil {
		return false, fmt.Errorf("db: invalid previous secret expiry for %s", name)
	}
	return deadline.After(time.Now().UTC()), nil
}

// DeletePreviousSecret removes a rotation's previous value and expiry rows.
func DeletePreviousSecret(ctx context.Context, sqlDB Handle, name string) error {
	return WithTransaction(ctx, sqlDB, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM app_secrets WHERE name IN (?,?)`, name+"_previous", name+"_previous_until"); err != nil {
			return HandleQueryError(err)
		}
		return nil
	})
}

// CleanupExpiredPreviousSecrets retires previous values after their grace
// periods and removes any rows left by the legacy grace-table schema.
func CleanupExpiredPreviousSecrets(ctx context.Context, sqlDB Handle) (int64, error) {
	var removed int64
	err := WithTransaction(ctx, sqlDB, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT name,value FROM app_secrets WHERE name LIKE '%_previous_until'`)
		if err != nil {
			return HandleQueryError(err)
		}
		type expiredSecret struct {
			name string
		}
		var expired []expiredSecret
		now := time.Now().UTC()
		for rows.Next() {
			var name, value string
			if err := rows.Scan(&name, &value); err != nil {
				rows.Close()
				return HandleQueryError(err)
			}
			deadline, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				rows.Close()
				return fmt.Errorf("db: invalid previous secret expiry row %s", name)
			}
			if !deadline.After(now) {
				expired = append(expired, expiredSecret{name: strings.TrimSuffix(name, "_previous_until")})
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return HandleQueryError(err)
		}
		if err := rows.Close(); err != nil {
			return HandleQueryError(err)
		}
		for _, item := range expired {
			result, err := tx.ExecContext(ctx, `DELETE FROM app_secrets WHERE name IN (?,?)`, item.name+"_previous", item.name+"_previous_until")
			if err != nil {
				return HandleQueryError(err)
			}
			count, err := result.RowsAffected()
			if err != nil {
				return err
			}
			removed += count / 2
		}
		if tableExists(ctx, sqlDB, "app_secret_grace") {
			if _, err := tx.ExecContext(ctx, `DELETE FROM app_secret_grace WHERE expires_at <= strftime('%s','now')`); err != nil {
				return HandleQueryError(err)
			}
		}
		return nil
	})
	return removed, err
}

// MigrateLegacyGraceSecrets moves pre-versioned grace values into canonical
// app_secrets rows, wrapping the previous encryption key under the current key.
func MigrateLegacyGraceSecrets(ctx context.Context, sqlDB Handle, currentKey []byte) error {
	if !tableExists(ctx, sqlDB, "app_secret_grace") {
		return nil
	}
	rows, err := sqlDB.QueryContext(ctx, `SELECT name,value,expires_at FROM app_secret_grace`)
	if err != nil {
		return HandleQueryError(err)
	}
	type grace struct {
		name, value string
		expires     int64
	}
	var legacy []grace
	for rows.Next() {
		var item grace
		if err := rows.Scan(&item.name, &item.value, &item.expires); err != nil {
			rows.Close()
			return HandleQueryError(err)
		}
		legacy = append(legacy, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return HandleQueryError(err)
	}
	if err := rows.Close(); err != nil {
		return HandleQueryError(err)
	}
	for _, item := range legacy {
		if item.name != SecretInstallation && item.name != "encryption_key" {
			continue
		}
		active, err := HasPreviousSecret(ctx, sqlDB, item.name)
		if err != nil {
			return err
		}
		if active {
			if _, err := sqlDB.ExecContext(ctx, `DELETE FROM app_secret_grace WHERE name=?`, item.name); err != nil {
				return HandleQueryError(err)
			}
			continue
		}
		if err := SetGraceSecret(ctx, sqlDB, item.name, item.value, time.Unix(item.expires, 0).UTC(), currentKey); err != nil {
			return err
		}
		if _, err := sqlDB.ExecContext(ctx, `DELETE FROM app_secret_grace WHERE name=?`, item.name); err != nil {
			return HandleQueryError(err)
		}
	}
	return nil
}

// GetGraceSecret returns an unexpired previous key and removes expired rows.
func GetGraceSecret(ctx context.Context, sqlDB Handle, name string) (string, error) {
	return GetPreviousSecret(ctx, sqlDB, name, nil)
}

// ReencryptStoredData re-seals persisted owner/linked token copies and
// security-report bodies under the replacement AES key in bounded batches.
func ReencryptStoredData(ctx context.Context, sqlDB Handle, oldKey, newKey []byte) error {
	if len(oldKey) != 32 || len(newKey) != 32 || bytes.Equal(oldKey, newKey) {
		return security.ErrSealKeySize
	}
	const batchSize = 100
	var lastTokenID int64
	for {
		count := 0
		err := WithTransaction(ctx, sqlDB, func(tx *sql.Tx) error {
			rows, err := tx.QueryContext(ctx, `SELECT id,token_enc FROM api_tokens WHERE id>? AND token_enc<>'' ORDER BY id LIMIT ?`, lastTokenID, batchSize)
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
					return HandleQueryError(err)
				}
				tokens = append(tokens, row)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return HandleQueryError(err)
			}
			if err := rows.Close(); err != nil {
				return HandleQueryError(err)
			}
			count = len(tokens)
			for _, row := range tokens {
				lastTokenID = row.id
				if _, err := security.Open(newKey, row.value); err == nil {
					continue
				}
				plain, err := security.Open(oldKey, row.value)
				if err != nil {
					return &DecryptFailure{Table: "api_tokens", RowID: fmt.Sprint(row.id), Cause: err}
				}
				sealed, err := security.Seal(newKey, plain)
				if err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, `UPDATE api_tokens SET token_enc=? WHERE id=?`, sealed, row.id); err != nil {
					return HandleQueryError(err)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		if count < batchSize {
			break
		}
	}

	var lastTrackingID string
	for {
		count := 0
		err := WithTransaction(ctx, sqlDB, func(tx *sql.Tx) error {
			rows, err := tx.QueryContext(ctx, `SELECT tracking_id,body_enc,body_alg,contact_enc FROM security_reports WHERE tracking_id>? ORDER BY tracking_id LIMIT ?`, lastTrackingID, batchSize)
			if err != nil {
				return HandleQueryError(err)
			}
			var reports []struct {
				id, algorithm string
				body, contact []byte
			}
			for rows.Next() {
				var row struct {
					id, algorithm string
					body, contact []byte
				}
				if err := rows.Scan(&row.id, &row.body, &row.algorithm, &row.contact); err != nil {
					rows.Close()
					return HandleQueryError(err)
				}
				reports = append(reports, row)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return HandleQueryError(err)
			}
			if err := rows.Close(); err != nil {
				return HandleQueryError(err)
			}
			count = len(reports)
			for _, row := range reports {
				lastTrackingID = row.id
				if row.algorithm == "aes-256-gcm" && len(row.body) != 0 {
					if _, currentErr := security.Open(newKey, string(row.body)); currentErr != nil {
						plain, err := security.Open(oldKey, string(row.body))
						if err != nil {
							return &DecryptFailure{Table: "security_reports", RowID: row.id, Cause: err}
						}
						sealed, err := security.Seal(newKey, plain)
						if err != nil {
							return err
						}
						if _, err = tx.ExecContext(ctx, `UPDATE security_reports SET body_enc=? WHERE tracking_id=?`, []byte(sealed), row.id); err != nil {
							return HandleQueryError(err)
						}
					}
				}
				if len(row.contact) != 0 {
					if _, currentErr := security.Open(newKey, string(row.contact)); currentErr != nil {
						plain, err := security.Open(oldKey, string(row.contact))
						if err != nil {
							return &DecryptFailure{Table: "security_reports", RowID: row.id, Cause: err}
						}
						sealed, err := security.Seal(newKey, plain)
						if err != nil {
							return err
						}
						if _, err = tx.ExecContext(ctx, `UPDATE security_reports SET contact_enc=? WHERE tracking_id=?`, []byte(sealed), row.id); err != nil {
							return HandleQueryError(err)
						}
					}
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		if count < batchSize {
			break
		}
	}
	return nil
}
