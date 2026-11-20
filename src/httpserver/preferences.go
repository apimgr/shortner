package httpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/common/i18n"
)

const preferenceCookieMaxAge = 31536000

var preferenceKeyPattern = regexp.MustCompile(`^shortner_pref_[a-z][a-z0-9_]{0,63}$`)

type preferenceSnapshot struct {
	Theme string            `json:"theme"`
	Lang  string            `json:"lang"`
	Prefs map[string]string `json:"prefs"`
	URL   string            `json:"url,omitempty"`
	Code  string            `json:"code,omitempty"`
}

type preferencePageData struct {
	Base        PageData
	State       preferenceSnapshot
	ExportURL   string
	ExportCode  string
	Preferences bool
	Analytics   bool
	CCPA        bool
	ShowCCPA    bool
	CancelURL   string
}

type preferenceApplyResult struct {
	State   preferenceSnapshot
	Applied []string
	Ignored []string
}

func (fd *frontendDeps) preferencesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		fd.savePreferences(w, r)
		return
	}
	if detectClientType(r) == clientText || r.URL.Query().Get("format") == "text" || strings.Contains(r.Header.Get("Accept"), "text/plain") {
		fullURL, code := fd.preferenceExport(r, readPreferences(r))
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = fmt.Fprintf(w, "URL: %s\nCode: %s\n", fullURL, code)
		return
	}
	fd.renderPreferences(w, r, http.StatusOK)
}

func (fd *frontendDeps) renderPreferences(w http.ResponseWriter, r *http.Request, status int) {
	state := readPreferences(r)
	if lang := langFromContext(r); lang != "" {
		state.Lang = lang
	}
	fullURL, code := fd.preferenceExport(r, state)
	consent := readConsent(r)
	data := preferencePageData{
		Base:  fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "common.preferences"), fd.cfg.Server.SEO.Description),
		State: state, ExportURL: fullURL, ExportCode: code,
		Preferences: consent.Preferences, Analytics: consent.Analytics,
		CCPA: hasCCPAOptOut(r), ShowCCPA: fd.cfg.Server.Privacy.Data.Sold,
		CancelURL: safePreferenceRedirect(r.URL.Query().Get("redirect"), r.Referer()),
	}
	if err := renderPage(w, status, "preferences", data); err != nil {
		sendError(w, r, apperr.New(apperr.CodeServerError))
	}
}

func (fd *frontendDeps) savePreferences(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/server/preferences", http.StatusSeeOther)
		return
	}
	if r.FormValue("action") == "reset" {
		resetPreferenceCookies(w, preferenceRequest(r, fd.resolver))
		w.Header().Set("Clear-Site-Data", `"storage"`)
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, "/server/preferences", http.StatusSeeOther)
		return
	}
	values := cloneValues(r.Form)
	if code := strings.TrimSpace(r.FormValue("code")); code != "" {
		for key, entries := range decodePreferenceCode(code) {
			values[key] = entries
		}
	}
	secureRequest := preferenceRequest(r, fd.resolver)
	if !isThemeOnlyPost(values) {
		applyPreferences(w, secureRequest, values)
		if r.FormValue("consent_form") == "1" {
			applyConsentPreferences(w, secureRequest, values, fd.cfg.Server.Privacy.Cookies.Analytics.Enabled, fd.cfg.Server.Privacy.Data.Sold)
		}
	} else if theme := values.Get("theme"); isValidTheme(theme) {
		setPreferenceCookie(w, secureRequest, "theme", theme)
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, safePreferenceRedirect(r.FormValue("redirect"), r.Referer()), http.StatusSeeOther)
}

func (fd *frontendDeps) preferenceExportHandler(w http.ResponseWriter, r *http.Request) {
	state := readPreferences(r)
	fullURL, code := fd.preferenceExport(r, state)
	if strings.Contains(r.Header.Get("Accept"), "text/plain") || r.URL.Query().Get("format") == "text" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = fmt.Fprintf(w, "URL: %s\nCode: %s\n", fullURL, code)
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		fd.renderPreferences(w, r, http.StatusOK)
		return
	}
	state.URL, state.Code = fullURL, code
	w.Header().Set("Cache-Control", "no-store")
	apperr.SendOK(w, state)
}

