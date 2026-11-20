package httpserver

import (
	"html/template"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/apimgr/shortner/src/common/sanitize"
)

func (fd *frontendDeps) operatorVariables(r *http.Request) footerVariables {
	overlay := fd.resolver.IsOverlay(r)
	return footerVariables{
		currentYear:    strconv.Itoa(overlayTime(time.Now(), overlay).Year()),
		projectName:    projectInfo.Name,
		projectOrg:     "apimgr",
		projectVersion: fd.version,
		buildDateTime:  footerBuildDateTime(fd.buildDate, overlay),
		onionAddress:   fd.resolver.overlayHost(OverlayTor),
		i2pAddress:     fd.resolver.overlayHost(OverlayI2P),
	}
}

func (fd *frontendDeps) operatorHTML(r *http.Request, name string, variables footerVariables) template.HTML {
	if fd.configDir == "" {
		return ""
	}
	content, _, _, err := sanitize.LoadOperatorContent(filepath.Join(fd.configDir, "html"), name, langFromContext(r), r.Host)
	if err != nil {
		log.Printf("operator content %s: %v", name, err)
		return ""
	}
	return sanitize.SanitizeOperatorHTML(expandFooterVariables(string(content), variables), name)
}
