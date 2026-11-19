package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/security"
)

type APIToken struct {
	ID            int64
	OwnerID       string
	Kind          string
	Name          string
	TokenHash     string
	TokenPrefix   string
	TokenEnc      string
	CreatedAt     time.Time
	RotatedAt     *time.Time
	ExpiresAt     *time.Time
	LastUsedAt    *time.Time
	RevokedAt     *time.Time
	RevokedReason string
}

type Owner struct {
	ID                    string
	CreatedAt, LastSeenAt time.Time
}

func (t *APIToken) IsActive() bool {
	return t != nil && t.RevokedAt == nil && (t.ExpiresAt == nil || time.Now().Before(*t.ExpiresAt))
}
func (t *APIToken) Status() string {
	if t.RevokedAt != nil {
		return "revoked"
	}
	if t.ExpiresAt != nil && !time.Now().Before(*t.ExpiresAt) {
		return "expired"
	}
	return "active"
}
func MaskAPIToken(t *APIToken) string {
	if t == nil {
		return ""
	}
	tail := t.TokenPrefix
	if len(tail) > 4 {
		tail = tail[len(tail)-4:]
	}
	return "tok_••••••••••••••••••••••••" + tail
}

func CreateOwner(ctx context.Context, sqlDB Handle) (string, error) {
	id, err := security.RandomString("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789", 32)
	if err != nil {
		return "", err
	}
	_, err = sqlDB.ExecContext(ctx, `INSERT INTO owners(id) VALUES (?)`, id)
	if err != nil {
		return "", HandleQueryError(err)
	}
	return id, nil
}

func GetOwner(ctx context.Context, sqlDB Handle, id string) (*Owner, error) {
	var o Owner
	var created, seen int64
	if err := sqlDB.QueryRowContext(ctx, `SELECT id,created_at,last_seen_at FROM owners WHERE id=?`, id).Scan(&o.ID, &created, &seen); err != nil {
		return nil, HandleQueryError(err)
	}
	o.CreatedAt = time.Unix(created, 0).UTC()
	o.LastSeenAt = time.Unix(seen, 0).UTC()
	return &o, nil
}

func CreatePrimaryToken(ctx context.Context, sqlDB Handle, ownerID string, key []byte) (string, *APIToken, error) {
	return createToken(ctx, sqlDB, ownerID, "primary", "", nil, key, nil)
}
func CreateLinkedToken(ctx context.Context, sqlDB Handle, ownerID, name string, expiry *time.Time, maximum int, key []byte) (string, *APIToken, error) {
	name = strings.TrimSpace(name)
	if len(name) < 1 || len(name) > 64 {
		return "", nil, errors.New("token name must contain 1 to 64 characters")
	}
	if maximum <= 0 {
		maximum = 25
	}
	preflight := func(tx *sql.Tx) error {
		var count, duplicate int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE owner_id=? AND kind='linked' AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>strftime('%s','now'))`, ownerID).Scan(&count); err != nil {
			return HandleQueryError(err)
		}
		if count >= maximum {
			return errors.New("maximum active linked tokens reached")
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE owner_id=? AND kind='linked' AND name=?`, ownerID, name).Scan(&duplicate); err != nil {
			return HandleQueryError(err)
		}
		if duplicate > 0 {
			return errors.New("token name already exists")
		}
		return nil
	}
	return createToken(ctx, sqlDB, ownerID, "linked", name, expiry, key, preflight)
}

