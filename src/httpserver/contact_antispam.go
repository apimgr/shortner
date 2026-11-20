package httpserver

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/fqdn"
)

const contactFormBodyLimit = 256 << 10

var contactLinkPattern = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://[^\s<>"']+|www\.[a-z0-9.-]+\.[a-z]{2,}|(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,})(?:/[^\s<>"']*)?`)
var contactMarkupPattern = regexp.MustCompile(`(?i)<\s*(?:/?\s*[a-z]|!)|\[(?:url|img|b)(?:\s|=|\])|\[/`)

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
	strikes    map[string][]time.Time
	bans       map[string]time.Time
	usedTokens map[string]time.Time
}

var contactSpamStates sync.Map

func contactSpamStateFor(secret string) *contactSpamState {
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(secret)))
	state, _ := contactSpamStates.LoadOrStore(key, &contactSpamState{
		buckets: make(map[string]contactSpamBucket), duplicates: make(map[string]contactSpamDuplicate),
		strikes: make(map[string][]time.Time), bans: make(map[string]time.Time), usedTokens: make(map[string]time.Time),
	})
	return state.(*contactSpamState)
}

func issueContactFormToken(secret string, now time.Time) string {
	issued := now.Unix()
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return ""
	}
	nonceText := base64.RawURLEncoding.EncodeToString(nonce)
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "contact-form|%d|%s", issued, nonceText)
	return fmt.Sprintf("%d.%s.%s", issued, nonceText, base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))
}

func verifyContactFormToken(secret, token string, now time.Time, minAge, maxAge int) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(parts[1]) != 22 || len(parts[2]) != 43 {
		return "", false
	}
	issuedText, nonce, signature := parts[0], parts[1], parts[2]
	issued, err := strconv.ParseInt(issuedText, 10, 64)
	if err != nil {
		return "", false
	}
	age := now.Unix() - issued
	if age < int64(minAge) || age < 0 || (maxAge > 0 && age > int64(maxAge)) {
		return "", false
	}
	provided, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "contact-form|%d|%s", issued, nonce)
	return nonce, hmac.Equal(provided, mac.Sum(nil))
}

func contactAllowedFields(security bool) map[string]struct{} {
	fields := map[string]struct{}{
		"csrf_token": {}, "name": {}, "email": {}, "subject": {}, "message": {},
		"captcha_response": {}, "captcha_id": {}, "captcha_answer": {},
		"g-recaptcha-response": {}, "h-captcha-response": {}, "credit_handle": {},
		"app_version": {}, "commit_hash": {}, "timestamp": {}, "request_user_agent": {},
		"website": {}, "form_token": {},
		"step": {}, "review_token": {}, "confirmed": {},
	}
	if security {
		for _, field := range []string{
			"security_id", "researcher_gpg", "component", "component_other", "endpoint",
			"severity", "summary", "steps", "impact", "suggested_fix", "cve_requested",
			"disclosure_days", "credit_preference", "agreed_disclosure",
		} {
			fields[field] = struct{}{}
		}
	} else {
		for _, field := range []string{
			"type", "reported_url", "reason", "copyrighted_work", "good_faith_statement",
			"accuracy_statement", "signature", "request_kind",
		} {
			fields[field] = struct{}{}
		}
	}
	return fields
}

