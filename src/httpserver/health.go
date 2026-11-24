// The PART 13 health endpoints: /server/healthz, /api/{api_version}/
// server/healthz, /api/healthz, and the optional /healthz root alias. See
// AI.md PART 13 "Health Checks" -> "Field Order & Structure".
package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/blocklist"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/geoip"
	"github.com/apimgr/shortner/src/mode"
)

// HealthResponse is the canonical /server/healthz shape, per AI.md PART 13
// "Field Order & Structure" -> "Backend Structure (Go)". Field order and
// JSON tags match the spec exactly.
type HealthResponse struct {
	Project ProjectInfo `json:"project"`

	Status         string   `json:"status"`
	PendingRestart bool     `json:"pending_restart,omitempty"`
	RestartReason  []string `json:"restart_reason,omitempty"`

	Version   string    `json:"version"`
	GoVersion string    `json:"go_version"`
	Build     BuildInfo `json:"build"`

	Uptime    string      `json:"uptime"`
	Mode      string      `json:"mode"`
	Timestamp time.Time   `json:"timestamp"`
	Cluster   ClusterInfo `json:"cluster"`

	Features FeaturesInfo `json:"features"`
	Checks   ChecksInfo   `json:"checks"`
	Stats    StatsInfo    `json:"stats"`
}

// ClusterInfo describes cluster mode, which this service does not enable.
type ClusterInfo struct {
	Enabled bool `json:"enabled"`
}

// ProjectInfo identifies the running application, per AI.md PART 13. These
// canonical project values are sourced from IDEA.md.
type ProjectInfo struct {
	Name        string `json:"name"`
	Tagline     string `json:"tagline"`
	Description string `json:"description"`
}

// BuildInfo carries build-time version metadata, per AI.md PART 13.
type BuildInfo struct {
	Commit string `json:"commit"`
	Date   string `json:"date"`
}

// FeaturesInfo lists PUBLIC non-negotiable features, per AI.md PART 13.
type FeaturesInfo struct {
	Tor   TorInfo `json:"tor"`
	I2P   I2PInfo `json:"i2p"`
	GeoIP bool    `json:"geoip"`
}

// TorInfo describes the Tor hidden service, per AI.md PART 13 and
// PART 31.1. Every field is Tier 2 public-safe: the onion address is
// published to the world by design, so exposing it here leaks nothing.
type TorInfo struct {
	Enabled  bool   `json:"enabled"`
	Running  bool   `json:"running"`
	Status   string `json:"status"`
	Hostname string `json:"hostname"`
}

// I2PInfo describes the I2P eepsite, per AI.md PART 13 and PART 31.2.
// I2P defaults off on bare hosts and on in containers; when disabled,
// fields stay zero-valued and provider reads "none".
type I2PInfo struct {
	Enabled  bool   `json:"enabled"`
	Running  bool   `json:"running"`
	Status   string `json:"status"`
	Hostname string `json:"hostname"`
	Provider string `json:"provider"`
}

// ChecksInfo reports public-safe component health. Dataset checks follow
// core and overlay checks in registration order, per AI.md PART 13.
type ChecksInfo struct {
	Database  string         `json:"database"`
	Cache     string         `json:"cache"`
	Disk      string         `json:"disk"`
	Scheduler string         `json:"scheduler"`
	Tor       string         `json:"tor,omitempty"`
	I2P       string         `json:"i2p,omitempty"`
	Datasets  []DatasetCheck `json:"-"`
}

// DatasetCheck is one dynamically registered dataset health result.
type DatasetCheck struct{ Key, Status string }

