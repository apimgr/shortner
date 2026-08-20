package metrics

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"regexp"
	"strings"
	"sync"
	"time"
)

// registered tracks which wrapped driver names have already been
// sql.Register'd in this process, since sql.Register panics on a duplicate
// name and Open (src/db/db.go) may be called more than once (tests, or a
// future reload feature).
var registered sync.Map

// RegisterInstrumentedDriver wraps underlying under a new driver name so
// every query issued through the resulting *sql.DB is recorded to the
// db_* metrics below, per AI.md PART 20 "Database Metrics". This is the
// single non-invasive instrumentation point: src/db.Open is the only
// sql.Open call site in the codebase, so wrapping the driver here reaches
// every one of the 30+ existing *sql.DB call sites in src/db and
// src/scheduler without changing any of them.
func (m *Metrics) RegisterInstrumentedDriver(name string, underlying driver.Driver) string {
	wrapped := name + "_shortner_instrumented"
	if _, loaded := registered.LoadOrStore(wrapped, true); !loaded {
		sql.Register(wrapped, &instrumentedDriver{underlying: underlying, m: m})
	}
	return wrapped
}

type instrumentedDriver struct {
	underlying driver.Driver
	m          *Metrics
}

func (d *instrumentedDriver) Open(dsn string) (driver.Conn, error) {
	conn, err := d.underlying.Open(dsn)
	if err != nil {
		return nil, err
	}
	return &instrumentedConn{Conn: conn, m: d.m}, nil
}

// instrumentedConn wraps a driver.Conn, recording query metrics for the
// context-aware Queryer/Execer paths that modernc.org/sqlite implements.
// Every optional interface method checks the underlying conn's own support
// at call time and returns driver.ErrSkip (or delegates to the
// non-context fallback) when unsupported, so database/sql's normal
// fallback behavior is preserved.
type instrumentedConn struct {
	driver.Conn
	m *Metrics
}

func (c *instrumentedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	qc, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	op, table := parseQuery(query)
	start := time.Now()
	rows, err := qc.QueryContext(ctx, query, args)
	c.m.recordDBQuery(op, table, time.Since(start), err)
	return rows, err
}

func (c *instrumentedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	ec, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	op, table := parseQuery(query)
	start := time.Now()
	res, err := ec.ExecContext(ctx, query, args)
	c.m.recordDBQuery(op, table, time.Since(start), err)
	return res, err
}

func (c *instrumentedConn) Ping(ctx context.Context) error {
	p, ok := c.Conn.(driver.Pinger)
	if !ok {
		return nil
	}
	return p.Ping(ctx)
}

func (c *instrumentedConn) ResetSession(ctx context.Context) error {
	r, ok := c.Conn.(driver.SessionResetter)
	if !ok {
		return nil
	}
	return r.ResetSession(ctx)
}

func (c *instrumentedConn) CheckNamedValue(nv *driver.NamedValue) error {
	n, ok := c.Conn.(driver.NamedValueChecker)
	if !ok {
		return driver.ErrSkip
	}
	return n.CheckNamedValue(nv)
}

func (c *instrumentedConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	b, ok := c.Conn.(driver.ConnBeginTx)
	if !ok {
		// Fallback for a driver.Conn that does not implement ConnBeginTx.
		// Begin is the only method the plain driver.Conn interface offers
		// in that case, so the deprecation warning does not apply here.
		//lint:ignore SA1019 required fallback for drivers without ConnBeginTx
		return c.Conn.Begin()
	}
	return b.BeginTx(ctx, opts)
}

func (c *instrumentedConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	p, ok := c.Conn.(driver.ConnPrepareContext)
	if !ok {
		return c.Conn.Prepare(query)
	}
	return p.PrepareContext(ctx, query)
}

// recordDBQuery updates db_queries_total, db_query_duration_seconds, and
// (on error) db_errors_total, per AI.md PART 20 "Database Metrics".
func (m *Metrics) recordDBQuery(operation, table string, d time.Duration, err error) {
	m.DBQueriesTotal.WithLabelValues(operation, table).Inc()
	m.DBQueryDuration.WithLabelValues(operation, table).Observe(d.Seconds())
	if err != nil && err != sql.ErrNoRows {
		m.DBErrorsTotal.WithLabelValues(operation, classifyError(err)).Inc()
	}
}

// UpdateConnectionMetrics reads sql.DB.Stats() into db_connections_open/
// db_connections_in_use. Callers poll this (e.g. from the system-metrics
// ticker) since database/sql does not push connection-pool events.
func (m *Metrics) UpdateConnectionMetrics(stats sql.DBStats) {
	m.DBConnectionsOpen.Set(float64(stats.OpenConnections))
	m.DBConnectionsInUse.Set(float64(stats.InUse))
}

var tableRe = regexp.MustCompile("(?i)(?:from|into|update|table)\\s+[\"'`]?([a-zA-Z0-9_]+)")

// parseQuery naively classifies a SQL statement's operation and target
// table from its text, per AI.md PART 20's "Database label values" table.
// This is intentionally simple string parsing, not a SQL AST parser — good
// enough for the small, hand-written query set in src/db.
func parseQuery(query string) (operation, table string) {
	q := strings.TrimSpace(query)
	fields := strings.Fields(q)
	if len(fields) == 0 {
		return "other", "unknown"
	}
	switch strings.ToLower(fields[0]) {
	case "select":
		operation = "select"
	case "insert":
		operation = "insert"
	case "update":
		operation = "update"
	case "delete":
		operation = "delete"
	default:
		operation = "other"
	}
	if m := tableRe.FindStringSubmatch(q); len(m) == 2 {
		table = strings.ToLower(m[1])
	} else {
		table = "unknown"
	}
	return operation, table
}

// classifyError maps a driver/sql error to the error_type label values
// from AI.md PART 20's "Database label values" table.
func classifyError(err error) string {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "busy"):
		return "timeout"
	case strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate"):
		return "duplicate"
	case strings.Contains(msg, "constraint"):
		return "constraint"
	case strings.Contains(msg, "connection") || strings.Contains(msg, "closed"):
		return "connection"
	default:
		return "other"
	}
}
