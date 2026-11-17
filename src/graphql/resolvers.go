package graphql

import (
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/apimgr/shortner/src/db"
)

// LinkResolver reads public link data using the application's shared database.
type LinkResolver struct {
	DB       *sql.DB
	Request  *http.Request
	BuildURL func(*http.Request, string) string
	ReadAPI  func(string) (any, error)
}

func (r LinkResolver) APIRead(path string) (any, error) {
	if r.ReadAPI == nil {
		return nil, errors.New("GraphQL REST read resolver is unavailable")
	}
	return r.ReadAPI(path)
}

func (r LinkResolver) Link(slug string) (*Link, error) {
	item, err := db.GetLinkByShortCode(r.Request.Context(), r.DB, slug)
	if err != nil {
		return nil, err
	}
	return r.convert(item), nil
}

func (r LinkResolver) Links(limit, offset int) ([]Link, error) {
	if limit < 1 {
		limit = 50
	}
	if limit > 250 {
		limit = 250
	}
	if offset < 0 {
		offset = 0
	}
	items, _, err := db.ListLinks(r.Request.Context(), r.DB, limit, offset)
	if err != nil {
		return nil, err
	}
	result := make([]Link, 0, len(items))
	for i := range items {
		result = append(result, *r.convert(&items[i]))
	}
	return result, nil
}

func (r LinkResolver) LinkStats(slug string) (*LinkStats, error) {
	link, err := db.GetLinkByShortCode(r.Request.Context(), r.DB, slug)
	if err != nil {
		return nil, err
	}
	clicks, err := db.ClicksForLink(r.Request.Context(), r.DB, link.ID, 1000)
	if err != nil {
		return nil, err
	}
	referrerCounts := make(map[string]int)
	byDay := make(map[string]int)
	recent := make([]ClickInfo, 0, min(len(clicks), 50))
	for i, click := range clicks {
		referrer := click.Referrer
		if referrer == "" {
			referrer = "(direct)"
		}
		referrerCounts[referrer]++
		byDay[click.Timestamp.Format("2006-01-02")]++
		if i < 50 {
			recent = append(recent, ClickInfo{
				Timestamp: click.Timestamp.Format(time.RFC3339),
				IP:        click.IP,
				Referrer:  optionalString(click.Referrer),
				Country:   optionalString(click.Country),
				Region:    optionalString(click.Region),
			})
		}
	}
	referrers := make([]Referrer, 0, len(referrerCounts))
	for name, count := range referrerCounts {
		referrers = append(referrers, Referrer{Name: name, Count: count})
	}
	sort.Slice(referrers, func(i, j int) bool { return referrers[i].Name < referrers[j].Name })
	days := make([]string, 0, len(byDay))
	for date := range byDay {
		days = append(days, date)
	}
	sort.Strings(days)
	timeSeries := make([]DayCount, 0, len(days))
	for _, date := range days {
		timeSeries = append(timeSeries, DayCount{Date: date, Count: byDay[date]})
	}
	return &LinkStats{ShortCode: link.ShortCode, TotalClicks: link.ClickCount, Referrers: referrers, TimeSeries: timeSeries, Recent: recent}, nil
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func (r LinkResolver) convert(item *db.Link) *Link {
	shortURL := r.BuildURL(r.Request, "/"+item.ShortCode)
	result := &Link{ShortCode: item.ShortCode, ShortURL: shortURL, DestinationURL: item.DestinationURL, CreatedAt: item.CreatedAt.UTC().Format(time.RFC3339), ClickCount: item.ClickCount}
	if item.ExpiresAt != nil {
		expires := item.ExpiresAt.UTC().Format(time.RFC3339)
		result.ExpiresAt = &expires
	}
	return result
}
