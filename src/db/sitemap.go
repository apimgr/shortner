package db

import (
	"context"
	"database/sql"
	"time"
)

// SitemapLink is public, unexpired resource metadata needed to generate the
// public URL sitemap without exposing destinations or click data.
type SitemapLink struct {
	ShortCode string
	CreatedAt time.Time
}

// ListSitemapLinks returns a stable page of links that currently resolve.
func ListSitemapLinks(ctx context.Context, sqlDB *sql.DB, limit, offset int, now time.Time) ([]SitemapLink, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cutoff := now.UTC().Unix()
	var total int64
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM links WHERE expires_at IS NULL OR expires_at > ?`, cutoff).Scan(&total); err != nil {
		return nil, 0, HandleQueryError(err)
	}
	rows, err := sqlDB.QueryContext(ctx,
		`SELECT short_code, created_at FROM links
		 WHERE expires_at IS NULL OR expires_at > ?
		 ORDER BY created_at ASC, id ASC LIMIT ? OFFSET ?`, cutoff, limit, offset)
	if err != nil {
		return nil, 0, HandleQueryError(err)
	}
	defer rows.Close()

	links := make([]SitemapLink, 0, limit)
	for rows.Next() {
		var link SitemapLink
		var createdAt int64
		if err := rows.Scan(&link.ShortCode, &createdAt); err != nil {
			return nil, 0, HandleQueryError(err)
		}
		link.CreatedAt = time.Unix(createdAt, 0).UTC()
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, HandleQueryError(err)
	}
	return links, total, nil
}
