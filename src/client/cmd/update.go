package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/client/api"
	"github.com/apimgr/shortner/src/common/version"
)

// cachedAutodiscover is the on-disk cache of the server's autodiscover
// document, so the per-invocation update check does not add a round trip to
// every single command.
type cachedAutodiscover struct {
	FetchedAt time.Time        `json:"fetched_at"`
	Server    string           `json:"server"`
	Document  api.Autodiscover `json:"document"`
	Missing   bool             `json:"missing"`
}

// platformKey is this build's os-arch key in an autodiscover response.
func platformKey() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

// runUpdate handles `--update [check|yes]`. A bare `--update` means `yes`,
// matching the server binary's behavior.
func (r *runner) runUpdate(ctx context.Context) int {
	action := strings.ToLower(strings.TrimSpace(r.flags.Update))
	if action == "" {
		action = "yes"
	}

	switch action {
	case "help", "--help", "-h":
		fmt.Fprintf(r.io.Out, "Usage: %s --update {check|yes}\n\n", r.binaryName)
		fmt.Fprintln(r.io.Out, "  check    Report whether a newer client is published")
		fmt.Fprintln(r.io.Out, "  yes      Download, verify, and install the newer client")
		return ExitOK
	case "check", "yes":
	default:
		r.printer.Error("unknown --update command %q (want check or yes)", action)
		return ExitUsage
	}

	if err := r.requireServer(); err != nil {
		return r.fail(err)
	}

	doc, err := r.fetchAutodiscover(ctx, true)
	if err != nil {
		if errors.Is(err, api.ErrNotFound) {
			r.printer.Message("This server does not publish client updates.")
			return ExitOK
		}
		return r.fail(err)
	}

	entry, ok := doc.CLIVersions[platformKey()]
	if !ok {
		r.printer.Message("No client build published for %s.", platformKey())
		return ExitOK
	}

	if compareVersions(version.Version, entry.Version) >= 0 {
		r.printer.Message("%s %s is up to date.", r.binaryName, version.Version)
		return ExitOK
	}

	if action == "check" {
		r.printer.Message("Update available: %s -> %s (run '%s --update yes')", version.Version, entry.Version, r.binaryName)
		return ExitOK
	}

	if err := r.installUpdate(ctx, entry); err != nil {
		return r.fail(err)
	}
	return ExitOK
}

// installUpdate performs steps 3-6 of AI.md PART 32's CLI auto-update flow:
// download, verify SHA-256, atomic swap, re-exec.
func (r *runner) installUpdate(ctx context.Context, entry api.CLIVersion) error {
	currentPath, err := os.Executable()
	if err != nil {
		return err
	}
	currentPath, err = filepath.EvalSymlinks(currentPath)
	if err != nil {
		return err
	}
	if err := checkWritable(currentPath); err != nil {
		return fmt.Errorf("you do not have permission to update %s; ask your admin or move the binary to a writable path", currentPath)
	}

	tmpRoot := filepath.Join(os.TempDir(), "apimgr")
	if err := os.MkdirAll(tmpRoot, 0o700); err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp(tmpRoot, "shortner-")
	if err != nil {
		return err
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	tmpPath := filepath.Join(tmpDir, "cli.update.tmp")
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return err
	}

	r.printer.Message("Downloading %s %s ...", ProjectName+"-cli", entry.Version)
	if _, err := r.client.DownloadCLIBinary(ctx, ProjectName, platformKey(), file); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}

	if err := verifyChecksum(tmpPath, entry.SHA256); err != nil {
		return err
	}

	if err := replaceBinary(currentPath, tmpPath); err != nil {
		return err
	}
	r.printer.Message("Updated %s to %s", r.binaryName, entry.Version)

	return reexec(currentPath)
}

// verifyChecksum compares a downloaded file against the published SHA-256.
// A mismatch aborts the update, matching the server's PART 22 behavior.
func verifyChecksum(path, expected string) error {
	if expected == "" {
		return errors.New("server published no SHA-256 for this client build; refusing to install")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
	}()

	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return err
	}
	actual := hex.EncodeToString(digest.Sum(nil))
	if !strings.EqualFold(actual, strings.TrimSpace(expected)) {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expected, actual)
	}
	return nil
}

// checkWritable reports whether the running binary can be replaced in place.
func checkWritable(path string) error {
	dir := filepath.Dir(path)
	probe, err := os.CreateTemp(dir, ".update-probe-")
	if err != nil {
		return err
	}
	name := probe.Name()
	_ = probe.Close()
	return os.Remove(name)
}

// reexec replaces the current process with the freshly installed binary,
// preserving the original argv so the in-progress command continues.
func reexec(path string) error {
	args := os.Args
	env := os.Environ()
	if err := syscallExec(path, args, env); err != nil {
		return err
	}
	return nil
}

