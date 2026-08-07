// The PART 13 health endpoints: /server/healthz, /api/{api_version}/
// server/healthz, /api/healthz, and the optional /healthz root alias. See
// AI.md PART 13 "Health Checks" -> "Field Order & Structure".
package httpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

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
// Tor (PART 31) and GeoIP (PART 19) are not implemented yet — reported
// honestly as disabled.
type FeaturesInfo struct {
	Tor   TorInfo `json:"tor"`
	GeoIP bool    `json:"geoip"`
}

// TorInfo describes the Tor hidden service, per AI.md PART 13. Always the
// zero value until PART 31 lands — see TODO.AI.md.
type TorInfo struct {
	Enabled  bool   `json:"enabled"`
	Running  bool   `json:"running"`
	Status   string `json:"status"`
	Hostname string `json:"hostname"`
}

// ChecksInfo reports component health as "ok"/"error" only, per AI.md
// PART 13 "Security: Public Info Only".
type ChecksInfo struct {
	Database  string `json:"database"`
	Cache     string `json:"cache"`
	Disk      string `json:"disk"`
	Scheduler string `json:"scheduler"`
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
}

// buildHealthResponse assembles the canonical health response. checks.tor
// is omitted entirely (Tor, PART 31, is not built); checks.scheduler
// reports "ok" because nothing can fail there yet — it becomes a real
// check once the PART 18 scheduler exists (see TODO.AI.md).
func (h *healthDeps) buildHealthResponse(ctx context.Context) HealthResponse {
	dbStatus := checkDatabase(ctx, h.sqlDB)
	diskStatus := checkDisk(h.dataDir)

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
			Tor:   TorInfo{},
			GeoIP: false,
		},
		Checks: ChecksInfo{
			Database:  dbStatus,
			Cache:     "ok",
			Disk:      diskStatus,
			Scheduler: "ok",
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
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// formatHealthText renders resp as simple "key: value" lines for
// non-interactive/text clients.
func formatHealthText(resp HealthResponse) string {
	var b strings.Builder
	fmt.Fprintf(&b, "project: %s\n", resp.Project.Name)
	fmt.Fprintf(&b, "status: %s\n", resp.Status)
	fmt.Fprintf(&b, "version: %s\n", resp.Version)
	fmt.Fprintf(&b, "go_version: %s\n", resp.GoVersion)
	fmt.Fprintf(&b, "build_commit: %s\n", resp.Build.Commit)
	fmt.Fprintf(&b, "build_date: %s\n", resp.Build.Date)
	fmt.Fprintf(&b, "uptime: %s\n", resp.Uptime)
	fmt.Fprintf(&b, "mode: %s\n", resp.Mode)
	fmt.Fprintf(&b, "timestamp: %s\n", resp.Timestamp.Format(time.RFC3339))
	fmt.Fprintf(&b, "checks.database: %s\n", resp.Checks.Database)
	fmt.Fprintf(&b, "checks.cache: %s\n", resp.Checks.Cache)
	fmt.Fprintf(&b, "checks.disk: %s\n", resp.Checks.Disk)
	fmt.Fprintf(&b, "checks.scheduler: %s\n", resp.Checks.Scheduler)
	fmt.Fprintf(&b, "features.geoip: %s\n", strconv.FormatBool(resp.Features.GeoIP))
	fmt.Fprintf(&b, "stats.requests_total: %d\n", resp.Stats.RequestsTotal)
	fmt.Fprintf(&b, "stats.requests_24h: %d\n", resp.Stats.Requests24h)
	fmt.Fprintf(&b, "stats.active_connections: %d\n", resp.Stats.ActiveConns)
	return b.String()
}
