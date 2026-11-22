// Package cve downloads the NVD and optional Trivy vulnerability databases
// required by AI.md PART 9 and PART 18.
package cve

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	pageSize         = 2000
	maxWindow        = 120 * 24 * time.Hour
	maxResponseBytes = 128 << 20
	trivyLayerType   = "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip"
)

var trivyHTTPClient = &http.Client{Timeout: 5 * time.Minute}

// Options describes the database paths and sources from the AI.md data area.
type Options struct {
	SecurityDir  string
	NVDSource    string
	APIKey       string
	TrivyEnabled bool
	TrivySource  string
}

// Update refreshes NVD and, when enabled, Trivy data. Each data set is
// committed independently so a failure never replaces its previous copy.
func Update(ctx context.Context, options Options) error {
	if err := updateNVD(ctx, options); err != nil {
		return err
	}
	if options.TrivyEnabled {
		if err := updateTrivy(ctx, options); err != nil {
			return err
		}
	}
	return nil
}

// NeedsInitialUpdate reports whether required security data is missing.
func NeedsInitialUpdate(options Options) bool {
	cveDir := filepath.Join(options.SecurityDir, "cve")
	var records = make(map[string]json.RawMessage)
	if err := loadNVD(filepath.Join(cveDir, "nvd.json"), records); err != nil || len(records) == 0 {
		return true
	}
	if _, err := readLastUpdated(filepath.Join(cveDir, ".last_updated")); err != nil {
		return true
	}
	if options.TrivyEnabled {
		trivyDir := filepath.Join(options.SecurityDir, "trivy")
		database, err := os.ReadFile(filepath.Join(trivyDir, "trivy.db"))
		if err != nil || len(database) == 0 {
			return true
		}
		if _, err := readTrivyMetadata(filepath.Join(trivyDir, "metadata.json")); err != nil {
			return true
		}
		if _, err := readLastUpdated(filepath.Join(trivyDir, ".last_updated")); err != nil {
			return true
		}
	}
	return false
}

type nvdPage struct {
	TotalResults    int `json:"totalResults"`
	Vulnerabilities []struct {
		CVE json.RawMessage `json:"cve"`
	} `json:"vulnerabilities"`
}

type cveRecord struct {
	ID string `json:"id"`
}

