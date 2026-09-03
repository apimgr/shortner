// Package geoip provides IP-to-location lookups backed by sapics/ip-location-db
// MMDB files, per AI.md PART 19 "GeoIP". Databases are never embedded in the
// binary — they are downloaded on first run and kept updated by the
// scheduler's geoip_update task (AI.md PART 18).
//
// GeoIP is a risk signal only, never a sole access-control gate (AI.md PART
// 19 "GeoIP Is a Risk Signal"): every lookup fails open — a missing,
// corrupt, or not-yet-downloaded database returns a zero Result instead of
// an error, and callers must never block a request solely because a lookup
// failed.
package geoip

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang"

	"github.com/apimgr/shortner/src/config"
)

// Filenames, per AI.md PART 19 "Database Sources (ip-location-db)".
const (
	asnFile     = "asn.mmdb"
	countryFile = "geo-whois-asn-country.mmdb"
	cityV4File  = "dbip-city-ipv4.mmdb"
	cityV6File  = "dbip-city-ipv6.mmdb"
)

// downloadURLs maps each on-disk filename to its jsDelivr CDN source, per
// AI.md PART 19's "Database Sources" table.
var downloadURLs = map[string]string{
	asnFile:     "https://cdn.jsdelivr.net/npm/@ip-location-db/asn-mmdb/asn.mmdb",
	countryFile: "https://cdn.jsdelivr.net/npm/@ip-location-db/geo-whois-asn-country-mmdb/geo-whois-asn-country.mmdb",
	cityV4File:  "https://cdn.jsdelivr.net/npm/@ip-location-db/dbip-city-mmdb/dbip-city-ipv4.mmdb",
	cityV6File:  "https://cdn.jsdelivr.net/npm/@ip-location-db/dbip-city-mmdb/dbip-city-ipv6.mmdb",
}

// Result is a single IP lookup's combined ASN/Country/City data. Every
// field is the zero value when its backing database is unavailable, private
// IPs are never looked up, or the address had no match.
type Result struct {
	CountryCode string
	City        string
	Region      string
	PostalCode  string
	Latitude    float64
	Longitude   float64
	Timezone    string
	ASN         uint
	ASOrg       string
}

// countryRecord/cityRecord/asnRecord mirror the ip-location-db field tables
// in AI.md PART 19. Field names/types match the mmdb schema, decoded
// directly by maxminddb (NOT the incompatible geoip2-golang decoder).
type countryRecord struct {
	CountryCode string `maxminddb:"country_code"`
}

type cityRecord struct {
	City        string  `maxminddb:"city"`
	CountryCode string  `maxminddb:"country_code"`
	State1      string  `maxminddb:"state1"`
	State2      string  `maxminddb:"state2"`
	Postcode    string  `maxminddb:"postcode"`
	Latitude    float64 `maxminddb:"latitude"`
	Longitude   float64 `maxminddb:"longitude"`
	Timezone    string  `maxminddb:"timezone"`
}

type asnRecord struct {
	ASN   uint   `maxminddb:"autonomous_system_number"`
	ASOrg string `maxminddb:"autonomous_system_organization"`
}

// Manager holds the currently open MMDB readers and serves lookups. All
// methods are safe for concurrent use; Reload swaps readers atomically
// under a write lock so in-flight lookups never see a half-closed reader.
type Manager struct {
	mu      sync.RWMutex
	dir     string
	enabled bool
	dbs     config.GeoIPDatabases

	asn     *maxminddb.Reader
	country *maxminddb.Reader
	cityV4  *maxminddb.Reader
	cityV6  *maxminddb.Reader
}

// Open builds a Manager and loads whichever configured MMDB files already
// exist in dir. Per the fail-open rule, a missing or unreadable file is
// silently skipped (that category's lookups simply return zero values)
// rather than returning an error — the caller should still call Download
// afterward (e.g. on first run) to populate dir.
func Open(dir string, enabled bool, dbs config.GeoIPDatabases) *Manager {
	m := &Manager{dir: dir, enabled: enabled, dbs: dbs}
	if enabled {
		m.loadAll()
	}
	return m
}

// loadAll (re)opens every enabled database file that exists on disk,
// closing any previously open reader first.
func (m *Manager) loadAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	closeAndClear(&m.asn)
	closeAndClear(&m.country)
	closeAndClear(&m.cityV4)
	closeAndClear(&m.cityV6)

	if m.dbs.ASN {
		m.asn = openIfExists(filepath.Join(m.dir, asnFile))
	}
	if m.dbs.Country {
		m.country = openIfExists(filepath.Join(m.dir, countryFile))
	}
	if m.dbs.City {
		m.cityV4 = openIfExists(filepath.Join(m.dir, cityV4File))
		m.cityV6 = openIfExists(filepath.Join(m.dir, cityV6File))
	}
}

func closeAndClear(r **maxminddb.Reader) {
	if *r != nil {
		_ = (*r).Close()
		*r = nil
	}
}

func openIfExists(path string) *maxminddb.Reader {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	r, err := maxminddb.Open(path)
	if err != nil {
		return nil
	}
	return r
}