func contactShapeValid(values url.Values, security, api bool) (string, bool) {
	step := values.Get("step")
	if step != "" && step != "submit" && step != "confirm" && step != "edit" {
		return "shape", false
	}
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
	if contactCaptchaProofInvalid(step, simpleID, simpleAnswer, providerProof) {
		return "shape", false
	}
	if step == "confirm" && values.Get("confirmed") != "true" && values.Get("confirmed") != "" {
		return "shape", false
	}
	if !api {
		if _, ok := values["website"]; !ok {
			return "shape", false
		}
	}
	if !security {
		for _, required := range []string{"subject", "message", "type"} {
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
	if !security {
		limits := map[string]int{"reported_url": 2048, "copyrighted_work": 2000, "signature": 100}
		for field, limit := range limits {
			if utf8.RuneCountInString(values.Get(field)) > limit {
				return "shape", false
			}
		}
		typeValue := values.Get("type")
		if typeValue != "abuse" && typeValue != "dmca" && typeValue != "privacy" {
			return "shape", false
		}
		switch typeValue {
		case "abuse":
			if !validReportedURL(values.Get("reported_url")) || !oneOf(values.Get("reason"), "illegal_content", "terms_violation", "harassment", "malware_phishing", "other") {
				return "shape", false
			}
		case "dmca":
			if !validReportedURL(values.Get("reported_url")) || values.Get("copyrighted_work") == "" || values.Get("good_faith_statement") != "true" || values.Get("accuracy_statement") != "true" || values.Get("signature") == "" {
				return "shape", false
			}
		case "privacy":
			if !oneOf(values.Get("request_kind"), "access", "export", "deletion", "correction", "opt_out", "other") {
				return "shape", false
			}
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

func validReportedURL(value string) bool {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(value))
	return err == nil && (strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")) && parsed.Hostname() != "" && parsed.User == nil
}

func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

func contactCaptchaProofInvalid(step string, simpleID, simpleAnswer, providerProof bool) bool {
	if step == "confirm" || step == "edit" {
		return false
	}
	if simpleID != simpleAnswer {
		return true
	}
	if providerProof {
		return false
	}
	return !simpleID
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
	if reason == "honeypot" || reason == "captcha" || reason == "content" || reason == "markup" || reason == "duplicate" || reason == "origin" || reason == "replay" {
		fd.recordContactStrike(r, time.Now())
	}
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

func (fd *frontendDeps) contactTokenValidationReason(token string, now time.Time) string {
	settings := fd.contactAntispam()
	nonce, ok := verifyContactFormToken(fd.installSecret, token, now, settings.MinFillSeconds, settings.MaxAgeSeconds)
	if !ok || nonce == "" {
		return "fill_time"
	}
	state := contactSpamStateFor(fd.installSecret)
	state.mu.Lock()
	defer state.mu.Unlock()
	for key, expires := range state.usedTokens {
		if !expires.After(now) {
			delete(state.usedTokens, key)
		}
	}
	if _, exists := state.usedTokens[nonce]; exists {
		return "replay"
	}
	if len(state.usedTokens) >= 50000 {
		for key := range state.usedTokens {
			delete(state.usedTokens, key)
			break
		}
	}
	state.usedTokens[nonce] = now.Add(time.Duration(settings.MaxAgeSeconds) * time.Second)
	return ""
}

func (fd *frontendDeps) contactOriginValid(r *http.Request) bool {
	origin, referer, fetchSite := strings.TrimSpace(r.Header.Get("Origin")), strings.TrimSpace(r.Header.Get("Referer")), strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))
	if fetchSite != "" && fetchSite != "same-origin" {
		return false
	}
	value := origin
	if value == "" {
		value = referer
	}
	if value == "" {
		return fetchSite == "same-origin"
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	if fd.resolver == nil {
		return strings.EqualFold(parsed.Host, r.Host) && strings.EqualFold(parsed.Scheme, requestScheme(r))
	}
	proto, host, port := fd.resolver.GetURLVars(r)
	if !strings.EqualFold(parsed.Scheme, proto) || !strings.EqualFold(parsed.Hostname(), host) {
		return false
	}
	originPort := parsed.Port()
	if originPort == "" {
		if proto == "https" {
			originPort = "443"
		} else {
			originPort = "80"
		}
	}
	if port == "" {
		if proto == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return originPort == port
}

func requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func normalizedContactHash(values url.Values) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if key == "form_token" || key == "review_token" || key == "step" || key == "confirmed" || strings.Contains(key, "captcha") || key == "csrf_token" || key == "website" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, key := range keys {
		value := strings.ToLower(strings.Join(strings.Fields(values.Get(key)), " "))
		fmt.Fprintf(h, "%s=%s\n", key, value)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func contactReviewValues(values url.Values) url.Values {
	result := make(url.Values)
	for key, entries := range values {
		if key == "csrf_token" || key == "form_token" || key == "review_token" || key == "step" || key == "confirmed" || key == "website" || strings.Contains(key, "captcha") || key == "g-recaptcha-response" || key == "h-captcha-response" {
			continue
		}
		result[key] = append([]string(nil), entries...)
	}
	return result
}

func issueContactReviewToken(secret string, values url.Values, now time.Time, flags string) string {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return ""
	}
	issued, nonceText := now.Unix(), base64.RawURLEncoding.EncodeToString(nonce)
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "contact-review|%d|%s|%s|%s", issued, nonceText, flags, normalizedContactHash(values))
	return fmt.Sprintf("%d.%s.%s.%s", issued, nonceText, flags, base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))
}

func verifyContactReviewToken(secret, token string, values url.Values, now time.Time, minAge, maxAge int) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 4 || len(parts[1]) != 22 || (parts[2] != "" && parts[2] != "d") || len(parts[3]) != 43 {
		return "", false
	}
	issued, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return "", false
	}
	age := now.Unix() - issued
	if age < int64(minAge) || age < 0 || (maxAge > 0 && age > int64(maxAge)) {
		return "", false
	}
	provided, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "contact-review|%d|%s|%s|%s", issued, parts[1], parts[2], normalizedContactHash(values))
	return parts[1], hmac.Equal(provided, mac.Sum(nil))
}

func contactReviewFlags(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) == 4 && parts[2] == "d" {
		return "d"
	}
	return ""
}

