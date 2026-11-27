package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"reflect"
	"sync"
	"testing"
)

func TestMigrateLibSQLIdentifiersPreservesLegacyRowsAndIndexes(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, query := range []string{
		`CREATE TABLE owners (id TEXT PRIMARY KEY)`,
		`CREATE TABLE links (id TEXT PRIMARY KEY, owner_id TEXT REFERENCES owners(id), short_code TEXT)`,
		`CREATE INDEX idx_links_short_code ON links(short_code)`,
		`INSERT INTO owners(id) VALUES ('owner-1')`,
		`INSERT INTO links(id, owner_id, short_code) VALUES ('link-1', 'owner-1', 'abc123')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}

	if err := migrateLibSQLIdentifiers(context.Background(), db); err != nil {
		t.Fatalf("migration: %v", err)
	}
	var ownerID, shortCode string
	if err := db.QueryRow(`SELECT owner_id, short_code FROM srv_links WHERE id='link-1'`).Scan(&ownerID, &shortCode); err != nil {
		t.Fatalf("legacy row was not preserved: %v", err)
	}
	if ownerID != "owner-1" || shortCode != "abc123" {
		t.Fatalf("migrated row = (%q, %q), want (owner-1, abc123)", ownerID, shortCode)
	}
	var indexTable string
	if err := db.QueryRow(`SELECT tbl_name FROM sqlite_master WHERE type='index' AND name='srv_idx_links_short_code'`).Scan(&indexTable); err != nil {
		t.Fatalf("prefixed index missing: %v", err)
	}
	if indexTable != "srv_links" {
		t.Fatalf("index table = %q, want srv_links", indexTable)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('links', 'owners', 'idx_links_short_code')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("legacy objects remain after migration: %d", count)
	}
	if err := migrateLibSQLIdentifiers(context.Background(), db); err != nil {
		t.Fatalf("second migration should be idempotent: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM srv_links`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("idempotent migration lost rows: count=%d err=%v", count, err)
	}
}

