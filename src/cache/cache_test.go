package cache

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestKeyLowercasesAndJoins(t *testing.T) {
	got := Key("User", "123", "Sessions")
	want := "user:123:sessions"
	if got != want {
		t.Errorf("Key() = %q, want %q", got, want)
	}
}

func TestMemorySetGet(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()

	if _, ok := m.Get(ctx, "missing"); ok {
		t.Errorf("Get(missing) ok = true, want false")
	}

	m.Set(ctx, "user:1", "alice", time.Minute)
	v, ok := m.Get(ctx, "user:1")
	if !ok || v != "alice" {
		t.Errorf("Get(user:1) = %v, %v, want alice, true", v, ok)
	}
}

func TestMemoryExpiry(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	m.Set(ctx, "k", "v", time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	if _, ok := m.Get(ctx, "k"); ok {
		t.Errorf("Get() after expiry ok = true, want false")
	}
}

func TestMemoryNoExpiry(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	m.Set(ctx, "k", "v", 0)
	time.Sleep(2 * time.Millisecond)
	if _, ok := m.Get(ctx, "k"); !ok {
		t.Errorf("Get() with ttl=0 expired, want persisted")
	}
}

func TestMemoryDelete(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	m.Set(ctx, "k", "v", time.Minute)
	m.Delete(ctx, "k")
	if _, ok := m.Get(ctx, "k"); ok {
		t.Errorf("Get() after Delete ok = true, want false")
	}
}

func TestMemoryGetOrSet(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	calls := 0
	fn := func() (any, error) {
		calls++
		return "computed", nil
	}

	v1, err := m.GetOrSet(ctx, "k", time.Minute, fn)
	if err != nil || v1 != "computed" {
		t.Fatalf("GetOrSet() = %v, %v", v1, err)
	}
	v2, err := m.GetOrSet(ctx, "k", time.Minute, fn)
	if err != nil || v2 != "computed" {
		t.Fatalf("GetOrSet() second call = %v, %v", v2, err)
	}
	if calls != 1 {
		t.Errorf("fn called %d times, want 1 (should be cached)", calls)
	}
}

func TestMemoryGetOrSetPropagatesError(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	wantErr := errors.New("boom")
	_, err := m.GetOrSet(ctx, "k", time.Minute, func() (any, error) {
		return nil, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Errorf("GetOrSet() error = %v, want %v", err, wantErr)
	}
	if _, ok := m.Get(ctx, "k"); ok {
		t.Errorf("Get() after failed GetOrSet ok = true, want false (nothing cached)")
	}
}

func TestMemorySweepRemovesExpired(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	m.Set(ctx, "expired", "v", time.Millisecond)
	m.Set(ctx, "fresh", "v", time.Minute)
	time.Sleep(5 * time.Millisecond)

	m.Sweep()

	if m.Len() != 1 {
		t.Errorf("Len() after Sweep = %d, want 1", m.Len())
	}
	if _, ok := m.Get(ctx, "fresh"); !ok {
		t.Errorf("Get(fresh) after Sweep ok = false, want true")
	}
}
