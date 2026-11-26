// Package blocklist downloads and applies remote IP and domain feeds, per
// AI.md PART 7 "Update Contract and Sources".
package blocklist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"

	"github.com/apimgr/shortner/src/config"
)

const (
	maxAge       = 72 * time.Hour
	maxFeedBytes = 50 << 20
	fireholURL   = "https://raw.githubusercontent.com/firehol/blocklist-ipsets/master/firehol_level1.netset"
)

type Source struct {
	ID      string
	Type    string
	URL     string
	Enabled bool
	Builtin bool
}

type Stats struct {
	Loaded    int
	Skipped   int
	Matches   uint64
	Updated   time.Time
	Status    string
	LastError string
}

type entry struct {
	network  *net.IPNet
	domain   string
	wildcard bool
}

type snapshot struct {
	entries map[string][]entry
	stats   map[string]Stats
}

type Manager struct {
	dir     string
	sources []Source
	trusted []string
	client  *http.Client
	mu      sync.RWMutex
	data    snapshot
	updates sync.Mutex
}

// Open loads cached feeds and prepares the source manager. Missing feeds
// stay unavailable until Update succeeds; startup does not wait for network.
func Open(dir string, cfg config.Blocklists, trusted ...string) (*Manager, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("blocklist: create directory: %w", err)
	}
	sources := []Source{{ID: "firehol_level1", Type: "ip", URL: fireholURL, Enabled: true, Builtin: true}}
	for _, source := range cfg.ExtraSources {
		enabled := source.Enabled == nil || *source.Enabled
		if cfg.Enabled && enabled {
			sources = append(sources, Source{ID: source.ID, Type: source.Type, URL: source.URL, Enabled: true})
		}
	}
	manager := &Manager{
		dir:     dir,
		sources: sources,
		trusted: append([]string(nil), trusted...),
		client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 || req.URL.Scheme != "https" {
				return http.ErrUseLastResponse
			}
			return nil
		}},
		data: snapshot{entries: make(map[string][]entry), stats: make(map[string]Stats)},
	}
	manager.loadCached()
	return manager, nil
}

// Sources returns the active feed definitions.
func (m *Manager) Sources() []Source { return append([]Source(nil), m.sources...) }

// Update refreshes every active source. Failed updates preserve its last good
// file and in-memory entries; errors are returned after all sources are tried.
func (m *Manager) Update(ctx context.Context) error {
	m.updates.Lock()
	defer m.updates.Unlock()
	var firstErr error
	for _, source := range m.sources {
		if err := m.updateSource(ctx, source); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("%s update failed", source.ID)
		}
	}
	return firstErr
}

func (m *Manager) updateSource(ctx context.Context, source Source) (resultErr error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		m.markFailure(source.ID, err)
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close feed response: %w", err))
			m.markFailure(source.ID, resultErr)
		}
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err = fmt.Errorf("feed returned HTTP %d", resp.StatusCode)
		m.markFailure(source.ID, err)
		return err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedBytes+1))
	if err != nil {
		m.markFailure(source.ID, err)
		return err
	}
	if len(body) == 0 || len(body) > maxFeedBytes {
		err = fmt.Errorf("feed is empty or exceeds %d bytes", maxFeedBytes)
		m.markFailure(source.ID, err)
		return err
	}
	parsed, skipped, err := parseFeed(string(body), source.Type, m.trusted...)
	if err != nil {
		m.markFailure(source.ID, err)
		return err
	}
	if len(parsed) == 0 {
		err = fmt.Errorf("feed contains no valid entries")
		m.markFailure(source.ID, err)
		return err
	}
	path := filepath.Join(m.dir, source.ID+".txt")
	tmp, err := os.CreateTemp(m.dir, "."+source.ID+"-*.tmp")
	if err != nil {
		m.markFailure(source.ID, err)
		return err
	}
	name := tmp.Name()
	defer func() {
		if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove temporary feed: %w", err))
		}
	}()
	if err := tmp.Chmod(0o644); err != nil {
		err = errors.Join(err, tmp.Close())
		m.markFailure(source.ID, err)
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		err = errors.Join(err, tmp.Close())
		m.markFailure(source.ID, err)
		return err
	}
	if err := tmp.Sync(); err != nil {
		err = errors.Join(err, tmp.Close())
		m.markFailure(source.ID, err)
		return err
	}
	if err := tmp.Close(); err != nil {
		m.markFailure(source.ID, err)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		m.markFailure(source.ID, err)
		return err
	}
	now := time.Now().UTC()
	if err := os.WriteFile(filepath.Join(m.dir, ".last_updated"), []byte(now.Format(time.RFC3339)+"\n"), 0o644); err != nil {
		m.markFailure(source.ID, err)
		return err
	}
	m.mu.Lock()
	m.data.entries[source.ID] = parsed
	m.data.stats[source.ID] = Stats{Loaded: len(parsed), Skipped: skipped, Updated: now, Status: "ok"}
	m.mu.Unlock()
	return nil
}

