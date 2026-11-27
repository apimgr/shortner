package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"regexp"
	"strings"
)

var serverTableNames = map[string]struct{}{
	"server_stats": {}, "server_stats_hourly": {}, "links": {}, "clicks": {},
	"owners": {}, "api_tokens": {}, "api_tokens_legacy": {}, "app_secrets": {},
	"app_secret_grace": {}, "captcha_challenges": {}, "scheduler_tasks": {},
	"scheduler_history": {}, "backups": {}, "security_reports": {},
	"security_thanks": {}, "pgp_keys": {}, "audit_log": {},
}

var serverIndexNames = map[string]struct{}{
	"idx_links_short_code": {}, "idx_links_expires_at": {},
	"idx_clicks_link_id": {}, "idx_clicks_timestamp": {},
	"idx_api_tokens_hash": {}, "idx_api_tokens_prefix": {},
	"idx_api_tokens_owner": {}, "idx_api_tokens_active": {},
	"idx_api_tokens_primary": {}, "idx_api_tokens_linked_name": {},
	"idx_captcha_challenges_expiry": {}, "idx_scheduler_history_task": {},
	"idx_scheduler_history_started": {}, "idx_backups_created": {},
	"idx_security_reports_received": {}, "idx_security_reports_status": {},
	"idx_audit_timestamp": {}, "idx_audit_category": {},
	"idx_audit_actor_ip": {}, "idx_audit_target": {},
}

var sqliteMasterNameParameter = regexp.MustCompile(`(?i)\bname\s*=\s*\?`)

type libSQLSchemaObject struct {
	kind string
	name string
}

// migrateLibSQLIdentifiers moves the known legacy unprefixed schema objects
// into their srv_ namespace. It preflights the complete rename set inside one
// transaction so an ambiguous old/new pair cannot partially migrate data.
func migrateLibSQLIdentifiers(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `SELECT type, name FROM sqlite_master WHERE type IN ('table', 'index', 'view', 'trigger') AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return err
	}
	objects := make([]libSQLSchemaObject, 0)
	byName := make(map[string]libSQLSchemaObject)
	for rows.Next() {
		var object libSQLSchemaObject
		if err := rows.Scan(&object.kind, &object.name); err != nil {
			_ = rows.Close()
			return err
		}
		objects = append(objects, object)
		byName[strings.ToLower(object.name)] = object
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	legacy := make([]libSQLSchemaObject, 0)
	for _, object := range objects {
		if object.kind == "table" {
			if _, ok := serverTableNames[strings.ToLower(object.name)]; !ok {
				continue
			}
		} else if object.kind == "index" {
			if _, ok := serverIndexNames[strings.ToLower(object.name)]; !ok {
				continue
			}
		} else {
			continue
		}
		target := databaseIdentifier(object.name, true)
		if target == object.name {
			continue
		}
		if _, exists := byName[strings.ToLower(target)]; exists {
			return errors.New("db: conflicting legacy and prefixed libSQL schema objects")
		}
		legacy = append(legacy, object)
	}

	// Rename tables first. SQLite updates foreign keys and stored index SQL
	// when a table is renamed; indexes are then rebuilt under their srv_ names.
	for _, object := range legacy {
		if object.kind != "table" {
			continue
		}
		target := databaseIdentifier(object.name, true)
		if _, err := tx.ExecContext(ctx, `ALTER TABLE `+quoteSQLIdentifier(object.name)+` RENAME TO `+quoteSQLIdentifier(target)); err != nil {
			return err
		}
	}
	for _, object := range legacy {
		if object.kind != "index" {
			continue
		}
		var createSQL string
		if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='index' AND name=?`, object.name).Scan(&createSQL); err != nil {
			return err
		}
		target := databaseIdentifier(object.name, true)
		mappedSQL, err := renameLibSQLIndexSQL(createSQL, object.name, target)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, mappedSQL); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DROP INDEX `+quoteSQLIdentifier(object.name)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func quoteSQLIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func renameLibSQLIndexSQL(createSQL, oldName, newName string) (string, error) {
	quotedName := `(?:"` + regexp.QuoteMeta(strings.ReplaceAll(oldName, `"`, `""`)) + `"|` +
		"`" + regexp.QuoteMeta(strings.ReplaceAll(oldName, "`", "``")) + "`|\\[" + regexp.QuoteMeta(oldName) + `\]|` + regexp.QuoteMeta(oldName) + `)`
	pattern := regexp.MustCompile(`(?is)^\s*CREATE\s+(?:UNIQUE\s+)?INDEX\s+(?:IF\s+NOT\s+EXISTS\s+)?(` + quotedName + `)(\s+ON\b)`)
	match := pattern.FindStringSubmatchIndex(createSQL)
	if match == nil {
		return "", errors.New("db: invalid legacy libSQL index definition")
	}
	return createSQL[:match[2]] + quoteSQLIdentifier(newName) + createSQL[match[3]:], nil
}