// MarshalJSON preserves core/overlay/dataset order required by PART 13.
func (c ChecksInfo) MarshalJSON() ([]byte, error) {
	fields := []struct{ key, value string }{{"database", c.Database}, {"cache", c.Cache}, {"disk", c.Disk}, {"scheduler", c.Scheduler}}
	if c.Tor != "" {
		fields = append(fields, struct{ key, value string }{"tor", c.Tor})
	}
	if c.I2P != "" {
		fields = append(fields, struct{ key, value string }{"i2p", c.I2P})
	}
	for _, dataset := range c.Datasets {
		fields = append(fields, struct{ key, value string }{dataset.Key, dataset.Status})
	}
	encoded := make([]byte, 0, 128)
	encoded = append(encoded, '{')
	for i, field := range fields {
		if i > 0 {
			encoded = append(encoded, ',')
		}
		key, err := json.Marshal(field.key)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(field.value)
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, key...)
		encoded = append(encoded, ':')
		encoded = append(encoded, value...)
	}
	encoded = append(encoded, '}')
	return encoded, nil
}

// StatsInfo carries public-safe aggregate request statistics, per AI.md
// PART 13.
type StatsInfo struct {
	RequestsTotal int64 `json:"requests_total"`
	Requests24h   int64 `json:"requests_24h"`
	ActiveConns   int   `json:"active_connections"`
}

// projectInfo is static until AI.md PART 16 branding config exists, per
// IDEA.md "Project variables" (app_name) and "Project description".
var projectInfo = ProjectInfo{
	Name:        "Shortner",
	Tagline:     "Self-hosted URL shortening",
	Description: "A self-hosted URL shortening service with an API and web interface.",
}

// TorReporter is the slice of *tor.Manager the health endpoint needs. It is
// an interface so httpserver never imports the overlay packages and so the
// endpoint can be tested without a live Tor daemon.
type TorReporter interface {
	Enabled() bool
	Running() bool
	Healthy() bool
	OnionAddress() string
	// Err returns the most recent start/restart failure's message, or "".
	Err() string
}

// I2PReporter is the slice of *i2p.Manager the health endpoint needs.
// ProviderName is the resolved provider ("i2pd", "sam", or "none").
type I2PReporter interface {
	Enabled() bool
	Starting() bool
	Running() bool
	Healthy() bool
	EepsiteAddress() string
	ProviderName() string
	// Err returns the most recent start/restart failure's message, or "".
	Err() string
}

// SchedulerReporter reports whether the built-in scheduler is active.
type SchedulerReporter interface {
	Healthy() bool
}

// healthDeps bundles what the health handler needs to build a
// HealthResponse.
type healthDeps struct {
	sqlDB       db.Handle
	cacheStatus func(context.Context) string
	dataDir     string
	startTime   time.Time
	stats       *Stats
	version     string
	commit      string
	buildDate   string
	// geoip is true when a GeoIP database is loaded (AI.md PART 19).
	geoip        bool
	geoipDir     string
	geoipDBs     config.GeoIPDatabases
	geoipManager *geoip.Manager
	blocklists   *blocklist.Manager
	// tor and i2p are nil when that overlay network is not configured at
	// all, which reads as disabled everywhere in the response.
	tor       TorReporter
	i2p       I2PReporter
	scheduler SchedulerReporter
	branding  *brandingStateStore
}

func (h *healthDeps) cacheHealth(ctx context.Context) string {
	if h.cacheStatus == nil {
		return "ok"
	}
	return healthCheckStatus(h.cacheStatus(ctx))
}

func (h *healthDeps) schedulerHealth() string {
	if h.scheduler == nil || !h.scheduler.Healthy() {
		return "error"
	}
	return "ok"
}

// torInfo renders features.tor from the live manager. AI.md PART 13's
// `features.tor.status` vocabulary is "disabled", "starting",
// "healthy" or "unavailable"; a disabled/absent manager omits checks.tor
// entirely rather than reporting an error.
func (h *healthDeps) torInfo() (TorInfo, string) {
	if h.tor == nil || !h.tor.Enabled() {
		return TorInfo{Status: "disabled"}, ""
	}
	info := TorInfo{Enabled: true, Running: h.tor.Running()}
	if info.Running && h.tor.Healthy() {
		info.Hostname = h.tor.OnionAddress()
		info.Status = "healthy"
		return info, "ok"
	}
	if !info.Running && h.tor.Err() == "" {
		info.Status = "starting"
		return info, "ok"
	}
	info.Status = "unavailable"
	return info, "degraded"
}