func (m *Manager) loadCached() {
	for _, source := range m.sources {
		path := filepath.Join(m.dir, source.ID+".txt")
		body, err := os.ReadFile(path)
		if err != nil {
			m.data.stats[source.ID] = Stats{Status: "error", LastError: "feed has never loaded"}
			continue
		}
		parsed, skipped, err := parseFeed(string(body), source.Type, m.trusted...)
		if err != nil || len(parsed) == 0 {
			m.data.stats[source.ID] = Stats{Status: "error", LastError: "cached feed is invalid"}
			continue
		}
		info, _ := os.Stat(path)
		updated := time.Time{}
		if info != nil {
			updated = info.ModTime().UTC()
		}
		status := "ok"
		if updated.IsZero() || time.Since(updated) > maxAge {
			status = "degraded"
		}
		m.data.entries[source.ID] = parsed
		m.data.stats[source.ID] = Stats{Loaded: len(parsed), Skipped: skipped, Updated: updated, Status: status}
	}
}

func (m *Manager) markFailure(id string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stats := m.data.stats[id]
	stats.LastError = err.Error()
	if stats.Loaded == 0 {
		stats.Status = "error"
	} else {
		stats.Status = "degraded"
	}
	m.data.stats[id] = stats
}

// Status returns health information for every configured source.
func (m *Manager) Status() map[string]Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]Stats, len(m.data.stats))
	for id, stats := range m.data.stats {
		if stats.Loaded > 0 && time.Since(stats.Updated) > maxAge {
			stats.Status = "degraded"
		}
		result[id] = stats
	}
	return result
}

// OrderedStatus returns public health values in source registration order.
func (m *Manager) OrderedStatus() []struct{ ID, Status string } {
	statuses := m.Status()
	result := make([]struct{ ID, Status string }, 0, len(m.sources))
	for _, source := range m.sources {
		if status, ok := statuses[source.ID]; ok {
			result = append(result, struct{ ID, Status string }{ID: source.ID, Status: status.Status})
		}
	}
	return result
}

// RecordMatch increments the diagnostic match counter for a feed.
func (m *Manager) RecordMatch(id string) {
	m.mu.Lock()
	stats := m.data.stats[id]
	stats.Matches++
	m.data.stats[id] = stats
	m.mu.Unlock()
}

// MatchIP returns the feed id that contains ip, or an empty string.
func (m *Manager) MatchIP(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ""
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, source := range m.sources {
		if source.Type != "ip" {
			continue
		}
		for _, item := range m.data.entries[source.ID] {
			if item.network != nil && item.network.Contains(parsed) {
				return source.ID
			}
		}
	}
	return ""
}

// MatchDomain returns the feed id matching hostname at a DNS-label boundary.
func (m *Manager) MatchDomain(hostname string) string {
	domain := normalizeDomain(hostname)
	if domain == "" {
		return ""
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, source := range m.sources {
		if source.Type != "domain" {
			continue
		}
		for _, item := range m.data.entries[source.ID] {
			if (!item.wildcard && domain == item.domain) || (item.wildcard && strings.HasSuffix(domain, "."+item.domain)) {
				return source.ID
			}
		}
	}
	return ""
}

// MatchRequest checks only AI.md's defined domain targets: Origin (or
// Referer when Origin is absent), submitted email domains, and URL-valued
// inputs. Request bodies are restored byte-for-byte for the handler.
func (m *Manager) MatchRequest(r *http.Request) (string, error) {
	ref := r.Header.Get("Origin")
	if ref == "" {
		ref = r.Header.Get("Referer")
	}
	if ref != "" {
		if parsed, err := url.Parse(ref); err == nil {
			if id := m.MatchDomain(parsed.Hostname()); id != "" {
				return id, nil
			}
		}
	}
	for key, values := range r.URL.Query() {
		for _, value := range values {
			if id := matchField(m, key, value); id != "" {
				return id, nil
			}
		}
	}
	if r.Body == nil {
		return "", nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, (10<<20)+1))
	r.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil || len(body) > 10<<20 {
		return "", fmt.Errorf("blocklist: request body unavailable for domain checks")
	}
	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.HasPrefix(contentType, "application/json") {
		var value any
		if json.Unmarshal(body, &value) == nil {
			if id := matchJSONValue(m, value); id != "" {
				return id, nil
			}
		}
	} else if strings.HasPrefix(contentType, "application/x-www-form-urlencoded") {
		if fields, err := url.ParseQuery(string(body)); err == nil {
			for key, values := range fields {
				for _, value := range values {
					if id := matchField(m, key, value); id != "" {
						return id, nil
					}
				}
			}
		}
	} else if strings.HasPrefix(contentType, "multipart/form-data") {
		_, params, parseErr := mime.ParseMediaType(contentType)
		if parseErr == nil && params["boundary"] != "" {
			reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
			for {
				part, nextErr := reader.NextPart()
				if nextErr != nil {
					break
				}
				value, readErr := io.ReadAll(io.LimitReader(part, (10<<20)+1))
				_ = part.Close()
				if readErr != nil || len(value) > 10<<20 {
					break
				}
				if id := matchField(m, part.FormName(), string(value)); id != "" {
					return id, nil
				}
			}
		}
	}
	return "", nil
}