func updateNVD(ctx context.Context, options Options) error {
	if options.SecurityDir == "" {
		return errors.New("cve: security data directory is empty")
	}
	source, err := url.Parse(options.NVDSource)
	if err != nil || source.Scheme != "https" || source.Host == "" {
		return errors.New("cve: NVD source must be an HTTPS URL")
	}
	root := filepath.Join(options.SecurityDir, "cve")
	previousAt, err := readLastUpdated(filepath.Join(root, ".last_updated"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cve: read NVD update time: %w", err)
	}
	data := make(map[string]json.RawMessage)
	if previousAt.IsZero() {
		if err := loadNVD(filepath.Join(root, "nvd.json"), data); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("cve: read existing NVD data: %w", err)
		}
	} else if err := loadNVD(filepath.Join(root, "nvd.json"), data); err != nil {
		return fmt.Errorf("cve: read existing NVD data: %w", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	windows := nvdWindows(previousAt, now)
	limiter := newRequestLimiter(options.APIKey != "")
	client := &http.Client{Timeout: 2 * time.Minute}
	for _, window := range windows {
		if err := fetchNVDWindow(ctx, client, limiter, source, options.APIKey, window, data); err != nil {
			return err
		}
	}
	if len(data) == 0 {
		return errors.New("cve: NVD update returned no CVE records")
	}
	return replaceNVD(root, data, now)
}

type dateWindow struct{ from, to time.Time }

func nvdWindows(previous, now time.Time) []dateWindow {
	if previous.IsZero() {
		return []dateWindow{{}}
	}
	start := previous.UTC()
	var windows []dateWindow
	for start.Before(now) {
		end := start.Add(maxWindow)
		if end.After(now) {
			end = now
		}
		windows = append(windows, dateWindow{from: start, to: end})
		start = end
	}
	if len(windows) == 0 {
		windows = append(windows, dateWindow{from: previous, to: now})
	}
	return windows
}

func fetchNVDWindow(ctx context.Context, client *http.Client, limiter *requestLimiter, source *url.URL, apiKey string, window dateWindow, data map[string]json.RawMessage) error {
	for start := 0; ; start += pageSize {
		if err := limiter.Wait(ctx); err != nil {
			return err
		}
		requestURL := *source
		query := requestURL.Query()
		query.Set("resultsPerPage", strconv.Itoa(pageSize))
		query.Set("startIndex", strconv.Itoa(start))
		if !window.from.IsZero() {
			query.Set("lastModStartDate", nvdDate(window.from))
			query.Set("lastModEndDate", nvdDate(window.to))
		}
		requestURL.RawQuery = query.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
		if err != nil {
			return fmt.Errorf("cve: create NVD request: %w", err)
		}
		if apiKey != "" {
			req.Header.Set("apiKey", apiKey)
		}
		req.Header.Set("User-Agent", "shortner-security-data/1")
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("cve: NVD request: %w", err)
		}
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			return fmt.Errorf("cve: NVD rate limited (HTTP %d); retry on the next scheduled run", resp.StatusCode)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return fmt.Errorf("cve: NVD returned HTTP %d", resp.StatusCode)
		}
		var page nvdPage
		readErr := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&page)
		closeErr := resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("cve: decode NVD page: %w", readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("cve: close NVD response: %w", closeErr)
		}
		for _, vulnerability := range page.Vulnerabilities {
			var record cveRecord
			if len(vulnerability.CVE) == 0 || json.Unmarshal(vulnerability.CVE, &record) != nil || record.ID == "" {
				continue
			}
			compact := make(json.RawMessage, len(vulnerability.CVE))
			copy(compact, vulnerability.CVE)
			data[record.ID] = compact
		}
		if start+len(page.Vulnerabilities) >= page.TotalResults || len(page.Vulnerabilities) == 0 {
			return nil
		}
	}
}

func nvdDate(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000")
}

type requestLimiter struct {
	interval time.Duration
	next     time.Time
}

func newRequestLimiter(hasKey bool) *requestLimiter {
	interval := 6100 * time.Millisecond
	if hasKey {
		interval = 610 * time.Millisecond
	}
	return &requestLimiter{interval: interval}
}

func (l *requestLimiter) Wait(ctx context.Context) error {
	now := time.Now()
	wait := l.next.Sub(now)
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	l.next = time.Now().Add(l.interval)
	return nil
}

func loadNVD(path string, records map[string]json.RawMessage) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	var values []json.RawMessage
	if err := json.NewDecoder(file).Decode(&values); err != nil {
		return err
	}
	for _, value := range values {
		var record cveRecord
		if err := json.Unmarshal(value, &record); err != nil || record.ID == "" {
			continue
		}
		records[record.ID] = value
	}
	return nil
}

func replaceNVD(root string, records map[string]json.RawMessage, now time.Time) error {
	parent := filepath.Dir(root)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".cve-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	ids := make([]string, 0, len(records))
	for id := range records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	file, err := os.OpenFile(filepath.Join(tmp, "nvd.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	writer := json.NewEncoder(file)
	items := make([]json.RawMessage, 0, len(ids))
	for _, id := range ids {
		items = append(items, records[id])
	}
	if err := writer.Encode(items); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, ".last_updated"), []byte(now.UTC().Format(time.RFC3339)+"\n"), 0o600); err != nil {
		return err
	}
	return swapDirectory(tmp, root)
}

