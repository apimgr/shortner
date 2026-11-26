package db

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/shortner/src/pgp"
	"github.com/apimgr/shortner/src/security"
)

func openTestDB(t *testing.T) *Store {
	t.Helper()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate DB test source")
	}
	projectRoot := filepath.Clean(filepath.Join(filepath.Dir(testFile), "../.."))
	idea, err := os.ReadFile(filepath.Join(projectRoot, "IDEA.md"))
	if err != nil {
		t.Fatalf("read IDEA.md for temp namespace: %v", err)
	}
	projectOrg := ""
	internalName := ""
	for _, line := range strings.Split(string(idea), "\n") {
		if strings.HasPrefix(line, "project_org:") {
			projectOrg = strings.TrimSpace(strings.TrimPrefix(line, "project_org:"))
		}
		if strings.HasPrefix(line, "internal_name:") {
			internalName = strings.TrimSpace(strings.TrimPrefix(line, "internal_name:"))
		}
	}
	if projectOrg == "" || internalName == "" {
		t.Fatal("IDEA.md does not define project_org and internal_name")
	}
	base := filepath.Join(os.TempDir(), projectOrg)
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("create temp namespace: %v", err)
	}
	tempDir, err := os.MkdirTemp(base, internalName+"-")
	if err != nil {
		t.Fatalf("create temp database directory: %v", err)
	}
	sqlDB, err := Open(filepath.Join(tempDir, "server.db"), DefaultPool(), nil)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close test Store: %v", err)
		}
		if err := os.RemoveAll(tempDir); err != nil {
			t.Errorf("remove test database directory: %v", err)
		}
	})
	return sqlDB
}

func TestOpenCreatesSchema(t *testing.T) {
	sqlDB := openTestDB(t)
	for _, table := range []string{"links", "clicks", "api_tokens", "app_secrets"} {
		var name string
		err := sqlDB.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}
}

func TestEnsureSchemaIdempotent(t *testing.T) {
	sqlDB := openTestDB(t)
	if err := EnsureSchema(context.Background(), sqlDB); err != nil {
		t.Errorf("second EnsureSchema() error = %v", err)
	}
}

