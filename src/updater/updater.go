// Package updater implements the self-update machinery of AI.md PART 22
// "UPDATE COMMAND": GitHub Releases lookup for the stable/beta/daily
// channels, the `defer_days` eligibility window, SHA-256 verified
// downloads, and the platform-specific binary replacement/restart that
// `--update yes` and the `update_check` scheduler task both drive.
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// repoOwner/repoName address the GitHub repository releases are published
// to. They are the frozen `{project_org}`/`{project_name}` values from
// IDEA.md "Project variables".
const (
	repoOwner = "apimgr"
	repoName  = "shortner"
)

// Release channels, per AI.md PART 22 "Update Branches".
const (
	BranchStable = "stable"
	BranchBeta   = "beta"
	BranchDaily  = "daily"
)

// DailyTag is the rolling pre-release tag of the `daily` channel: a single
// release that is deleted and recreated nightly, so its publish time — not
// its tag — decides whether a newer build exists.
const DailyTag = "daily"

// Branches lists every valid `server.update.branch` value, most stable
// first.
var Branches = []string{BranchStable, BranchBeta, BranchDaily}

// ValidBranch reports whether name is one of the three release channels.
func ValidBranch(name string) bool {
	for _, b := range Branches {
		if name == b {
			return true
		}
	}
	return false
}

// apiBaseURL is the GitHub API root. It is a variable so tests can point
// the whole package at an httptest server instead of the network.
var apiBaseURL = "https://api.github.com"

// httpClient is used for both the API calls and the asset downloads. The
// timeout is generous enough for a multi-megabyte binary on a slow link
// but still bounded, so a hung mirror can never wedge a scheduler run.
var httpClient = &http.Client{Timeout: 10 * time.Minute}

// ErrNoAsset is returned when a release carries no binary for this
// platform, which is a release-publishing problem rather than a transient
// network error.
var ErrNoAsset = errors.New("updater: release has no binary for this platform")

