package db

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/security"
)

// Link is a shortened URL record, per IDEA.md "Data models": "Link: id,
// short_code (or custom slug), destination_url, created_at, expires_at,
// click_count."
type Link struct {
	ID             int64
	ShortCode      string
	DestinationURL string
	CreatedAt      time.Time
	ExpiresAt      *time.Time
	ClickCount     int64
}

// IsExpired reports whether the link has an expiration set and it has
// passed, per IDEA.md "Business rules": "Expired links resolve with 410
// Gone instead of redirecting." Response-layer handling of 410 is out of
// scope here (AI.md PART 14) — this is the DB-layer predicate that layer
// depends on.
func (l *Link) IsExpired() bool {
	return l.ExpiresAt != nil && time.Now().After(*l.ExpiresAt)
}

// maxShortCodeAttempts bounds the collision-retry loop in
// CreateLinkAutoCode; with a 62^6 keyspace, collisions are vanishingly
// rare even at high volume.
const maxShortCodeAttempts = 10

// CreateLinkAutoCode inserts a new link with a freshly generated 6-char
// short code, retrying on a uniqueness collision, per IDEA.md "Business
// rules" ("Short codes: 6-char alphanumeric, auto-generated (62^6
// keyspace)"). Query has a 10s timeout per AI.md PART 10 "Query
// Timeouts" (INSERT tier).
func CreateLinkAutoCode(ctx context.Context, sqlDB *sql.DB, destinationURL string, expiresAt *time.Time) (*Link, error) {
	for attempt := 0; attempt < maxShortCodeAttempts; attempt++ {
		code, err := security.GenerateShortCode()
		if err != nil {
			return nil, err
		}
		link, err := insertLink(ctx, sqlDB, code, destinationURL, expiresAt)
		if err == nil {
			return link, nil
		}
		if isUniqueConstraintError(err) {
			continue
		}
		return nil, err
	}
	return nil, fmt.Errorf("db: create link: exhausted %d short-code attempts", maxShortCodeAttempts)
}

// CreateLinkCustomSlug inserts a new link with an operator/user-supplied
// slug. Format and reserved-name validation is the caller's
// responsibility (see security.ValidateSlugFormat / IsReservedSlug) —
// this function only enforces the DB-level uniqueness constraint.
func CreateLinkCustomSlug(ctx context.Context, sqlDB *sql.DB, slug, destinationURL string, expiresAt *time.Time) (*Link, error) {
	link, err := insertLink(ctx, sqlDB, slug, destinationURL, expiresAt)
	if err != nil {
		if isUniqueConstraintError(err) {
			return nil, fmt.Errorf("db: create link: %w", ErrSlugTaken)
		}
		return nil, err
	}
	return link, nil
}

// ErrSlugTaken is returned when a custom slug collides with an existing
// link's short_code.
var ErrSlugTaken = fmt.Errorf("slug already in use")

