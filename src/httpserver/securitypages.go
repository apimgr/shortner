// The AI.md PART 11 public security pages (/server/security,
// /server/security/policy, /server/security/thanks) and the PART 11
// "Compliance Routes" DPO page (/server/dpo).
package httpserver

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/security"
)

// defaultDisclosurePolicy is the built-in body of /server/security/policy,
// used whenever `web.security.policy` is empty. It carries the four things
// AI.md PART 11 requires the page to list: the coordinated-disclosure
// window, in-scope domains, out-of-scope behaviors, and safe-harbor
// language.
const defaultDisclosurePolicy = `We ask researchers to follow coordinated disclosure: report privately first, and give us 90 days to ship a fix before publishing details. If a fix lands sooner, we will agree a public date with you.

In scope: this deployment's own web interface, its API, and the software that serves them.

Out of scope: denial-of-service and volumetric testing, social engineering of operators or users, physical attacks, spam or automated scanning noise, findings that require a compromised device or a privileged account you already control, and reports from automated tools with no demonstrated impact.

Safe harbor: we will not pursue or support legal action against researchers who act in good faith under this policy — who avoid privacy violations, avoid destroying or exfiltrating data, avoid degrading service for others, and report promptly. If you are unsure whether an action is in scope, ask before you test.`

// securityPageData is bound to page/security.tmpl.
type securityPageData struct {
	Base PageData
	// ReportURL is the primary channel (private vulnerability reporting).
	ReportURL string
	// ContactFormURL carries the current {security_id}, so following it
	// puts the contact form in security-report mode.
	ContactFormURL string
	ContactEmail   string
	Expires        string
	HasPGPKey      bool
	PGPKeyURL      string
	Keyservers     []string
	SecurityTxtURL string
	PolicyURL      string
	ThanksURL      string
}

// securityHandler renders the human-readable mirror of security.txt, per
// AI.md PART 11 "Public Pages". Everything comes from live config and the
// current request, so there is nothing for an operator to edit.
func (fd *frontendDeps) securityHandler(w http.ResponseWriter, r *http.Request) {
	sec := fd.cfg.Web.Security
	data := securityPageData{
		Base:           fd.newPageData(r, requestCSRFToken(r, fd.cfg), "Security", fd.cfg.Server.SEO.Description),
		ReportURL:      sec.ReportURL,
		ContactEmail:   fd.securityContactEmail(r),
		Expires:        fd.securityExpires(),
		HasPGPKey:      fd.hasPGPKey() && sec.PublishPGPKey,
		PGPKeyURL:      fd.resolver.BuildURL(r, "/.well-known/pgp-key.asc"),
		Keyservers:     sec.Keyservers,
		SecurityTxtURL: fd.resolver.BuildURL(r, "/.well-known/security.txt"),
		PolicyURL:      "/server/security/policy",
		ThanksURL:      "/server/security/thanks",
	}
	if id := security.SecurityID(fd.installSecret, time.Now()); id != "" {
		data.ContactFormURL = "/server/contact?security_id=" + id
	}
	_ = renderPage(w, http.StatusOK, "security", data)
}

// securityPolicyPageData is bound to page/security_policy.tmpl.
type securityPolicyPageData struct {
	Base PageData
	// Paragraphs is the policy body split on blank lines. Splitting here
	// rather than in the template keeps the rendered text escaped: the
	// operator-supplied policy is never treated as HTML.
	Paragraphs []string
}

// securityPolicyHandler renders the coordinated-disclosure policy.
func (fd *frontendDeps) securityPolicyHandler(w http.ResponseWriter, r *http.Request) {
	policy := fd.cfg.Web.Security.Policy
	if policy == "" {
		policy = defaultDisclosurePolicy
	}
	var paragraphs []string
	for _, p := range strings.Split(policy, "\n\n") {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			paragraphs = append(paragraphs, trimmed)
		}
	}
	data := securityPolicyPageData{
		Base:       fd.newPageData(r, requestCSRFToken(r, fd.cfg), "Disclosure Policy", fd.cfg.Server.SEO.Description),
		Paragraphs: paragraphs,
	}
	_ = renderPage(w, http.StatusOK, "security_policy", data)
}

// securityThanksPageData is bound to page/security_thanks.tmpl.
type securityThanksPageData struct {
	Base   PageData
	Thanks []config.SecurityThanks
}

// securityThanksHandler renders the acknowledgments page. Entries are
// operator-curated in `web.security.thanks`; a researcher who chose not to
// be credited simply has no entry.
func (fd *frontendDeps) securityThanksHandler(w http.ResponseWriter, r *http.Request) {
	data := securityThanksPageData{
		Base:   fd.newPageData(r, requestCSRFToken(r, fd.cfg), "Security Acknowledgments", fd.cfg.Server.SEO.Description),
		Thanks: fd.cfg.Web.Security.Thanks,
	}
	_ = renderPage(w, http.StatusOK, "security_thanks", data)
}

// dpoPageData is bound to page/dpo.tmpl.
type dpoPageData struct {
	Base    PageData
	Name    string
	Email   string
	Address string
}

// dpoHandler renders the GDPR Data Protection Officer contact page, per
// AI.md PART 11 "Compliance Routes". It exists only when a compliance
// standard that requires a DPO is enabled — otherwise it is a 404, not an
// empty page, so a disabled standard advertises nothing.
func (fd *frontendDeps) dpoHandler(w http.ResponseWriter, r *http.Request) {
	if !fd.cfg.Server.Compliance.RequiresDPOContact() {
		apperr.SendError(w, apperr.New(apperr.CodeNotFound))
		return
	}
	dpo := fd.cfg.Server.Contact.DPO
	data := dpoPageData{
		Base:    fd.newPageData(r, requestCSRFToken(r, fd.cfg), "Data Protection Officer", fd.cfg.Server.SEO.Description),
		Name:    dpo.Name,
		Email:   dpo.Email,
		Address: dpo.Address,
	}
	_ = renderPage(w, http.StatusOK, "dpo", data)
}

// securityContactEmail resolves the CC address the same way security.txt
// does, defaulting to security@{fqdn} for the host this client used.
func (fd *frontendDeps) securityContactEmail(r *http.Request) string {
	if c := fd.cfg.Web.Security.Contact; c != "" {
		return c
	}
	if c := fd.cfg.Server.Contact.Security.Email; c != "" {
		return c
	}
	_, fqdn, _ := fd.resolver.GetURLVars(r)
	if fqdn == "" {
		return ""
	}
	return "security@" + fqdn
}

// securityExpires mirrors security.txt's Expires value.
func (fd *frontendDeps) securityExpires() string {
	if e := fd.cfg.Web.Security.Expires; e != "" {
		return e
	}
	return time.Now().UTC().AddDate(1, 0, 0).Format(time.RFC3339)
}

// hasPGPKey reports whether a project keypair has been generated.
func (fd *frontendDeps) hasPGPKey() bool {
	info, err := os.Stat(filepath.Join(fd.configDir, "security", "pgp.pub.asc"))
	return err == nil && !info.IsDir()
}