func updateTrivy(ctx context.Context, options Options) error {
	root := filepath.Join(options.SecurityDir, "trivy")
	metadataPath := filepath.Join(root, "metadata.json")
	if metadata, err := readTrivyMetadata(metadataPath); err == nil && metadata.NextUpdate.After(time.Now().UTC()) {
		return nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cve: read existing Trivy metadata: %w", err)
	}
	ref, err := parseImageRef(options.TrivySource)
	if err != nil {
		return err
	}
	manifest, err := fetchManifest(ctx, ref)
	if err != nil {
		return err
	}
	var layer string
	for _, candidate := range manifest.Layers {
		if candidate.MediaType == trivyLayerType {
			layer = candidate.Digest
			break
		}
	}
	if layer == "" {
		return errors.New("cve: Trivy image has no vulnerability database layer")
	}
	compressed, err := fetchBlob(ctx, ref, layer)
	if err != nil {
		return err
	}
	return extractTrivy(root, compressed, time.Now().UTC().Truncate(time.Second))
}

type trivyMetadata struct {
	NextUpdate time.Time `json:"NextUpdate"`
}

func readTrivyMetadata(path string) (trivyMetadata, error) {
	var metadata trivyMetadata
	body, err := os.ReadFile(path)
	if err != nil {
		return metadata, err
	}
	err = json.Unmarshal(body, &metadata)
	if err == nil && metadata.NextUpdate.IsZero() {
		err = errors.New("metadata has no NextUpdate")
	}
	return metadata, err
}

type imageRef struct{ host, repository, tag string }

func parseImageRef(value string) (imageRef, error) {
	parts := strings.SplitN(strings.TrimSpace(value), "/", 2)
	if len(parts) != 2 || parts[0] == "" {
		return imageRef{}, errors.New("cve: Trivy source must be an OCI registry reference")
	}
	image := strings.Split(parts[1], ":")
	if len(image) != 2 || image[0] == "" || image[1] == "" {
		return imageRef{}, errors.New("cve: Trivy source must include repository and tag")
	}
	return imageRef{host: parts[0], repository: image[0], tag: image[1]}, nil
}

type ociManifest struct {
	Layers []struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
	} `json:"layers"`
}

func fetchManifest(ctx context.Context, ref imageRef) (ociManifest, error) {
	var manifest ociManifest
	endpoint := "https://" + ref.host + "/v2/" + ref.repository + "/manifests/" + ref.tag
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return manifest, err
	}
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")
	resp, err := trivyHTTPClient.Do(req)
	if err != nil {
		return manifest, fmt.Errorf("cve: fetch Trivy manifest: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		challenge := resp.Header.Get("WWW-Authenticate")
		resp.Body.Close()
		token, tokenErr := anonymousToken(ctx, challenge, ref)
		if tokenErr != nil {
			return manifest, tokenErr
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return manifest, err
		}
		req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err = trivyHTTPClient.Do(req)
		if err != nil {
			return manifest, fmt.Errorf("cve: retry Trivy manifest: %w", err)
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return manifest, fmt.Errorf("cve: Trivy registry returned HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&manifest); err != nil {
		return manifest, fmt.Errorf("cve: decode Trivy manifest: %w", err)
	}
	return manifest, nil
}

func anonymousToken(ctx context.Context, challenge string, ref imageRef) (string, error) {
	if !strings.HasPrefix(challenge, "Bearer ") {
		return "", errors.New("cve: Trivy registry requires unsupported authentication")
	}
	params := map[string]string{}
	for _, field := range strings.Split(strings.TrimPrefix(challenge, "Bearer "), ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(field), "=")
		if ok {
			params[key] = strings.Trim(value, "\"")
		}
	}
	realm, err := url.Parse(params["realm"])
	if err != nil || realm.Scheme != "https" || realm.Host == "" {
		return "", errors.New("cve: invalid Trivy registry token realm")
	}
	query := realm.Query()
	if service := params["service"]; service != "" {
		query.Set("service", service)
	}
	query.Set("scope", "repository:"+ref.repository+":pull")
	realm.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := trivyHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("cve: get Trivy registry token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("cve: Trivy token service returned HTTP %d", resp.StatusCode)
	}
	var token struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&token); err != nil {
		return "", err
	}
	if token.Token == "" {
		token.Token = token.AccessToken
	}
	if token.Token == "" {
		return "", errors.New("cve: Trivy token service returned no token")
	}
	return token.Token, nil
}

