package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	TokenLast4    string
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
	return "own_••••••••••••••••••••••••" + t.TokenLast4
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
		res, err := tx.ExecContext(ctx, `INSERT INTO api_tokens(owner_id,kind,name,token_hash,token_prefix,token_last4,token_enc,expires_at) VALUES (?,?,?,?,?,?,?,?)`, ownerID, kind, dbName, security.HashToken(raw), security.TokenPrefix(raw), tokenLast4(raw), sealed, expires)
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

func LookupTokenByRaw(ctx context.Context, sqlDB Handle, raw string, encryptionKeys ...[]byte) (*APIToken, error) {
	if !strings.HasPrefix(raw, "own_") {
		return nil, ErrNotFound
	}
	tok, err := tokenByHash(ctx, sqlDB, security.TokenPrefix(raw), security.HashToken(raw))
	if err != nil {
		return nil, err
	}
	if !tok.IsActive() {
		return nil, ErrNotFound
	}
	keyAvailable := len(encryptionKeys) > 0 && len(encryptionKeys[0]) == 32
	needsEncryption := tok.TokenEnc == ""
	if tok.TokenEnc != "" && keyAvailable {
		decrypted, openErr := security.Open(encryptionKeys[0], tok.TokenEnc)
		needsEncryption = openErr != nil || string(decrypted) != raw
	}
	verifiedLast4 := tokenLast4(raw)
	if tok.TokenLast4 != verifiedLast4 || (needsEncryption && keyAvailable) {
		tok.TokenLast4 = tokenLast4(raw)
		sealed := tok.TokenEnc
		replaceEncrypted := false
		if needsEncryption && keyAvailable {
			if encrypted, sealErr := security.Seal(encryptionKeys[0], []byte(raw)); sealErr == nil {
				sealed = encrypted
				replaceEncrypted = true
			}
		}
		if replaceEncrypted {
			_, _ = sqlDB.ExecContext(ctx, `UPDATE api_tokens SET token_last4=?, token_enc=? WHERE id=? AND token_hash=? AND token_prefix=?`, tok.TokenLast4, sealed, tok.ID, tok.TokenHash, tok.TokenPrefix)
			tok.TokenEnc = sealed
		} else {
			_, _ = sqlDB.ExecContext(ctx, `UPDATE api_tokens SET token_last4=? WHERE id=? AND token_hash=? AND token_prefix=?`, tok.TokenLast4, tok.ID, tok.TokenHash, tok.TokenPrefix)
		}
	}
	_ = TouchTokenLastUsed(ctx, sqlDB, tok.ID, tok.OwnerID)
	return tok, nil
}

// BackfillTokenLast4 fills the display suffix for rows whose encrypted token
// copy is available. Older hash-only rows cannot be reconstructed and are
// completed the next time their raw token is successfully presented.
func BackfillTokenLast4(ctx context.Context, sqlDB Handle, key []byte) (int, error) {
	if len(key) != 32 {
		return 0, security.ErrSealKeySize
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rows, err := sqlDB.QueryContext(ctx, `SELECT id,token_hash,token_prefix,token_enc FROM api_tokens WHERE token_last4=''`)
	if err != nil {
		return 0, HandleQueryError(err)
	}
	type pending struct {
		id                int64
		hash, prefix, enc string
	}
	var items []pending
	for rows.Next() {
		var item pending
		if err := rows.Scan(&item.id, &item.hash, &item.prefix, &item.enc); err != nil {
			rows.Close()
			return 0, HandleQueryError(err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, HandleQueryError(err)
	}
	if err := rows.Close(); err != nil {
		return 0, HandleQueryError(err)
	}
	skipped := 0
	for _, item := range items {
		if item.enc == "" {
			skipped++
			continue
		}
		raw, err := security.Open(key, item.enc)
		if err != nil {
			skipped++
			continue
		}
		if security.HashToken(string(raw)) != item.hash || security.TokenPrefix(string(raw)) != item.prefix {
			skipped++
			continue
		}
		if _, err := sqlDB.ExecContext(ctx, `UPDATE api_tokens SET token_last4=? WHERE id=? AND token_hash=? AND token_prefix=? AND token_last4=''`, tokenLast4(string(raw)), item.id, item.hash, item.prefix); err != nil {
			return skipped, HandleQueryError(err)
		}
	}
	return skipped, nil
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

const tokenSelect = `SELECT id,owner_id,kind,name,token_hash,token_prefix,token_last4,token_enc,created_at,rotated_at,expires_at,last_used_at,revoked_at,revoked_reason FROM api_tokens`

func tokenByHash(ctx context.Context, sqlDB Handle, prefix, hash string) (*APIToken, error) {
	return scanToken(sqlDB.QueryRowContext(ctx, tokenSelect+` WHERE token_prefix=? AND token_hash=?`, prefix, hash))
}
func scanToken(row tokenRowScanner) (*APIToken, error) {
	var t APIToken
	var created int64
	var rotated, expiry, last, revoked sql.NullInt64
	var name, reason sql.NullString
	if err := row.Scan(&t.ID, &t.OwnerID, &t.Kind, &name, &t.TokenHash, &t.TokenPrefix, &t.TokenLast4, &t.TokenEnc, &created, &rotated, &expiry, &last, &revoked, &reason); err != nil {
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
	if err != nil {
		return "", err
	}
	raw := string(value)
	if security.HashToken(raw) != t.TokenHash || security.TokenPrefix(raw) != t.TokenPrefix {
		return "", errors.New("token ciphertext does not match stored credential")
	}
	return raw, nil
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
	err = WithTransaction(ctx, db, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE api_tokens SET token_hash=?,token_prefix=?,token_last4=?,token_enc=?,rotated_at=strftime('%s','now') WHERE id=? AND owner_id=? AND revoked_at IS NULL`, security.HashToken(raw), security.TokenPrefix(raw), tokenLast4(raw), sealed, id, owner)
		if err != nil {
			return HandleQueryError(err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return HandleQueryError(err)
		}
		if updated != 1 {
			return ErrNotFound
		}
		if err := RotateCSRFSecretTx(ctx, tx); err != nil {
			return fmt.Errorf("db: rotate CSRF secret after API token rotation: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	tok, err := GetTokenByID(ctx, db, id)
	return raw, tok, err
}

func tokenLast4(raw string) string {
	if len(raw) < 4 {
		return ""
	}
	return raw[len(raw)-4:]
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

// DeleteOwner removes the owner, all tokens, linked resources, and their
// click events in one transaction, as required for primary-token revocation.
func DeleteOwner(ctx context.Context, db Handle, owner string) (int, int, error) {
	var links, tokens int
	err := WithTransaction(ctx, db, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM links WHERE owner_id=?`, owner).Scan(&links); err != nil {
			return HandleQueryError(err)
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE owner_id=?`, owner).Scan(&tokens); err != nil {
			return HandleQueryError(err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM clicks WHERE link_id IN (SELECT id FROM links WHERE owner_id=?)`, owner); err != nil {
			return HandleQueryError(err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM links WHERE owner_id=?`, owner); err != nil {
			return HandleQueryError(err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM api_tokens WHERE owner_id=?`, owner); err != nil {
			return HandleQueryError(err)
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM owners WHERE id=?`, owner)
		return checkRowsAffected(res, err)
	})
	if err != nil {
		return 0, 0, err
	}
	return links, tokens, nil
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
