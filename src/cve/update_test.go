package cve

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNVDWindowsRespectMaximumRange(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(250 * 24 * time.Hour)
	windows := nvdWindows(start, end)
	if len(windows) != 3 {
		t.Fatalf("nvdWindows() count = %d, want 3", len(windows))
	}
	for _, window := range windows {
		if window.to.Sub(window.from) > maxWindow {
			t.Errorf("window %s..%s exceeds %s", window.from, window.to, maxWindow)
		}
	}
}

func TestFetchNVDWindowPaginatesAndSkipsInvalidRecords(t *testing.T) {
	var starts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		starts = append(starts, r.URL.Query().Get("startIndex"))
		if got := r.URL.Query().Get("resultsPerPage"); got != fmt.Sprint(pageSize) {
			t.Errorf("resultsPerPage = %q, want %d", got, pageSize)
		}
		if got := r.Header.Get("apiKey"); got != "test-key" {
			t.Errorf("apiKey header = %q, want configured key", got)
		}
		total, count := 2001, pageSize
		if r.URL.Query().Get("startIndex") == "2000" {
			count = 1
		}
		var body strings.Builder
		fmt.Fprintf(&body, `{"totalResults":%d,"vulnerabilities":[`, total)
		for i := 0; i < count; i++ {
			if i > 0 {
				body.WriteByte(',')
			}
			if r.URL.Query().Get("startIndex") == "0" && i == 0 {
				body.WriteString(`{"cve":{"id":""}}`)
				continue
			}
			fmt.Fprintf(&body, `{"cve":{"id":"CVE-2026-%04d"}}`, i)
		}
		body.WriteString(`]}`)
		_, _ = w.Write([]byte(body.String()))
	}))
	defer server.Close()

	source, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	limiter := newRequestLimiter(true)
	limiter.interval = 0
	data := map[string]json.RawMessage{}
	if err := fetchNVDWindow(t.Context(), server.Client(), limiter, source, "test-key", dateWindow{}, data); err != nil {
		t.Fatal(err)
	}
	if strings.Join(starts, ",") != "0,2000" {
		t.Fatalf("requested startIndex values = %v, want [0 2000]", starts)
	}
	if len(data) != 2000 {
		t.Fatalf("loaded %d CVEs, want 2000 valid records", len(data))
	}
}

func TestFetchNVDWindowReportsRemoteFailures(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		wantError string
	}{
		{name: "forbidden", status: http.StatusForbidden, wantError: "rate limited"},
		{name: "too many requests", status: http.StatusTooManyRequests, wantError: "rate limited"},
		{name: "server error", status: http.StatusBadGateway, wantError: "HTTP 502"},
		{name: "invalid response", status: http.StatusOK, body: "not-json", wantError: "decode NVD page"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			source, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			limiter := newRequestLimiter(true)
			limiter.interval = 0
			err = fetchNVDWindow(t.Context(), server.Client(), limiter, source, "", dateWindow{}, map[string]json.RawMessage{})
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("fetchNVDWindow() error = %v, want substring %q", err, tt.wantError)
			}
		})
	}
}

func TestLoadNVDFiltersMalformedRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nvd.json")
	if err := os.WriteFile(path, []byte(`[{"id":"CVE-1"},{"id":""},{"not":"a CVE"},null]`), 0o600); err != nil {
		t.Fatal(err)
	}
	records := map[string]json.RawMessage{}
	if err := loadNVD(path, records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || string(records["CVE-1"]) != `{"id":"CVE-1"}` {
		t.Fatalf("loaded records = %#v, want only CVE-1", records)
	}
}

func TestParseImageRefValidation(t *testing.T) {
	ref, err := parseImageRef(" ghcr.io/aquasecurity/trivy-db:2 ")
	if err != nil || ref != (imageRef{host: "ghcr.io", repository: "aquasecurity/trivy-db", tag: "2"}) {
		t.Fatalf("parseImageRef() = %+v, %v", ref, err)
	}
	for _, value := range []string{"", "registry.example", "registry.example/repo", "registry.example/repo:"} {
		if _, err := parseImageRef(value); err == nil {
			t.Errorf("parseImageRef(%q) accepted invalid image ref", value)
		}
	}
}

