// Package api is the client's HTTP transport to the shortner server. Every
// URL it builds goes through src/common/urlutil so no user input is ever
// interpolated raw into a request path, per AI.md PART 32.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/common/urlutil"
)

// ErrNotFound is returned when the server answers 404 for a link.
var ErrNotFound = errors.New("not found")

// ErrUnauthorized is returned when the server rejects the supplied token.
var ErrUnauthorized = errors.New("unauthorized")

// ErrTokenRevoked is returned when the server reports a revoked token, which
// maps to the `cli.token_revoked_detected` audit event in AI.md PART 32.
var ErrTokenRevoked = errors.New("token revoked")

// Link mirrors the server's LinkResponse.
type Link struct {
	ShortCode      string  `json:"short_code"`
	ShortURL       string  `json:"short_url"`
	DestinationURL string  `json:"destination_url"`
	CreatedAt      string  `json:"created_at"`
	ExpiresAt      *string `json:"expires_at"`
	ClickCount     int64   `json:"click_count"`
}

// CreatedLink is a Link plus the one-time owner token issued at creation.
type CreatedLink struct {
	Link
	OwnerToken string `json:"owner_token"`
}

// Pagination mirrors the server's pagination envelope.
type Pagination struct {
	Page  int   `json:"page"`
	Limit int   `json:"limit"`
	Total int64 `json:"total"`
	Pages int   `json:"pages"`
}

// LinkList is a paginated listing of links.
type LinkList struct {
	Data       []Link     `json:"data"`
	Pagination Pagination `json:"pagination"`
}

// DayCount is one point of a click time series.
type DayCount struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

// ClickInfo is a single recent click, already anonymized server-side.
type ClickInfo struct {
	Timestamp string `json:"timestamp"`
	IP        string `json:"ip"`
	Referrer  string `json:"referrer"`
	Country   string `json:"country"`
	Region    string `json:"region"`
}

// Stats mirrors the server's StatsResponse.
type Stats struct {
	ShortCode   string         `json:"short_code"`
	TotalClicks int64          `json:"total_clicks"`
	Referrers   map[string]int `json:"referrers"`
	TimeSeries  []DayCount     `json:"time_series"`
	Recent      []ClickInfo    `json:"recent"`
}

// Health is the subset of /server/healthz the client renders.
type Health struct {
	Status    string `json:"status"`
	Version   string `json:"version"`
	GoVersion string `json:"go_version"`
	Mode      string `json:"mode"`
	Uptime    string `json:"uptime"`
	Project   struct {
		Name string `json:"name"`
	} `json:"project"`
}

// CLIVersion is one platform entry of an autodiscover response.
type CLIVersion struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

// Autodiscover is the subset of /api/autodiscover the client's self-update
// flow consumes (AI.md PART 32 "CLI Auto-Update").
type Autodiscover struct {
	CLIVersions   map[string]CLIVersion `json:"cli_versions"`
	CLIMinVersion string                `json:"cli_min_version"`
}

// apiError is the server's error envelope (src/apperr).
type apiError struct {
	Success bool `json:"success"`
	Error   struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Message string `json:"message"`
}

// Client talks to one shortner server.
type Client struct {
	baseURL    string
	apiVersion string
	httpClient *http.Client
	userAgent  string
	token      string
	retry      int
	retryDelay time.Duration
}

// Options configures a Client.
type Options struct {
	BaseURL    string
	APIVersion string
	UserAgent  string
	Token      string
	Timeout    time.Duration
	Retry      int
	RetryDelay time.Duration
}

// New builds a Client. The base URL is normalized so a trailing slash or a
// missing scheme never produces a malformed request.
func New(opts Options) *Client {
	base := strings.TrimSuffix(strings.TrimSpace(opts.BaseURL), "/")
	if base != "" && !strings.Contains(base, "://") {
		base = "https://" + base
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	apiVersion := opts.APIVersion
	if apiVersion == "" {
		apiVersion = "v1"
	}
	return &Client{
		baseURL:    base,
		apiVersion: apiVersion,
		httpClient: &http.Client{Timeout: timeout},
		userAgent:  opts.UserAgent,
		token:      opts.Token,
		retry:      opts.Retry,
		retryDelay: opts.RetryDelay,
	}
}

// BaseURL reports the normalized server base URL.
func (c *Client) BaseURL() string { return c.baseURL }

// SetToken replaces the bearer token used for subsequent requests.
func (c *Client) SetToken(token string) { c.token = token }

// apiPath prefixes a resource path with the configured API version.
func (c *Client) apiPath(suffix string) string {
	return "/api/" + c.apiVersion + suffix
}

// do performs one API request, retrying only idempotent GETs on transport
// failure. It decodes into out when out is non-nil.
func (c *Client) do(ctx context.Context, method, url string, body any, out any) error {
	if c.baseURL == "" {
		return errors.New("no server configured: pass --server URL or run the setup wizard")
	}

	var payload []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = encoded
	}

	attempts := 1
	if method == http.MethodGet && c.retry > 0 {
		attempts += c.retry
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 && c.retryDelay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.retryDelay):
			}
		}

		var reader io.Reader
		if payload != nil {
			reader = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, reader)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", c.userAgent)
		req.Header.Set("Accept", "application/json")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("connect to %s: %w", c.baseURL, err)
			continue
		}

		err = decodeResponse(resp, out)
		if err != nil {
			return err
		}
		return nil
	}

	return lastErr
}