// Reload closes and reopens every database file, picking up files that
// Download just wrote. Call after a successful Download.
func (m *Manager) Reload() {
	if !m.enabled {
		return
	}
	m.loadAll()
}

// Close releases every open database file. Safe to call on a Manager with
// no databases loaded.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	closeAndClear(&m.asn)
	closeAndClear(&m.country)
	closeAndClear(&m.cityV4)
	closeAndClear(&m.cityV6)
	return nil
}

// Lookup returns everything known about ip. Private/loopback addresses
// (RFC 1918, RFC 4193, loopback) are never looked up, per AI.md PART 19
// "Private/internal IPs". A nil ip, a disabled Manager, or a missing/
// corrupt database all fail open to a zero Result rather than an error.
func (m *Manager) Lookup(ip net.IP) Result {
	var out Result
	if ip == nil || !m.enabled || ip.IsLoopback() || ip.IsPrivate() {
		return out
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.country != nil {
		var rec countryRecord
		if err := m.country.Lookup(ip, &rec); err == nil {
			out.CountryCode = rec.CountryCode
		}
	}

	cityReader := m.cityReaderFor(ip)
	if cityReader != nil {
		var rec cityRecord
		if err := cityReader.Lookup(ip, &rec); err == nil {
			out.City = rec.City
			out.Region = firstNonEmpty(rec.State1, rec.State2)
			out.PostalCode = rec.Postcode
			out.Latitude = rec.Latitude
			out.Longitude = rec.Longitude
			out.Timezone = rec.Timezone
			if out.CountryCode == "" {
				out.CountryCode = rec.CountryCode
			}
		}
	}

	if m.asn != nil {
		var rec asnRecord
		if err := m.asn.Lookup(ip, &rec); err == nil {
			out.ASN = rec.ASN
			out.ASOrg = rec.ASOrg
		}
	}

	return out
}

// cityReaderFor picks the IPv4 or IPv6 city database for ip. Must be called
// with m.mu held (read or write).
func (m *Manager) cityReaderFor(ip net.IP) *maxminddb.Reader {
	if ip.To4() != nil {
		return m.cityV4
	}
	return m.cityV6
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// IsBlocked reports whether countryCode should be blocked under the
// deny/allow lists, per AI.md PART 19 "Country blocking behavior":
// allow_countries wins if both are set; an empty countryCode (lookup
// failed/unavailable) is never blocked (fail open).
func IsBlocked(countryCode string, deny, allow []string) bool {
	if countryCode == "" {
		return false
	}
	if len(allow) > 0 {
		return !containsFold(allow, countryCode)
	}
	if len(deny) > 0 {
		return containsFold(deny, countryCode)
	}
	return false
}

func containsFold(list []string, code string) bool {
	for _, c := range list {
		if len(c) == len(code) && equalFold(c, code) {
			return true
		}
	}
	return false
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'a' <= ca && ca <= 'z' {
			ca -= 'a' - 'A'
		}
		if 'a' <= cb && cb <= 'z' {
			cb -= 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// Download fetches every enabled database into dir, per AI.md PART 19
// "Database Sources (ip-location-db)". Each file is written to a temporary
// path in dir and renamed into place atomically only after a fully
// successful download, so a failed/interrupted download never corrupts an
// existing, working database. Returns the first error encountered but still
// attempts every remaining file (a transient failure on one database
// shouldn't block the others from updating).
func Download(ctx context.Context, dir string, dbs config.GeoIPDatabases) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("geoip: create %s: %w", dir, err)
	}

	var files []string
	if dbs.ASN {
		files = append(files, asnFile)
	}
	if dbs.Country {
		files = append(files, countryFile)
	}
	if dbs.City {
		files = append(files, cityV4File, cityV6File)
	}

	var firstErr error
	for _, name := range files {
		if err := downloadOne(ctx, dir, name); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// downloadClient is a package-level http.Client so tests can point
// downloadURLs at a local httptest.Server without touching global state
// per request.
var downloadClient = &http.Client{Timeout: 5 * time.Minute}

func downloadOne(ctx context.Context, dir, name string) error {
	url, ok := downloadURLs[name]
	if !ok {
		return fmt.Errorf("geoip: no download URL for %s", name)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("geoip: build request for %s: %w", name, err)
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		return fmt.Errorf("geoip: download %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("geoip: download %s: unexpected status %s", name, resp.Status)
	}

	tmp, err := os.CreateTemp(dir, name+".download-*")
	if err != nil {
		return fmt.Errorf("geoip: create temp file for %s: %w", name, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return fmt.Errorf("geoip: write %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("geoip: close %s: %w", name, err)
	}

	// Sanity-check the download is a valid MMDB before replacing the
	// existing (working) database with it.
	if r, err := maxminddb.Open(tmpPath); err != nil {
		return fmt.Errorf("geoip: downloaded %s is not a valid MMDB: %w", name, err)
	} else {
		_ = r.Close()
	}

	if err := os.Rename(tmpPath, filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("geoip: install %s: %w", name, err)
	}
	return nil
}