// i2pInfo renders features.i2p from the live manager. A nil or disabled
// manager reports provider "none" and checks.i2p is omitted. AI.md PART
// 13's fixed vocabulary never exposes error text.
func (h *healthDeps) i2pInfo() (I2PInfo, string) {
	if h.i2p == nil || !h.i2p.Enabled() {
		return I2PInfo{Status: "disabled", Provider: "none"}, ""
	}
	if h.i2p.Starting() {
		return I2PInfo{Enabled: true, Status: "starting", Provider: h.i2p.ProviderName()}, "ok"
	}
	provider := h.i2p.ProviderName()
	if provider == "none" && h.i2p.Err() != "" {
		return I2PInfo{Status: "disabled", Provider: "none"}, ""
	}
	info := I2PInfo{
		Enabled:  true,
		Running:  h.i2p.Running(),
		Hostname: h.i2p.EepsiteAddress(),
		Provider: provider,
	}
	if info.Running && h.i2p.Healthy() {
		info.Status = "healthy"
		return info, "ok"
	}
	if !info.Running && h.i2p.Err() == "" {
		info.Status = "starting"
		return info, "ok"
	}
	info.Status = "unavailable"
	return info, "degraded"
}

// buildHealthResponse assembles the canonical health response.
// checks.tor/checks.i2p are present only while that overlay network is
// active (AI.md PART 31); checks.scheduler reflects the always-running
// scheduler loop (PART 18).
//
// An unhealthy overlay never makes the whole service unhealthy: the
// clearnet site is fully functional without it, and the manager restarts
// the provider on its own within 30 seconds.
func (h *healthDeps) buildHealthResponse(ctx context.Context) HealthResponse {
	dbStatus := checkDatabase(ctx, h.sqlDB)
	diskStatus := checkDisk(h.dataDir)
	torInfo, torCheck := h.torInfo()
	i2pInfo, i2pCheck := h.i2pInfo()
	var datasetChecks []DatasetCheck
	if h.blocklists != nil {
		for _, check := range h.blocklists.OrderedStatus() {
			datasetChecks = append(datasetChecks, DatasetCheck{Key: "blocklist_" + check.ID, Status: check.Status})
		}
	}
	if h.geoip {
		for _, dataset := range []struct {
			name    string
			enabled bool
		}{{"asn", h.geoipDBs.ASN}, {"country", h.geoipDBs.Country}, {"city", h.geoipDBs.City}} {
			if dataset.enabled {
				status := geoipDatasetStatus(h.geoipDir)
				if h.geoipManager != nil {
					status = h.geoipManager.DatasetStatus(dataset.name)
				}
				datasetChecks = append(datasetChecks, DatasetCheck{Key: "geoip_" + dataset.name, Status: status})
			}
		}
	}

	status := "healthy"
	if dbStatus != "ok" || diskStatus != "ok" {
		status = "unhealthy"
	} else if torCheck == "degraded" || i2pCheck == "degraded" || h.schedulerHealth() != "ok" || h.cacheHealth(ctx) != "ok" || hasUnhealthyDataset(datasetChecks) {
		status = "degraded"
	}

	total, last24h, active := h.stats.Snapshot()

	project := projectInfo
	if branding := h.branding.load(nil); branding != nil {
		if branding.Branding.Title != "" {
			project.Name = branding.Branding.Title
		}
		if branding.Branding.Tagline != "" {
			project.Tagline = branding.Branding.Tagline
		}
		if branding.Branding.Description != "" {
			project.Description = branding.Branding.Description
		}
	}
	return HealthResponse{
		Project:   project,
		Status:    status,
		Version:   h.version,
		GoVersion: runtime.Version(),
		Build: BuildInfo{
			Commit: h.commit,
			Date:   h.buildDate,
		},
		Uptime:    formatUptime(time.Since(h.startTime)),
		Mode:      mode.GetCurrentAppMode().String(),
		Timestamp: time.Now().UTC().Truncate(time.Second),
		Cluster:   ClusterInfo{Enabled: false},
		Features: FeaturesInfo{
			Tor:   torInfo,
			I2P:   i2pInfo,
			GeoIP: h.geoip,
		},
		Checks: ChecksInfo{
			Database:  dbStatus,
			Cache:     h.cacheHealth(ctx),
			Disk:      diskStatus,
			Scheduler: h.schedulerHealth(),
			Tor:       torCheck,
			I2P:       i2pCheck,
			Datasets:  datasetChecks,
		},
		Stats: StatsInfo{
			RequestsTotal: total,
			Requests24h:   last24h,
			ActiveConns:   active,
		},
	}
}

