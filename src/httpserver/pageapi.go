package httpserver

import (
	"net/http"

	"github.com/apimgr/shortner/src/apperr"
)

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
