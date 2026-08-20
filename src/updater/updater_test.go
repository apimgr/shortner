package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ago returns a UTC timestamp days in the past, for defer-window tests.
func ago(days int) time.Time {
	return time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
}

func TestValidBranch(t *testing.T) {
	for _, name := range Branches {
		if !ValidBranch(name) {
			t.Errorf("ValidBranch(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "nightly", "Stable", "release"} {
		if ValidBranch(name) {
			t.Errorf("ValidBranch(%q) = true, want false", name)
		}
	}
}

func TestReleaseVersionAndKey(t *testing.T) {
	published := time.Date(2026, 8, 19, 6, 0, 0, 0, time.UTC)
	stable := Release{TagName: "v1.2.3", PublishedAt: published}
	if got := stable.Version(); got != "v1.2.3" {
		t.Errorf("Version() = %q, want v1.2.3", got)
	}
	if got := stable.Key(); got != "v1.2.3" {
		t.Errorf("Key() = %q, want v1.2.3", got)
	}

	// The rolling daily tag never changes, so both the label and the
	// notification key must be derived from the publish time instead.
	daily := Release{TagName: DailyTag, Prerelease: true, PublishedAt: published}
	if got := daily.Version(); !strings.Contains(got, "2026-08-19T06:00:00Z") {
		t.Errorf("Version() = %q, want the publish time", got)
	}
	if got := daily.Key(); got != "daily@2026-08-19T06:00:00Z" {
		t.Errorf("Key() = %q, want the publish-time key", got)
	}
	second := Release{TagName: DailyTag, Prerelease: true, PublishedAt: published.Add(24 * time.Hour)}
	if daily.Key() == second.Key() {
		t.Error("two nightly rebuilds share a notification key; the notice would fire only once")
	}
}

func TestMatchesBranch(t *testing.T) {
	stable := Release{TagName: "v1.0.0"}
	beta := Release{TagName: "202512051430-beta", Prerelease: true}
	daily := Release{TagName: DailyTag, Prerelease: true}
	other := Release{TagName: "v2.0.0-rc1", Prerelease: true}

	tests := []struct {
		name    string
		release Release
		branch  string
		want    bool
	}{
		// Channels are cumulative: stable releases match everywhere.
		{"stable/stable", stable, BranchStable, true},
		{"stable/beta", stable, BranchBeta, true},
		{"stable/daily", stable, BranchDaily, true},
		{"beta/stable", beta, BranchStable, false},
		{"beta/beta", beta, BranchBeta, true},
		{"beta/daily", beta, BranchDaily, true},
		{"daily/stable", daily, BranchStable, false},
		{"daily/beta", daily, BranchBeta, false},
		{"daily/daily", daily, BranchDaily, true},
		{"rc/daily", other, BranchDaily, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesBranch(tt.release, tt.branch); got != tt.want {
				t.Errorf("matchesBranch(%q, %q) = %v, want %v", tt.release.TagName, tt.branch, got, tt.want)
			}
		})
	}
}

func TestEligible(t *testing.T) {
	tests := []struct {
		name      string
		release   Release
		deferDays int
		want      bool
	}{
		{"no window", Release{PublishedAt: ago(0)}, 0, true},
		{"negative window", Release{PublishedAt: ago(0)}, -5, true},
		{"no publish date", Release{}, 30, true},
		{"aged past window", Release{PublishedAt: ago(40)}, 30, true},
		{"exactly at window", Release{PublishedAt: ago(30)}, 30, true},
		{"inside window", Release{PublishedAt: ago(5)}, 30, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Eligible(tt.release, tt.deferDays, time.Now().UTC()); got != tt.want {
				t.Errorf("Eligible = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSelect(t *testing.T) {
	now := time.Now().UTC()
	// GitHub returns releases newest-first; these mirror the AI.md PART 22
	// "Defer Semantics" worked example.
	releases := []Release{
		{TagName: "v1.2.4", PublishedAt: ago(5)},
		{TagName: "202608100000-beta", Prerelease: true, PublishedAt: ago(10)},
		{TagName: "v1.2.3", PublishedAt: ago(40)},
		{TagName: "v1.2.2", PublishedAt: ago(90)},
	}

	tests := []struct {
		name      string
		current   string
		branch    string
		deferDays int
		want      string
	}{
		{"newest stable", "v1.2.2", BranchStable, 0, "v1.2.4"},
		{"defer window picks the aged release", "v1.2.2", BranchStable, 30, "v1.2.3"},
		{"beta sees betas and stables", "v1.2.2", BranchBeta, 0, "v1.2.4"},
		{"already current", "v1.2.4", BranchStable, 0, ""},
		{"current is newest stable, beta channel", "v1.2.4", BranchBeta, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Select(releases, tt.current, tt.branch, 0, tt.deferDays, now)
			if tt.want == "" {
				if got != nil {
					t.Fatalf("Select = %q, want nil", got.TagName)
				}
				return
			}
			if got == nil {
				t.Fatalf("Select = nil, want %q", tt.want)
			}
			if got.TagName != tt.want {
				t.Errorf("Select = %q, want %q", got.TagName, tt.want)
			}
		})
	}
}

func TestSelectNeverDowngrades(t *testing.T) {
	// The running build is the newest release. Walking past it would pick
	// the next entry down, silently installing an older binary.
	releases := []Release{
		{TagName: "v2.0.0", PublishedAt: ago(1)},
		{TagName: "v1.9.0", PublishedAt: ago(60)},
	}
	if got := Select(releases, "v2.0.0", BranchStable, 0, 0, time.Now().UTC()); got != nil {
		t.Errorf("Select = %q, want nil (a downgrade was selected)", got.TagName)
	}
}

func TestSelectDailyUsesBuildEpoch(t *testing.T) {
	published := time.Now().UTC().Add(-2 * time.Hour)
	releases := []Release{{TagName: DailyTag, Prerelease: true, PublishedAt: published}}

	// Built before tonight's nightly: an update exists.
	older := published.Add(-24 * time.Hour).Unix()
	if got := Select(releases, "daily", BranchDaily, older, 0, time.Now().UTC()); got == nil {
		t.Error("Select = nil, want the newer nightly")
	}
	// Built after it: nothing to do, even though the tag is unchanged.
	newer := published.Add(time.Hour).Unix()
	if got := Select(releases, "daily", BranchDaily, newer, 0, time.Now().UTC()); got != nil {
		t.Errorf("Select = %q, want nil (this build is newer than the nightly)", got.Version())
	}
}

func TestBinaryName(t *testing.T) {
	want := "shortner-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	if got := BinaryName(); got != want {
		t.Errorf("BinaryName() = %q, want %q", got, want)
	}
}

// withAPI points the package at srv for the duration of the test.
func withAPI(t *testing.T, srv *httptest.Server) {
	t.Helper()
	prev := apiBaseURL
	apiBaseURL = srv.URL
	t.Cleanup(func() { apiBaseURL = prev })
}

func TestCheckForUpdateNotFoundMeansCurrent(t *testing.T) {
	// AI.md PART 22: "HTTP 404 from GitHub API means no updates available".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	withAPI(t, srv)

	release, err := CheckForUpdate(context.Background(), "v1.0.0", BranchStable, 0)
	if err != nil {
		t.Fatalf("CheckForUpdate error = %v, want nil", err)
	}
	if release != nil {
		t.Errorf("CheckForUpdate = %q, want nil", release.TagName)
	}
}

func TestCheckForUpdateStableUsesLatestEndpoint(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		fmt.Fprint(w, `{"tag_name":"v1.5.0","prerelease":false,"published_at":"2026-01-01T00:00:00Z"}`)
	}))
	defer srv.Close()
	withAPI(t, srv)

	release, err := CheckForUpdate(context.Background(), "v1.0.0", BranchStable, 0)
	if err != nil {
		t.Fatalf("CheckForUpdate error = %v", err)
	}
	if gotPath != "/repos/apimgr/shortner/releases/latest" {
		t.Errorf("path = %q, want the latest-release endpoint", gotPath)
	}
	if release == nil || release.TagName != "v1.5.0" {
		t.Fatalf("CheckForUpdate = %+v, want v1.5.0", release)
	}
}