func geoipDatasetStatus(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, ".last_updated"))
	updated, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(string(data)))
	if err != nil || parseErr != nil || updated.After(time.Now().Add(time.Minute)) {
		return "error"
	}
	if time.Since(updated) > 21*24*time.Hour {
		return "degraded"
	}
	return "ok"
}

func hasUnhealthyDataset(checks []DatasetCheck) bool {
	for _, check := range checks {
		if check.Status != "ok" {
			return true
		}
	}
	return false
}

func healthCheckStatus(status string) string {
	if status == "error" {
		return "degraded"
	}
	if status == "degraded" {
		return "degraded"
	}
	return "ok"
}

// checkDatabase pings sqlDB with a short timeout, per AI.md PART 13
// "checks.database".
func checkDatabase(ctx context.Context, sqlDB db.Handle) string {
	if sqlDB == nil {
		return "error"
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		return "error"
	}
	return "ok"
}

// checkDisk verifies dataDir is writable by creating and removing a temp
// file, per AI.md PART 13 "checks.disk".
func checkDisk(dataDir string) string {
	if dataDir == "" {
		return "error"
	}
	f, err := os.CreateTemp(dataDir, ".healthz-check-*")
	if err != nil {
		return "error"
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return "ok"
}

// formatUptime renders d as "{days}d {hours}h {minutes}m", per AI.md
// PART 13 "uptime": "human readable '2d 5h 30m'".
func formatUptime(d time.Duration) string {
	if d < time.Minute {
		seconds := int(d / time.Second)
		return fmt.Sprintf("%ds", seconds)
	}
	d = d.Truncate(time.Minute)
	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour
	hours := d / time.Hour
	d -= hours * time.Hour
	minutes := d / time.Minute
	parts := make([]string, 0, 3)
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	return strings.Join(parts, " ")
}

// healthHandler serves the machine-readable health response with JSON
// default and text for .txt, Accept:text/plain, or non-interactive clients.
// Browser requests are rendered by the frontend health page.
func (h *healthDeps) healthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := h.buildHealthResponse(r.Context())

		status := http.StatusOK
		if resp.Status == "unhealthy" {
			status = http.StatusServiceUnavailable
		}

		if wantsText(r) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(status)
			body := formatHealthText(resp)
			if !strings.HasPrefix(r.URL.Path, "/api/") {
				body = formatHealthFrontendText(resp)
			}
			_, _ = w.Write([]byte(body))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		apperr.WriteJSON(w, resp)
	}
}

