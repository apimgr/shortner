package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
	"github.com/google/uuid"
)

type webhookPayload struct {
	Role      string `json:"role"`
	Event     string `json:"event"`
	Subject   string `json:"subject"`
	Body      string `json:"body"`
	Severity  string `json:"severity"`
	Timestamp string `json:"timestamp"`
	// AppName is used by provider adapters and omitted from the generic payload.
	AppName        string `json:"-"`
	ProjectName    string `json:"project_name"`
	ProjectVersion string `json:"project_version"`
	AppURL         string `json:"app_url"`
	TrackingID     string `json:"tracking_id,omitempty"`
}

type webhookMessage struct {
	payload webhookPayload
	secret  string
	urls    map[string]string
	id      string
}

type webhookSender struct {
	mu      sync.Mutex
	logged  map[string]time.Time
	contact config.Contact
	client  *http.Client
	name    string
	appName string
	version string
	appURL  string
	log     *applog.Logger
}

func newWebhookSender(contact config.Contact, name, appName, version, appURL string, logger *applog.Logger) *webhookSender {
	if strings.TrimSpace(appName) == "" {
		appName = name
	}
	return &webhookSender{
		contact: contact,
		client:  &http.Client{Timeout: 10 * time.Second},
		logged:  map[string]time.Time{},
		name:    name,
		appName: appName,
		version: version,
		appURL:  appURL,
		log:     logger,
	}
}

func (s *webhookSender) dispatch(role, event, subject, body, severity, trackingID string) {
	if s == nil {
		return
	}
	urls, secrets := s.resolve(role)
	if len(urls) == 0 {
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	payload := webhookPayload{
		Role: role, Event: event, Subject: subject, Body: body,
		Severity: normalizeSeverity(severity), Timestamp: now.Format(time.RFC3339),
		AppName: s.appName, ProjectName: s.name, ProjectVersion: s.version, AppURL: s.appURL, TrackingID: trackingID,
	}
	id, err := uuid.NewV7()
	if err != nil {
		s.logFailure("admin", "uuid", "id_generation")
		return
	}
	message := webhookMessage{payload: payload, urls: urls, secret: "", id: id.String()}
	for transport, endpoint := range urls {
		transport, endpoint := transport, endpoint
		message.secret = secrets[transport]
		go s.deliver(transport, endpoint, message)
	}
}

func (s *webhookSender) resolve(role string) (map[string]string, map[string]string) {
	var roles []config.ContactRecipient
	switch role {
	case "admin":
		roles = []config.ContactRecipient{s.contact.Admin}
	case "security":
		roles = []config.ContactRecipient{s.contact.Security, s.contact.Admin}
	case "abuse":
		roles = []config.ContactRecipient{s.contact.Abuse, s.contact.General, s.contact.Admin}
	case "general":
		roles = []config.ContactRecipient{s.contact.General, s.contact.Admin}
	default:
		return nil, nil
	}
	urls, secrets := map[string]string{}, map[string]string{}
	for _, recipient := range roles {
		for transport, endpoint := range recipient.Webhooks {
			if strings.HasSuffix(transport, "_secret") || strings.TrimSpace(endpoint) == "" || urls[transport] != "" {
				continue
			}
			urls[transport] = strings.TrimSpace(endpoint)
			secrets[transport] = strings.TrimSpace(recipient.Webhooks[transport+"_secret"])
		}
	}
	return urls, secrets
}

func (s *webhookSender) deliver(transport, endpoint string, message webhookMessage) {
	delays := []time.Duration{0, time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour}
	for attempt, delay := range delays {
		if delay > 0 {
			time.Sleep(delay)
		}
		if err := s.sendOnce(transport, endpoint, message); err == nil {
			return
		}
		if attempt == len(delays)-1 {
			s.logFailure(message.payload.Role, transport, message.payload.Event)
		}
	}
}

func (s *webhookSender) logFailure(role, transport, event string) {
	if s == nil || s.log == nil {
		return
	}
	key := role + "." + transport
	now := time.Now()
	s.mu.Lock()
	if last, ok := s.logged[key]; ok && now.Sub(last) < time.Minute {
		s.mu.Unlock()
		return
	}
	s.logged[key] = now
	s.mu.Unlock()
	_ = s.log.WriteLine(applog.LevelError, "notify.webhook_failed role="+role+" event="+event+" transport="+transport+"\n")
}

func (s *webhookSender) sendOnce(transport, endpoint string, message webhookMessage) (resultErr error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return errors.New("notify: webhook endpoint must be an HTTPS URL")
	}
	body, contentType, err := webhookRequest(transport, u, message.payload)
	if err != nil {
		return err
	}
	timestamp, err := time.Parse(time.RFC3339, message.payload.Timestamp)
	if err != nil {
		return errors.New("notify: invalid webhook timestamp")
	}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return errors.New("notify: invalid webhook request")
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("User-Agent", s.name+"/"+s.version+" (+"+s.appURL+")")
	request.Header.Set("X-Webhook-Timestamp", fmt.Sprint(timestamp.Unix()))
	request.Header.Set("X-Webhook-ID", message.id)
	request.Header.Set("X-Webhook-Event", message.payload.Event)
	mac := hmac.New(sha256.New, []byte(message.secret))
	_, _ = mac.Write(body)
	request.Header.Set("X-Webhook-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	client := *s.client
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("notify: webhook delivery failed")
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("notify: close webhook response: %w", err))
		}
	}()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return errors.New("notify: webhook receiver returned a non-success status")
	}
	return nil
}

func webhookRequest(transport string, endpoint *url.URL, payload webhookPayload) ([]byte, string, error) {
	priority := 2
	switch payload.Severity {
	case "warning":
		priority = 5
	case "critical":
		priority = 8
	}
	switch strings.ToLower(transport) {
	case "telegram":
		query := endpoint.Query()
		query.Set("text", payload.Subject+"\n\n"+payload.Body)
		endpoint.RawQuery = query.Encode()
		return nil, "application/json", nil
	case "discord":
		return marshalWebhookJSON(map[string]string{"content": payload.Subject + "\n\n" + payload.Body, "username": payload.AppName})
	case "slack", "mattermost":
		return marshalWebhookJSON(map[string]string{"text": payload.Subject + "\n\n" + payload.Body})
	case "pushover":
		query := endpoint.Query()
		values := url.Values{"user": {query.Get("user")}, "token": {query.Get("token")}, "title": {payload.Subject}, "message": {payload.Body}, "priority": {fmt.Sprint(map[bool]int{true: 1, false: 0}[payload.Severity == "critical"])}}
		return []byte(values.Encode()), "application/x-www-form-urlencoded", nil
	case "gotify":
		query := endpoint.Query()
		token := query.Get("token")
		query = url.Values{}
		query.Set("token", token)
		if !strings.HasSuffix(strings.TrimRight(endpoint.Path, "/"), "/message") {
			endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/message"
		}
		endpoint.RawQuery = query.Encode()
		return marshalWebhookJSON(map[string]any{"title": payload.Subject, "message": payload.Body, "priority": priority})
	case "generic":
		return marshalWebhookJSON(payload)
	default:
		return nil, "", errors.New("notify: unsupported webhook transport")
	}
}

func marshalWebhookJSON(value any) ([]byte, string, error) {
	body, err := json.Marshal(value)
	return body, "application/json", err
}

func normalizeSeverity(severity string) string {
	switch severity {
	case "info", "warning", "critical":
		return severity
	case "error":
		return "critical"
	case "warn":
		return "warning"
	default:
		return "info"
	}
}
