package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/apimgr/shortner/src/security"
)

// APIToken mirrors the api_tokens table, per AI.md PART 8 "API Tokens"
// schema and PART 11 "API Token Model".
type APIToken struct {
	ID            int64
	TokenHash     string
	TokenPrefix   string
	ResourceType  string
	ResourceID    string
	CreatedAt     time.Time
	ExpiresAt     *time.Time
	LastUsedAt    *time.Time
	RevokedAt     *time.Time
	RevokedReason string
}

// IsActive reports whether the token is neither revoked nor expired.
func (t *APIToken) IsActive() bool {
	if t.RevokedAt != nil {
		return false
	}
	if t.ExpiresAt != nil && time.Now().After(*t.ExpiresAt) {
		return false
	}
	return true
}

// CreateResourceToken generates a new "tok_" owner token for
// (resourceType, resourceID), stores its SHA-256 hash, and returns both
// the DB row and the raw token — the ONLY time the raw value is ever
// available, per AI.md PART 11 "Resource Owner Tokens": "the raw token is
// shown once ... and never retrievable again."
func CreateResourceToken(ctx context.Context, sqlDB *sql.DB, resourceType, resourceID string, expiresAt *time.Time) (raw string, tok *APIToken, err error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	raw, err = security.GenerateToken()
	if err != nil {
		return "", nil, err
	}
	hash := security.HashToken(raw)
	prefix := security.TokenPrefix(raw)

	var expires sql.NullInt64
	if expiresAt != nil {
		expires = sql.NullInt64{Int64: expiresAt.Unix(), Valid: true}
	}

	res, execErr := sqlDB.ExecContext(ctx,
		`INSERT INTO api_tokens (token_hash, token_prefix, resource_type, resource_id, expires_at)
		 VALUES (?, ?, ?, ?, ?)`,
		hash, prefix, resourceType, resourceID, expires)
	if execErr != nil {
		return "", nil, HandleQueryError(execErr)
	}
	id, execErr := res.LastInsertId()
	if execErr != nil {
		return "", nil, fmt.Errorf("db: create resource token: %w", execErr)
	}

	tok, err = getTokenByID(ctx, sqlDB, id)
	if err != nil {
		return "", nil, err
	}
	return raw, tok, nil
}

// LookupTokenByRaw finds the api_tokens row matching raw's prefix, then
// confirms the full hash with a constant-time comparison, per AI.md
// PART 11 "Ownership check": "SHA-256 hash, lookup by prefix ...
// Verify hash matches." Returns ErrNotFound if no active token matches.
func LookupTokenByRaw(ctx context.Context, sqlDB *sql.DB, raw string) (*APIToken, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	prefix := security.TokenPrefix(raw)
	rows, err := sqlDB.QueryContext(ctx,
		`SELECT id, token_hash, token_prefix, resource_type, resource_id,
		        created_at, expires_at, last_used_at, revoked_at, revoked_reason
		 FROM api_tokens WHERE token_prefix = ?`, prefix)
	if err != nil {
		return nil, HandleQueryError(err)
	}
	defer rows.Close()

	for rows.Next() {
		tok, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		if security.CompareTokenHash(raw, tok.TokenHash) {
			if !tok.IsActive() {
				return nil, ErrNotFound
			}
			return tok, nil
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate tokens: %w", err)
	}
	return nil, ErrNotFound
}

// TouchTokenLastUsed updates last_used_at to now for the given token id,
// per AI.md PART 11 "Ownership check" step 6.
func TouchTokenLastUsed(ctx context.Context, sqlDB *sql.DB, id int64) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := sqlDB.ExecContext(ctx,
		`UPDATE api_tokens SET last_used_at = strftime('%s', 'now') WHERE id = ?`, id)
	if err != nil {
		return HandleQueryError(err)
	}
	return nil
}

// RevokeToken marks a token revoked with reason, per AI.md PART 11
// "Operator revocation": "{project_name} --maintenance token revoke
// <prefix>".
func RevokeToken(ctx context.Context, sqlDB *sql.DB, prefix, reason string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := sqlDB.ExecContext(ctx,
		`UPDATE api_tokens SET revoked_at = strftime('%s', 'now'), revoked_reason = ?
		 WHERE token_prefix = ? AND revoked_at IS NULL`, reason, prefix)
	return checkRowsAffected(res, err)
}

