package db

import (
	"context"
	"testing"
	"time"
)

func TestCleanupExpiredTokens(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()

	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)

	key := make([]byte, 32)
	ownerID, err := CreateOwner(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	expiredRaw, expired, err := CreateLinkedToken(ctx, sqlDB, ownerID, "expired", &past, 25, key)
	if err != nil {
		t.Fatal(err)
	}
	activeRaw, active, err := CreateLinkedToken(ctx, sqlDB, ownerID, "active", &future, 25, key)
	if err != nil {
		t.Fatal(err)
	}
	_, noExpiry, err := CreateLinkedToken(ctx, sqlDB, ownerID, "no-expiry", nil, 25, key)
	if err != nil {
		t.Fatal(err)
	}

	removed, err := CleanupExpiredTokens(ctx, sqlDB)
	if err != nil {
		t.Fatalf("CleanupExpiredTokens() error = %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0 because expired linked tokens remain listed", removed)
	}

	tokens, err := ListOwnerTokens(ctx, sqlDB, ownerID)
	if err != nil {
		t.Fatalf("ListActiveTokens() error = %v", err)
	}
	if len(tokens) != 3 {
		t.Fatalf("len(tokens) = %d, want 3", len(tokens))
	}
	if expired.Status() != "expired" || active.Status() != "active" || noExpiry.Status() != "active" {
		t.Errorf("unexpected statuses: expired=%q active=%q no-expiry=%q", expired.Status(), active.Status(), noExpiry.Status())
	}
	if _, err := LookupTokenByRaw(ctx, sqlDB, expiredRaw); err == nil {
		t.Error("expired token authenticated")
	}
	if _, err := LookupTokenByRaw(ctx, sqlDB, activeRaw); err != nil {
		t.Errorf("active token lookup error = %v", err)
	}

	removedAgain, err := CleanupExpiredTokens(ctx, sqlDB)
	if err != nil {
		t.Fatalf("CleanupExpiredTokens() second call error = %v", err)
	}
	if removedAgain != 0 {
		t.Errorf("removed on second call = %d, want 0", removedAgain)
	}
}

func TestPurgeIdleOwnersOnlyWithoutResources(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()
	idleOwner, err := CreateOwner(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	linkedOwner, err := CreateOwner(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateLinkAutoCodeOwned(ctx, sqlDB, linkedOwner, "https://example.com", nil); err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(0, 0, -30).Unix()
	if _, err := sqlDB.Exec(`UPDATE owners SET last_seen_at = ? WHERE id IN (?, ?)`, old, idleOwner, linkedOwner); err != nil {
		t.Fatal(err)
	}
	removed, err := PurgeIdleOwners(ctx, sqlDB, 14)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := GetOwner(ctx, sqlDB, idleOwner); err == nil {
		t.Error("idle owner without resources still exists")
	}
	if _, err := GetOwner(ctx, sqlDB, linkedOwner); err != nil {
		t.Errorf("owner with a link was purged: %v", err)
	}
	removed, err = PurgeIdleOwners(ctx, sqlDB, 0)
	if err != nil || removed != 0 {
		t.Errorf("disabled purge = (%d, %v), want (0, nil)", removed, err)
	}
}
