package httpserver

import (
	"sync/atomic"

	"github.com/apimgr/shortner/src/config"
)

type brandingSettings struct {
	Config   *config.Config
	Branding config.Branding
	SEO      config.SEO
	Theme    string
	Accent   string
}

type brandingStateStore struct {
	current atomic.Pointer[brandingSettings]
}

func newBrandingStateStore(cfg *config.Config) *brandingStateStore {
	store := &brandingStateStore{}
	store.store(cfg)
	return store
}

func (s *brandingStateStore) load(cfg *config.Config) *brandingSettings {
	if s != nil {
		if value := s.current.Load(); value != nil {
			return value
		}
	}
	return makeBrandingSettings(cfg)
}

func (s *brandingStateStore) store(cfg *config.Config) {
	if s != nil && cfg != nil {
		s.current.Store(makeBrandingSettings(cfg))
	}
}

func makeBrandingSettings(cfg *config.Config) *brandingSettings {
	if cfg == nil {
		return &brandingSettings{}
	}
	seo := cfg.Server.SEO
	seo.Keywords = append([]string(nil), seo.Keywords...)
	seo.MetaTags = cloneStringMap(seo.MetaTags)
	seo.Verification.Custom = append([]config.SEOCustomMetaTag(nil), seo.Verification.Custom...)
	branding := cfg.Server.Branding
	branding.Content.Header = copyOperatorPageContent(branding.Content.Header)
	branding.Content.About = copyOperatorPageContent(branding.Content.About)
	branding.Content.Privacy = copyOperatorPageContent(branding.Content.Privacy)
	branding.Content.Footer = copyOperatorPageContent(branding.Content.Footer)
	branding.Content.Terms = copyOperatorPageContent(branding.Content.Terms)
	return &brandingSettings{Config: cfg, Branding: branding, SEO: seo, Theme: cfg.Web.UI.Theme, Accent: cfg.Web.UI.Accent}
}

func copyOperatorPageContent(content config.OperatorPageContent) config.OperatorPageContent {
	content.Body.Values = cloneStringMap(content.Body.Values)
	return content
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	copy := make(map[string]string, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}