// CleanupExpiredTokens permanently deletes tokens whose expires_at has
// passed, per AI.md PART 18 "Built-in Tasks" -> `token_cleanup`: "Remove
// expired API tokens and sessions". It returns the number of rows removed.
// Revoked-but-not-yet-expired tokens are left in place (RevokeToken already
// makes them inactive; deletion is reserved for tokens whose own expiry has
// passed).
func CleanupExpiredTokens(ctx context.Context, sqlDB *sql.DB) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	res, err := sqlDB.ExecContext(ctx,
		`DELETE FROM api_tokens WHERE expires_at IS NOT NULL AND expires_at < strftime('%s', 'now')`)
	if err != nil {
		return 0, HandleQueryError(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, HandleQueryError(err)
	}
	return n, nil
}

// ListActiveTokens returns all non-revoked tokens, per AI.md PART 11
// "Operator revocation": "{project_name} --maintenance token list".
func ListActiveTokens(ctx context.Context, sqlDB *sql.DB) ([]APIToken, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rows, err := sqlDB.QueryContext(ctx,
		`SELECT id, token_hash, token_prefix, resource_type, resource_id,
		        created_at, expires_at, last_used_at, revoked_at, revoked_reason
		 FROM api_tokens WHERE revoked_at IS NULL ORDER BY created_at DESC`)
	if err != nil {
		return nil, HandleQueryError(err)
	}
	defer rows.Close()

	var out []APIToken
	for rows.Next() {
		tok, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *tok)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate tokens: %w", err)
	}
	return out, nil
}

func getTokenByID(ctx context.Context, sqlDB *sql.DB, id int64) (*APIToken, error) {
	row := sqlDB.QueryRowContext(ctx,
		`SELECT id, token_hash, token_prefix, resource_type, resource_id,
		        created_at, expires_at, last_used_at, revoked_at, revoked_reason
		 FROM api_tokens WHERE id = ?`, id)
	var t APIToken
	var createdAt int64
	var expiresAt, lastUsedAt, revokedAt sql.NullInt64
	var revokedReason sql.NullString
	if err := row.Scan(&t.ID, &t.TokenHash, &t.TokenPrefix, &t.ResourceType, &t.ResourceID,
		&createdAt, &expiresAt, &lastUsedAt, &revokedAt, &revokedReason); err != nil {
		return nil, HandleQueryError(err)
	}
	fillTokenTimes(&t, createdAt, expiresAt, lastUsedAt, revokedAt, revokedReason)
	return &t, nil
}

// rowScanner abstracts *sql.Rows for scanToken so it can be reused by
// both single-row and multi-row query sites.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanToken(row rowScanner) (*APIToken, error) {
	var t APIToken
	var createdAt int64
	var expiresAt, lastUsedAt, revokedAt sql.NullInt64
	var revokedReason sql.NullString
	if err := row.Scan(&t.ID, &t.TokenHash, &t.TokenPrefix, &t.ResourceType, &t.ResourceID,
		&createdAt, &expiresAt, &lastUsedAt, &revokedAt, &revokedReason); err != nil {
		return nil, fmt.Errorf("db: scan token: %w", err)
	}
	fillTokenTimes(&t, createdAt, expiresAt, lastUsedAt, revokedAt, revokedReason)
	return &t, nil
}

func fillTokenTimes(t *APIToken, createdAt int64, expiresAt, lastUsedAt, revokedAt sql.NullInt64, revokedReason sql.NullString) {
	t.CreatedAt = time.Unix(createdAt, 0).UTC()
	if expiresAt.Valid {
		v := time.Unix(expiresAt.Int64, 0).UTC()
		t.ExpiresAt = &v
	}
	if lastUsedAt.Valid {
		v := time.Unix(lastUsedAt.Int64, 0).UTC()
		t.LastUsedAt = &v
	}
	if revokedAt.Valid {
		v := time.Unix(revokedAt.Int64, 0).UTC()
		t.RevokedAt = &v
	}
	t.RevokedReason = revokedReason.String
}
