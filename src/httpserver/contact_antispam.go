package httpserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/apimgr/shortner/src/applog"
)

const contactFormBodyLimit = 16 << 10

var contactURLPattern = regexp.MustCompile(`(?i)(https?://|www\.)`)

type contactSpamBucket struct {
	start time.Time
	count int
}

type contactSpamDuplicate struct {
	acceptedAt time.Time
	lastUsed   time.Time
}

type contactSpamState struct {
	mu         sync.Mutex
	buckets    map[string]contactSpamBucket
	duplicates map[string]contactSpamDuplicate
}

var contactSpamStates sync.Map

func contactSpamStateFor(secret string) *contactSpamState {
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(secret)))
	state, _ := contactSpamStates.LoadOrStore(key, &contactSpamState{
		buckets: make(map[string]contactSpamBucket), duplicates: make(map[string]contactSpamDuplicate),
	})
	return state.(*contactSpamState)
}

func issueContactFormToken(secret string, now time.Time) string {
	issued := now.Unix()
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "contact-form|%d", issued)
	return fmt.Sprintf("%d.%s", issued, hex.EncodeToString(mac.Sum(nil)))
}

func verifyContactFormToken(secret, token string, now time.Time, minAge, maxAge int) bool {
	issuedText, signature, found := strings.Cut(token, ".")
	if !found || strings.Contains(signature, ".") || len(signature) != sha256.Size*2 {
		return false
	}
	issued, err := strconv.ParseInt(issuedText, 10, 64)
	if err != nil {
		return false
	}
	age := now.Unix() - issued
	if age < int64(minAge) || age < 0 || (maxAge > 0 && age > int64(maxAge)) {
		return false
	}
	provided, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "contact-form|%d", issued)
	return hmac.Equal(provided, mac.Sum(nil))
}

func contactAllowedFields(security bool) map[string]struct{} {
	fields := map[string]struct{}{
		"csrf_token": {}, "name": {}, "email": {}, "subject": {}, "message": {},
		"captcha_response": {}, "captcha_id": {}, "captcha_answer": {},
		"g-recaptcha-response": {}, "h-captcha-response": {}, "credit_handle": {},
		"app_version": {}, "commit_hash": {}, "timestamp": {}, "request_user_agent": {},
		"website": {}, "form_token": {},
	}
	if security {
		for _, field := range []string{
			"security_id", "researcher_gpg", "component", "component_other", "endpoint",
			"severity", "summary", "steps", "impact", "suggested_fix", "cve_requested",
			"disclosure_days", "credit_preference", "agreed_disclosure",
		} {
			fields[field] = struct{}{}
		}
	}
	return fields
}

func contactShapeValid(values url.Values, security, api bool) (string, bool) {
	if api {
		if _, exists := values["csrf_token"]; exists {
			return "shape", false
		}
	}
	allowed := contactAllowedFields(security)
	for name, entries := range values {
		if _, ok := allowed[name]; !ok {
			return "shape", false
		}
		if len(entries) != 1 {
			return "shape", false
		}
		for _, value := range entries {
			if !utf8.ValidString(value) || containsUnsafeUnicode(value) {
				return "shape", false
			}
		}
	}
	for _, name := range []string{"name", "email", "subject"} {
		for _, r := range values.Get(name) {
			if unicode.IsControl(r) {
				return "shape", false
			}
		}
	}
	for _, required := range []string{"name", "email", "form_token"} {
		if _, ok := values[required]; !ok {
			return "shape", false
		}
	}
	providerProofs := 0
	for _, name := range []string{"captcha_response", "g-recaptcha-response", "h-captcha-response"} {
		if values.Get(name) != "" {
			providerProofs++
		}
	}
	if providerProofs > 1 {
		return "shape", false
	}
	providerProof := providerProofs == 1
	simpleID, simpleAnswer := values.Get("captcha_id") != "", values.Get("captcha_answer") != ""
	if simpleID != simpleAnswer || (!providerProof && !(simpleID && simpleAnswer)) {
		return "shape", false
	}
	if !api {
		if _, ok := values["website"]; !ok {
			return "shape", false
		}
	}
	if !security {
		for _, required := range []string{"subject", "message"} {
			if _, ok := values[required]; !ok {
				return "shape", false
			}
		}
	}
	name, email, subject, message := values.Get("name"), values.Get("email"), values.Get("subject"), values.Get("message")
	if utf8.RuneCountInString(name) > 100 || utf8.RuneCountInString(email) > 254 || utf8.RuneCountInString(subject) > 200 || utf8.RuneCountInString(message) > 5000 || utf8.RuneCountInString(values.Get("website")) > 200 || utf8.RuneCountInString(values.Get("form_token")) > 100 {
		return "shape", false
	}
	if security {
		limits := map[string]int{"researcher_gpg": 16384, "component": 255, "component_other": 255, "endpoint": 2048, "summary": 200, "steps": 10000, "impact": 5000, "suggested_fix": 5000, "credit_handle": 100}
		for field, limit := range limits {
			if utf8.RuneCountInString(values.Get(field)) > limit {
				return "shape", false
			}
		}
		if strings.HasPrefix(values.Get("researcher_gpg"), "https://") && utf8.RuneCountInString(values.Get("researcher_gpg")) > 2048 {
			return "shape", false
		}
	}
	if strings.ContainsAny(email, ",;<>\r\n") {
		return "shape", false
	}
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || parsed.Name != "" {
		return "shape", false
	}
	return "", true
}

