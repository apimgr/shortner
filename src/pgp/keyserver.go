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

// Keyserver submission tuning, per AI.md PART 11 "Publish to keyservers":
// "Failures are logged + retried with exponential backoff."
const (
	publishAttempts       = 4
	publishInitialBackoff = time.Second
	publishTimeout        = 20 * time.Second
)

// vksUploadPath is the HTTP submission endpoint of a Verifying Key Server
// (keys.openpgp.org and every other VKS deployment, including the Ubuntu
// keyserver), named verbatim by AI.md PART 11.
const vksUploadPath = "/vks/v1/upload"

// PublishResult records one keyserver submission attempt.
type PublishResult struct {
	Keyserver string    `json:"keyserver"`
	URL       string    `json:"url"`
	At        time.Time `json:"at"`
	Attempts  int       `json:"attempts"`
	Err       string    `json:"error,omitempty"`
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
		return u.String(), nil
	}
	u.Path = vksUploadPath
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

	backoff := publishInitialBackoff
	for attempt := 1; attempt <= publishAttempts; attempt++ {
		res.Attempts = attempt
		err = submit(ctx, client, endpoint, payload)
		if err == nil {
			res.Err = ""
			return res
		}
		res.Err = err.Error()
		if attempt == publishAttempts {
			break
		}
		select {
		case <-ctx.Done():
			res.Err = ctx.Err().Error()
			return res
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return res
}

// submit POSTs the VKS upload body once.
func submit(ctx context.Context, client *http.Client, endpoint string, payload []byte) error {
	reqCtx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("keyserver returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// KeyserverState is the persisted `keyservers.state` file AI.md PART 11
// "Backup Integration" lists: which keyservers hold which fingerprint, and
// when they were last told, so a restore does not double-submit.
type KeyserverState struct {
	Fingerprint string                   `json:"fingerprint"`
	Published   map[string]PublishResult `json:"published"`
}

// LoadKeyserverState reads the publish state, returning an empty state
// when the file does not exist yet.
func (s *Store) LoadKeyserverState() (KeyserverState, error) {
	state := KeyserverState{Published: map[string]PublishResult{}}
	raw, err := os.ReadFile(filepath.Join(s.Dir, KeyserverStateName))
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, fmt.Errorf("pgp: read keyserver state: %w", err)
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return KeyserverState{Published: map[string]PublishResult{}}, nil
	}
	if state.Published == nil {
		state.Published = map[string]PublishResult{}
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
		state = KeyserverState{Fingerprint: fingerprint, Published: map[string]PublishResult{}}
	}
	for _, r := range results {
		if r.OK() {
			state.Published[r.Keyserver] = r
		}
	}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("pgp: encode keyserver state: %w", err)
	}
	return writeFile(filepath.Join(s.Dir, KeyserverStateName), append(body, '\n'))
}
