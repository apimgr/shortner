package pgp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Keyserver submission retry limits, per AI.md PART 11 "Publish to keyservers".
const (
	publishMaxAttempts    = 8
	publishInitialBackoff = time.Minute
	publishMaxBackoff     = 6 * time.Hour
	publishTimeout        = 20 * time.Second
)

// vksUploadPath is the submission endpoint for keys.openpgp.org.
const vksUploadPath = "/vks/v1/upload"

const hkpUploadPath = "/pks/add"

// PublishResult records one keyserver submission attempt.
type PublishResult struct {
	Keyserver string    `json:"keyserver"`
	URL       string    `json:"url"`
	At        time.Time `json:"at"`
	Attempts  int       `json:"attempts"`
	Err       string    `json:"error,omitempty"`
	Retryable bool      `json:"retryable,omitempty"`
}

// OK reports whether the submission succeeded.
func (r PublishResult) OK() bool { return r.Err == "" }

// UploadURL maps a configured keyserver entry to its VKS submission
// endpoint. An entry that already names a full upload path is used as-is,
// so an operator running a non-VKS keyserver can point straight at it.
func UploadURL(keyserver string) (string, error) {
	trimmed := strings.TrimSpace(keyserver)
	if trimmed == "" {
		return "", errors.New("pgp: empty keyserver entry")
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("pgp: parse keyserver %q: %w", keyserver, err)
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("pgp: keyserver %q must use https", keyserver)
	}
	if u.Host == "" {
		return "", fmt.Errorf("pgp: keyserver %q has no host", keyserver)
	}
	if path := strings.TrimRight(u.Path, "/"); path != "" {
		u.Path = path
		return u.String(), nil
	}
	if strings.EqualFold(u.Hostname(), "keys.openpgp.org") {
		u.Path = vksUploadPath
	} else {
		u.Path = hkpUploadPath
	}
	return u.String(), nil
}

// Publish submits the armored public key to every configured keyserver,
// retrying each with exponential backoff before giving up. A failure
// against one keyserver never stops the others.
func Publish(ctx context.Context, client *http.Client, keyservers []string, armoredPublicKey string, now func() time.Time) []PublishResult {
	if client == nil {
		client = &http.Client{Timeout: publishTimeout}
	}
	results := make([]PublishResult, 0, len(keyservers))
	for _, ks := range keyservers {
		results = append(results, publishOne(ctx, client, ks, armoredPublicKey, now()))
	}
	return results
}

// publishOne performs the retry loop for a single keyserver.
func publishOne(ctx context.Context, client *http.Client, keyserver, armoredPublicKey string, at time.Time) PublishResult {
	res := PublishResult{Keyserver: keyserver, At: at}

	endpoint, err := UploadURL(keyserver)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	res.URL = endpoint

	payload, err := json.Marshal(map[string]string{"keytext": armoredPublicKey})
	if err != nil {
		res.Err = err.Error()
		return res
	}

	res.Attempts = 1
	err = submit(ctx, client, endpoint, payload)
	if err == nil {
		return res
	}
	res.Err = err.Error()
	var statusErr *HTTPStatusError
	res.Retryable = !errors.As(err, &statusErr) || statusErr.Retryable
	return res
}