func emailDomain(email string) string {
	_, host, ok := strings.Cut(strings.ToLower(email), "@")
	if !ok {
		return ""
	}
	return host
}

func contactLinks(value string) []string {
	indexes := contactLinkPattern.FindAllStringIndex(value, -1)
	links := make([]string, 0, len(indexes))
	for _, index := range indexes {
		if index[0] > 0 && value[index[0]-1] == '@' {
			continue
		}
		links = append(links, value[index[0]:index[1]])
	}
	return links
}

func (fd *frontendDeps) contactContentReason(r *http.Request, values url.Values) string {
	settings := fd.contactAntispam()
	textFields := []string{"name", "email", "subject", "message", "reported_url", "reason", "copyrighted_work", "good_faith_statement", "accuracy_statement", "signature", "request_kind"}
	var links []string
	var textValues []string
	if settings.RejectMarkup {
		for _, field := range textFields {
			value := values.Get(field)
			textValues = append(textValues, value)
			if contactMarkupPattern.MatchString(value) || strings.ContainsAny(html.UnescapeString(value), "<>") {
				return "markup"
			}
		}
	} else {
		for _, field := range textFields {
			textValues = append(textValues, values.Get(field))
		}
	}
	if len(contactLinks(values.Get("name"))) > 0 || len(contactLinks(values.Get("subject"))) > 0 {
		return "content"
	}
	for _, field := range textFields {
		if field == "name" || field == "email" || field == "subject" {
			continue
		}
		links = append(links, contactLinks(values.Get(field))...)
	}
	if settings.MaxLinks > 0 && len(links) > settings.MaxLinks {
		return "content"
	}
	allowedHosts := contactAllowedLinkHosts(fd, r)
	for _, raw := range links {
		host := linkHost(raw)
		if host == "" || !allowedHosts[host] {
			return "content"
		}
	}
	domain := emailDomain(values.Get("email"))
	for _, blocked := range settings.BlockedEmailDomains {
		if domain == strings.ToLower(strings.TrimSpace(blocked)) {
			return "content"
		}
	}
	for _, blocked := range settings.BlockedWords {
		needle := strings.ToLower(strings.TrimSpace(blocked))
		if needle == "" {
			continue
		}
		for _, value := range textValues {
			if strings.Contains(strings.ToLower(value), needle) {
				return "content"
			}
		}
	}
	return ""
}

func (fd *frontendDeps) validReportTarget(r *http.Request, values url.Values) bool {
	typeValue := values.Get("type")
	if typeValue != "abuse" && typeValue != "dmca" {
		return true
	}
	parsed, err := url.ParseRequestURI(strings.TrimSpace(values.Get("reported_url")))
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	routePath := path.Clean(parsed.Path)
	if routePath == "." || routePath == "/" || routePath == "/server/contact" || strings.HasPrefix(routePath, "/api/") || routePath == "/api" || routePath == "/server/admin" || strings.HasPrefix(routePath, "/server/admin/") {
		return false
	}
	if fd.routeMatch == nil {
		return false
	}
	pattern, params, matched := fd.routeMatch(routePath)
	if !matched {
		return false
	}
	if pattern == "/{slug}" {
		_, err := db.GetLinkByShortCode(r.Context(), fd.ld.sqlDB, params["slug"])
		return err == nil
	}
	return true
}