// fetchAutodiscover returns the server's autodiscover document, using the
// on-disk cache unless force is set or the cache has expired.
func (r *runner) fetchAutodiscover(ctx context.Context, force bool) (*api.Autodiscover, error) {
	cachePath := filepath.Join(r.paths.CacheDir, "autodiscover.json")

	if !force && r.cfg.Cache.Enabled {
		if cached, ok := readAutodiscoverCache(cachePath, r.client.BaseURL(), parseDuration(r.cfg.Cache.TTL, 5*time.Minute)); ok {
			if cached.Missing {
				return nil, fmt.Errorf("%w: autodiscover", api.ErrNotFound)
			}
			doc := cached.Document
			return &doc, nil
		}
	}

	doc, err := r.client.Autodiscover(ctx)
	if err != nil {
		if errors.Is(err, api.ErrNotFound) && r.cfg.Cache.Enabled {
			writeAutodiscoverCache(cachePath, cachedAutodiscover{
				FetchedAt: time.Now(),
				Server:    r.client.BaseURL(),
				Missing:   true,
			})
		}
		return nil, err
	}
	if r.cfg.Cache.Enabled {
		writeAutodiscoverCache(cachePath, cachedAutodiscover{
			FetchedAt: time.Now(),
			Server:    r.client.BaseURL(),
			Document:  *doc,
		})
	}
	return doc, nil
}

// readAutodiscoverCache loads a cached autodiscover document if it is still
// fresh and belongs to the configured server.
func readAutodiscoverCache(path, server string, ttl time.Duration) (cachedAutodiscover, bool) {
	var cached cachedAutodiscover
	data, err := os.ReadFile(path)
	if err != nil {
		return cached, false
	}
	if err := json.Unmarshal(data, &cached); err != nil {
		return cached, false
	}
	if cached.Server != server {
		return cached, false
	}
	if time.Since(cached.FetchedAt) > ttl {
		return cached, false
	}
	return cached, true
}

// writeAutodiscoverCache stores an autodiscover result. Cache failures are
// never fatal — the client simply re-fetches next time.
func writeAutodiscoverCache(path string, cached cachedAutodiscover) {
	data, err := json.Marshal(cached)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}

// updateGate implements step 2 of AI.md PART 32's auto-update flow: notify
// when a newer client exists, and refuse to keep making requests when this
// client is older than the server's cli_min_version. A server that does not
// publish autodiscover, or any transport failure, leaves the gate open —
// an update check must never break an otherwise working command.
func (r *runner) updateGate(ctx context.Context) bool {
	if r.cfg.Update.CheckInterval == "never" {
		return true
	}

	doc, err := r.fetchAutodiscover(ctx, false)
	if err != nil || doc == nil {
		return true
	}

	if doc.CLIMinVersion != "" && compareVersions(version.Version, doc.CLIMinVersion) < 0 {
		r.printer.Error("this CLI is too old; the server requires %s — run '%s --update yes' to upgrade.", doc.CLIMinVersion, r.binaryName)
		return false
	}

	entry, ok := doc.CLIVersions[platformKey()]
	if ok && compareVersions(version.Version, entry.Version) < 0 {
		if r.cfg.Update.Auto {
			if err := r.installUpdate(ctx, entry); err != nil {
				r.printer.Warn("automatic update failed: %v", err)
			}
			return true
		}
		r.printer.Warn("update available: %s -> %s (run '%s --update yes')", version.Version, entry.Version, r.binaryName)
	}
	return true
}

// compareVersions compares two dotted version strings, ignoring a leading
// "v" and any pre-release suffix. It returns -1, 0, or 1. A version that
// does not parse sorts as equal so a devel build never triggers an update.
func compareVersions(a, b string) int {
	if a == b {
		return 0
	}
	aParts, aOK := versionParts(a)
	bParts, bOK := versionParts(b)
	if !aOK || !bOK {
		return 0
	}
	for i := 0; i < 3; i++ {
		if aParts[i] != bParts[i] {
			if aParts[i] < bParts[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// versionParts splits a version into major, minor, and patch numbers.
func versionParts(value string) ([3]int, bool) {
	var parts [3]int
	trimmed := strings.TrimPrefix(strings.TrimSpace(value), "v")
	if idx := strings.IndexAny(trimmed, "-+"); idx >= 0 {
		trimmed = trimmed[:idx]
	}
	fields := strings.Split(trimmed, ".")
	if len(fields) == 0 || fields[0] == "" {
		return parts, false
	}
	for i := 0; i < 3 && i < len(fields); i++ {
		number, err := strconv.Atoi(fields[i])
		if err != nil {
			return parts, false
		}
		parts[i] = number
	}
	return parts, true
}