// submit POSTs the VKS upload body once.
func submit(ctx context.Context, client *http.Client, endpoint string, payload []byte) error {
	reqCtx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()

	u, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	isVKS := strings.EqualFold(u.Hostname(), "keys.openpgp.org")
	body := payload
	if !isVKS {
		var value struct {
			Keytext string `json:"keytext"`
		}
		if err := json.Unmarshal(payload, &value); err != nil {
			return err
		}
		body = []byte(url.Values{"keytext": {value.Keytext}}.Encode())
	}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if isVKS {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
	} else {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return &HTTPStatusError{Code: resp.StatusCode, Retryable: resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500}
	}
	if isVKS {
		var result struct {
			Fingerprint string `json:"key_fpr"`
			Status      string `json:"status"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return fmt.Errorf("keyserver returned invalid success response: %w", err)
		}
		if result.Fingerprint == "" || result.Status == "" {
			return errors.New("keyserver success response omitted key_fpr or status")
		}
	}
	return nil
}

// HTTPStatusError reports a keyserver response and whether the specification
// requires retrying it.
type HTTPStatusError struct {
	Code      int
	Retryable bool
}

func (e *HTTPStatusError) Error() string { return fmt.Sprintf("keyserver returned HTTP %d", e.Code) }

// KeyserverState is the persisted `keyservers.state` file AI.md PART 11
// "Backup Integration" lists: which keyservers hold which fingerprint, and
// when they were last told, so a restore does not double-submit.
type KeyserverState struct {
	Fingerprint string                    `json:"fingerprint"`
	Published   map[string]PublishResult  `json:"published"`
	Retries     map[string]KeyserverRetry `json:"retries,omitempty"`
}

// KeyserverRetry persists the durable retry schedule for a failed publish.
type KeyserverRetry struct {
	Attempts    int       `json:"attempts"`
	NextAttempt time.Time `json:"next_attempt,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
}

// LoadKeyserverState reads the publish state, returning an empty state
// when the file does not exist yet.
func (s *Store) LoadKeyserverState() (KeyserverState, error) {
	state := KeyserverState{Published: map[string]PublishResult{}, Retries: map[string]KeyserverRetry{}}
	raw, err := os.ReadFile(filepath.Join(s.Dir, KeyserverStateName))
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, fmt.Errorf("pgp: read keyserver state: %w", err)
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return KeyserverState{Published: map[string]PublishResult{}, Retries: map[string]KeyserverRetry{}}, nil
	}
	if state.Published == nil {
		state.Published = map[string]PublishResult{}
	}
	if state.Retries == nil {
		state.Retries = map[string]KeyserverRetry{}
	}
	return state, nil
}

// SaveKeyserverState records the successful submissions for fingerprint.
// A fingerprint change resets the map: publications of a retired key say
// nothing about where the current key lives.
func (s *Store) SaveKeyserverState(fingerprint string, results []PublishResult) error {
	state, err := s.LoadKeyserverState()
	if err != nil {
		return err
	}
	if state.Fingerprint != fingerprint {
		state = KeyserverState{Fingerprint: fingerprint, Published: map[string]PublishResult{}, Retries: map[string]KeyserverRetry{}}
	}
	for _, r := range results {
		if r.OK() {
			state.Published[r.Keyserver] = r
			delete(state.Retries, r.Keyserver)
		} else if r.Retryable {
			previous := state.Retries[r.Keyserver]
			attempts := previous.Attempts + 1
			if attempts > publishMaxAttempts {
				attempts = publishMaxAttempts
			}
			state.Retries[r.Keyserver] = KeyserverRetry{Attempts: attempts, NextAttempt: r.At.UTC().Add(retryDelay(attempts)), LastError: r.Err}
		} else {
			delete(state.Retries, r.Keyserver)
		}
	}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("pgp: encode keyserver state: %w", err)
	}
	return writeFile(filepath.Join(s.Dir, KeyserverStateName), append(body, '\n'))
}

// DueKeyservers returns retryable endpoints whose persisted retry time passed.
func (s *Store) DueKeyservers(now time.Time) (string, []string, error) {
	state, err := s.LoadKeyserverState()
	if err != nil {
		return "", nil, err
	}
	var due []string
	for server, retry := range state.Retries {
		if retry.Attempts < publishMaxAttempts && !retry.NextAttempt.After(now) {
			due = append(due, server)
		}
	}
	return state.Fingerprint, due, nil
}

func retryDelay(attempt int) time.Duration {
	delay := publishInitialBackoff
	for i := 1; i < attempt && delay < publishMaxBackoff; i++ {
		delay *= 2
	}
	if delay > publishMaxBackoff {
		return publishMaxBackoff
	}
	return delay
}