func matchJSONValue(m *Manager, value any) string {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			if text, ok := child.(string); ok {
				if id := matchField(m, key, text); id != "" {
					return id
				}
			}
			if id := matchJSONValue(m, child); id != "" {
				return id
			}
		}
	case []any:
		for _, child := range item {
			if id := matchJSONValue(m, child); id != "" {
				return id
			}
		}
	}
	return ""
}

func matchField(m *Manager, key, value string) string {
	key = strings.ToLower(key)
	if key == "email" || strings.HasSuffix(key, "_email") {
		if address, err := mail.ParseAddress(value); err == nil {
			if at := strings.LastIndexByte(address.Address, '@'); at >= 0 {
				return m.MatchDomain(address.Address[at+1:])
			}
		}
	}
	if key == "url" || strings.HasSuffix(key, "_url") || strings.Contains(key, "destination") {
		if parsed, err := url.Parse(strings.TrimSpace(value)); err == nil && parsed.Hostname() != "" {
			return m.MatchDomain(parsed.Hostname())
		}
	}
	return ""
}

func parseFeed(body, kind string, trusted ...string) ([]entry, int, error) {
	var entries []entry
	skipped := 0
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if i := strings.IndexAny(line, " \t;"); i >= 0 {
			line = line[:i]
		}
		if kind == "ip" {
			ip, network, err := parseIPEntry(line)
			if err != nil || unsafeNetwork(network, trusted...) {
				skipped++
				continue
			}
			_ = ip
			entries = append(entries, entry{network: network})
			continue
		}
		wildcard := strings.HasPrefix(line, "*.")
		if wildcard {
			line = strings.TrimPrefix(line, "*.")
		}
		domain := normalizeFeedDomain(line)
		if domain == "" || (wildcard && isPublicSuffix(domain)) {
			skipped++
			continue
		}
		entries = append(entries, entry{domain: domain, wildcard: wildcard})
	}
	if kind != "ip" && kind != "domain" {
		return nil, skipped, fmt.Errorf("unknown feed type %q", kind)
	}
	return entries, skipped, nil
}

func parseIPEntry(value string) (net.IP, *net.IPNet, error) {
	if ip := net.ParseIP(value); ip != nil {
		bits := 128
		if ip.To4() != nil {
			bits = 32
		}
		return ip, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}, nil
	}
	_, network, err := net.ParseCIDR(value)
	return nil, network, err
}

var unsafeRanges = func() []*net.IPNet {
	values := []string{"0.0.0.0/8", "10.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "::1/128", "fc00::/7", "fe80::/10"}
	var ranges []*net.IPNet
	for _, value := range values {
		_, n, _ := net.ParseCIDR(value)
		ranges = append(ranges, n)
	}
	return ranges
}()

func unsafeNetwork(network *net.IPNet, trusted ...string) bool {
	if network == nil {
		return true
	}
	for _, unsafe := range append(append([]*net.IPNet(nil), unsafeRanges...), parseTrustedNetworks(trusted...)...) {
		if network.Contains(unsafe.IP) || unsafe.Contains(network.IP) {
			return true
		}
	}
	return false
}

func parseTrustedNetworks(values ...string) []*net.IPNet {
	var networks []*net.IPNet
	for _, value := range values {
		value = strings.TrimSpace(value)
		if _, network, err := net.ParseCIDR(value); err == nil {
			networks = append(networks, network)
			continue
		}
		if ip := net.ParseIP(value); ip != nil {
			bits := 128
			if ip.To4() != nil {
				bits = 32
			}
			networks = append(networks, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		if ips, err := net.LookupIP(value); err == nil {
			for _, ip := range ips {
				bits := 128
				if ip.To4() != nil {
					bits = 32
				}
				networks = append(networks, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			}
		}
	}
	return networks
}

func normalizeFeedDomain(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "/:@?#[]") {
		return ""
	}
	return normalizeDomain(value)
}

func normalizeDomain(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	if parsed, err := url.Parse("//" + value); err == nil && parsed.Host != "" {
		value = parsed.Hostname()
	}
	value = strings.TrimSuffix(value, ".")
	if net.ParseIP(value) != nil {
		return ""
	}
	ascii, err := idna.Lookup.ToASCII(value)
	if err != nil || ascii == "" {
		return ""
	}
	if len(ascii) > 253 {
		return ""
	}
	for _, label := range strings.Split(ascii, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return ""
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return ""
			}
		}
	}
	return strings.ToLower(ascii)
}

func isPublicSuffix(domain string) bool {
	suffix, _ := publicsuffix.PublicSuffix(domain)
	return suffix == domain
}