// databaseIdentifier is the single mapping point for server-owned table and
// index names. Names outside the finite server schema inventory are untouched.
func databaseIdentifier(name string, remote bool) string {
	if !remote {
		return name
	}
	lower := strings.ToLower(name)
	if _, ok := serverTableNames[lower]; ok {
		return "srv_" + lower
	}
	if _, ok := serverIndexNames[lower]; ok {
		return "srv_" + lower
	}
	return name
}

// mapLibSQLQuery rewrites only known server schema identifiers. The scanner
// preserves SQL strings, comments, and unrelated quoted or bare identifiers.
func mapLibSQLQuery(query string) string {
	var out strings.Builder
	out.Grow(len(query) + 16)
	for i := 0; i < len(query); {
		if i+1 < len(query) && query[i] == '-' && query[i+1] == '-' {
			end := strings.IndexByte(query[i:], '\n')
			if end < 0 {
				out.WriteString(query[i:])
				break
			}
			out.WriteString(query[i : i+end+1])
			i += end + 1
			continue
		}
		if i+1 < len(query) && query[i] == '/' && query[i+1] == '*' {
			end := strings.Index(query[i+2:], "*/")
			if end < 0 {
				out.WriteString(query[i:])
				break
			}
			end += i + 4
			out.WriteString(query[i:end])
			i = end
			continue
		}
		if query[i] == '\'' {
			start := i
			i = quotedSQLStringEnd(query, i, '\'')
			out.WriteString(query[start:i])
			continue
		}
		if query[i] == '"' || query[i] == '`' {
			quote := query[i]
			start := i
			i = quotedSQLIdentifierEnd(query, i, quote)
			body := query[start+1 : i-1]
			body = strings.ReplaceAll(body, string([]byte{quote, quote}), string(quote))
			mapped := databaseIdentifier(body, true)
			mapped = strings.ReplaceAll(mapped, string(quote), string([]byte{quote, quote}))
			out.WriteByte(quote)
			out.WriteString(mapped)
			out.WriteByte(quote)
			continue
		}
		if query[i] == '[' {
			end := strings.IndexByte(query[i+1:], ']')
			if end >= 0 {
				end += i + 1
				body := query[i+1 : end]
				out.WriteByte('[')
				out.WriteString(databaseIdentifier(body, true))
				out.WriteByte(']')
				i = end + 1
				continue
			}
		}
		if isSQLIdentifierStart(query[i]) {
			start := i
			i++
			for i < len(query) && isSQLIdentifierPart(query[i]) {
				i++
			}
			out.WriteString(databaseIdentifier(query[start:i], true))
			continue
		}
		out.WriteByte(query[i])
		i++
	}
	return out.String()
}

func quotedSQLStringEnd(query string, start int, quote byte) int {
	for i := start + 1; i < len(query); i++ {
		if query[i] == quote {
			if i+1 < len(query) && query[i+1] == quote {
				i++
				continue
			}
			return i + 1
		}
	}
	return len(query)
}

func quotedSQLIdentifierEnd(query string, start int, quote byte) int {
	return quotedSQLStringEnd(query, start, quote)
}

