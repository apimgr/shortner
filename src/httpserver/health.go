// The PART 13 health endpoints: /server/healthz, /api/{api_version}/
// server/healthz, /api/healthz, and the optional /healthz root alias. See
// AI.md PART 13 "Health Checks" -> "Field Order & Structure".
package httpserver

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/apperr"
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

	Uptime    string    `json:"uptime"`
	Mode      string    `json:"mode"`
	Timestamp time.Time `json:"timestamp"`

	Features FeaturesInfo `json:"features"`
	Checks   ChecksInfo   `json:"checks"`
	Stats    StatsInfo    `json:"stats"`
}

// ProjectInfo identifies the running application, per AI.md PART 13. The
// branding config (AI.md PART 16) doesn't exist yet, so these are static
// values sourced from IDEA.md — see TODO.AI.md.
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

// I2PInfo describes the opt-in I2P eepsite, per AI.md PART 13 and
// PART 31.2. I2P is off by default, in which case every field stays
// zero-valued and provider reads "none".
type I2PInfo struct {
	Enabled  bool   `json:"enabled"`
	Running  bool   `json:"running"`
	Status   string `json:"status"`
	Hostname string `json:"hostname"`
	Provider string `json:"provider"`
}

// ChecksInfo reports component health as "ok"/"error" only, per AI.md
// PART 13 "Security: Public Info Only". checks.tor and checks.i2p are
// omitted entirely unless that overlay network is enabled (AI.md PART 31).
type ChecksInfo struct {
	Database  string `json:"database"`
	Cache     string `json:"cache"`
	Disk      string `json:"disk"`
	Scheduler string `json:"scheduler"`
	Tor       string `json:"tor,omitempty"`
	I2P       string `json:"i2p,omitempty"`
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
	Running() bool
	Healthy() bool
	EepsiteAddress() string
	ProviderName() string
	// Err returns the most recent start/restart failure's message, or "".
	Err() string
}

// healthDeps bundles what the health handler needs to build a
// HealthResponse.
type healthDeps struct {
	sqlDB     *sql.DB
	dataDir   string
	startTime time.Time
	stats     *Stats
	version   string
	commit    string
	buildDate string
	// geoip is true when a GeoIP database is loaded (AI.md PART 19).
	geoip bool
	// tor and i2p are nil when that overlay network is not configured at
	// all, which reads as disabled everywhere in the response.
	tor TorReporter
	i2p I2PReporter
}

// torInfo renders features.tor from the live manager. AI.md PART 13's
// `features.tor.status` vocabulary is exactly "healthy" or
// "error:{short message}"; a disabled/absent manager omits checks.tor
// entirely rather than reporting an error.
func (h *healthDeps) torInfo() (TorInfo, string) {
	if h.tor == nil || !h.tor.Enabled() {
		return TorInfo{Status: "disabled"}, ""
	}
	info := TorInfo{Enabled: true, Running: h.tor.Running(), Hostname: h.tor.OnionAddress()}
	if info.Running && h.tor.Healthy() && info.Hostname != "" {
		info.Status = "healthy"
		return info, "ok"
	}
	info.Status = "error:" + torErrorMessage(h.tor)
	return info, "error"
}

// torErrorMessage picks the short message for features.tor.status's
// "error:{short message}" form: the manager's own last-start error when
// there is one, or a generic reason describing which health condition
// failed otherwise (never a bare "error" with nothing after the colon).
func torErrorMessage(tor TorReporter) string {
	if msg := tor.Err(); msg != "" {
		return msg
	}
	if !tor.Running() {
		return "not running"
	}
	if !tor.Healthy() {
		return "control connection unresponsive"
	}
	return "no onion address published"
}

// i2pInfo renders features.i2p from the live manager. I2P is opt-in, so a
// nil or disabled manager reports provider "none" with every other field
// zero-valued, and checks.i2p is omitted. AI.md PART 13's
// `features.i2p.status` vocabulary is "disabled", "healthy", or
// "error:{short message}".
func (h *healthDeps) i2pInfo() (I2PInfo, string) {
	if h.i2p == nil || !h.i2p.Enabled() {
		return I2PInfo{Status: "disabled", Provider: "none"}, ""
	}
	info := I2PInfo{
		Enabled:  true,
		Running:  h.i2p.Running(),
		Hostname: h.i2p.EepsiteAddress(),
		Provider: h.i2p.ProviderName(),
	}
	if info.Running && h.i2p.Healthy() && info.Hostname != "" {
		info.Status = "healthy"
		return info, "ok"
	}
	info.Status = "error:" + i2pErrorMessage(h.i2p)
	return info, "error"
}