func insertLink(ctx context.Context, sqlDB *sql.DB, code, destinationURL string, expiresAt *time.Time) (*Link, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var expires sql.NullInt64
	if expiresAt != nil {
		expires = sql.NullInt64{Int64: expiresAt.Unix(), Valid: true}
	}

	res, err := sqlDB.ExecContext(ctx,
		`INSERT INTO links (short_code, destination_url, expires_at) VALUES (?, ?, ?)`,
		code, destinationURL, expires)
	if err != nil {
		return nil, HandleQueryError(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("db: create link: %w", err)
	}

	return GetLinkByID(ctx, sqlDB, id)
}

// GetLinkByShortCode looks up a link by its short_code/slug. Returns
// ErrNotFound if no such link exists.
func GetLinkByShortCode(ctx context.Context, sqlDB *sql.DB, code string) (*Link, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	row := sqlDB.QueryRowContext(ctx,
		`SELECT id, short_code, destination_url, created_at, expires_at, click_count
		 FROM links WHERE short_code = ?`, code)
	return scanLink(row)
}

// GetLinkByID looks up a link by its internal primary key. Returns
// ErrNotFound if no such link exists.
func GetLinkByID(ctx context.Context, sqlDB *sql.DB, id int64) (*Link, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	row := sqlDB.QueryRowContext(ctx,
		`SELECT id, short_code, destination_url, created_at, expires_at, click_count
		 FROM links WHERE id = ?`, id)
	return scanLink(row)
}

func scanLink(row *sql.Row) (*Link, error) {
	var l Link
	var createdAt int64
	var expiresAt sql.NullInt64

	if err := row.Scan(&l.ID, &l.ShortCode, &l.DestinationURL, &createdAt, &expiresAt, &l.ClickCount); err != nil {
		return nil, HandleQueryError(err)
	}

	l.CreatedAt = time.Unix(createdAt, 0).UTC()
	if expiresAt.Valid {
		t := time.Unix(expiresAt.Int64, 0).UTC()
		l.ExpiresAt = &t
	}
	return &l, nil
}

// UpdateLinkDestination updates a link's destination URL and/or
// expiration, per IDEA.md "Endpoints": "Update destination URL,
// expiration (owner token or operator token required)." Auth enforcement
// is the caller's responsibility (AI.md PART 14); this is the DB-layer
// mutation only. Pass expiresAt == nil with clearExpiry == true to clear
// an existing expiration.
func UpdateLinkDestination(ctx context.Context, sqlDB *sql.DB, id int64, destinationURL string, expiresAt *time.Time, clearExpiry bool) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var expires sql.NullInt64
	if expiresAt != nil {
		expires = sql.NullInt64{Int64: expiresAt.Unix(), Valid: true}
	} else if !clearExpiry {
		// Caller did not supply a new expiration and did not ask to
		// clear it — leave the existing value untouched via COALESCE.
		res, err := sqlDB.ExecContext(ctx,
			`UPDATE links SET destination_url = ? WHERE id = ?`, destinationURL, id)
		return checkRowsAffected(res, err)
	}

	res, err := sqlDB.ExecContext(ctx,
		`UPDATE links SET destination_url = ?, expires_at = ? WHERE id = ?`,
		destinationURL, expires, id)
	return checkRowsAffected(res, err)
}

// DeleteLink removes a link together with everything that references it,
// in a single transaction: its clicks (clicks.link_id is a NOT NULL
// foreign key into links(id) and the connection runs with
// PRAGMA foreign_keys(1), so the link row cannot be removed while any
// click survives) and any API token scoped to it (an owner token whose
// resource no longer exists must not stay valid).
func DeleteLink(ctx context.Context, sqlDB *sql.DB, id int64) error {
	return WithTransaction(ctx, sqlDB, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM clicks WHERE link_id = ?`, id); err != nil {
			return HandleQueryError(err)
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM api_tokens WHERE resource_type = 'link' AND resource_id = ?`,
			strconv.FormatInt(id, 10)); err != nil {
			return HandleQueryError(err)
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM links WHERE id = ?`, id)
		return checkRowsAffected(res, err)
	})
}

// IncrementClickCount atomically increments a link's click_count.
func IncrementClickCount(ctx context.Context, sqlDB *sql.DB, id int64) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	_, err := sqlDB.ExecContext(ctx, `UPDATE links SET click_count = click_count + 1 WHERE id = ?`, id)
	if err != nil {
		return HandleQueryError(err)
	}
	return nil
}

// ListLinks returns a page of links ordered newest-first, together with the
// total row count, per AI.md PART 14 "Pagination" (default 250 items) and
// IDEA.md "Endpoints": "List all created links (public, paginated)." limit
// and offset are the caller's responsibility to validate/clamp.
func ListLinks(ctx context.Context, sqlDB *sql.DB, limit, offset int) ([]Link, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var total int64
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM links`).Scan(&total); err != nil {
		return nil, 0, HandleQueryError(err)
	}

	rows, err := sqlDB.QueryContext(ctx,
		`SELECT id, short_code, destination_url, created_at, expires_at, click_count
		 FROM links ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, HandleQueryError(err)
	}
	defer rows.Close()

	links := make([]Link, 0, limit)
	for rows.Next() {
		var l Link
		var createdAt int64
		var expiresAt sql.NullInt64
		if err := rows.Scan(&l.ID, &l.ShortCode, &l.DestinationURL, &createdAt, &expiresAt, &l.ClickCount); err != nil {
			return nil, 0, HandleQueryError(err)
		}
		l.CreatedAt = time.Unix(createdAt, 0).UTC()
		if expiresAt.Valid {
			t := time.Unix(expiresAt.Int64, 0).UTC()
			l.ExpiresAt = &t
		}
		links = append(links, l)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, HandleQueryError(err)
	}
	return links, total, nil
}

func checkRowsAffected(res sql.Result, err error) error {
	if err != nil {
		return HandleQueryError(err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("db: rows affected: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// isUniqueConstraintError reports whether err is a SQLite UNIQUE
// constraint violation.
func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
