package db

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := Open(":memory:", DefaultPool())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	// :memory: is per-connection — force a single connection so the pool
	// never hands out a second, empty, in-memory database mid-test.
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
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

func TestRecordClickAndList(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()

	link, err := CreateLinkAutoCode(ctx, sqlDB, "https://example.com", nil)
	if err != nil {
		t.Fatalf("CreateLinkAutoCode() error = %v", err)
	}

	click, err := RecordClick(ctx, sqlDB, link.ID, "203.0.113.42", "test-agent/1.0", "https://referrer.example.com")
	if err != nil {
		t.Fatalf("RecordClick() error = %v", err)
	}
	if click.IP != "203.0.113.0" {
		t.Errorf("Click.IP = %q, want anonymized 203.0.113.0", click.IP)
	}
	if click.UserAgent != "test-agent/1.0" {
		t.Errorf("Click.UserAgent = %q, want test-agent/1.0", click.UserAgent)
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

func TestResourceTokenLifecycle(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()

	raw, tok, err := CreateResourceToken(ctx, sqlDB, "link", "42", nil)
	if err != nil {
		t.Fatalf("CreateResourceToken() error = %v", err)
	}
	if raw == "" || tok.ID == 0 {
		t.Fatalf("CreateResourceToken() returned empty raw or token")
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

	if _, err := LookupTokenByRaw(ctx, sqlDB, "tok_wrongwrongwrongwrongwrongwrong"); err != ErrNotFound {
		t.Errorf("LookupTokenByRaw(wrong) error = %v, want ErrNotFound", err)
	}

	if err := TouchTokenLastUsed(ctx, sqlDB, tok.ID); err != nil {
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