func (fd *frontendDeps) preferenceAPISnapshotHandler(w http.ResponseWriter, r *http.Request) {
	state := readPreferences(r)
	w.Header().Set("Cache-Control", "no-store")
	apperr.SendOK(w, state)
}

func (fd *frontendDeps) preferenceImportHandler(w http.ResponseWriter, r *http.Request) {
	values := cloneValues(r.URL.Query())
	if code := values.Get("code"); code != "" {
		for key, entries := range decodePreferenceCode(code) {
			if _, exists := values[key]; !exists {
				values[key] = entries
			}
		}
	}
	result := applyPreferences(w, preferenceRequest(r, fd.resolver), values)
	if strings.Contains(r.Header.Get("Accept"), "text/html") && !strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, safePreferenceRedirect(r.URL.Query().Get("redirect"), r.Referer()), http.StatusSeeOther)
		return
	}
	response := struct {
		Theme   string            `json:"theme"`
		Lang    string            `json:"lang"`
		Prefs   map[string]string `json:"prefs"`
		Applied []string          `json:"applied"`
		Ignored []string          `json:"ignored"`
	}{result.State.Theme, result.State.Lang, result.State.Prefs, result.Applied, result.Ignored}
	w.Header().Set("Cache-Control", "no-store")
	apperr.SendOK(w, response)
}

func (fd *frontendDeps) preferenceAPISaveHandler(w http.ResponseWriter, r *http.Request) {
	values := make(url.Values)
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		var input map[string]any
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil {
			sendError(w, r, apperr.New(apperr.CodeBadRequest))
			return
		}
		for key, value := range input {
			if str, ok := value.(string); ok {
				values.Set(key, str)
			} else {
				values.Set(key, "")
			}
		}
	} else if err := r.ParseForm(); err == nil {
		values = cloneValues(r.Form)
	}
	result := applyPreferences(w, preferenceRequest(r, fd.resolver), values)
	if len(result.Applied) == 0 && len(values) > 0 {
		sendError(w, r, apperr.New(apperr.CodeValidationFailed))
		return
	}
	response := struct {
		Theme   string            `json:"theme"`
		Lang    string            `json:"lang"`
		Prefs   map[string]string `json:"prefs"`
		Applied []string          `json:"applied"`
		Ignored []string          `json:"ignored"`
	}{result.State.Theme, result.State.Lang, result.State.Prefs, result.Applied, result.Ignored}
	w.Header().Set("Cache-Control", "no-store")
	apperr.SendOK(w, response)
}

func (fd *frontendDeps) preferenceExport(r *http.Request, state preferenceSnapshot) (string, string) {
	query := canonicalPreferenceQuery(state)
	code := base64.RawURLEncoding.EncodeToString([]byte(query))
	path := "/server/preferences/import?" + query
	if fd.resolver != nil {
		return fd.resolver.BuildURL(r, path), code
	}
	return "https://" + r.Host + path, code
}

func readPreferences(r *http.Request) preferenceSnapshot {
	theme := "dark"
	if cookie, err := r.Cookie("theme"); err == nil && isValidTheme(cookie.Value) {
		theme = cookie.Value
	}
	lang := i18n.DefaultLanguage
	if cookie, err := r.Cookie(i18n.CookieName); err == nil {
		if normalized := normalizePreferenceLang(cookie.Value); normalized != "" {
			lang = normalized
		}
	} else if detected := i18n.FromAcceptLanguage(r.Header.Get("Accept-Language")); detected != "" {
		lang = detected
	}
	prefs := map[string]string{}
	for _, cookie := range r.Cookies() {
		if preferenceKeyPattern.MatchString(cookie.Name) && validPreferenceValue(cookie.Value) {
			prefs[strings.TrimPrefix(cookie.Name, "shortner_pref_")] = cookie.Value
		}
	}
	return preferenceSnapshot{Theme: theme, Lang: lang, Prefs: prefs}
}