// i2pErrorMessage picks the short message for features.i2p.status's
// "error:{short message}" form, mirroring torErrorMessage.
func i2pErrorMessage(i2p I2PReporter) string {
	if msg := i2p.Err(); msg != "" {
		return msg
	}
	if !i2p.Running() {
		return "not running"
	}
	if !i2p.Healthy() {
		return "provider unresponsive"
	}
	return "no eepsite address published"
}

// buildHealthResponse assembles the canonical health response.
// checks.tor/checks.i2p are present only while that overlay network is
// enabled (AI.md PART 31); checks.scheduler reports "ok" because nothing
// can fail there yet — it becomes a real check once the PART 18 scheduler
// exposes task failures (see TODO.AI.md).
//
// An unhealthy overlay never makes the whole service unhealthy: the
// clearnet site is fully functional without it, and the manager restarts
// the provider on its own within 30 seconds.
func (h *healthDeps) buildHealthResponse(ctx context.Context) HealthResponse {
	dbStatus := checkDatabase(ctx, h.sqlDB)
	diskStatus := checkDisk(h.dataDir)
	torInfo, torCheck := h.torInfo()
	i2pInfo, i2pCheck := h.i2pInfo()

	status := "healthy"
	if dbStatus != "ok" || diskStatus != "ok" {
		status = "unhealthy"
	}

	total, last24h, active := h.stats.Snapshot()

	return HealthResponse{
		Project:   projectInfo,
		Status:    status,
		Version:   h.version,
		GoVersion: runtime.Version(),
		Build: BuildInfo{
			Commit: h.commit,
			Date:   h.buildDate,
		},
		Uptime:    formatUptime(time.Since(h.startTime)),
		Mode:      mode.GetCurrentAppMode().String(),
		Timestamp: time.Now().UTC(),
		Features: FeaturesInfo{
			Tor:   torInfo,
			I2P:   i2pInfo,
			GeoIP: h.geoip,
		},
		Checks: ChecksInfo{
			Database:  dbStatus,
			Cache:     "ok",
			Disk:      diskStatus,
			Scheduler: "ok",
			Tor:       torCheck,
			I2P:       i2pCheck,
		},
		Stats: StatsInfo{
			RequestsTotal: total,
			Requests24h:   last24h,
			ActiveConns:   active,
		},
	}
}

// checkDatabase pings sqlDB with a short timeout, per AI.md PART 13
// "checks.database".
func checkDatabase(ctx context.Context, sqlDB *sql.DB) string {
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
	d = d.Round(time.Minute)
	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour
	hours := d / time.Hour
	d -= hours * time.Hour
	minutes := d / time.Minute
	return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
}

// healthHandler serves the canonical health response with simplified
// content negotiation (JSON default, text for .txt/Accept:text-plain/
// non-interactive clients). Full PART 16 HTML rendering is deferred — see
// TODO.AI.md.
func (h *healthDeps) healthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := h.buildHealthResponse(r.Context())

		status := http.StatusOK
		if resp.Status != "healthy" {
			status = http.StatusServiceUnavailable
		}

		if wantsText(r) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(formatHealthText(resp)))
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
	fmt.Fprintf(&b, "features.tor.enabled: %s\n", strconv.FormatBool(resp.Features.Tor.Enabled))
	fmt.Fprintf(&b, "features.tor.running: %s\n", strconv.FormatBool(resp.Features.Tor.Running))
	fmt.Fprintf(&b, "features.tor.status: %s\n", statusOrDisabled(resp.Features.Tor.Status))
	fmt.Fprintf(&b, "features.tor.hostname: %s\n", resp.Features.Tor.Hostname)
	fmt.Fprintf(&b, "features.i2p.enabled: %s\n", strconv.FormatBool(resp.Features.I2P.Enabled))
	fmt.Fprintf(&b, "features.i2p.running: %s\n", strconv.FormatBool(resp.Features.I2P.Running))
	fmt.Fprintf(&b, "features.i2p.status: %s\n", statusOrDisabled(resp.Features.I2P.Status))
	fmt.Fprintf(&b, "features.i2p.hostname: %s\n", resp.Features.I2P.Hostname)
	fmt.Fprintf(&b, "features.i2p.provider: %s\n", resp.Features.I2P.Provider)
	fmt.Fprintf(&b, "features.geoip: %s\n", strconv.FormatBool(resp.Features.GeoIP))
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

// statusOrDisabled renders an overlay-network status field, substituting
// the spec's "disabled" wording for the zero value used in JSON, per AI.md
// PART 13 "Health Response Fields".
func statusOrDisabled(status string) string {
	if status == "" {
		return "disabled"
	}
	return status
}
