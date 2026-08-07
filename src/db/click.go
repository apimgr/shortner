package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/apimgr/shortner/src/security"
)

// Click is a single recorded visit to a link, per IDEA.md "Data models":
// "Click: id, link_id, timestamp, ip (anonymized ...), user_agent,
// referrer, country/region (GeoIP ...)." GeoIP-derived Country/Region
// population is AI.md PART 19 (not built yet — fields are always empty
// until that lands; tracked in TODO.AI.md).
type Click struct {
	ID        int64
	LinkID    int64
	Timestamp time.Time
	IP        string
	UserAgent string
	Referrer  string
	Country   string
	Region    string
}

// RecordClick inserts a click and increments the parent link's
// click_count in one transaction. rawIP is anonymized here — defense in
// depth per AI.md PART 11 "Defense-in-Depth Layers": even if a caller
// forgets to anonymize, the raw IP is never persisted. Per IDEA.md
// "Business rules": "Click tracking excludes known bot/crawler user
// agents" — bot filtering is the caller's responsibility (it requires a
// UA-classification list this project has not yet defined; tracked in
// TODO.AI.md) since it decides whether to call RecordClick at all.
func RecordClick(ctx context.Context, sqlDB *sql.DB, linkID int64, rawIP, userAgent, referrer string) (*Click, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	anonIP := security.AnonymizeIP(rawIP)

	var click *Click
	err := WithTransaction(ctx, sqlDB, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO clicks (link_id, ip, user_agent, referrer) VALUES (?, ?, ?, ?)`,
			linkID, nullableString(anonIP), nullableString(userAgent), nullableString(referrer))
		if err != nil {
			return HandleQueryError(err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("db: record click: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `UPDATE links SET click_count = click_count + 1 WHERE id = ?`, linkID); err != nil {
			return HandleQueryError(err)
		}

		row := tx.QueryRowContext(ctx,
			`SELECT id, link_id, timestamp, ip, user_agent, referrer, country, region
			 FROM clicks WHERE id = ?`, id)
		click, err = scanClick(row)
		return err
	})
	if err != nil {
		return nil, err
	}
	return click, nil
}

// ClicksForLink returns clicks for linkID, most recent first, up to
// limit rows. Used by the per-link analytics page (IDEA.md "Endpoints":
// "Get link click statistics").
func ClicksForLink(ctx context.Context, sqlDB *sql.DB, linkID int64, limit int) ([]Click, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	rows, err := sqlDB.QueryContext(ctx,
		`SELECT id, link_id, timestamp, ip, user_agent, referrer, country, region
		 FROM clicks WHERE link_id = ? ORDER BY timestamp DESC LIMIT ?`, linkID, limit)
	if err != nil {
		return nil, HandleQueryError(err)
	}
	defer rows.Close()

	var out []Click
	for rows.Next() {
		var c Click
		var ts int64
		var ip, ua, ref, country, region sql.NullString
		if err := rows.Scan(&c.ID, &c.LinkID, &ts, &ip, &ua, &ref, &country, &region); err != nil {
			return nil, fmt.Errorf("db: scan click: %w", err)
		}
		c.Timestamp = time.Unix(ts, 0).UTC()
		c.IP = ip.String
		c.UserAgent = ua.String
		c.Referrer = ref.String
		c.Country = country.String
		c.Region = region.String
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate clicks: %w", err)
	}
	return out, nil
}

func scanClick(row *sql.Row) (*Click, error) {
	var c Click
	var ts int64
	var ip, ua, ref, country, region sql.NullString
	if err := row.Scan(&c.ID, &c.LinkID, &ts, &ip, &ua, &ref, &country, &region); err != nil {
		return nil, HandleQueryError(err)
	}
	c.Timestamp = time.Unix(ts, 0).UTC()
	c.IP = ip.String
	c.UserAgent = ua.String
	c.Referrer = ref.String
	c.Country = country.String
	c.Region = region.String
	return &c, nil
}

func nullableString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