func applyPreferences(w http.ResponseWriter, r *http.Request, values url.Values) preferenceApplyResult {
	result := preferenceApplyResult{Applied: []string{}, Ignored: []string{}}
	keys := sortedQueryKeys(values)
	if len(keys) > 32 {
		keys = keys[:32]
	}
	for _, key := range keys {
		if isPreferenceControl(key) {
			continue
		}
		value := ""
		if len(values[key]) > 0 {
			value = values[key][0]
		}
		name, canonical, ok := validatePreference(key, value)
		if !ok {
			result.Ignored = append(result.Ignored, key)
			continue
		}
		setPreferenceCookie(w, r, name, canonical)
		result.Applied = append(result.Applied, key)
	}
	result.State = readPreferencesWithValues(r, values)
	sort.Strings(result.Applied)
	sort.Strings(result.Ignored)
	return result
}

func readPreferencesWithValues(r *http.Request, values url.Values) preferenceSnapshot {
	state := readPreferences(r)
	if value := values.Get("theme"); isValidTheme(value) {
		state.Theme = value
	}
	if value := values.Get("lang"); value != "" {
		if lang := normalizePreferenceLang(value); lang != "" {
			state.Lang = lang
		}
	}
	for key, value := range values {
		if preferenceKeyPattern.MatchString(key) && len(value) > 0 && validPreferenceValue(value[0]) {
			state.Prefs[strings.TrimPrefix(key, "shortner_pref_")] = value[0]
		}
	}
	return state
}

func validatePreference(key, value string) (string, string, bool) {
	switch key {
	case "theme":
		return key, value, isValidTheme(value)
	case "lang":
		lang := normalizePreferenceLang(value)
		return key, lang, lang != ""
	default:
		return key, value, preferenceKeyPattern.MatchString(key) && validPreferenceValue(value)
	}
}

func normalizePreferenceLang(value string) string {
	value = strings.TrimSpace(value)
	if i18n.IsSupported(value) {
		return i18n.Normalize(value)
	}
	if i := strings.IndexAny(value, "-_"); i > 0 && i18n.IsSupported(value[:i]) {
		return i18n.Normalize(value[:i])
	}
	return ""
}

func validPreferenceValue(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', strings.ContainsRune("_.-", r):
		default:
			return false
		}
	}
	return true
}

func setPreferenceCookie(w http.ResponseWriter, r *http.Request, name, value string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: preferenceCookieMaxAge, SameSite: http.SameSiteLaxMode, Secure: requestIsHTTPS(r)})
}

func canonicalPreferenceQuery(state preferenceSnapshot) string {
	values := url.Values{}
	values.Set("theme", state.Theme)
	values.Set("lang", state.Lang)
	for key, value := range state.Prefs {
		if preferenceKeyPattern.MatchString("shortner_pref_"+key) && validPreferenceValue(value) {
			values.Set("shortner_pref_"+key, value)
		}
	}
	return values.Encode()
}

func decodePreferenceCode(code string) url.Values {
	code = strings.TrimSpace(code)
	if len(code) > 2048 {
		return url.Values{}
	}
	if i := strings.Index(code, "?"); i >= 0 {
		code = code[i+1:]
	}
	if strings.HasSuffix(code, "=") || !strings.Contains(code, "=") {
		decoded, err := base64.StdEncoding.DecodeString(code)
		if err != nil {
			decoded, err = base64.URLEncoding.DecodeString(code)
		}
		if err != nil {
			decoded, err = base64.RawURLEncoding.DecodeString(code)
		}
		if err == nil {
			code = string(decoded)
		} else if !strings.Contains(code, "=") {
			return url.Values{}
		}
	}
	values, err := url.ParseQuery(code)
	if err != nil {
		return url.Values{}
	}
	return values
}

func safePreferenceRedirect(redirect, referer string) string {
	for _, candidate := range []string{redirect, referer} {
		if candidate == "" || strings.ContainsAny(candidate, "\r\n") {
			continue
		}
		if !isSafeLocalRedirect(candidate) || strings.HasPrefix(candidate, "/server/preferences/import") || strings.HasPrefix(candidate, "/server/preferences/export") || candidate == "/server/preferences" {
			continue
		}
		return candidate
	}
	return "/"
}

