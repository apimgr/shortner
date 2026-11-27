package server

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/common/i18n"
)

type preferenceSyncPageData struct {
	Base       PageData
	ExportURL  string
	ExportCode string
	CSRFToken  string
}

// preferenceSyncHandler serves the preference export page and its import form.
func (fd *frontendDeps) preferenceSyncHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		fd.preferenceSyncPost(w, r)
		return
	}
	state := readPreferences(r)
	if lang := langFromContext(r); lang != "" {
		state.Lang = lang
		state.LangName = i18n.MetaFor(lang).NativeName
	}
	fullURL, code := fd.preferenceExport(r, state)
	accept := strings.ToLower(r.Header.Get("Accept"))
	if detectClientType(r) == clientText || r.URL.Query().Get("format") == "text" || strings.Contains(accept, "text/plain") {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = fmt.Fprintf(w, "URL: %s\nCode: %s\n", fullURL, code)
		return
	}
	if strings.Contains(accept, "application/json") {
		state.URL, state.Code = fullURL, code
		w.Header().Set("Cache-Control", "no-store")
		apperr.SendOK(w, state)
		return
	}
	data := preferenceSyncPageData{
		Base:       fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "preferences.sync_title"), fd.cfg.Server.Branding.Description),
		ExportURL:  fullURL,
		ExportCode: code,
		CSRFToken:  requestCSRFToken(r, fd.cfg),
	}
	if err := renderPage(w, http.StatusOK, "preferences_sync", data); err != nil {
		sendError(w, r, apperr.New(apperr.CodeServerError))
	}
}

// preferenceSyncPost applies decoded and directly submitted values, with
// submitted values taking precedence over the shared code.
func (fd *frontendDeps) preferenceSyncPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		fd.preferenceSyncRedirect(w, r, url.Values{})
		return
	}
	values := decodePreferenceCode(r.FormValue("code"))
	for key, entries := range r.Form {
		if isPreferenceControl(key) {
			continue
		}
		values[key] = append([]string(nil), entries...)
	}
	secureRequest := preferenceRequest(r, fd.resolver)
	result := applyPreferences(w, secureRequest, values)
	w.Header().Set("Cache-Control", "no-store")
	target := preferenceRedirectTarget(r.FormValue("redirect"), r.Referer(), r)
	if target == "/" {
		target = "/server/preferences/sync"
	}
	if result.ConsentRequired {
		target = withConsentRequired(target)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// preferenceSyncImportHandler applies shared-link query values additively.
func (fd *frontendDeps) preferenceSyncImportHandler(w http.ResponseWriter, r *http.Request) {
	values := cloneValues(r.URL.Query())
	code := values.Get("code")
	delete(values, "code")
	for key, entries := range decodePreferenceCode(code) {
		if _, exists := values[key]; !exists {
			values[key] = entries
		}
	}
	result := applyPreferences(w, preferenceRequest(r, fd.resolver), values)
	accept := strings.ToLower(r.Header.Get("Accept"))
	if !strings.Contains(accept, "application/json") &&
		(strings.Contains(accept, "text/html") || (accept == "" && detectClientType(r) == clientHTML)) {
		w.Header().Set("Cache-Control", "no-store")
		target := preferenceRedirectTarget(r.URL.Query().Get("redirect"), r.Referer(), r)
		if result.ConsentRequired {
			target = withConsentRequired(target)
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	response := preferenceAPIResponse{result.State.Theme, result.State.Lang, result.State.Prefs, result.Applied, result.Ignored, result.ConsentRequired}
	w.Header().Set("Cache-Control", "no-store")
	apperr.SendOK(w, response)
}

func (fd *frontendDeps) preferenceSyncRedirect(w http.ResponseWriter, r *http.Request, values url.Values) {
	result := applyPreferences(w, preferenceRequest(r, fd.resolver), values)
	w.Header().Set("Cache-Control", "no-store")
	target := preferenceRedirectTarget(r.FormValue("redirect"), r.Referer(), r)
	if target == "/" {
		target = "/server/preferences/sync"
	}
	if result.ConsentRequired {
		target = withConsentRequired(target)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func preferenceRedirectTarget(redirect, referer string, r *http.Request) string {
	if validPreferenceRedirectPath(redirect) {
		return redirect
	}
	if parsed, err := url.Parse(referer); err == nil && parsed.IsAbs() && strings.EqualFold(parsed.Host, r.Host) {
		candidate := parsed.EscapedPath()
		if candidate == "" {
			candidate = "/"
		}
		if parsed.RawQuery != "" {
			candidate += "?" + parsed.RawQuery
		}
		if validPreferenceRedirectPath(candidate) {
			return candidate
		}
	}
	return "/"
}

func validPreferenceRedirectPath(candidate string) bool {
	return !strings.ContainsAny(candidate, "\r\n") && isSafeLocalRedirect(candidate) &&
		!strings.HasPrefix(candidate, "/server/preferences/sync/import") &&
		candidate != "/server/preferences/sync" && candidate != "/server/preferences"
}