func TestEnsureSchemaMigratesLegacySecurityReportBodies(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	_, err = sqlDB.Exec(`CREATE TABLE security_reports (
		tracking_id TEXT PRIMARY KEY,
		received_at INTEGER NOT NULL,
		severity TEXT NOT NULL,
		component TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'received',
		sealed TEXT NOT NULL
	)`)
	if err != nil {
		t.Fatal(err)
	}
	legacyPGP := "-----BEGIN PGP MESSAGE-----\nlegacy ciphertext\n-----END PGP MESSAGE-----"
	if _, err := sqlDB.Exec(`INSERT INTO security_reports (tracking_id, received_at, severity, component, sealed) VALUES (?, ?, ?, ?, ?), (?, ?, ?, ?, ?)`,
		"sec_aaaaaaaaaaaaaaaa", 1, "high", "api", legacyPGP,
		"sec_bbbbbbbbbbbbbbbb", 2, "low", "api", "legacy-aes-ciphertext"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(context.Background(), sqlDB); err != nil {
		t.Fatalf("EnsureSchema() error = %v", err)
	}
	for _, want := range []struct{ id, body, algorithm string }{
		{"sec_aaaaaaaaaaaaaaaa", legacyPGP, "pgp"},
		{"sec_bbbbbbbbbbbbbbbb", "legacy-aes-ciphertext", "aes-256-gcm"},
	} {
		var body []byte
		var algorithm string
		if err := sqlDB.QueryRow(`SELECT body_enc, body_alg FROM security_reports WHERE tracking_id = ?`, want.id).Scan(&body, &algorithm); err != nil {
			t.Fatal(err)
		}
		if string(body) != want.body || algorithm != want.algorithm {
			t.Errorf("migrated report %s = (%q, %q), want (%q, %q)", want.id, body, algorithm, want.body, want.algorithm)
		}
	}
	if columnExists(context.Background(), sqlDB, "security_reports", "sealed") {
		t.Fatal("legacy sealed column remains after migration")
	}
}

func TestReencryptStoredDataRekeysReportBodiesAndContacts(t *testing.T) {
	sqlDB := openTestDB(t)
	oldKey := make([]byte, 32)
	newKey := make([]byte, 32)
	newKey[0] = 1
	body, err := security.Seal(oldKey, []byte("private report"))
	if err != nil {
		t.Fatal(err)
	}
	contact, err := security.Seal(oldKey, []byte(`{"email":"researcher@example.test"}`))
	if err != nil {
		t.Fatal(err)
	}
	pgpBody := "-----BEGIN PGP MESSAGE-----\nnot rekeyed\n-----END PGP MESSAGE-----"
	for _, report := range []SecurityReport{
		{TrackingID: "sec_aaaaaaaaaaaaaaaa", ReceivedAt: time.Now().UTC(), Severity: "high", Component: "api", BodyEnc: []byte(body), BodyAlg: "aes-256-gcm", ContactEnc: []byte(contact)},
		{TrackingID: "sec_bbbbbbbbbbbbbbbb", ReceivedAt: time.Now().UTC(), Severity: "low", Component: "api", BodyEnc: []byte(pgpBody), BodyAlg: "pgp"},
	} {
		if err := InsertSecurityReport(context.Background(), sqlDB, report); err != nil {
			t.Fatal(err)
		}
	}
	if err := ReencryptStoredData(context.Background(), sqlDB, oldKey, newKey); err != nil {
		t.Fatalf("ReencryptStoredData() error = %v", err)
	}
	updated, err := GetSecurityReport(context.Background(), sqlDB, "sec_aaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := security.Open(newKey, string(updated.BodyEnc)); err != nil || string(got) != "private report" {
		t.Fatalf("rotated report body = (%q, %v)", got, err)
	}
	if got, err := security.Open(newKey, string(updated.ContactEnc)); err != nil || string(got) != `{"email":"researcher@example.test"}` {
		t.Fatalf("rotated report contact = (%q, %v)", got, err)
	}
	updatedPGP, err := GetSecurityReport(context.Background(), sqlDB, "sec_bbbbbbbbbbbbbbbb")
	if err != nil {
		t.Fatal(err)
	}
	if string(updatedPGP.BodyEnc) != pgpBody {
		t.Fatal("AES-key rotation changed a PGP-encrypted report body")
	}
}

func TestDeleteClosedSecurityReportsBeforePreservesOpenReportsAndThanks(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()
	cutoff := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	for _, report := range []SecurityReport{
		{TrackingID: "sec_aaaaaaaaaaaaaaaa", ReceivedAt: cutoff.Add(-time.Hour), ClosedAt: cutoff.Add(-time.Hour), Severity: "high", Component: "api", Sealed: "old"},
		{TrackingID: "sec_bbbbbbbbbbbbbbbb", ReceivedAt: cutoff.Add(-time.Hour), Severity: "high", Component: "api", Sealed: "open"},
	} {
		if err := InsertSecurityReport(ctx, sqlDB, report); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sqlDB.Exec(`INSERT INTO security_thanks (tracking_id, credit, severity, disclosed_on) VALUES (?, ?, ?, ?)`, "sec_aaaaaaaaaaaaaaaa", "Researcher", "high", "2026-01-01"); err != nil {
		t.Fatal(err)
	}
	deleted, err := DeleteClosedSecurityReportsBefore(ctx, sqlDB, cutoff)
	if err != nil || deleted != 1 {
		t.Fatalf("DeleteClosedSecurityReportsBefore() = (%d, %v), want (1, nil)", deleted, err)
	}
	if _, err := GetSecurityReport(ctx, sqlDB, "sec_bbbbbbbbbbbbbbbb"); err != nil {
		t.Fatalf("open report was deleted: %v", err)
	}
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM security_thanks WHERE tracking_id = ?`, "sec_aaaaaaaaaaaaaaaa").Scan(&count); err != nil || count != 1 {
		t.Fatalf("security_thanks count = (%d, %v), want (1, nil)", count, err)
	}
}

func TestRotateSecurityReportPGPReencryptsAtomically(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()
	keyTime := time.Now().UTC().Add(-time.Minute)
	oldKey, err := pgp.Generate("Shortner", "security@example.test", keyTime)
	if err != nil {
		t.Fatal(err)
	}
	newKey, err := pgp.Generate("Shortner", "security@example.test", keyTime.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	oldPublic, err := oldKey.ArmorPublic()
	if err != nil {
		t.Fatal(err)
	}
	recipients, err := pgp.ParsePublic(oldPublic)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := pgp.EncryptArmored(recipients, []byte("report payload"))
	if err != nil {
		t.Fatal(err)
	}
	report := SecurityReport{TrackingID: "sec_aaaaaaaaaaaaaaaa", ReceivedAt: time.Now().UTC(), Severity: "high", Component: "api", BodyEnc: []byte(sealed), BodyAlg: "pgp"}
	if err := InsertSecurityReport(ctx, sqlDB, report); err != nil {
		t.Fatal(err)
	}
	if err := RotateSecurityReportPGP(ctx, sqlDB, oldKey, newKey); err != nil {
		t.Fatalf("RotateSecurityReportPGP() error = %v", err)
	}
	rotated, err := GetSecurityReport(ctx, sqlDB, report.TrackingID)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := pgp.DecryptArmored(newKey, string(rotated.BodyEnc))
	if err != nil || string(plain) != "report payload" {
		t.Fatalf("new key decrypt = (%q, %v)", plain, err)
	}
	latest, err := LatestPGPKey(ctx, sqlDB)
	if err != nil {
		t.Fatalf("new active key metadata missing: %v", err)
	} else if latest.Fingerprint != newKey.Fingerprint {
		t.Fatalf("latest key fingerprint = %q, want %q", latest.Fingerprint, newKey.Fingerprint)
	}
}

func TestRotateSecurityReportPGPRollsBackOnUndecryptableReport(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()
	keyTime := time.Now().UTC().Add(-time.Minute)
	oldKey, err := pgp.Generate("Shortner", "security@example.test", keyTime)
	if err != nil {
		t.Fatal(err)
	}
	newKey, err := pgp.Generate("Shortner", "security@example.test", keyTime.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	report := SecurityReport{TrackingID: "sec_aaaaaaaaaaaaaaaa", ReceivedAt: time.Now().UTC(), Severity: "high", Component: "api", BodyEnc: []byte("not a PGP message"), BodyAlg: "pgp"}
	if err := InsertSecurityReport(ctx, sqlDB, report); err != nil {
		t.Fatal(err)
	}
	if err := RotateSecurityReportPGP(ctx, sqlDB, oldKey, newKey); err == nil {
		t.Fatal("RotateSecurityReportPGP() accepted an undecryptable report")
	}
	unchanged, err := GetSecurityReport(ctx, sqlDB, report.TrackingID)
	if err != nil {
		t.Fatal(err)
	}
	if string(unchanged.BodyEnc) != "not a PGP message" {
		t.Fatal("failed PGP rotation changed the report body")
	}
	if _, err := LatestPGPKey(ctx, sqlDB); err != ErrNotFound {
		t.Fatalf("failed rotation wrote key metadata: %v", err)
	}
}

func TestEnsureSchemaMigratesResourceTokensToOwners(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	if _, err := sqlDB.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE links (id INTEGER PRIMARY KEY AUTOINCREMENT, short_code TEXT NOT NULL UNIQUE, destination_url TEXT NOT NULL, created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')), expires_at INTEGER, click_count INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE clicks (id INTEGER PRIMARY KEY AUTOINCREMENT, link_id INTEGER NOT NULL REFERENCES links(id), timestamp INTEGER NOT NULL)`,
		`CREATE TABLE api_tokens (id INTEGER PRIMARY KEY AUTOINCREMENT, token_hash TEXT NOT NULL UNIQUE, token_prefix TEXT NOT NULL, resource_type TEXT NOT NULL, resource_id TEXT NOT NULL, created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')), expires_at INTEGER, last_used_at INTEGER, revoked_at INTEGER, revoked_reason TEXT)`,
		`INSERT INTO links (id, short_code, destination_url) VALUES (1, 'legacy', 'https://example.com')`,
		`INSERT INTO api_tokens (id, token_hash, token_prefix, resource_type, resource_id) VALUES (7, 'hash-value', 'old_prefix', 'link', '1')`,
	} {
		if _, err := sqlDB.Exec(statement); err != nil {
			t.Fatalf("legacy schema/data: %v", err)
		}
	}
	if err := EnsureSchema(context.Background(), sqlDB); err != nil {
		t.Fatalf("EnsureSchema migration: %v", err)
	}
	var linkOwner, tokenOwner, kind, tokenEnc string
	if err := sqlDB.QueryRow(`SELECT owner_id FROM links WHERE id = 1`).Scan(&linkOwner); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.QueryRow(`SELECT owner_id, kind, token_enc FROM api_tokens WHERE id = 7`).Scan(&tokenOwner, &kind, &tokenEnc); err != nil {
		t.Fatal(err)
	}
	if linkOwner == "" || tokenOwner != linkOwner || kind != "primary" || tokenEnc != "" {
		t.Fatalf("migrated link/token state = owner %q / %q, kind %q, ciphertext %q", linkOwner, tokenOwner, kind, tokenEnc)
	}
	if err := EnsureSchema(context.Background(), sqlDB); err != nil {
		t.Fatalf("second EnsureSchema: %v", err)
	}
}

func TestWithTransactionCommitsAndRollsBack(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()

	link, err := CreateLinkAutoCode(ctx, sqlDB, "https://example.com", nil)
	if err != nil {
		t.Fatalf("CreateLinkAutoCode() error = %v", err)
	}

	err = WithTransaction(ctx, sqlDB, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE links SET destination_url = ? WHERE id = ?`, "https://updated.example.com", link.ID)
		return err
	})
	if err != nil {
		t.Fatalf("WithTransaction(commit) error = %v", err)
	}
	got, err := GetLinkByID(ctx, sqlDB, link.ID)
	if err != nil {
		t.Fatalf("GetLinkByID() error = %v", err)
	}
	if got.DestinationURL != "https://updated.example.com" {
		t.Errorf("destination_url = %q, want updated value", got.DestinationURL)
	}

	sentinel := sql.ErrNoRows
	err = WithTransaction(ctx, sqlDB, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE links SET destination_url = ? WHERE id = ?`, "https://should-not-stick.example.com", link.ID); err != nil {
			return err
		}
		return sentinel
	})
	if err != sentinel {
		t.Errorf("WithTransaction(rollback) error = %v, want sentinel", err)
	}
	got, err = GetLinkByID(ctx, sqlDB, link.ID)
	if err != nil {
		t.Fatalf("GetLinkByID() error = %v", err)
	}
	if got.DestinationURL != "https://updated.example.com" {
		t.Errorf("destination_url = %q, want rollback to have preserved prior value", got.DestinationURL)
	}
}