func containsUnsafeUnicode(value string) bool {
	for _, r := range value {
		if r == '\u200b' || r == '\u200c' || r == '\u200d' || r == '\u2060' || r == '\ufeff' || (r >= '\u202a' && r <= '\u202e') || (r >= '\u2066' && r <= '\u2069') {
			return true
		}
	}
	return false
}

func (fd *frontendDeps) contactToken(now time.Time) string {
	return issueContactFormToken(fd.installSecret, now)
}

func (fd *frontendDeps) contactSpamBlock(r *http.Request, reason string) {
	if fd.metrics != nil && fd.metrics.ContactBlockedTotal != nil {
		fd.metrics.ContactBlockedTotal.WithLabelValues(reason).Inc()
	}
	if fd.audit != nil {
		ip := ""
		if fd.resolver != nil {
			ip = fd.resolver.ResolveClientIP(r)
		} else {
			ip, _, _ = net.SplitHostPort(r.RemoteAddr)
		}
		_ = fd.audit.Write(applog.Entry{
			Time: time.Now().UTC(), Event: "contact.spam_blocked", Category: "security",
			Severity: applog.SeverityWarn, Actor: applog.Actor{IP: ip},
			Details: map[string]any{"reason": reason, "client_ip": ip}, Result: applog.ResultFailure,
		})
	}
}

func (fd *frontendDeps) contactTokenValid(token string, now time.Time) bool {
	settings := fd.contactAntispam()
	return verifyContactFormToken(fd.installSecret, token, now, settings.MinFillSeconds, settings.MaxAgeSeconds)
}

func emailDomain(email string) string {
	_, host, ok := strings.Cut(strings.ToLower(email), "@")
	if !ok {
		return ""
	}
	return host
}

func (fd *frontendDeps) contactContentBlocked(name, email, subject, message string) bool {
	settings := fd.contactAntispam()
	if settings.MaxLinks > 0 && len(contactURLPattern.FindAllStringIndex(message, -1)) > settings.MaxLinks {
		return true
	}
	if contactURLPattern.MatchString(subject) || contactURLPattern.MatchString(name) {
		return true
	}
	domain := emailDomain(email)
	for _, blocked := range settings.BlockedEmailDomains {
		if domain == strings.ToLower(strings.TrimSpace(blocked)) {
			return true
		}
	}
	for _, blocked := range settings.BlockedWords {
		needle := strings.ToLower(strings.TrimSpace(blocked))
		if needle != "" && (strings.Contains(strings.ToLower(message), needle) || strings.Contains(strings.ToLower(subject), needle) || strings.Contains(strings.ToLower(name), needle)) {
			return true
		}
	}
	return false
}

func (fd *frontendDeps) contactDuplicateKey(email, subject, message string) string {
	normalize := func(value string) string {
		return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
	}
	sum := sha256.Sum256([]byte(normalize(email) + "\n" + normalize(subject) + "\n" + normalize(message)))
	return hex.EncodeToString(sum[:])
}