func createToken(ctx context.Context, sqlDB Handle, ownerID, kind, name string, expiry *time.Time, key []byte, preflight func(*sql.Tx) error) (string, *APIToken, error) {
	if len(key) != 32 {
		return "", nil, security.ErrSealKeySize
	}
	raw, err := security.GenerateToken()
	if err != nil {
		return "", nil, err
	}
	sealed, err := security.Seal(key, []byte(raw))
	if err != nil {
		return "", nil, err
	}
	var expires any
	if expiry != nil {
		expires = expiry.UTC().Unix()
	}
	var dbName any
	if name != "" {
		dbName = name
	}
	var id int64
	err = WithTransaction(ctx, sqlDB, func(tx *sql.Tx) error {
		if preflight != nil {
			if err := preflight(tx); err != nil {
				return err
			}
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO api_tokens(owner_id,kind,name,token_hash,token_prefix,token_enc,expires_at) VALUES (?,?,?,?,?,?,?)`, ownerID, kind, dbName, security.HashToken(raw), security.TokenPrefix(raw), sealed, expires)
		if err != nil {
			return HandleQueryError(err)
		}
		id, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return "", nil, err
	}
	tok, err := GetTokenByID(ctx, sqlDB, id)
	return raw, tok, err
}

func LookupTokenByRaw(ctx context.Context, sqlDB Handle, raw string) (*APIToken, error) {
	if !strings.HasPrefix(raw, "tok_") {
		return nil, ErrNotFound
	}
	tok, err := tokenByHash(ctx, sqlDB, security.TokenPrefix(raw), security.HashToken(raw))
	if err != nil {
		return nil, err
	}
	if !tok.IsActive() {
		return nil, ErrNotFound
	}
	_ = TouchTokenLastUsed(ctx, sqlDB, tok.ID, tok.OwnerID)
	return tok, nil
}

func TouchTokenLastUsed(ctx context.Context, sqlDB Handle, id int64, ownerID string) error {
	return WithTransaction(ctx, sqlDB, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE api_tokens SET last_used_at=CASE WHEN last_used_at IS NULL OR last_used_at<=strftime('%s','now')-60 THEN strftime('%s','now') ELSE last_used_at END WHERE id=?`, id); err != nil {
			return HandleQueryError(err)
		}
		_, err := tx.ExecContext(ctx, `UPDATE owners SET last_seen_at=strftime('%s','now') WHERE id=?`, ownerID)
		return HandleQueryError(err)
	})
}

func GetTokenByID(ctx context.Context, sqlDB Handle, id int64) (*APIToken, error) {
	return scanToken(sqlDB.QueryRowContext(ctx, tokenSelect+` WHERE id=?`, id))
}
func GetTokenByPrefix(ctx context.Context, sqlDB Handle, prefix string) (*APIToken, error) {
	return tokenByPrefix(ctx, sqlDB, prefix)
}
func GetPrimaryToken(ctx context.Context, sqlDB Handle, owner string) (*APIToken, error) {
	return scanToken(sqlDB.QueryRowContext(ctx, tokenSelect+` WHERE owner_id=? AND kind='primary' AND revoked_at IS NULL`, owner))
}
func ListOwnerTokens(ctx context.Context, sqlDB Handle, owner string) ([]APIToken, error) {
	return listTokens(ctx, sqlDB, tokenSelect+` WHERE owner_id=? ORDER BY kind,created_at DESC,id DESC`, owner)
}
func ListActiveTokens(ctx context.Context, sqlDB Handle) ([]APIToken, error) {
	return listTokens(ctx, sqlDB, tokenSelect+` WHERE revoked_at IS NULL ORDER BY created_at DESC`)
}
func ListAllTokens(ctx context.Context, sqlDB Handle) ([]APIToken, error) {
	return listTokens(ctx, sqlDB, tokenSelect+` ORDER BY created_at DESC,id DESC`)
}

const tokenSelect = `SELECT id,owner_id,kind,name,token_hash,token_prefix,token_enc,created_at,rotated_at,expires_at,last_used_at,revoked_at,revoked_reason FROM api_tokens`

func tokenByHash(ctx context.Context, sqlDB Handle, prefix, hash string) (*APIToken, error) {
	return scanToken(sqlDB.QueryRowContext(ctx, tokenSelect+` WHERE token_prefix=? AND token_hash=?`, prefix, hash))
}
func scanToken(row tokenRowScanner) (*APIToken, error) {
	var t APIToken
	var created int64
	var rotated, expiry, last, revoked sql.NullInt64
	var name, reason sql.NullString
	if err := row.Scan(&t.ID, &t.OwnerID, &t.Kind, &name, &t.TokenHash, &t.TokenPrefix, &t.TokenEnc, &created, &rotated, &expiry, &last, &revoked, &reason); err != nil {
		return nil, HandleQueryError(err)
	}
	t.Name = name.String
	t.CreatedAt = time.Unix(created, 0).UTC()
	t.RotatedAt = unixPtr(rotated)
	t.ExpiresAt = unixPtr(expiry)
	t.LastUsedAt = unixPtr(last)
	t.RevokedAt = unixPtr(revoked)
	t.RevokedReason = reason.String
	return &t, nil
}

type tokenRowScanner interface{ Scan(...any) error }

func unixPtr(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.Unix(v.Int64, 0).UTC()
	return &t
}
func listTokens(ctx context.Context, db Handle, q string, args ...any) ([]APIToken, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, HandleQueryError(err)
	}
	defer rows.Close()
	out := []APIToken{}
	for rows.Next() {
		t, e := scanToken(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, HandleQueryError(err)
	}
	return out, nil
}

