package httpserver

import (
	"net/http"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/pgp"
	"github.com/apimgr/shortner/src/security"
)

func (fd *frontendDeps) pageContentAPIHandler(page string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var title, content string
		switch page {
		case "about":
			title, content = t(r, "about.title"), fd.cfg.Pages.About.Content
		case "help":
			title, content = t(r, "help.title"), fd.cfg.Pages.Help.Content
		case "terms":
			title, content = t(r, "terms.title"), fd.cfg.Pages.Terms.Content
		default:
			http.NotFound(w, r)
			return
		}
		apperr.SendOK(w, map[string]string{"title": title, "content": content, "format": "markdown"})
	}
}

func (fd *frontendDeps) privacyAPIHandler(w http.ResponseWriter, r *http.Request) {
	privacy := fd.cfg.Server.Privacy
	apperr.SendOK(w, map[string]any{
		"title": t(r, "privacy.title"), "content": fd.cfg.Pages.Privacy.Content,
		"data_collection": privacy.Content.DataCollection,
		"data_usage":      privacy.GetDataUsageContent(), "data_security": privacy.Content.DataSecurity,
		"cookies": privacy.Cookies, "sold": privacy.Data.Sold,
		"third_party_services": privacy.ThirdParty.Services, "retention_period": privacy.Retention.Period,
	})
}

func (fd *frontendDeps) securityAPIHandler(w http.ResponseWriter, r *http.Request) {
	contacts := make([]map[string]string, 0, 3)
	sec := fd.cfg.Web.Security
	if strings.TrimSpace(sec.ReportURL) != "" {
		contacts = append(contacts, map[string]string{"type": "github", "value": sec.ReportURL})
	}
	if id := security.SecurityID(fd.installSecret, time.Now()); id != "" {
		contacts = append(contacts, map[string]string{"type": "contact_form", "value": fd.resolver.BuildURL(r, "/server/contact?security_id="+id)})
	}
	if email := fd.securityContactEmail(r); email != "" {
		contacts = append(contacts, map[string]string{"type": "email", "value": email})
	}
	var pgpInfo map[string]any
	if sec.PublishPGPKey {
		armored, err := pgp.NewStore(fd.configDir).ReadPublicKey()
		if err == nil {
			entities, parseErr := pgp.ParsePublic(armored)
			if parseErr == nil && len(entities) > 0 {
				pgpInfo = map[string]any{"available": true, "url": fd.resolver.BuildURL(r, "/.well-known/pgp-key.asc"), "fingerprint": pgp.FingerprintOf(entities[0])}
			}
		}
	}
	if pgpInfo == nil {
		pgpInfo = map[string]any{"available": false, "url": nil, "fingerprint": nil}
	}
	apperr.SendOK(w, map[string]any{
		"contacts": contacts, "expires": fd.securityExpires(), "pgp": pgpInfo,
		"policy_url": fd.resolver.BuildURL(r, "/server/security/policy"),
		"thanks_url": fd.resolver.BuildURL(r, "/server/security/thanks"),
	})
}

func (fd *frontendDeps) securityThanksAPIHandler(w http.ResponseWriter, r *http.Request) {
	entries := make([]map[string]string, 0, len(fd.cfg.Web.Security.Thanks))
	for _, entry := range fd.cfg.Web.Security.Thanks {
		if entry.Date == "" || entry.Severity == "" {
			continue
		}
		entries = append(entries, map[string]string{"credit": entry.Credit, "date": entry.Date, "severity": entry.Severity})
	}
	apperr.SendOK(w, map[string]any{"entries": entries})
}

func (fd *frontendDeps) mcpHelpAPIHandler(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}

func unavailableMCPHandler(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}

// securityPolicyAPIHandler returns the configured disclosure policy in the
// same form used by the public policy page.
func (fd *frontendDeps) securityPolicyAPIHandler(w http.ResponseWriter, r *http.Request) {
	policy := fd.cfg.Web.Security.Policy
	if policy == "" {
		policy = defaultDisclosurePolicy
	}
	apperr.SendOK(w, map[string]string{"policy": policy})
}

// dpoAPIHandler returns the public DPO contact and a request-resolved
// absolute privacy URL. The API always responds, even when no address is set.
func (fd *frontendDeps) dpoAPIHandler(w http.ResponseWriter, r *http.Request) {
	dpo := fd.cfg.Server.Contact.DPO
	name, email := dpo.Name, dpo.Email
	if email == "" {
		email = fd.cfg.Server.Contact.General.Email
	}
	base := fd.resolver.BuildURL(r, "/server/privacy")
	apperr.SendOK(w, map[string]string{"name": name, "email": email, "privacy_url": base})
}
