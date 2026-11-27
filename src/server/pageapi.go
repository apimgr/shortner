package server

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
		format := "markdown"
		switch page {
		case "about":
			title = t(r, "about.title")
			content, format, _ = fd.operatorContentSource(r, "about")
		case "help":
			title = t(r, "help.title")
			content, _ = fd.cfg.Server.Pages.Help.Content.Resolve(langFromContext(r))
		case "terms":
			title = t(r, "terms.title")
			var configured bool
			content, format, configured = fd.operatorContentSource(r, "terms")
			if !configured {
				format = "markdown"
				content = strings.Join([]string{
					"## " + t(r, "terms.acceptance_title") + "\n\n" + tf(r, "terms.acceptance_body", map[string]string{"site": projectInfo.Name}),
					"## " + t(r, "terms.acceptable_use") + "\n\n" + t(r, "terms.acceptable_use_body"),
					"## " + t(r, "terms.liability_title") + "\n\n" + t(r, "terms.liability_body"),
					"## " + t(r, "terms.changes_title") + "\n\n" + t(r, "terms.changes_body"),
					"## " + t(r, "terms.governing_law") + "\n\n" + t(r, "terms.governing_law_body"),
				}, "\n\n")
			}
		default:
			http.NotFound(w, r)
			return
		}
		response := map[string]any{"title": title, "content": content, "format": format}
		if page == "help" {
			response["pages"] = registeredHelpPages(fd.cfg, langFromContext(r))
		}
		apperr.SendOK(w, response)
	}
}

func (fd *frontendDeps) operatorContentSource(r *http.Request, name string) (string, string, bool) {
	entry, configured := configuredOperatorContent(fd.branding.load(fd.cfg).Branding.Content, name)
	if !configured {
		return "", "markdown", false
	}
	body, ok := entry.Body.Resolve(langFromContext(r))
	if !ok || len(body) > 256*1024 || strings.Contains(strings.ToLower(body), "/server/contact") {
		return "", "markdown", false
	}
	format := strings.ToLower(strings.TrimSpace(entry.Format))
	if format == "" {
		format = "md"
	}
	switch format {
	case "md":
		format = "markdown"
	case "txt":
		format = "text"
	}
	return expandFooterVariables(body, fd.operatorVariables(r)), format, true
}

func (fd *frontendDeps) privacyAPIHandler(w http.ResponseWriter, r *http.Request) {
	privacy := fd.cfg.Server.Privacy
	content := privacyPageContent{Sold: privacy.Data.Sold, DataUsage: t(r, "privacy.no_third_party"), DataUsageIfSold: t(r, "privacy.data_sale_body")}
	ccpaApplicable := privacy.Data.Sold
	apperr.SendOK(w, map[string]any{
		"title": t(r, "privacy.title"),
		"summary": map[string]any{
			"data_stored_on_server": privacy.Data.StoredOnServer,
			"data_sold":             privacy.Data.Sold,
			"user_control":          true,
		},
		"cookies": privacy.Cookies,
		"data": map[string]any{
			"sold": privacy.Data.Sold, "stored_on_server": privacy.Data.StoredOnServer,
			"sharing": privacy.Data.Sharing,
		},
		"tracking": map[string]any{"enabled": false, "type": "", "type_name": ""},
		"retention": map[string]any{
			"period":             privacy.Retention.Period,
			"export_available":   privacy.Retention.ExportAvailable,
			"deletion_available": privacy.Retention.DeletionAvailable,
		},
		"third_party": map[string]any{"services": privacy.ThirdParty.Services},
		"ccpa": map[string]any{
			"applicable":     ccpaApplicable,
			"opt_out_url":    "/server/privacy#ccpa-opt-out",
			"user_opted_out": hasCCPAOptOut(r),
		},
		"content": map[string]any{
			"consent_message": privacy.GetConsentMessage(),
			"data_usage":      content.GetDataUsageContent(),
		},
	})
}

func (fd *frontendDeps) securityAPIHandler(w http.ResponseWriter, r *http.Request) {
	contacts := make([]map[string]string, 0, 3)
	sec := fd.cfg.Web.Security
	if strings.TrimSpace(sec.ReportURL) != "" {
		contacts = append(contacts, map[string]string{"type": "github", "value": sec.ReportURL})
	}
	if fd.cfg.Server.Pages.Contact.Enabled {
		if id := security.SecurityID(fd.installSecret, time.Now()); id != "" {
			contacts = append(contacts, map[string]string{"type": "contact_form", "value": fd.resolver.BuildURL(r, "/server/contact?security_id="+id)})
		}
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
	data := map[string]any{
		"contacts": contacts, "expires": fd.securityExpires(), "pgp": pgpInfo,
		"policy_url": fd.resolver.BuildURL(r, "/server/security/policy"),
		"thanks_url": fd.resolver.BuildURL(r, "/server/security/thanks"),
	}
	if fd.cfg.Server.Pages.Contact.Enabled {
		data["report_url"] = fd.resolver.BuildURL(r, "/server/contact")
	}
	apperr.SendOK(w, data)
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
	current := fd.branding.load(fd.cfg).Config
	tools := mcpStandardTools(fd, r)
	items := make([]map[string]string, 0, len(tools))
	for _, tool := range tools {
		items = append(items, map[string]string{"name": tool.Name, "description": tool.Description})
	}
	aliases := []string{fd.resolver.BuildURL(r, "/api/mcp"), fd.resolver.BuildURL(r, "/mcp")}
	apperr.SendOK(w, map[string]any{"enabled": true, "url": fd.resolver.BuildURL(r, "/api/"+current.Server.APIVersion+"/server/mcp"), "aliases": aliases, "auth": map[string]string{"scheme": "Bearer"}, "tools": items, "project": current.Server.Pages.Help.MCP})
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