func (fd *frontendDeps) contactRateReason(r *http.Request, email string, now time.Time) (string, bool) {
	state := contactSpamStateFor(fd.installSecret)
	settings := fd.contactAntispam()
	window, err := time.ParseDuration(settings.Window)
	if err != nil || window <= 0 {
		window = time.Hour
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	for key, bucket := range state.buckets {
		if now.Sub(bucket.start) >= window {
			delete(state.buckets, key)
		}
	}
	if fd.resolver == nil || !fd.resolver.IsOverlay(r) {
		ip := ""
		if fd.resolver != nil {
			ip = fd.resolver.ResolveClientIP(r)
		} else {
			ip, _, _ = net.SplitHostPort(r.RemoteAddr)
		}
		if settings.PerIP > 0 && state.buckets["ip:"+ip].count >= settings.PerIP {
			return "rate_ip", true
		}
	}
	if settings.PerEmail > 0 && state.buckets["email:"+strings.ToLower(email)].count >= settings.PerEmail {
		return "rate_email", true
	}
	if settings.Global > 0 && state.buckets["global"].count >= settings.Global {
		return "rate_global", true
	}
	return "", false
}

func (fd *frontendDeps) contactRecordAccepted(r *http.Request, email, subject, message string, now time.Time, deduplicate bool) (string, bool) {
	state := contactSpamStateFor(fd.installSecret)
	settings := fd.contactAntispam()
	window, err := time.ParseDuration(settings.Window)
	if err != nil || window <= 0 {
		window = time.Hour
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	for key, bucket := range state.buckets {
		if now.Sub(bucket.start) >= window {
			delete(state.buckets, key)
		}
	}
	if len(state.buckets) > 10000 {
		for len(state.buckets) > 9000 {
			oldestKey := ""
			var oldest time.Time
			for key, bucket := range state.buckets {
				if oldestKey == "" || bucket.start.Before(oldest) {
					oldestKey, oldest = key, bucket.start
				}
			}
			delete(state.buckets, oldestKey)
		}
	}
	ip := ""
	if fd.resolver == nil || !fd.resolver.IsOverlay(r) {
		if fd.resolver != nil {
			ip = fd.resolver.ResolveClientIP(r)
		}
		if settings.PerIP > 0 && state.buckets["ip:"+ip].count >= settings.PerIP {
			return "rate_ip", true
		}
	}
	if settings.PerEmail > 0 && state.buckets["email:"+strings.ToLower(email)].count >= settings.PerEmail {
		return "rate_email", true
	}
	if settings.Global > 0 && state.buckets["global"].count >= settings.Global {
		return "rate_global", true
	}
	if state.duplicates == nil {
		state.duplicates = make(map[string]contactSpamDuplicate)
	}
	duplicateWindow := time.Duration(settings.DuplicateWindowHours) * time.Hour
	for key, duplicate := range state.duplicates {
		if duplicateWindow <= 0 || now.Sub(duplicate.acceptedAt) >= duplicateWindow {
			delete(state.duplicates, key)
		}
	}
	if deduplicate && duplicateWindow > 0 {
		key := fd.contactDuplicateKey(email, subject, message)
		if duplicate, exists := state.duplicates[key]; exists {
			duplicate.lastUsed = now
			state.duplicates[key] = duplicate
			return "duplicate", true
		}
		if len(state.duplicates) >= 10000 {
			oldestKey := ""
			var oldest time.Time
			for candidate, duplicate := range state.duplicates {
				if oldestKey == "" || duplicate.lastUsed.Before(oldest) {
					oldestKey, oldest = candidate, duplicate.lastUsed
				}
			}
			delete(state.duplicates, oldestKey)
		}
		state.duplicates[key] = contactSpamDuplicate{acceptedAt: now, lastUsed: now}
	}
	settingsWindow := window
	keys := []string{"email:" + strings.ToLower(email), "global"}
	if fd.resolver == nil || !fd.resolver.IsOverlay(r) {
		keys = append(keys, "ip:"+ip)
	}
	for _, key := range keys {
		bucket := state.buckets[key]
		if bucket.start.IsZero() || now.Sub(bucket.start) >= settingsWindow {
			bucket = contactSpamBucket{start: now}
		}
		bucket.count++
		state.buckets[key] = bucket
	}
	return "", false
}