func TestHandleQueryError(t *testing.T) {
	if err := HandleQueryError(nil); err != nil {
		t.Errorf("HandleQueryError(nil) = %v, want nil", err)
	}
	if err := HandleQueryError(sql.ErrNoRows); err != ErrNotFound {
		t.Errorf("HandleQueryError(ErrNoRows) = %v, want ErrNotFound", err)
	}
	if err := HandleQueryError(context.DeadlineExceeded); err == nil {
		t.Errorf("HandleQueryError(DeadlineExceeded) = nil, want error")
	}
}

func TestWithSerializableRetrySucceeds(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()

	link, err := CreateLinkAutoCode(ctx, sqlDB, "https://example.com", nil)
	if err != nil {
		t.Fatalf("CreateLinkAutoCode() error = %v", err)
	}

	err = WithSerializableRetry(ctx, sqlDB, 3, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE links SET click_count = click_count + 1 WHERE id = ?`, link.ID)
		return err
	})
	if err != nil {
		t.Fatalf("WithSerializableRetry() error = %v", err)
	}
	got, err := GetLinkByID(ctx, sqlDB, link.ID)
	if err != nil {
		t.Fatalf("GetLinkByID() error = %v", err)
	}
	if got.ClickCount != 1 {
		t.Errorf("ClickCount = %d, want 1", got.ClickCount)
	}
}

func TestLinkCreateGetUpdateDelete(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()

	link, err := CreateLinkAutoCode(ctx, sqlDB, "https://example.com", nil)
	if err != nil {
		t.Fatalf("CreateLinkAutoCode() error = %v", err)
	}
	if len(link.ShortCode) != 6 {
		t.Errorf("len(ShortCode) = %d, want 6", len(link.ShortCode))
	}
	if link.IsExpired() {
		t.Errorf("new link with no expiry reports IsExpired() = true")
	}

	byCode, err := GetLinkByShortCode(ctx, sqlDB, link.ShortCode)
	if err != nil {
		t.Fatalf("GetLinkByShortCode() error = %v", err)
	}
	if byCode.ID != link.ID {
		t.Errorf("GetLinkByShortCode ID = %d, want %d", byCode.ID, link.ID)
	}

	if err := UpdateLinkDestination(ctx, sqlDB, link.ID, "https://new.example.com", nil, false); err != nil {
		t.Fatalf("UpdateLinkDestination() error = %v", err)
	}
	updated, err := GetLinkByID(ctx, sqlDB, link.ID)
	if err != nil {
		t.Fatalf("GetLinkByID() error = %v", err)
	}
	if updated.DestinationURL != "https://new.example.com" {
		t.Errorf("DestinationURL = %q, want updated", updated.DestinationURL)
	}

	past := time.Now().Add(-time.Hour)
	custom, err := CreateLinkCustomSlug(ctx, sqlDB, "my-custom-slug", "https://slug.example.com", &past)
	if err != nil {
		t.Fatalf("CreateLinkCustomSlug() error = %v", err)
	}
	if !custom.IsExpired() {
		t.Errorf("link with past expiry reports IsExpired() = false")
	}

	if _, err := CreateLinkCustomSlug(ctx, sqlDB, "my-custom-slug", "https://dup.example.com", nil); err == nil {
		t.Errorf("CreateLinkCustomSlug(duplicate) error = nil, want ErrSlugTaken")
	}

	if err := DeleteLink(ctx, sqlDB, link.ID); err != nil {
		t.Fatalf("DeleteLink() error = %v", err)
	}
	if _, err := GetLinkByID(ctx, sqlDB, link.ID); err != ErrNotFound {
		t.Errorf("GetLinkByID(deleted) error = %v, want ErrNotFound", err)
	}
	if err := DeleteLink(ctx, sqlDB, link.ID); err != ErrNotFound {
		t.Errorf("DeleteLink(already deleted) error = %v, want ErrNotFound", err)
	}
}

// TestDeleteLinkWithClicksAndToken covers the case that used to fail with
// a FOREIGN KEY constraint error: clicks.link_id references links(id) and
// the connection runs with PRAGMA foreign_keys(1), so a link that has ever
// been clicked could not be deleted at all. Owner identity survives a
// resource deletion because it may own other resources.
func TestDeleteLinkWithClicksAndToken(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()

	link, err := CreateLinkAutoCode(ctx, sqlDB, "https://example.com", nil)
	if err != nil {
		t.Fatalf("CreateLinkAutoCode() error = %v", err)
	}
	if _, err := RecordClick(ctx, sqlDB, link.ID, "203.0.113.42", "agent/1.0", "", "", ""); err != nil {
		t.Fatalf("RecordClick() error = %v", err)
	}
	ownerID, err := CreateOwner(ctx, sqlDB)
	if err != nil {
		t.Fatalf("CreateOwner() error = %v", err)
	}
	key := make([]byte, 32)
	raw, _, err := CreatePrimaryToken(ctx, sqlDB, ownerID, key)
	if err != nil {
		t.Fatalf("CreatePrimaryToken() error = %v", err)
	}

	if err := DeleteLink(ctx, sqlDB, link.ID); err != nil {
		t.Fatalf("DeleteLink(link with clicks) error = %v, want nil", err)
	}

	var clicks int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM clicks WHERE link_id = ?`, link.ID).Scan(&clicks); err != nil {
		t.Fatalf("count clicks: %v", err)
	}
	if clicks != 0 {
		t.Errorf("clicks remaining = %d, want 0", clicks)
	}

	if _, err := LookupTokenByRaw(ctx, sqlDB, raw); err != nil {
		t.Errorf("LookupTokenByRaw(owner token after resource deletion) error = %v, want nil", err)
	}
}