// decodeResponse reads the body, maps error statuses to sentinel errors, and
// unmarshals a successful payload into out.
func decodeResponse(resp *http.Response, out any) error {
	defer func() {
		_ = resp.Body.Close()
	}()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}

	if resp.StatusCode >= 400 {
		return statusError(resp.StatusCode, data)
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// statusError converts an error response into a typed error, preferring the
// server's own message when the envelope carries one.
func statusError(status int, body []byte) error {
	message := strings.TrimSpace(string(body))
	code := ""
	var envelope apiError
	if json.Unmarshal(body, &envelope) == nil {
		if envelope.Error.Message != "" {
			message = envelope.Error.Message
			code = envelope.Error.Code
		} else if envelope.Message != "" {
			message = envelope.Message
		}
	}
	if message == "" {
		message = http.StatusText(status)
	}

	switch {
	case status == http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, message)
	case status == http.StatusUnauthorized && code == "TOKEN_REVOKED":
		return fmt.Errorf("%w: %s", ErrTokenRevoked, message)
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Errorf("%w: %s", ErrUnauthorized, message)
	default:
		return fmt.Errorf("server returned %d: %s", status, message)
	}
}

// CreateLink shortens a destination URL, optionally under a custom slug and
// with an expiration timestamp.
func (c *Client) CreateLink(ctx context.Context, destination, slug, expiresAt string) (*CreatedLink, error) {
	body := map[string]string{"url": destination}
	if slug != "" {
		body["slug"] = slug
	}
	if expiresAt != "" {
		body["expires_at"] = expiresAt
	}

	url := urlutil.BuildAPIURL(c.baseURL, c.apiPath("/links"), nil, nil)
	var created CreatedLink
	if err := c.do(ctx, http.MethodPost, url, body, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

// GetLink fetches one link by short code or slug.
func (c *Client) GetLink(ctx context.Context, slug string) (*Link, error) {
	url := urlutil.BuildAPIURL(c.baseURL, c.apiPath("/links/{slug}"), map[string]string{"slug": slug}, nil)
	var link Link
	if err := c.do(ctx, http.MethodGet, url, nil, &link); err != nil {
		return nil, err
	}
	return &link, nil
}

// ListLinks fetches a page of the public link listing.
func (c *Client) ListLinks(ctx context.Context, page, limit int) (*LinkList, error) {
	query := map[string]string{}
	if page > 0 {
		query["page"] = strconv.Itoa(page)
	}
	if limit > 0 {
		query["limit"] = strconv.Itoa(limit)
	}
	url := urlutil.BuildAPIURL(c.baseURL, c.apiPath("/links"), nil, query)
	var list LinkList
	if err := c.do(ctx, http.MethodGet, url, nil, &list); err != nil {
		return nil, err
	}
	return &list, nil
}

// UpdateLink changes a link's destination or expiration. It requires the
// link's owner token or the operator token.
func (c *Client) UpdateLink(ctx context.Context, slug, destination, expiresAt string) (*Link, error) {
	body := map[string]string{}
	if destination != "" {
		body["url"] = destination
	}
	if expiresAt != "" {
		body["expires_at"] = expiresAt
	}
	if len(body) == 0 {
		return nil, errors.New("nothing to update: pass --url or --expire")
	}

	url := urlutil.BuildAPIURL(c.baseURL, c.apiPath("/links/{slug}"), map[string]string{"slug": slug}, nil)
	var link Link
	if err := c.do(ctx, http.MethodPatch, url, body, &link); err != nil {
		return nil, err
	}
	return &link, nil
}

// DeleteLink removes a link. It requires the link's owner token or the
// operator token.
func (c *Client) DeleteLink(ctx context.Context, slug string) error {
	url := urlutil.BuildAPIURL(c.baseURL, c.apiPath("/links/{slug}"), map[string]string{"slug": slug}, nil)
	return c.do(ctx, http.MethodDelete, url, nil, nil)
}

// GetStats fetches a link's public click analytics.
func (c *Client) GetStats(ctx context.Context, slug string) (*Stats, error) {
	url := urlutil.BuildAPIURL(c.baseURL, c.apiPath("/links/{slug}/stats"), map[string]string{"slug": slug}, nil)
	var stats Stats
	if err := c.do(ctx, http.MethodGet, url, nil, &stats); err != nil {
		return nil, err
	}
	return &stats, nil
}

// Health fetches the server's health document.
func (c *Client) Health(ctx context.Context) (*Health, error) {
	url := urlutil.BuildAPIURL(c.baseURL, c.apiPath("/server/healthz"), nil, nil)
	var health Health
	if err := c.do(ctx, http.MethodGet, url, nil, &health); err != nil {
		return nil, err
	}
	return &health, nil
}

// Autodiscover fetches the server's autodiscover document. A server that
// does not publish the endpoint yields ErrNotFound, which callers treat as
// "no update information available" rather than a failure.
func (c *Client) Autodiscover(ctx context.Context) (*Autodiscover, error) {
	url := urlutil.BuildAPIURL(c.baseURL, "/api/autodiscover", nil, nil)
	var doc Autodiscover
	if err := c.do(ctx, http.MethodGet, url, nil, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// DownloadCLIBinary streams a published client binary for the given
// os-arch pair into w and reports how many bytes were written.
func (c *Client) DownloadCLIBinary(ctx context.Context, projectName, osArch string, w io.Writer) (int64, error) {
	name := projectName + "-cli-" + osArch
	url := urlutil.BuildAPIURL(c.baseURL, "/cli/binaries/{name}", map[string]string{"name": name}, nil)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("connect to %s: %w", c.baseURL, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return 0, statusError(resp.StatusCode, data)
	}
	return io.Copy(w, resp.Body)
}
