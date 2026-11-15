package metrics

import (
	"context"
	"database/sql"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
)

// collectInterval is how often the background collector samples system,
// Go runtime, and database-connection-pool metrics.
const collectInterval = 15 * time.Second

// StartTorCollector samples PART 20 Tor process state until ctx is
// canceled. The source must expose an actual circuit-established signal;
// process/control-port health is not used as a substitute.
func (m *Metrics) StartTorCollector(ctx context.Context, source TorMetricsSource) {
	collect := func() { m.UpdateTorMetrics(source) }
	collect()
	go func() {
		ticker := time.NewTicker(collectInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				collect()
			}
		}
	}()
}

// StartCollector runs a ticker-driven goroutine until ctx is canceled,
// updating system_* (if includeSystem), go_* (if includeRuntime), and
// db_connections_* metrics, per AI.md PART 20 "System Metrics" / "Go
// Runtime Metrics" / "Database Metrics". dataDir is the path labeled on
// the disk-usage gauges (the data directory's filesystem, per PART 20's
// example `path="/var/lib/myorg/myapp"`). sqlDB may be nil (metrics
// disabled or DB not yet open).
func (m *Metrics) StartCollector(ctx context.Context, dataDir string, sqlDB *sql.DB, includeSystem, includeRuntime bool) {
	go func() {
		ticker := time.NewTicker(collectInterval)
		defer ticker.Stop()

		var lastNumGC uint32
		var lastPauseNs uint64

		collect := func() {
			if sqlDB != nil {
				m.UpdateConnectionMetrics(sqlDB.Stats())
				m.collectBusinessMetrics(ctx, sqlDB)
			}
			if includeSystem {
				m.collectSystem(dataDir)
			}
			if includeRuntime {
				lastNumGC, lastPauseNs = m.collectRuntime(lastNumGC, lastPauseNs)
			}
		}
		collect()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				collect()
			}
		}
	}()
}

// collectBusinessMetrics samples shortner-specific counts from the existing
// links, clicks, and api_tokens tables. A failed query leaves the prior
// values intact rather than publishing a partial snapshot.
func (m *Metrics) collectBusinessMetrics(parent context.Context, sqlDB *sql.DB) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	cutoff := time.Now().Add(-24 * time.Hour).Unix()
	var linksTotal, linksCreated, linksClicked, tokensActive int64
	queries := []struct {
		dest  *int64
		query string
		args  []any
	}{
		{&linksTotal, `SELECT COUNT(*) FROM links`, nil},
		{&linksCreated, `SELECT COUNT(*) FROM links WHERE created_at >= ?`, []any{cutoff}},
		{&linksClicked, `SELECT COUNT(*) FROM clicks WHERE timestamp >= ?`, []any{cutoff}},
		{&tokensActive, `SELECT COUNT(*) FROM api_tokens WHERE kind IN ('primary', 'linked') AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`, []any{time.Now().Unix()}},
	}
	for _, item := range queries {
		if err := sqlDB.QueryRowContext(ctx, item.query, item.args...).Scan(item.dest); err != nil {
			return
		}
	}
	m.LinksTotal.Set(float64(linksTotal))
	m.LinksCreated24h.Set(float64(linksCreated))
	m.LinksClicked24h.Set(float64(linksClicked))
	m.APITokensActive.Set(float64(tokensActive))
}

func (m *Metrics) collectSystem(dataDir string) {
	if percents, err := cpu.Percent(0, false); err == nil && len(percents) > 0 {
		m.SystemCPUUsagePercent.Set(percents[0])
	}
	if vm, err := mem.VirtualMemory(); err == nil {
		m.SystemMemoryUsagePercent.Set(vm.UsedPercent)
		m.SystemMemoryUsedBytes.Set(float64(vm.Used))
		m.SystemMemoryTotalBytes.Set(float64(vm.Total))
	}
	if dataDir == "" {
		dataDir = "/"
	}
	if du, err := disk.Usage(dataDir); err == nil {
		m.SystemDiskUsagePercent.WithLabelValues(dataDir).Set(du.UsedPercent)
		m.SystemDiskUsedBytes.WithLabelValues(dataDir).Set(float64(du.Used))
		m.SystemDiskTotalBytes.WithLabelValues(dataDir).Set(float64(du.Total))
	}
}

// collectRuntime samples runtime.MemStats, updating go_goroutines,
// go_mem_alloc_bytes, go_mem_sys_bytes, and the GC counters as deltas
// since the previous sample (both are Counters, per AI.md PART 20's "Go
// Runtime Metrics" table). Returns the new (numGC, pauseTotalNs)
// baseline for the next call.
func (m *Metrics) collectRuntime(lastNumGC uint32, lastPauseNs uint64) (uint32, uint64) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	m.GoGoroutines.Set(float64(runtime.NumGoroutine()))
	m.GoMemAllocBytes.Set(float64(ms.HeapAlloc))
	m.GoMemSysBytes.Set(float64(ms.Sys))

	if ms.NumGC > lastNumGC {
		m.GoGCRunsTotal.Add(float64(ms.NumGC - lastNumGC))
	}
	if ms.PauseTotalNs > lastPauseNs {
		m.GoGCPauseTotalSeconds.Add(float64(ms.PauseTotalNs-lastPauseNs) / 1e9)
	}
	return ms.NumGC, ms.PauseTotalNs
}