// formatHealthText renders resp as flattened "key: value" lines in the
// canonical field order of AI.md PART 13 "Plain Text (Accept: text/plain)".
func formatHealthText(resp HealthResponse) string {
	var b strings.Builder
	fmt.Fprintf(&b, "project.name: %s\n", resp.Project.Name)
	fmt.Fprintf(&b, "project.tagline: %s\n", resp.Project.Tagline)
	fmt.Fprintf(&b, "project.description: %s\n", resp.Project.Description)
	fmt.Fprintf(&b, "status: %s\n", resp.Status)
	if resp.PendingRestart {
		fmt.Fprintf(&b, "pending_restart: %s\n", strconv.FormatBool(resp.PendingRestart))
		fmt.Fprintf(&b, "restart_reason: %s\n", strings.Join(resp.RestartReason, ", "))
	}
	fmt.Fprintf(&b, "version: %s\n", resp.Version)
	fmt.Fprintf(&b, "go_version: %s\n", resp.GoVersion)
	fmt.Fprintf(&b, "build.commit: %s\n", resp.Build.Commit)
	fmt.Fprintf(&b, "build.date: %s\n", resp.Build.Date)
	fmt.Fprintf(&b, "uptime: %s\n", resp.Uptime)
	fmt.Fprintf(&b, "mode: %s\n", resp.Mode)
	fmt.Fprintf(&b, "timestamp: %s\n", resp.Timestamp.Format(time.RFC3339))
	fmt.Fprintf(&b, "cluster.enabled: %s\n", strconv.FormatBool(resp.Cluster.Enabled))
	fmt.Fprintf(&b, "features.tor: %s", resp.Features.Tor.Status)
	if resp.Features.Tor.Hostname != "" {
		fmt.Fprintf(&b, " %s", resp.Features.Tor.Hostname)
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "features.i2p: %s", resp.Features.I2P.Status)
	if resp.Features.I2P.Hostname != "" {
		fmt.Fprintf(&b, " %s", resp.Features.I2P.Hostname)
	} else if resp.Features.I2P.Provider != "" {
		fmt.Fprintf(&b, " provider=%s", resp.Features.I2P.Provider)
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "features.geoip: %s\n", enabledDisabled(resp.Features.GeoIP))
	fmt.Fprintf(&b, "checks.database: %s\n", resp.Checks.Database)
	fmt.Fprintf(&b, "checks.cache: %s\n", resp.Checks.Cache)
	fmt.Fprintf(&b, "checks.disk: %s\n", resp.Checks.Disk)
	fmt.Fprintf(&b, "checks.scheduler: %s\n", resp.Checks.Scheduler)
	if resp.Checks.Tor != "" {
		fmt.Fprintf(&b, "checks.tor: %s\n", resp.Checks.Tor)
	}
	if resp.Checks.I2P != "" {
		fmt.Fprintf(&b, "checks.i2p: %s\n", resp.Checks.I2P)
	}
	fmt.Fprintf(&b, "stats.requests_total: %d\n", resp.Stats.RequestsTotal)
	fmt.Fprintf(&b, "stats.requests_24h: %d\n", resp.Stats.Requests24h)
	fmt.Fprintf(&b, "stats.active_connections: %d\n", resp.Stats.ActiveConns)
	return b.String()
}