func fetchBlob(ctx context.Context, ref imageRef, digest string) ([]byte, error) {
	endpoint := "https://" + ref.host + "/v2/" + ref.repository + "/blobs/" + digest
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := trivyHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cve: fetch Trivy database: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		challenge := resp.Header.Get("WWW-Authenticate")
		resp.Body.Close()
		token, tokenErr := anonymousToken(ctx, challenge, ref)
		if tokenErr != nil {
			return nil, tokenErr
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err = trivyHTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("cve: retry Trivy database: %w", err)
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cve: Trivy blob returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	if err != nil {
		return nil, err
	}
	algorithm, expected, ok := strings.Cut(digest, ":")
	if !ok || algorithm != "sha256" {
		return nil, errors.New("cve: unsupported Trivy layer digest")
	}
	actual := sha256.Sum256(body)
	if !strings.EqualFold(hex.EncodeToString(actual[:]), expected) {
		return nil, errors.New("cve: Trivy layer digest mismatch")
	}
	return body, nil
}

func extractTrivy(root string, compressed []byte, now time.Time) error {
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return fmt.Errorf("cve: open Trivy database layer: %w", err)
	}
	defer reader.Close()
	tmp, err := os.MkdirTemp(filepath.Dir(root), ".trivy-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	archive := tar.NewReader(reader)
	foundDB, foundMetadata := false, false
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("cve: read Trivy archive: %w", err)
		}
		name := filepath.Base(filepath.Clean(header.Name))
		if header.Typeflag != tar.TypeReg || (name != "trivy.db" && name != "metadata.json") {
			continue
		}
		if header.Size <= 0 || header.Size > 1<<30 {
			return fmt.Errorf("cve: invalid Trivy archive entry size for %s", name)
		}
		file, err := os.OpenFile(filepath.Join(tmp, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(file, archive, header.Size)
		syncErr := file.Sync()
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
		if name == "trivy.db" {
			foundDB = true
		} else {
			foundMetadata = true
		}
	}
	if !foundDB || !foundMetadata {
		return errors.New("cve: Trivy archive is missing trivy.db or metadata.json")
	}
	if _, err := readTrivyMetadata(filepath.Join(tmp, "metadata.json")); err != nil {
		return fmt.Errorf("cve: invalid Trivy metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".last_updated"), []byte(now.UTC().Format(time.RFC3339)+"\n"), 0o600); err != nil {
		return err
	}
	return swapDirectory(tmp, root)
}

func swapDirectory(staged, target string) error {
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	old := target + ".previous"
	if _, err := os.Lstat(old); err == nil {
		if _, targetErr := os.Lstat(target); errors.Is(targetErr, os.ErrNotExist) {
			if restoreErr := os.Rename(old, target); restoreErr != nil {
				return fmt.Errorf("cve: recover previous dataset: %w", restoreErr)
			}
		} else {
			return fmt.Errorf("cve: previous dataset backup already exists at %s", old)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cve: inspect previous dataset: %w", err)
	}
	hadOld := false
	if _, err := os.Stat(target); err == nil {
		if err := os.Rename(target, old); err != nil {
			return fmt.Errorf("cve: preserve previous dataset: %w", err)
		}
		hadOld = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(staged, target); err != nil {
		if hadOld {
			if restoreErr := os.Rename(old, target); restoreErr != nil {
				return fmt.Errorf("cve: replace dataset: %v; restore previous dataset: %w", err, restoreErr)
			}
		}
		return fmt.Errorf("cve: replace dataset: %w", err)
	}
	if hadOld {
		if err := os.RemoveAll(old); err != nil {
			return fmt.Errorf("cve: remove previous dataset: %w", err)
		}
	}
	return nil
}

func readLastUpdated(path string) (time.Time, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339, strings.TrimSpace(string(body)))
}