func sortedQueryKeys(values url.Values) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func cloneValues(values url.Values) url.Values {
	copy := make(url.Values, len(values))
	for key, entries := range values {
		copy[key] = append([]string(nil), entries...)
	}
	return copy
}

func isPreferenceControl(key string) bool {
	switch key {
	case "csrf_token", "code", "redirect", "action", "consent_form", "consent_preferences", "consent_analytics", "ccpa_opt_out":
		return true
	default:
		return false
	}
}

func isThemeOnlyPost(values url.Values) bool {
	for key := range values {
		if key != "theme" && key != "csrf_token" && key != "redirect" {
			return false
		}
	}
	return true
}

func readConsent(r *http.Request) consentChoice {
	if cookie, err := r.Cookie(consentCookieName); err == nil {
		if raw, err := base64.RawURLEncoding.DecodeString(cookie.Value); err == nil {
			var choice consentChoice
			if json.Unmarshal(raw, &choice) == nil {
				choice.Essential = true
				return choice
			}
		}
	}
	return consentChoice{Essential: true}
}

func hasCCPAOptOut(r *http.Request) bool {
	cookie, err := r.Cookie("ccpa_opt_out")
	return err == nil && cookie.Value == "true"
}

func applyConsentPreferences(w http.ResponseWriter, r *http.Request, values url.Values, analyticsEnabled, ccpaEnabled bool) {
	choice := struct {
		Essential   bool  `json:"essential"`
		Preferences bool  `json:"preferences"`
		Analytics   bool  `json:"analytics"`
		Timestamp   int64 `json:"timestamp"`
	}{Essential: true, Preferences: lastValue(values, "consent_preferences") == "on", Analytics: lastValue(values, "consent_analytics") == "on" && analyticsEnabled, Timestamp: time.Now().Unix()}
	raw, err := json.Marshal(choice)
	if err == nil {
		setConsentCookie(w, r, raw)
	}
	if ccpaEnabled && lastValue(values, "ccpa_opt_out") == "on" {
		http.SetCookie(w, &http.Cookie{Name: "ccpa_opt_out", Value: "true", Path: "/", MaxAge: preferenceCookieMaxAge, SameSite: http.SameSiteLaxMode, Secure: requestIsHTTPS(r)})
	} else if ccpaEnabled {
		deletePreferenceCookie(w, r, "ccpa_opt_out")
	}
}

func lastValue(values url.Values, key string) string {
	items := values[key]
	if len(items) == 0 {
		return ""
	}
	return items[len(items)-1]
}

func setConsentCookie(w http.ResponseWriter, r *http.Request, raw []byte) {
	http.SetCookie(w, &http.Cookie{Name: consentCookieName, Value: base64.RawURLEncoding.EncodeToString(raw), Path: "/", MaxAge: preferenceCookieMaxAge, SameSite: http.SameSiteLaxMode, Secure: requestIsHTTPS(r)})
}

func requestIsHTTPS(r *http.Request) bool {
	trusted, _ := r.Context().Value(preferenceSecureContextKey{}).(bool)
	return r.TLS != nil || trusted
}

type preferenceSecureContextKey struct{}

func preferenceRequest(r *http.Request, resolver *ProxyResolver) *http.Request {
	secure := r.TLS != nil
	if resolver != nil {
		proto, _, _ := resolver.GetURLVars(r)
		secure = secure || proto == "https"
	}
	return r.WithContext(context.WithValue(r.Context(), preferenceSecureContextKey{}, secure))
}

func resetPreferenceCookies(w http.ResponseWriter, r *http.Request) {
	for _, name := range []string{"theme", i18n.CookieName, consentCookieName, "ccpa_opt_out", "shortner_build", "dismissed_announcements"} {
		deletePreferenceCookie(w, r, name)
	}
	for _, cookie := range r.Cookies() {
		if preferenceKeyPattern.MatchString(cookie.Name) || cookie.Name == "shortner_build" {
			deletePreferenceCookie(w, r, cookie.Name)
		}
	}
}

func deletePreferenceCookie(w http.ResponseWriter, r *http.Request, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, SameSite: http.SameSiteLaxMode, Secure: requestIsHTTPS(r)})
}