func TestNeedsInitialUpdate(t *testing.T) {
	root := t.TempDir()
	options := Options{SecurityDir: root}
	if !NeedsInitialUpdate(options) {
		t.Fatal("missing NVD data did not require an initial update")
	}
	cveDir := filepath.Join(root, "cve")
	if err := os.MkdirAll(cveDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cveDir, "nvd.json"), []byte(`[{"id":"CVE-2026-1234"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cveDir, ".last_updated"), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if NeedsInitialUpdate(options) {
		t.Fatal("valid NVD data unexpectedly required an update")
	}
	options.TrivyEnabled = true
	if !NeedsInitialUpdate(options) {
		t.Fatal("missing enabled Trivy data did not require an update")
	}
	if err := os.MkdirAll(filepath.Join(root, "trivy"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "trivy", "trivy.db"), []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(trivyMetadata{NextUpdate: time.Now().Add(time.Hour).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "trivy", "metadata.json"), metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "trivy", ".last_updated"), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if NeedsInitialUpdate(options) {
		t.Fatal("present NVD and Trivy data unexpectedly required an update")
	}
}

func TestNeedsInitialUpdateRejectsCorruptOrIncompleteData(t *testing.T) {
	root := t.TempDir()
	cveDir := filepath.Join(root, "cve")
	if err := os.MkdirAll(cveDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cveDir, "nvd.json"), []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	options := Options{SecurityDir: root}
	if !NeedsInitialUpdate(options) {
		t.Fatal("empty NVD feed was treated as a complete initial dataset")
	}
	if err := os.WriteFile(filepath.Join(cveDir, "nvd.json"), []byte(`[{"id":"CVE-2026-1234"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !NeedsInitialUpdate(options) {
		t.Fatal("NVD data without a successful-update marker was treated as complete")
	}
	if err := os.WriteFile(filepath.Join(cveDir, ".last_updated"), []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !NeedsInitialUpdate(options) {
		t.Fatal("NVD data with an invalid update marker was treated as complete")
	}
	if err := os.WriteFile(filepath.Join(cveDir, ".last_updated"), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	options.TrivyEnabled = true
	if !NeedsInitialUpdate(options) {
		t.Fatal("missing enabled Trivy files were treated as a complete dataset")
	}
}

func TestRequestLimiterHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	limiter := newRequestLimiter(false)
	limiter.next = time.Now().Add(time.Hour)
	if err := limiter.Wait(ctx); err == nil {
		t.Fatal("Wait() succeeded despite cancelled context")
	}
}

func TestUpdateTrivySkipsFreshDatabase(t *testing.T) {
	root := filepath.Join(t.TempDir(), "trivy")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	metadata := trivyMetadata{NextUpdate: time.Now().Add(time.Hour).UTC()}
	body, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "metadata.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := updateTrivy(t.Context(), Options{SecurityDir: filepath.Dir(root), TrivySource: "invalid"}); err != nil {
		t.Fatalf("updateTrivy() did not skip fresh data: %v", err)
	}
}

func TestUpdateTrivyRejectsCorruptExistingMetadata(t *testing.T) {
	root := filepath.Join(t.TempDir(), "trivy")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "metadata.json"), []byte(`{"NextUpdate":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := updateTrivy(t.Context(), Options{SecurityDir: filepath.Dir(root), TrivySource: "ghcr.io/example/db:1"}); err == nil || !strings.Contains(err.Error(), "existing Trivy metadata") {
		t.Fatalf("updateTrivy() error = %v, want corrupt metadata error", err)
	}
}

func TestReplaceNVDWritesValidAtomicDataset(t *testing.T) {
	root := filepath.Join(t.TempDir(), "security", "cve")
	now := time.Date(2026, 11, 21, 12, 30, 0, 0, time.UTC)
	records := map[string]json.RawMessage{
		"CVE-2026-1000": json.RawMessage(`{"id":"CVE-2026-1000","descriptions":[]}`),
	}
	if err := replaceNVD(root, records, now); err != nil {
		t.Fatal(err)
	}
	var saved []map[string]any
	body, err := os.ReadFile(filepath.Join(root, "nvd.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &saved); err != nil || len(saved) != 1 || saved[0]["id"] != "CVE-2026-1000" {
		t.Fatalf("nvd.json = %s, %v; want one valid CVE record", body, err)
	}
	updated, err := readLastUpdated(filepath.Join(root, ".last_updated"))
	if err != nil || !updated.Equal(now) {
		t.Fatalf("last updated = %s, %v; want %s", updated, err, now)
	}
}

func TestExtractTrivyValidatesAndSwapsDatabase(t *testing.T) {
	root := filepath.Join(t.TempDir(), "security", "trivy")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "trivy.db"), []byte("old-db"), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := trivyArchive(t)
	now := time.Date(2026, 11, 21, 12, 30, 0, 0, time.UTC)
	if err := extractTrivy(root, archive, now); err != nil {
		t.Fatal(err)
	}
	db, err := os.ReadFile(filepath.Join(root, "trivy.db"))
	if err != nil || string(db) != "new-db" {
		t.Fatalf("trivy.db = %q, %v; want new database", db, err)
	}
	updated, err := readLastUpdated(filepath.Join(root, ".last_updated"))
	if err != nil || !updated.Equal(now) {
		t.Fatalf("last updated = %s, %v; want %s", updated, err, now)
	}
	metadata, err := readTrivyMetadata(filepath.Join(root, "metadata.json"))
	if err != nil || !metadata.NextUpdate.After(now) {
		t.Fatalf("Trivy metadata = %+v, %v; expected a valid future update", metadata, err)
	}
}

func TestExtractTrivyFailurePreservesPreviousData(t *testing.T) {
	root := filepath.Join(t.TempDir(), "security", "trivy")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	old := []byte("old-db")
	if err := os.WriteFile(filepath.Join(root, "trivy.db"), old, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := extractTrivy(root, []byte("not-gzip"), time.Now()); err == nil {
		t.Fatal("extractTrivy() unexpectedly accepted invalid data")
	}
	got, err := os.ReadFile(filepath.Join(root, "trivy.db"))
	if err != nil || !bytes.Equal(got, old) {
		t.Fatalf("existing Trivy database changed after failed update: %q, %v", got, err)
	}
}

func trivyArchive(t *testing.T) []byte {
	t.Helper()
	var body bytes.Buffer
	gz := gzip.NewWriter(&body)
	tw := tar.NewWriter(gz)
	entries := map[string][]byte{
		"trivy.db":      []byte("new-db"),
		"metadata.json": []byte(`{"NextUpdate":"2026-11-22T12:00:00Z"}`),
	}
	for name, data := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}