func RevealToken(t *APIToken, key []byte) (string, error) {
	if t == nil || t.TokenEnc == "" {
		return "", errors.New("token ciphertext unavailable")
	}
	value, err := security.Open(key, t.TokenEnc)
	return string(value), err
}
func RotateToken(ctx context.Context, db Handle, id int64, owner, kind string, key []byte) (string, *APIToken, error) {
	if len(key) != 32 {
		return "", nil, security.ErrSealKeySize
	}
	old, err := GetTokenByID(ctx, db, id)
	if err != nil {
		return "", nil, err
	}
	if old.OwnerID != owner || old.Kind != kind || !old.IsActive() {
		return "", nil, ErrNotFound
	}
	raw, err := security.GenerateToken()
	if err != nil {
		return "", nil, err
	}
	sealed, err := security.Seal(key, []byte(raw))
	if err != nil {
		return "", nil, err
	}
	_, err = db.ExecContext(ctx, `UPDATE api_tokens SET token_hash=?,token_prefix=?,token_enc=?,rotated_at=strftime('%s','now') WHERE id=? AND owner_id=? AND revoked_at IS NULL`, security.HashToken(raw), security.TokenPrefix(raw), sealed, id, owner)
	if err != nil {
		return "", nil, HandleQueryError(err)
	}
	tok, err := GetTokenByID(ctx, db, id)
	return raw, tok, err
}

func UpdateLinkedToken(ctx context.Context, db Handle, owner string, id int64, name string, expiry *time.Time, clearExpiry bool) error {
	name = strings.TrimSpace(name)
	if len(name) < 1 || len(name) > 64 {
		return errors.New("token name must contain 1 to 64 characters")
	}
	var exp any
	if expiry != nil {
		exp = expiry.UTC().Unix()
	} else if !clearExpiry {
		var current sql.NullInt64
		if err := db.QueryRowContext(ctx, `SELECT expires_at FROM api_tokens WHERE id=? AND owner_id=? AND kind='linked' AND revoked_at IS NULL`, id, owner).Scan(&current); err != nil {
			return HandleQueryError(err)
		}
		if current.Valid {
			exp = current.Int64
		}
	}
	res, err := db.ExecContext(ctx, `UPDATE api_tokens SET name=?,expires_at=? WHERE id=? AND owner_id=? AND kind='linked' AND revoked_at IS NULL`, name, exp, id, owner)
	return checkRowsAffected(res, err)
}
func RevokeLinkedToken(ctx context.Context, db Handle, owner string, id int64) error {
	res, err := db.ExecContext(ctx, `UPDATE api_tokens SET revoked_at=strftime('%s','now'),revoked_reason='owner_revoked' WHERE id=? AND owner_id=? AND kind='linked' AND revoked_at IS NULL`, id, owner)
	return checkRowsAffected(res, err)
}
func RevokeToken(ctx context.Context, db Handle, prefix, reason string) error {
	tok, err := tokenByPrefix(ctx, db, prefix)
	if err != nil {
		return err
	}
	if !tok.IsActive() {
		return ErrNotFound
	}
	if tok.Kind == "primary" {
		_, err = db.ExecContext(ctx, `UPDATE api_tokens SET revoked_at=strftime('%s','now'),revoked_reason=? WHERE owner_id=? AND revoked_at IS NULL`, reason, tok.OwnerID)
		return HandleQueryError(err)
	}
	res, err := db.ExecContext(ctx, `UPDATE api_tokens SET revoked_at=strftime('%s','now'),revoked_reason=? WHERE id=? AND revoked_at IS NULL`, reason, tok.ID)
	return checkRowsAffected(res, err)
}
func tokenByPrefix(ctx context.Context, db Handle, prefix string) (*APIToken, error) {
	return scanToken(db.QueryRowContext(ctx, tokenSelect+` WHERE token_prefix=?`, prefix))
}
func CleanupExpiredTokens(ctx context.Context, db Handle) (int64, error) {
	// Expired linked tokens remain visible to their owner until revoked or
	// reconfigured, per AI.md PART 8 "Token Management". Authentication
	// rejects them in LookupTokenByRaw, so cleanup intentionally removes none.
	return 0, nil
}

func PurgeIdleOwners(ctx context.Context, db Handle, idleDays int) (int64, error) {
	if idleDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -idleDays).Unix()
	result, err := db.ExecContext(ctx, `DELETE FROM owners WHERE last_seen_at <= ? AND NOT EXISTS (SELECT 1 FROM links WHERE links.owner_id = owners.id)`, cutoff)
	if err != nil {
		return 0, HandleQueryError(err)
	}
	return result.RowsAffected()
}