// Release is one GitHub release, decoded from the Releases API.
type Release struct {
	TagName     string    `json:"tag_name"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []Asset   `json:"assets"`
}

// Asset is one downloadable file attached to a release.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Version returns the human label for this release: the tag for a normal
// release, and "daily (published ...)" for the rolling daily tag, whose
// tag alone never changes.
func (r Release) Version() string {
	if r.TagName == DailyTag {
		return fmt.Sprintf("%s (published %s)", DailyTag, r.PublishedAt.UTC().Format(time.RFC3339))
	}
	return r.TagName
}

// Key returns a stable per-version identity used to fire the "update
// available" notification exactly once per version (AI.md PART 22
// "Surfacing rules"). The rolling daily tag is keyed by publish time
// because every nightly rebuild reuses the tag.
func (r Release) Key() string {
	if r.TagName == DailyTag {
		return DailyTag + "@" + r.PublishedAt.UTC().Format(time.RFC3339)
	}
	return r.TagName
}

// CheckForUpdate returns the newest release on branch that is newer than
// the running build, or nil when the running build is current. It is the
// unfiltered check behind `--update check` / `--update yes`, which per
// AI.md PART 22 "Defer Semantics" always see the true latest release.
//
// buildEpoch is the caller's own embedded build timestamp (version.Epoch),
// needed to detect a newer nightly on the rolling `daily` tag.
func CheckForUpdate(ctx context.Context, currentVersion, branch string, buildEpoch int64) (*Release, error) {
	return checkForUpdate(ctx, currentVersion, branch, buildEpoch, 0, time.Now().UTC())
}

// CheckEligible is CheckForUpdate restricted to releases that have already
// aged past deferDays, per AI.md PART 22 "Defer Semantics". Only the
// scheduled `update_check` task uses it — the defer window gates the task
// alone, never an explicit operator action.
func CheckEligible(ctx context.Context, currentVersion, branch string, buildEpoch int64, deferDays int, now time.Time) (*Release, error) {
	return checkForUpdate(ctx, currentVersion, branch, buildEpoch, deferDays, now)
}

// checkForUpdate fetches the candidate releases for branch and selects the
// newest eligible one.
func checkForUpdate(ctx context.Context, currentVersion, branch string, buildEpoch int64, deferDays int, now time.Time) (*Release, error) {
	releases, err := fetchReleases(ctx, branch, deferDays)
	if err != nil {
		return nil, err
	}
	return Select(releases, currentVersion, branch, buildEpoch, deferDays, now), nil
}

// fetchReleases retrieves the release candidates for branch. The stable
// channel with no defer window can use the single-release `latest`
// endpoint; every other combination needs the full list, because the
// newest release may be ineligible (deferred) or belong to a different
// channel.
//
// HTTP 404 means the repository has published no matching release yet —
// per AI.md PART 22, that is "no updates available", not an error.
func fetchReleases(ctx context.Context, branch string, deferDays int) ([]Release, error) {
	url := apiBaseURL + "/repos/" + repoOwner + "/" + repoName + "/releases"
	single := branch == BranchStable && deferDays == 0
	if single {
		url += "/latest"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("updater: GitHub API error: %d", resp.StatusCode)
	}

	if single {
		var release Release
		if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
			return nil, err
		}
		return []Release{release}, nil
	}

	var releases []Release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}
	return releases, nil
}

// Select picks the newest release from releases (GitHub returns them
// newest-first) that belongs to branch, is newer than the running build,
// and has aged past deferDays. It returns nil when nothing qualifies.
//
// Walking stops at the running version: a release list is ordered, so
// anything past the entry matching currentVersion is older and selecting
// it would silently downgrade the binary.
func Select(releases []Release, currentVersion, branch string, buildEpoch int64, deferDays int, now time.Time) *Release {
	for i := range releases {
		r := releases[i]
		if !matchesBranch(r, branch) {
			continue
		}
		if r.TagName == DailyTag {
			// Rolling tag: a newer nightly exists when the release was
			// published after this binary was built.
			if r.PublishedAt.Unix() <= buildEpoch {
				continue
			}
			if !Eligible(r, deferDays, now) {
				continue
			}
			return &r
		}
		if r.TagName == currentVersion {
			return nil
		}
		if !Eligible(r, deferDays, now) {
			continue
		}
		return &r
	}
	return nil
}

// Eligible reports whether r has been public long enough to be adopted,
// per AI.md PART 22: "a release is eligible only once
// now - published_at >= defer_days". A release with no publish date cannot
// be gated, so it is treated as eligible.
func Eligible(r Release, deferDays int, now time.Time) bool {
	if deferDays <= 0 || r.PublishedAt.IsZero() {
		return true
	}
	return now.UTC().Sub(r.PublishedAt.UTC()) >= time.Duration(deferDays)*24*time.Hour
}

// matchesBranch implements the cumulative channels of AI.md PART 22
// "Channel Semantics": each channel also accepts every release from all
// more-stable channels, so a beta or daily user is never left older than a
// stable release.
func matchesBranch(r Release, branch string) bool {
	// Stable releases match every channel.
	if !r.Prerelease {
		return true
	}
	isBeta := strings.HasSuffix(r.TagName, "-beta")
	isDaily := r.TagName == DailyTag
	switch branch {
	case BranchBeta:
		return isBeta
	case BranchDaily:
		return isBeta || isDaily
	default:
		return false
	}
}

// DoUpdate downloads release's binary for this platform, verifies it
// against the release's sha256.txt, and replaces the running executable.
// It does not restart anything — see Restart.
func DoUpdate(ctx context.Context, release *Release) error {
	assetName := BinaryName()
	var downloadURL string
	for _, asset := range release.Assets {
		if asset.Name == assetName {
			downloadURL = asset.BrowserDownloadURL
			break
		}
	}
	if downloadURL == "" {
		return fmt.Errorf("%w: %s/%s", ErrNoAsset, runtime.GOOS, runtime.GOARCH)
	}

	tmpFile, err := os.CreateTemp("", repoName+"-update-*")
	if err != nil {
		return fmt.Errorf("updater: create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	// Removed on every error path; a successful replacement renames the
	// file away first, which makes this a harmless no-op.
	defer os.Remove(tmpPath)

	if err := download(ctx, downloadURL, tmpFile); err != nil {
		tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("updater: close temp file: %w", err)
	}

	// Checksum verification is mandatory (AI.md PART 22 "Update Checksum
	// Verification") — an unverified binary is never installed.
	expectedHash, err := fetchExpectedChecksum(ctx, release, assetName)
	if err != nil {
		return fmt.Errorf("updater: fetch checksum: %w", err)
	}
	if err := verifyChecksum(tmpPath, expectedHash); err != nil {
		return err
	}

	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmpPath, 0o755); err != nil {
			return fmt.Errorf("updater: set permissions: %w", err)
		}
	}

	currentPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("updater: locate executable: %w", err)
	}
	currentPath, err = filepath.EvalSymlinks(currentPath)
	if err != nil {
		return fmt.Errorf("updater: resolve symlinks: %w", err)
	}

	return replaceBinary(currentPath, tmpPath)
}

// download streams url into dst.
func download(ctx context.Context, url string, dst io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("updater: download failed: %d", resp.StatusCode)
	}
	if _, err := io.Copy(dst, resp.Body); err != nil {
		return fmt.Errorf("updater: download: %w", err)
	}
	return nil
}

// BinaryName returns the release asset name for this platform, matching
// the Makefile's release naming ("{project_name}-{os}-{arch}").
func BinaryName() string {
	name := repoName + "-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// fetchExpectedChecksum downloads the release's sha256.txt asset and
// returns the hash recorded for assetName.
func fetchExpectedChecksum(ctx context.Context, release *Release, assetName string) (string, error) {
	var checksumsURL string
	for _, asset := range release.Assets {
		if asset.Name == "sha256.txt" {
			checksumsURL = asset.BrowserDownloadURL
			break
		}
	}
	if checksumsURL == "" {
		return "", errors.New("release has no sha256.txt asset")
	}

	var buf strings.Builder
	if err := download(ctx, checksumsURL, &buf); err != nil {
		return "", err
	}

	// Each line is "{sha256}  {filename}".
	for _, line := range strings.Split(buf.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == assetName {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("no checksum entry for %s", assetName)
}

// verifyChecksum verifies filePath's SHA-256 against expectedHash.
func verifyChecksum(filePath, expectedHash string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}

	actualHash := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(actualHash, expectedHash) {
		return fmt.Errorf("updater: checksum mismatch: expected %s, got %s", expectedHash, actualHash)
	}
	return nil
}