func formatHealthFrontendText(resp HealthResponse) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s - %s\n\n", resp.Project.Name, resp.Project.Tagline)
	fmt.Fprintf(&b, "Status:    %s\n", titleHealthStatus(resp.Status))
	fmt.Fprintf(&b, "Version:   %s\n", resp.Version)
	fmt.Fprintf(&b, "Build:     %s (%s)\n", resp.Build.Commit, humanHealthTimestamp(resp.Build.Date))
	fmt.Fprintf(&b, "Go:        %s\n", resp.GoVersion)
	fmt.Fprintf(&b, "Uptime:    %s\n", formatHumanUptime(parseCompactUptime(resp.Uptime)))
	fmt.Fprintf(&b, "Mode:      %s\n", resp.Mode)
	fmt.Fprintf(&b, "Checked:   %s\n\n", resp.Timestamp.UTC().Format("January 02, 2006 at 15:04:05 MST"))
	b.WriteString("Features\n")
	fmt.Fprintf(&b, "  Tor:     %s", resp.Features.Tor.Status)
	if resp.Features.Tor.Hostname != "" {
		fmt.Fprintf(&b, " - %s", resp.Features.Tor.Hostname)
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "  I2P:     %s", resp.Features.I2P.Status)
	if resp.Features.I2P.Hostname != "" {
		fmt.Fprintf(&b, " - %s", resp.Features.I2P.Hostname)
	}
	b.WriteByte('\n')
	if resp.Features.GeoIP {
		b.WriteString("  GeoIP:   enabled\n")
	} else {
		b.WriteString("  GeoIP:   disabled\n")
	}
	b.WriteString("\nChecks\n")
	fmt.Fprintf(&b, "  Database:  %s\n  Cache:     %s\n  Disk:      %s\n  Scheduler: %s\n", resp.Checks.Database, resp.Checks.Cache, resp.Checks.Disk, resp.Checks.Scheduler)
	if resp.Checks.Tor != "" {
		fmt.Fprintf(&b, "  Tor:       %s\n", resp.Checks.Tor)
	}
	if resp.Checks.I2P != "" {
		fmt.Fprintf(&b, "  I2P:       %s\n", resp.Checks.I2P)
	}
	fmt.Fprintf(&b, "\nStatistics\n  Total requests:      %s\n  Requests (24 hours): %s\n  Active connections:  %s\n", groupHealthCount(resp.Stats.RequestsTotal), groupHealthCount(resp.Stats.Requests24h), groupHealthCount(int64(resp.Stats.ActiveConns)))
	return b.String()
}

func humanHealthTimestamp(value string) string {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	return parsed.UTC().Format("January 02, 2006 at 15:04:05 MST")
}

func formatHumanUptime(value time.Duration) string {
	if value < time.Minute {
		return fmt.Sprintf("%d seconds", int(value.Seconds()))
	}
	value = value.Truncate(time.Minute)
	days := int(value / (24 * time.Hour))
	value -= time.Duration(days) * 24 * time.Hour
	hours := int(value / time.Hour)
	value -= time.Duration(hours) * time.Hour
	minutes := int(value / time.Minute)
	parts := make([]string, 0, 3)
	if days > 0 {
		unit := "days"
		if days == 1 {
			unit = "day"
		}
		parts = append(parts, fmt.Sprintf("%d %s", days, unit))
	}
	if hours > 0 {
		unit := "hours"
		if hours == 1 {
			unit = "hour"
		}
		parts = append(parts, fmt.Sprintf("%d %s", hours, unit))
	}
	if minutes > 0 && len(parts) < 2 {
		unit := "minutes"
		if minutes == 1 {
			unit = "minute"
		}
		parts = append(parts, fmt.Sprintf("%d %s", minutes, unit))
	}
	return strings.Join(parts, " ")
}

func parseCompactUptime(value string) time.Duration {
	var total time.Duration
	for _, part := range strings.Fields(value) {
		if len(part) < 2 {
			continue
		}
		amount, err := strconv.Atoi(part[:len(part)-1])
		if err != nil {
			continue
		}
		switch part[len(part)-1] {
		case 'd':
			total += time.Duration(amount) * 24 * time.Hour
		case 'h':
			total += time.Duration(amount) * time.Hour
		case 'm':
			total += time.Duration(amount) * time.Minute
		case 's':
			total += time.Duration(amount) * time.Second
		}
	}
	return total
}

func groupHealthCount(value int64) string {
	if value < 0 {
		value = 0
	}
	text := strconv.FormatInt(value, 10)
	for index := len(text) - 3; index > 0; index -= 3 {
		text = text[:index] + "," + text[index:]
	}
	return text
}

func enabledDisabled(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func titleHealthStatus(status string) string {
	if status == "" {
		return status
	}
	return strings.ToUpper(status[:1]) + status[1:]
}
