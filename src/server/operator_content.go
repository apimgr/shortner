package server

import (
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/common/sanitize"
	"github.com/apimgr/shortner/src/config"
)

func (fd *frontendDeps) operatorVariables(r *http.Request) footerVariables {
	overlay := fd.resolver.IsOverlay(r)
	onionAddress := fd.resolver.overlayHost(OverlayTor)
	i2pAddress := fd.resolver.overlayHost(OverlayI2P)
	if !fd.cfg.Server.Tor.Expose {
		onionAddress = ""
	}
	if !fd.cfg.Server.I2P.Expose {
		i2pAddress = ""
	}
	return footerVariables{
		currentYear:    strconv.Itoa(overlayTime(time.Now(), overlay).Year()),
		projectName:    projectInfo.Name,
		projectOrg:     "apimgr",
		projectVersion: fd.version,
		buildDateTime:  footerBuildDateTime(fd.buildDate, overlay),
		onionAddress:   onionAddress,
		i2pAddress:     i2pAddress,
	}
}

func (fd *frontendDeps) operatorHTML(r *http.Request, name string, variables footerVariables) template.HTML {
	entry, ok := configuredOperatorContent(fd.branding.load(fd.cfg).Branding.Content, name)
	if !ok {
		return ""
	}
	raw, ok := entry.Body.Resolve(langFromContext(r))
	if !ok || len(raw) > 256*1024 || strings.Contains(strings.ToLower(raw), "/server/contact") {
		log.Printf("branding.content.%s: invalid or unavailable body; using built-in content", name)
		return ""
	}
	if name == "footer" && strings.TrimSpace(raw) == "" {
		return ""
	}
	expanded := expandFooterVariables(raw, variables)
	format := strings.ToLower(strings.TrimSpace(entry.Format))
	if format == "" {
		if name == "header" || name == "footer" {
			format = "html"
		} else {
			format = "md"
		}
	}
	var rendered string
	switch format {
	case "html":
		rendered = expanded
	case "md":
		rendered = string(sanitize.RenderMarkdown(expanded, r.Host))
	case "txt":
		paragraphs := strings.Split(strings.TrimSpace(expanded), "\n\n")
		for index := range paragraphs {
			paragraphs[index] = "<p>" + template.HTMLEscapeString(strings.TrimSpace(paragraphs[index])) + "</p>"
		}
		rendered = strings.Join(paragraphs, "\n")
	default:
		log.Printf("branding.content.%s: unsupported format; using built-in content", name)
		return ""
	}
	safe := sanitize.SanitizeOperatorHTML(rendered, name)
	if string(safe) != rendered {
		log.Printf("branding.content.%s: unsafe markup was sanitized", name)
	}
	return safe
}

func configuredOperatorContent(content config.BrandingContent, name string) (config.OperatorPageContent, bool) {
	switch name {
	case "header":
		return content.Header, content.Header.Body.Has
	case "about":
		return content.About, content.About.Body.Has
	case "privacy":
		return content.Privacy, content.Privacy.Body.Has
	case "footer":
		return content.Footer, content.Footer.Body.Has
	case "terms":
		return content.Terms, content.Terms.Body.Has
	default:
		return config.OperatorPageContent{}, false
	}
}