func TestCheckEligibleUsesFullList(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		fmt.Fprintf(w, `[{"tag_name":"v1.2.4","published_at":%q},{"tag_name":"v1.2.3","published_at":%q}]`,
			ago(5).Format(time.RFC3339), ago(40).Format(time.RFC3339))
	}))
	defer srv.Close()
	withAPI(t, srv)

	release, err := CheckEligible(context.Background(), "v1.2.2", BranchStable, 0, 30, time.Now().UTC())
	if err != nil {
		t.Fatalf("CheckEligible error = %v", err)
	}
	if gotPath != "/repos/apimgr/shortner/releases" {
		t.Errorf("path = %q, want the full releases list (the newest may be deferred)", gotPath)
	}
	if release == nil || release.TagName != "v1.2.3" {
		t.Fatalf("CheckEligible = %+v, want the aged v1.2.3", release)
	}
}

func TestCheckForUpdateAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	withAPI(t, srv)

	if _, err := CheckForUpdate(context.Background(), "v1.0.0", BranchStable, 0); err == nil {
		t.Fatal("CheckForUpdate error = nil, want an API error")
	}
}

func TestVerifyChecksum(t *testing.T) {
	path := filepath.Join(t.TempDir(), "binary")
	payload := []byte("shortner release binary")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])

	if err := verifyChecksum(path, hash); err != nil {
		t.Errorf("verifyChecksum error = %v, want nil", err)
	}
	// Hex case must not matter.
	if err := verifyChecksum(path, strings.ToUpper(hash)); err != nil {
		t.Errorf("verifyChecksum (uppercase) error = %v, want nil", err)
	}
	if err := verifyChecksum(path, strings.Repeat("0", 64)); err == nil {
		t.Error("verifyChecksum error = nil, want a mismatch error")
	}
	if err := verifyChecksum(filepath.Join(t.TempDir(), "missing"), hash); err == nil {
		t.Error("verifyChecksum error = nil for a missing file, want an error")
	}
}