func TestMigrateLibSQLIdentifiersFailsClosedOnConflict(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, query := range []string{
		`CREATE TABLE links (id TEXT PRIMARY KEY)`,
		`CREATE TABLE srv_links (id TEXT PRIMARY KEY)`,
		`INSERT INTO links(id) VALUES ('legacy')`,
		`INSERT INTO srv_links(id) VALUES ('current')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}
	if err := migrateLibSQLIdentifiers(context.Background(), db); err == nil {
		t.Fatal("migration succeeded with conflicting legacy and prefixed tables")
	}
	for table, want := range map[string]string{"links": "legacy", "srv_links": "current"} {
		var got string
		query := `SELECT id FROM ` + quoteSQLIdentifier(table)
		if err := db.QueryRow(query).Scan(&got); err != nil {
			t.Fatalf("table %s changed after failed migration: %v", table, err)
		}
		if got != want {
			t.Errorf("table %s row = %q, want %q", table, got, want)
		}
	}
}

func TestDatabaseIdentifier(t *testing.T) {
	tests := []struct {
		name   string
		remote bool
		want   string
	}{
		{name: "links", remote: true, want: "srv_links"},
		{name: "idx_links_short_code", remote: true, want: "srv_idx_links_short_code"},
		{name: "Links", remote: true, want: "srv_links"},
		{name: "links", remote: false, want: "links"},
		{name: "custom_table", remote: true, want: "custom_table"},
		{name: "token_prefix", remote: true, want: "token_prefix"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := databaseIdentifier(tt.name, tt.remote); got != tt.want {
				t.Fatalf("databaseIdentifier(%q, %t) = %q, want %q", tt.name, tt.remote, got, tt.want)
			}
		})
	}
}

func TestMapLibSQLQuery(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "schema creates and references mapped tables and indexes",
			query: `CREATE TABLE IF NOT EXISTS links (owner_id TEXT REFERENCES owners(id)); CREATE INDEX idx_links_short_code ON links(owner_id)`,
			want:  `CREATE TABLE IF NOT EXISTS srv_links (owner_id TEXT REFERENCES srv_owners(id)); CREATE INDEX srv_idx_links_short_code ON srv_links(owner_id)`,
		},
		{
			name:  "reads and migrations map table identifiers",
			query: `SELECT l.id FROM links AS l JOIN clicks c ON c.link_id=l.id; ALTER TABLE api_tokens ADD COLUMN test TEXT`,
			want:  `SELECT l.id FROM srv_links AS l JOIN srv_clicks c ON c.link_id=l.id; ALTER TABLE srv_api_tokens ADD COLUMN test TEXT`,
		},
		{
			name:  "quoted identifiers map while strings and comments stay intact",
			query: "SELECT \"links\".id FROM [links] JOIN `clicks` ON 1=1 WHERE note='links' -- links\n/* audit_log */",
			want:  "SELECT \"srv_links\".id FROM [srv_links] JOIN `srv_clicks` ON 1=1 WHERE note='links' -- links\n/* audit_log */",
		},
		{
			name:  "table metadata pragma and migration table",
			query: `PRAGMA table_info(api_tokens_legacy); DROP TABLE app_secret_grace`,
			want:  `PRAGMA table_info(srv_api_tokens_legacy); DROP TABLE srv_app_secret_grace`,
		},
		{
			name:  "unknown identifiers are not prefixed",
			query: `CREATE TABLE customer_data (links_count INTEGER); SELECT * FROM customer_data`,
			want:  `CREATE TABLE customer_data (links_count INTEGER); SELECT * FROM customer_data`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mapLibSQLQuery(tt.query); got != tt.want {
				t.Fatalf("mapLibSQLQuery() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMapLibSQLArgumentsMapsSQLiteCatalogLookup(t *testing.T) {
	query := `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=? AND version=?`
	args := []driver.NamedValue{{Ordinal: 1, Value: "api_tokens"}, {Ordinal: 2, Value: int64(3)}}
	got := mapLibSQLArguments(query, args)
	if got[0].Value != "srv_api_tokens" || got[1].Value != int64(3) {
		t.Fatalf("mapped arguments = %#v", got)
	}
	if args[0].Value != "api_tokens" {
		t.Fatalf("input arguments were mutated: %#v", args)
	}
	indexArgs := []driver.NamedValue{{Ordinal: 1, Value: "idx_links_short_code"}}
	if got := mapLibSQLArguments(`SELECT name FROM sqlite_schema WHERE name=?`, indexArgs); got[0].Value != "srv_idx_links_short_code" {
		t.Fatalf("mapped index argument = %#v", got[0].Value)
	}
}

func TestMappedLibSQLConnectorRoutesAllSQLSurfaces(t *testing.T) {
	recorder := &mappingRecorder{}
	db := sql.OpenDB(mapLibSQLConnector(mappingTestConnector{recorder: recorder}))
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.ExecContext(context.Background(), `INSERT INTO links(short_code) VALUES(?)`, "x"); err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(context.Background(), `SELECT * FROM links`)
	if err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	stmt, err := db.PrepareContext(context.Background(), `INSERT INTO links(short_code) VALUES(?)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stmt.ExecContext(context.Background(), "y"); err != nil {
		t.Fatal(err)
	}
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(context.Background(), `UPDATE links SET short_code=?`, "z"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if rows, err := db.QueryContext(context.Background(), `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, "links"); err != nil {
		t.Fatal(err)
	} else {
		_ = rows.Close()
	}

	wantQueries := []string{
		`INSERT INTO srv_links(short_code) VALUES(?)`,
		`SELECT * FROM srv_links`,
		`INSERT INTO srv_links(short_code) VALUES(?)`,
		`UPDATE srv_links SET short_code=?`,
		`SELECT name FROM sqlite_master WHERE type='table' AND name=?`,
	}
	if got := recorder.queriesSnapshot(); !reflect.DeepEqual(got, wantQueries) {
		t.Fatalf("driver queries = %#v, want %#v", got, wantQueries)
	}
	args := recorder.argsSnapshot()
	if args[4][0].Value != "srv_links" {
		t.Fatalf("catalog arguments = %#v", args[4])
	}
}

type mappingRecorder struct {
	mu      sync.Mutex
	queries []string
	args    [][]driver.NamedValue
}

func (r *mappingRecorder) record(query string, args []driver.NamedValue) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queries = append(r.queries, query)
	r.args = append(r.args, append([]driver.NamedValue(nil), args...))
}

func (r *mappingRecorder) queriesSnapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.queries...)
}

func (r *mappingRecorder) argsSnapshot() [][]driver.NamedValue {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]driver.NamedValue, len(r.args))
	for i := range r.args {
		out[i] = append([]driver.NamedValue(nil), r.args[i]...)
	}
	return out
}

type mappingTestConnector struct{ recorder *mappingRecorder }

func (c mappingTestConnector) Connect(context.Context) (driver.Conn, error) {
	return mappingTestConn(c), nil
}

func (mappingTestConnector) Driver() driver.Driver { return mappingTestDriver{} }

type mappingTestDriver struct{}

func (mappingTestDriver) Open(string) (driver.Conn, error) { return nil, driver.ErrSkip }

type mappingTestConn struct{ recorder *mappingRecorder }

func (c mappingTestConn) Prepare(query string) (driver.Stmt, error) {
	return mappingTestStmt{recorder: c.recorder, query: query}, nil
}

func (mappingTestConn) Close() error { return nil }

func (c mappingTestConn) Begin() (driver.Tx, error) { return mappingTestTx{}, nil }

func (c mappingTestConn) PrepareContext(_ context.Context, query string) (driver.Stmt, error) {
	return c.Prepare(query)
}

func (c mappingTestConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return mappingTestTx{}, nil
}

func (c mappingTestConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.recorder.record(query, args)
	return driver.RowsAffected(1), nil
}

func (c mappingTestConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.recorder.record(query, args)
	return mappingTestRows{}, nil
}

type mappingTestStmt struct {
	recorder *mappingRecorder
	query    string
}

func (s mappingTestStmt) Close() error { return nil }
func (mappingTestStmt) NumInput() int  { return -1 }
func (s mappingTestStmt) Exec([]driver.Value) (driver.Result, error) {
	return driver.RowsAffected(1), nil
}
func (mappingTestStmt) Query([]driver.Value) (driver.Rows, error) {
	return mappingTestRows{}, nil
}
func (s mappingTestStmt) ExecContext(_ context.Context, args []driver.NamedValue) (driver.Result, error) {
	s.recorder.record(s.query, args)
	return driver.RowsAffected(1), nil
}
func (s mappingTestStmt) QueryContext(_ context.Context, args []driver.NamedValue) (driver.Rows, error) {
	s.recorder.record(s.query, args)
	return mappingTestRows{}, nil
}

type mappingTestTx struct{}

func (mappingTestTx) Commit() error   { return nil }
func (mappingTestTx) Rollback() error { return nil }

type mappingTestRows struct{}

func (mappingTestRows) Columns() []string { return []string{"value"} }
func (mappingTestRows) Close() error      { return nil }
func (mappingTestRows) Next([]driver.Value) error {
	return io.EOF
}

func TestSQLiteIdentifierRemainsUnchanged(t *testing.T) {
	if got := databaseIdentifier("links", false); got != "links" {
		t.Fatalf("SQLite identifier = %q, want links", got)
	}
}