func isSQLIdentifierStart(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func isSQLIdentifierPart(value byte) bool {
	return isSQLIdentifierStart(value) || value >= '0' && value <= '9' || value == '$'
}

func mapLibSQLArguments(query string, args []driver.NamedValue) []driver.NamedValue {
	lowerQuery := strings.ToLower(query)
	if len(args) == 0 || !strings.Contains(lowerQuery, "sqlite_master") && !strings.Contains(lowerQuery, "sqlite_schema") {
		return args
	}
	match := sqliteMasterNameParameter.FindStringIndex(query)
	if match == nil {
		return args
	}
	ordinal := strings.Count(query[:match[0]], "?") + 1
	for i := range args {
		if args[i].Ordinal == ordinal {
			if value, ok := args[i].Value.(string); ok {
				args = append([]driver.NamedValue(nil), args...)
				args[i].Value = databaseIdentifier(value, true)
			}
			break
		}
	}
	return args
}

type mappedLibSQLConnector struct{ driver.Connector }

func (c mappedLibSQLConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return mappedLibSQLConn{Conn: conn}, nil
}

type mappedLibSQLConn struct{ driver.Conn }

func (c mappedLibSQLConn) Prepare(query string) (driver.Stmt, error) {
	stmt, err := c.Conn.Prepare(mapLibSQLQuery(query))
	if err != nil {
		return nil, err
	}
	return mappedLibSQLStmt{Stmt: stmt, query: query}, nil
}

func (c mappedLibSQLConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	var stmt driver.Stmt
	var err error
	if preparer, ok := c.Conn.(driver.ConnPrepareContext); ok {
		stmt, err = preparer.PrepareContext(ctx, mapLibSQLQuery(query))
	} else {
		stmt, err = c.Prepare(query)
	}
	if err != nil {
		return nil, err
	}
	return mappedLibSQLStmt{Stmt: stmt, query: query}, nil
}

func (c mappedLibSQLConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if execer, ok := c.Conn.(driver.ExecerContext); ok {
		return execer.ExecContext(ctx, mapLibSQLQuery(query), mapLibSQLArguments(query, args))
	}
	return nil, driver.ErrSkip
}

func (c mappedLibSQLConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if queryer, ok := c.Conn.(driver.QueryerContext); ok {
		return queryer.QueryContext(ctx, mapLibSQLQuery(query), mapLibSQLArguments(query, args))
	}
	return nil, driver.ErrSkip
}

func (c mappedLibSQLConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if beginner, ok := c.Conn.(driver.ConnBeginTx); ok {
		return beginner.BeginTx(ctx, opts)
	}
	if opts.Isolation != driver.IsolationLevel(0) || opts.ReadOnly {
		return nil, errors.New("db: libSQL driver does not support requested transaction options")
	}
	//lint:ignore SA1019 ConnBeginTx is optional, so support legacy drivers that only implement Conn.Begin.
	return c.Conn.Begin()
}

func (c mappedLibSQLConn) Ping(ctx context.Context) error {
	if pinger, ok := c.Conn.(driver.Pinger); ok {
		return pinger.Ping(ctx)
	}
	return nil
}

func (c mappedLibSQLConn) ResetSession(ctx context.Context) error {
	if resetter, ok := c.Conn.(driver.SessionResetter); ok {
		return resetter.ResetSession(ctx)
	}
	return nil
}

func (c mappedLibSQLConn) IsValid() bool {
	if validator, ok := c.Conn.(driver.Validator); ok {
		return validator.IsValid()
	}
	return true
}

func (c mappedLibSQLConn) CheckNamedValue(value *driver.NamedValue) error {
	if checker, ok := c.Conn.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(value)
	}
	return driver.ErrSkip
}

type mappedLibSQLStmt struct {
	driver.Stmt
	query string
}

func (s mappedLibSQLStmt) Exec(args []driver.Value) (driver.Result, error) {
	values := make([]driver.NamedValue, len(args))
	for i, value := range args {
		values[i] = driver.NamedValue{Ordinal: i + 1, Value: value}
	}
	values = mapLibSQLArguments(s.query, values)
	for i := range values {
		args[i] = values[i].Value
	}
	//lint:ignore SA1019 StmtExecContext is optional, so preserve the required legacy driver fallback.
	return s.Stmt.Exec(args)
}

func (s mappedLibSQLStmt) Query(args []driver.Value) (driver.Rows, error) {
	values := make([]driver.NamedValue, len(args))
	for i, value := range args {
		values[i] = driver.NamedValue{Ordinal: i + 1, Value: value}
	}
	values = mapLibSQLArguments(s.query, values)
	for i := range values {
		args[i] = values[i].Value
	}
	//lint:ignore SA1019 StmtQueryContext is optional, so preserve the required legacy driver fallback.
	return s.Stmt.Query(args)
}

func (s mappedLibSQLStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if execer, ok := s.Stmt.(driver.StmtExecContext); ok {
		return execer.ExecContext(ctx, mapLibSQLArguments(s.query, args))
	}
	return nil, driver.ErrSkip
}

func (s mappedLibSQLStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if queryer, ok := s.Stmt.(driver.StmtQueryContext); ok {
		return queryer.QueryContext(ctx, mapLibSQLArguments(s.query, args))
	}
	return nil, driver.ErrSkip
}

func (s mappedLibSQLStmt) CheckNamedValue(value *driver.NamedValue) error {
	if checker, ok := s.Stmt.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(value)
	}
	return driver.ErrSkip
}

func mapLibSQLConnector(connector driver.Connector) driver.Connector {
	return mappedLibSQLConnector{Connector: connector}
}
