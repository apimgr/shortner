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

	if _, _, err := CreateResourceToken(ctx, sqlDB, "link", "expired-1", &past); err != nil {
		t.Fatalf("CreateResourceToken(expired) error = %v", err)
	}
	if _, _, err := CreateResourceToken(ctx, sqlDB, "link", "active-1", &future); err != nil {
		t.Fatalf("CreateResourceToken(active) error = %v", err)
	}
	if _, _, err := CreateResourceToken(ctx, sqlDB, "link", "no-expiry", nil); err != nil {
		t.Fatalf("CreateResourceToken(no expiry) error = %v", err)
	}

	removed, err := CleanupExpiredTokens(ctx, sqlDB)
	if err != nil {
		t.Fatalf("CleanupExpiredTokens() error = %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}

	tokens, err := ListActiveTokens(ctx, sqlDB)
	if err != nil {
		t.Fatalf("ListActiveTokens() error = %v", err)
	}
	if len(tokens) != 2 {
		t.Fatalf("len(tokens) = %d, want 2 (active + no-expiry)", len(tokens))
	}
	for _, tok := range tokens {
		if tok.ResourceID == "expired-1" {
			t.Error("expired token was not removed")
		}
	}

	removedAgain, err := CleanupExpiredTokens(ctx, sqlDB)
	if err != nil {
		t.Fatalf("CleanupExpiredTokens() second call error = %v", err)
	}
	if removedAgain != 0 {
		t.Errorf("removed on second call = %d, want 0", removedAgain)
	}
}
