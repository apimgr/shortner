package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/apimgr/shortner/src/common/i18n"
	"github.com/apimgr/shortner/src/config"
)

type preferenceHubEntry struct {
	Path        string `json:"path"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Order       int    `json:"order"`
}

type preferencePageRegistration struct {
	Path     string
	TitleKey string
	DescKey  string
	Order    int
	Enabled  func(*config.Config) bool
	Methods  []string
	Handler  func(*frontendDeps) http.HandlerFunc
}

var preferencePageRegistry = []preferencePageRegistration{
	{Path: "/server/preferences/tokens", TitleKey: "token_page.title", DescKey: "preferences.tokens_description", Order: 1, Enabled: func(*config.Config) bool { return true }, Methods: []string{http.MethodGet}, Handler: func(fd *frontendDeps) http.HandlerFunc { return fd.tokenPageHandler }},
	{Path: "/server/preferences/sync", TitleKey: "preferences.sync_title", DescKey: "preferences.sync_description", Order: 2, Enabled: func(*config.Config) bool { return true }, Methods: []string{http.MethodGet, http.MethodPost}, Handler: func(fd *frontendDeps) http.HandlerFunc { return fd.preferenceSyncHandler }},
}

var helpPageRegistry = []preferencePageRegistration{
	{Path: "/server/help/mcp", TitleKey: "mcp.title", DescKey: "mcp.subtitle", Order: 1, Enabled: func(*config.Config) bool { return true }, Methods: []string{http.MethodGet}, Handler: func(fd *frontendDeps) http.HandlerFunc { return fd.mcpHelpPage }},
}

func registeredPreferencePages(cfg *config.Config, lang string) []preferenceHubEntry {
	return registeredHubPages(preferencePageRegistry, cfg, lang)
}

func registeredHelpPages(cfg *config.Config, lang string) []preferenceHubEntry {
	return registeredHubPages(helpPageRegistry, cfg, lang)
}

func registeredHubPages(registry []preferencePageRegistration, cfg *config.Config, lang string) []preferenceHubEntry {
	if lang == "" {
		lang = i18n.DefaultLanguage
	}
	entries := make([]preferenceHubEntry, 0, len(registry))
	for _, registration := range registry {
		if registration.Enabled != nil && !registration.Enabled(cfg) {
			continue
		}
		entries = append(entries, preferenceHubEntry{
			Path:        registration.Path,
			Title:       i18n.Translate(lang, registration.TitleKey),
			Description: i18n.Translate(lang, registration.DescKey),
			Order:       registration.Order,
		})
	}
	return entries
}

func registerPreferencePages(r chi.Router, fd *frontendDeps, cfg *config.Config) {
	registerHubPageRoutes(r, preferencePageRegistry, fd, cfg)
}

func registerHelpPages(r chi.Router, fd *frontendDeps, cfg *config.Config) {
	registerHubPageRoutes(r, helpPageRegistry, fd, cfg)
}

func registerHubPageRoutes(r chi.Router, registry []preferencePageRegistration, fd *frontendDeps, cfg *config.Config) {
	for _, page := range registry {
		if page.Enabled != nil && !page.Enabled(cfg) {
			continue
		}
		if page.Handler == nil {
			continue
		}
		for _, method := range page.Methods {
			r.Method(method, page.Path, page.Handler(fd))
		}
	}
}