func TestIncrementClickCount(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()

	link, err := CreateLinkAutoCode(ctx, sqlDB, "https://example.com", nil)
	if err != nil {
		t.Fatalf("CreateLinkAutoCode() error = %v", err)
	}
	if err := IncrementClickCount(ctx, sqlDB, link.ID); err != nil {
		t.Fatalf("IncrementClickCount() error = %v", err)
	}
	got, err := GetLinkByID(ctx, sqlDB, link.ID)
	if err != nil {
		t.Fatalf("GetLinkByID() error = %v", err)
	}
	if got.ClickCount != 1 {
		t.Errorf("ClickCount = %d, want 1", got.ClickCount)
	}
}

func TestListLinks(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()

	var created []*Link
	for i := 0; i < 5; i++ {
		link, err := CreateLinkAutoCode(ctx, sqlDB, "https://example.com/"+strconv.Itoa(i), nil)
		if err != nil {
			t.Fatalf("CreateLinkAutoCode() error = %v", err)
		}
		created = append(created, link)
	}

	links, total, err := ListLinks(ctx, sqlDB, 2, 0)
	if err != nil {
		t.Fatalf("ListLinks(limit=2, offset=0) error = %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if len(links) != 2 {
		t.Fatalf("len(links) = %d, want 2", len(links))
	}
	// Newest-first: the most recently created link (highest ID) comes back
	// first.
	if links[0].ID != created[4].ID {
		t.Errorf("links[0].ID = %d, want %d (newest first)", links[0].ID, created[4].ID)
	}
	if links[1].ID != created[3].ID {
		t.Errorf("links[1].ID = %d, want %d", links[1].ID, created[3].ID)
	}

	page2, total2, err := ListLinks(ctx, sqlDB, 2, 2)
	if err != nil {
		t.Fatalf("ListLinks(limit=2, offset=2) error = %v", err)
	}
	if total2 != 5 {
		t.Errorf("total (page 2) = %d, want 5", total2)
	}
	if len(page2) != 2 || page2[0].ID != created[2].ID {
		t.Fatalf("page 2 = %+v, want IDs [%d, %d]", page2, created[2].ID, created[1].ID)
	}

	empty, _, err := ListLinks(ctx, sqlDB, 10, 100)
	if err != nil {
		t.Fatalf("ListLinks(offset beyond total) error = %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("len(empty) = %d, want 0", len(empty))
	}
}

func TestRecordClickAndList(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()

	link, err := CreateLinkAutoCode(ctx, sqlDB, "https://example.com", nil)
	if err != nil {
		t.Fatalf("CreateLinkAutoCode() error = %v", err)
	}

	click, err := RecordClick(ctx, sqlDB, link.ID, "203.0.113.42", "test-agent/1.0", "https://referrer.example.com", "US", "California")
	if err != nil {
		t.Fatalf("RecordClick() error = %v", err)
	}
	if click.IP != "203.0.113.0" {
		t.Errorf("Click.IP = %q, want anonymized 203.0.113.0", click.IP)
	}
	if click.UserAgent != "test-agent/1.0" {
		t.Errorf("Click.UserAgent = %q, want test-agent/1.0", click.UserAgent)
	}
	if click.Country != "US" {
		t.Errorf("Click.Country = %q, want US", click.Country)
	}
	if click.Region != "California" {
		t.Errorf("Click.Region = %q, want California", click.Region)
	}

	updated, err := GetLinkByID(ctx, sqlDB, link.ID)
	if err != nil {
		t.Fatalf("GetLinkByID() error = %v", err)
	}
	if updated.ClickCount != 1 {
		t.Errorf("ClickCount = %d, want 1", updated.ClickCount)
	}

	clicks, err := ClicksForLink(ctx, sqlDB, link.ID, 10)
	if err != nil {
		t.Fatalf("ClicksForLink() error = %v", err)
	}
	if len(clicks) != 1 {
		t.Fatalf("len(clicks) = %d, want 1", len(clicks))
	}
	if clicks[0].ID != click.ID {
		t.Errorf("clicks[0].ID = %d, want %d", clicks[0].ID, click.ID)
	}
}

func TestOwnerTokenLifecycle(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()

	ownerID, err := CreateOwner(ctx, sqlDB)
	if err != nil {
		t.Fatalf("CreateOwner() error = %v", err)
	}
	key := make([]byte, 32)
	raw, tok, err := CreatePrimaryToken(ctx, sqlDB, ownerID, key)
	if err != nil {
		t.Fatalf("CreatePrimaryToken() error = %v", err)
	}
	if raw == "" || tok.ID == 0 {
		t.Fatalf("CreatePrimaryToken() returned empty raw or token")
	}
	if tok.TokenLast4 != raw[len(raw)-4:] || !strings.HasSuffix(MaskAPIToken(tok), tok.TokenLast4) {
		t.Fatalf("created token last4/mask = %q / %q, raw suffix %q", tok.TokenLast4, MaskAPIToken(tok), raw[len(raw)-4:])
	}
	if _, err := sqlDB.ExecContext(ctx, `UPDATE api_tokens SET token_last4='' WHERE id=?`, tok.ID); err != nil {
		t.Fatal(err)
	}
	skipped, err := BackfillTokenLast4(ctx, sqlDB, key)
	if err != nil || skipped != 0 {
		t.Fatalf("BackfillTokenLast4() = skipped %d, err %v", skipped, err)
	}
	backfilled, err := GetTokenByID(ctx, sqlDB, tok.ID)
	if err != nil || backfilled.TokenLast4 != raw[len(raw)-4:] {
		t.Fatalf("backfilled last4 = %q, err %v", backfilled.TokenLast4, err)
	}
	if !tok.IsActive() {
		t.Errorf("freshly created token IsActive() = false")
	}

	found, err := LookupTokenByRaw(ctx, sqlDB, raw)
	if err != nil {
		t.Fatalf("LookupTokenByRaw() error = %v", err)
	}
	if found.ID != tok.ID {
		t.Errorf("LookupTokenByRaw ID = %d, want %d", found.ID, tok.ID)
	}

	if _, err := LookupTokenByRaw(ctx, sqlDB, "old_"+strings.Repeat("x", 32)); err != ErrNotFound {
		t.Errorf("LookupTokenByRaw(wrong) error = %v, want ErrNotFound", err)
	}

	if err := TouchTokenLastUsed(ctx, sqlDB, tok.ID, ownerID); err != nil {
		t.Fatalf("TouchTokenLastUsed() error = %v", err)
	}
	touched, err := LookupTokenByRaw(ctx, sqlDB, raw)
	if err != nil {
		t.Fatalf("LookupTokenByRaw() error = %v", err)
	}
	if touched.LastUsedAt == nil {
		t.Errorf("LastUsedAt = nil, want set after TouchTokenLastUsed")
	}

	active, err := ListActiveTokens(ctx, sqlDB)
	if err != nil {
		t.Fatalf("ListActiveTokens() error = %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("len(active) = %d, want 1", len(active))
	}

	if err := RevokeToken(ctx, sqlDB, tok.TokenPrefix, "test revoke"); err != nil {
		t.Fatalf("RevokeToken() error = %v", err)
	}
	if _, err := LookupTokenByRaw(ctx, sqlDB, raw); err != ErrNotFound {
		t.Errorf("LookupTokenByRaw(revoked) error = %v, want ErrNotFound", err)
	}
	if err := RevokeToken(ctx, sqlDB, tok.TokenPrefix, "again"); err != ErrNotFound {
		t.Errorf("RevokeToken(already revoked) error = %v, want ErrNotFound", err)
	}

	active, err = ListActiveTokens(ctx, sqlDB)
	if err != nil {
		t.Fatalf("ListActiveTokens() error = %v", err)
	}
	if len(active) != 0 {
		t.Errorf("len(active) after revoke = %d, want 0", len(active))
	}
}

func TestLegacyHashOnlyTokenPreservesAuthenticationAndRecoversOnUse(t *testing.T) {
	ctx := context.Background()
	sqlDB := openTestDB(t)
	ownerID, err := CreateOwner(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	raw, created, err := CreatePrimaryToken(ctx, sqlDB, ownerID, key)
	if err != nil {
		t.Fatal(err)
	}
	originalHash, originalPrefix := created.TokenHash, created.TokenPrefix
	if _, err := sqlDB.ExecContext(ctx, `UPDATE api_tokens SET token_enc='',token_last4='' WHERE id=?`, created.ID); err != nil {
		t.Fatal(err)
	}
	skipped, err := BackfillTokenLast4(ctx, sqlDB, key)
	if err != nil || skipped != 1 {
		t.Fatalf("BackfillTokenLast4() = skipped %d, err %v; want one unrecoverable row", skipped, err)
	}
	dormant, err := GetTokenByID(ctx, sqlDB, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if dormant.TokenLast4 != "" || dormant.TokenEnc != "" {
		t.Fatalf("backfill fabricated legacy metadata: last4=%q enc=%q", dormant.TokenLast4, dormant.TokenEnc)
	}
	if _, err := RevealToken(dormant, key); err == nil {
		t.Fatal("hash-only legacy row was revealable before raw-token recovery")
	}

	withoutKey, err := LookupTokenByRaw(ctx, sqlDB, raw)
	if err != nil {
		t.Fatalf("hash-only credential stopped authenticating: %v", err)
	}
	if withoutKey.OwnerID != ownerID || withoutKey.TokenHash != originalHash || withoutKey.TokenPrefix != originalPrefix {
		t.Fatalf("legacy token identity changed during authentication: %+v", withoutKey)
	}
	if withoutKey.TokenLast4 != raw[len(raw)-4:] || withoutKey.TokenEnc != "" {
		t.Fatalf("no-key lazy metadata = last4 %q, enc %q", withoutKey.TokenLast4, withoutKey.TokenEnc)
	}
	if _, err := RevealToken(withoutKey, key); err == nil {
		t.Fatal("legacy row without ciphertext revealed a value")
	}

	withKey, err := LookupTokenByRaw(ctx, sqlDB, raw, key)
	if err != nil {
		t.Fatalf("token did not authenticate during encrypted recovery: %v", err)
	}
	if withKey.OwnerID != ownerID || withKey.TokenHash != originalHash || withKey.TokenPrefix != originalPrefix || withKey.TokenLast4 != raw[len(raw)-4:] || withKey.TokenEnc == "" {
		t.Fatalf("recovered token metadata = %+v", withKey)
	}
	revealed, err := RevealToken(withKey, key)
	if err != nil || revealed != raw {
		t.Fatalf("recovered token reveal = %q, err %v", revealed, err)
	}
}

func TestTokenMetadataBackfillRejectsCiphertextThatDoesNotMatchHash(t *testing.T) {
	ctx := context.Background()
	sqlDB := openTestDB(t)
	key := make([]byte, 32)
	ownerA, err := CreateOwner(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	rawA, tokenA, err := CreatePrimaryToken(ctx, sqlDB, ownerA, key)
	if err != nil {
		t.Fatal(err)
	}
	ownerB, err := CreateOwner(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	rawB, tokenB, err := CreatePrimaryToken(ctx, sqlDB, ownerB, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(ctx, `UPDATE api_tokens SET token_enc=?,token_last4='' WHERE id=?`, tokenB.TokenEnc, tokenA.ID); err != nil {
		t.Fatal(err)
	}

	skipped, err := BackfillTokenLast4(ctx, sqlDB, key)
	if err != nil || skipped != 1 {
		t.Fatalf("BackfillTokenLast4() = skipped %d, err %v; want one unrecoverable mismatch", skipped, err)
	}
	stale, err := GetTokenByID(ctx, sqlDB, tokenA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stale.TokenLast4 != "" {
		t.Fatalf("mismatched ciphertext produced last4 %q", stale.TokenLast4)
	}
	if _, err := RevealToken(stale, key); err == nil {
		t.Fatal("mismatched ciphertext was revealed")
	}

	recovered, err := LookupTokenByRaw(ctx, sqlDB, rawA, key)
	if err != nil {
		t.Fatalf("owner A token stopped authenticating: %v", err)
	}
	if recovered.OwnerID != ownerA || recovered.TokenHash != tokenA.TokenHash || recovered.TokenPrefix != tokenA.TokenPrefix || recovered.TokenLast4 != rawA[len(rawA)-4:] {
		t.Fatalf("owner A recovery changed credential identity or failed suffix recovery: %+v", recovered)
	}
	revealed, err := RevealToken(recovered, key)
	if err != nil || revealed != rawA {
		t.Fatalf("owner A recovered reveal = %q, err %v", revealed, err)
	}
	if _, err := LookupTokenByRaw(ctx, sqlDB, rawB, key); err != nil {
		t.Fatalf("owner B was blocked by owner A metadata: %v", err)
	}
}

func TestSecrets(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()

	if _, err := GetSecret(ctx, sqlDB, "nope"); err != ErrNotFound {
		t.Errorf("GetSecret(missing) error = %v, want ErrNotFound", err)
	}

	if err := SetSecret(ctx, sqlDB, "custom", "value1"); err != nil {
		t.Fatalf("SetSecret() error = %v", err)
	}
	got, err := GetSecret(ctx, sqlDB, "custom")
	if err != nil {
		t.Fatalf("GetSecret() error = %v", err)
	}
	if got != "value1" {
		t.Errorf("GetSecret = %q, want value1", got)
	}

	if err := SetSecret(ctx, sqlDB, "custom", "value2"); err != nil {
		t.Fatalf("SetSecret(update) error = %v", err)
	}
	got, err = GetSecret(ctx, sqlDB, "custom")
	if err != nil {
		t.Fatalf("GetSecret() error = %v", err)
	}
	if got != "value2" {
		t.Errorf("GetSecret after update = %q, want value2", got)
	}
}

func TestEnsureSecretGeneratesOnce(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()

	first, err := EnsureSecret(ctx, sqlDB, SecretInstallation)
	if err != nil {
		t.Fatalf("EnsureSecret() error = %v", err)
	}
	if first == "" {
		t.Fatalf("EnsureSecret() returned empty value")
	}

	second, err := EnsureSecret(ctx, sqlDB, SecretInstallation)
	if err != nil {
		t.Fatalf("EnsureSecret() second call error = %v", err)
	}
	if first != second {
		t.Errorf("EnsureSecret() not stable across calls: %q != %q", first, second)
	}
}

func TestEnsureCoreSecrets(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()

	installation, cookieSigning, csrfToken, err := EnsureCoreSecrets(ctx, sqlDB)
	if err != nil {
		t.Fatalf("EnsureCoreSecrets() error = %v", err)
	}
	if installation == "" || cookieSigning == "" || csrfToken == "" {
		t.Errorf("EnsureCoreSecrets() returned an empty secret")
	}
	if installation == cookieSigning || cookieSigning == csrfToken {
		t.Errorf("EnsureCoreSecrets() returned duplicate values across distinct secrets")
	}
}

func TestTryTouchSecurityReportViewConsumesIntervalAtomically(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()
	report := SecurityReport{
		TrackingID: "sec_0123456789abcdef",
		ReceivedAt: time.Now().UTC(),
		Severity:   "high",
		Component:  "api",
		Sealed:     "encrypted",
	}
	if err := InsertSecurityReport(ctx, sqlDB, report); err != nil {
		t.Fatalf("InsertSecurityReport() error = %v", err)
	}
	now := time.Now().UTC()
	allowed, err := TryTouchSecurityReportView(ctx, sqlDB, report.TrackingID, now, 24*time.Hour)
	if err != nil || !allowed {
		t.Fatalf("first TryTouchSecurityReportView() = (%t, %v), want (true, nil)", allowed, err)
	}
	allowed, err = TryTouchSecurityReportView(ctx, sqlDB, report.TrackingID, now.Add(time.Minute), 24*time.Hour)
	if err != nil || allowed {
		t.Fatalf("second TryTouchSecurityReportView() = (%t, %v), want (false, nil)", allowed, err)
	}
	allowed, err = TryTouchSecurityReportView(ctx, sqlDB, report.TrackingID, now.Add(25*time.Hour), 24*time.Hour)
	if err != nil || !allowed {
		t.Fatalf("TryTouchSecurityReportView() after interval = (%t, %v), want (true, nil)", allowed, err)
	}
}

func TestSecurityReportOperatorWorkflow(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	report := SecurityReport{TrackingID: "sec_0123456789abcdef", ReceivedAt: now.Add(-time.Hour), Severity: "high", Component: "api", Sealed: "ciphertext"}
	if err := InsertSecurityReport(ctx, sqlDB, report); err != nil {
		t.Fatal(err)
	}
	listed, err := ListSecurityReports(ctx, sqlDB, StatusReceived)
	if err != nil || len(listed) != 1 || listed[0].TrackingID != report.TrackingID {
		t.Fatalf("ListSecurityReports() = (%v, %v)", listed, err)
	}
	next := StatusTriaged
	comment := "We are reviewing this report."
	disclosure := now.AddDate(0, 1, 0)
	updated, old, err := UpdateSecurityReport(ctx, sqlDB, report.TrackingID, SecurityReportUpdate{Status: &next, Comments: &comment, DisclosureAt: &disclosure}, now)
	if err != nil || old != StatusReceived || updated.Status != StatusTriaged || updated.Comments != comment || !updated.UpdatedAt.Equal(now) {
		t.Fatalf("UpdateSecurityReport() = (%+v, %q, %v)", updated, old, err)
	}
	back := StatusReceived
	if _, _, err := UpdateSecurityReport(ctx, sqlDB, report.TrackingID, SecurityReportUpdate{Status: &back}, now.Add(time.Minute)); err == nil {
		t.Fatal("backward status transition succeeded")
	}
	if updated.Sealed != "ciphertext" {
		t.Fatal("operator update altered encrypted content")
	}
}
