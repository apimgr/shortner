package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// PGPKey is the keypair metadata AI.md PART 11 "Keypair properties stored
// in DB" tracks. The key material itself never reaches this table — the
// public key is a file under `{config_dir}/security/`, and the private key
// is a sealed file next to it.
type PGPKey struct {
	Fingerprint string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	// LastRotatedAt is when this key replaced an earlier one; zero when
	// the key was generated rather than rotated into place.
	LastRotatedAt time.Time
	// KeyserversPublished maps a keyserver entry to the time its last
	// successful submission completed.
	KeyserversPublished map[string]time.Time
	// Revoked is set when `--maintenance pgp delete` removed the key
	// files. The row stays so the fingerprint remains in audit history.
	Revoked bool
}

// Expired reports whether the key's two-year lifetime has elapsed.
func (k PGPKey) Expired(now time.Time) bool {
	return !k.ExpiresAt.IsZero() && now.UTC().After(k.ExpiresAt)
}

// UpsertPGPKey stores or replaces a keypair's metadata row.
func UpsertPGPKey(ctx context.Context, sqlDB *sql.DB, key PGPKey) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	published, err := encodePublished(key.KeyserversPublished)
	if err != nil {
		return err
	}

	var rotated any
	if !key.LastRotatedAt.IsZero() {
		rotated = key.LastRotatedAt.UTC().Unix()
	}

	revoked := 0
	if key.Revoked {
		revoked = 1
	}

	_, err = sqlDB.ExecContext(ctx,
		`INSERT INTO pgp_keys (fingerprint, created_at, expires_at, last_rotated_at, keyservers_published, revoked)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(fingerprint) DO UPDATE SET
		     created_at           = excluded.created_at,
		     expires_at           = excluded.expires_at,
		     last_rotated_at      = excluded.last_rotated_at,
		     keyservers_published = excluded.keyservers_published,
		     revoked              = excluded.revoked`,
		strings.ToUpper(key.Fingerprint), key.CreatedAt.UTC().Unix(), key.ExpiresAt.UTC().Unix(),
		rotated, published, revoked)
	if err != nil {
		return HandleQueryError(err)
	}
	return nil
}

// GetPGPKey returns one keypair row by fingerprint, or ErrNotFound.
func GetPGPKey(ctx context.Context, sqlDB *sql.DB, fingerprint string) (*PGPKey, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	row := sqlDB.QueryRowContext(ctx,
		`SELECT fingerprint, created_at, expires_at, last_rotated_at, keyservers_published, revoked
		   FROM pgp_keys WHERE fingerprint = ?`, strings.ToUpper(fingerprint))
	return scanPGPKey(row)
}

// LatestPGPKey returns the most recently created non-revoked keypair row,
// or ErrNotFound when no keypair has been generated.
func LatestPGPKey(ctx context.Context, sqlDB *sql.DB) (*PGPKey, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	row := sqlDB.QueryRowContext(ctx,
		`SELECT fingerprint, created_at, expires_at, last_rotated_at, keyservers_published, revoked
		   FROM pgp_keys WHERE revoked = 0 ORDER BY created_at DESC LIMIT 1`)
	return scanPGPKey(row)
}

// RevokePGPKey marks a fingerprint revoked without deleting the row, per
// AI.md PART 11: "the key file may be gone but the fingerprint stays in
// audit history".
func RevokePGPKey(ctx context.Context, sqlDB *sql.DB, fingerprint string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := sqlDB.ExecContext(ctx,
		`UPDATE pgp_keys SET revoked = 1 WHERE fingerprint = ?`, strings.ToUpper(fingerprint))
	if err != nil {
		return HandleQueryError(err)
	}
	return nil
}

// RevokeAllPGPKeys marks every keypair row revoked, used by
// `--maintenance pgp delete` when the on-disk keys are removed.
func RevokeAllPGPKeys(ctx context.Context, sqlDB *sql.DB) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if _, err := sqlDB.ExecContext(ctx, `UPDATE pgp_keys SET revoked = 1`); err != nil {
		return HandleQueryError(err)
	}
	return nil
}

// scanPGPKey decodes one pgp_keys row.
func scanPGPKey(row *sql.Row) (*PGPKey, error) {
	var (
		key       PGPKey
		created   int64
		expires   int64
		rotated   sql.NullInt64
		published string
		revoked   int
	)
	if err := row.Scan(&key.Fingerprint, &created, &expires, &rotated, &published, &revoked); err != nil {
		return nil, HandleQueryError(err)
	}
	key.CreatedAt = time.Unix(created, 0).UTC()
	key.ExpiresAt = time.Unix(expires, 0).UTC()
	if rotated.Valid {
		key.LastRotatedAt = time.Unix(rotated.Int64, 0).UTC()
	}
	key.KeyserversPublished = decodePublished(published)
	key.Revoked = revoked != 0
	return &key, nil
}

// encodePublished serializes the keyserver publication map. An empty map
// stores an empty string so the column stays readable by hand.
func encodePublished(published map[string]time.Time) (string, error) {
	if len(published) == 0 {
		return "", nil
	}
	stamps := make(map[string]string, len(published))
	for server, at := range published {
		stamps[server] = at.UTC().Format(time.RFC3339)
	}
	body, err := json.Marshal(stamps)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// decodePublished reverses encodePublished, tolerating an unreadable value
// by returning an empty map — publication history is advisory, and a
// corrupt column must never make the keypair unusable.
func decodePublished(raw string) map[string]time.Time {
	out := map[string]time.Time{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	stamps := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &stamps); err != nil {
		return out
	}
	for server, value := range stamps {
		at, err := time.Parse(time.RFC3339, value)
		if err != nil {
			continue
		}
		out[server] = at.UTC()
	}
	return out
}