func TestFetchExpectedChecksum(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "aaaa  shortner-linux-amd64\nbbbb  shortner-darwin-arm64\n")
	}))
	defer srv.Close()

	release := &Release{Assets: []Asset{{Name: "sha256.txt", BrowserDownloadURL: srv.URL + "/sha256.txt"}}}
	got, err := fetchExpectedChecksum(context.Background(), release, "shortner-darwin-arm64")
	if err != nil {
		t.Fatalf("fetchExpectedChecksum error = %v", err)
	}
	if got != "bbbb" {
		t.Errorf("checksum = %q, want bbbb", got)
	}

	if _, err := fetchExpectedChecksum(context.Background(), release, "shortner-plan9-386"); err == nil {
		t.Error("error = nil for an unlisted asset, want an error")
	}
	if _, err := fetchExpectedChecksum(context.Background(), &Release{}, "shortner-linux-amd64"); err == nil {
		t.Error("error = nil for a release with no sha256.txt, want an error")
	}
}

func TestDoUpdateWithoutPlatformAsset(t *testing.T) {
	// A release carrying no binary for this platform must fail before it
	// touches the running executable.
	err := DoUpdate(context.Background(), &Release{TagName: "v1.0.0"})
	if !errors.Is(err, ErrNoAsset) {
		t.Errorf("DoUpdate error = %v, want ErrNoAsset", err)
	}
}