func contactAllowedLinkHosts(fd *frontendDeps, r *http.Request) map[string]bool {
	hosts := make(map[string]bool)
	add := func(host string) {
		host = strings.ToLower(strings.TrimSpace(host))
		if strings.Contains(host, "://") {
			if parsed, err := url.Parse(host); err == nil {
				host = parsed.Hostname()
			}
		} else if parsedHost, _, err := net.SplitHostPort(host); err == nil {
			host = parsedHost
		}
		host = strings.TrimSuffix(strings.Trim(host, "[]"), ".")
		if host != "" {
			hosts[host] = true
		}
	}
	if fd.resolver != nil {
		_, host, _ := fd.resolver.GetURLVars(r)
		add(host)
	} else {
		host, _, _ := net.SplitHostPort(r.Host)
		if host == "" {
			host = r.Host
		}
		add(host)
	}
	for _, host := range fqdn.GetAllDomains() {
		add(host)
	}
	return hosts
}

func linkHost(raw string) string {
	trimmed := strings.TrimRight(raw, ".,;:!?)]}")
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		parsed, err = url.Parse("//" + trimmed)
	}
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
}

func contactClientIP(fd *frontendDeps, r *http.Request) string {
	if fd.resolver != nil {
		return fd.resolver.ResolveClientIP(r)
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	return ip
}

func (fd *frontendDeps) recordContactStrike(r *http.Request, now time.Time) {
	if fd.resolver != nil && fd.resolver.IsOverlay(r) {
		return
	}
	settings := fd.contactAntispam()
	if settings.Strikes <= 0 || settings.BanHours <= 0 {
		return
	}
	ip := contactClientIP(fd, r)
	if ip == "" {
		return
	}
	state := contactSpamStateFor(fd.installSecret)
	window, err := time.ParseDuration(settings.Window)
	if err != nil || window <= 0 {
		window = time.Hour
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	strikeKey := "ip:" + ip
	strikes := state.strikes[strikeKey][:0]
	for _, strike := range state.strikes[strikeKey] {
		if now.Sub(strike) < window {
			strikes = append(strikes, strike)
		}
	}
	strikes = append(strikes, now)
	if len(strikes) >= settings.Strikes {
		state.bans[strikeKey] = now.Add(time.Duration(settings.BanHours) * time.Hour)
		delete(state.strikes, strikeKey)
	} else {
		state.strikes[strikeKey] = strikes
	}
}

func (fd *frontendDeps) setContactRetryAfter(w http.ResponseWriter, r *http.Request, reason string) {
	if reason == "banned" {
		seconds := fd.contactAntispam().BanHours * 3600
		if fd.resolver == nil || !fd.resolver.IsOverlay(r) {
			state := contactSpamStateFor(fd.installSecret)
			key := "ip:" + contactClientIP(fd, r)
			state.mu.Lock()
			if remaining := time.Until(state.bans[key]); remaining > 0 {
				seconds = int((remaining + time.Second - 1) / time.Second)
			}
			state.mu.Unlock()
		}
		if seconds < 1 {
			seconds = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		return
	}
	setContactRetryAfter(w, fd.contactAntispam().Window)
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
	if fd.resolver == nil || !fd.resolver.IsOverlay(r) {
		ip := contactClientIP(fd, r)
		if ip != "" {
			banKey := "ip:" + ip
			if until := state.bans[banKey]; until.After(now) {
				return "banned", true
			}
			delete(state.bans, banKey)
		}
	}
	for key, bucket := range state.buckets {
		if now.Sub(bucket.start) >= window {
			delete(state.buckets, key)
		}
	}
	if fd.resolver == nil || !fd.resolver.IsOverlay(r) {
		ip := contactClientIP(fd, r)
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
		ip = contactClientIP(fd, r)
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
