package server

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/apimgr/shortner/src/config"
)

func (fd *frontendDeps) accentCSSHandler(w http.ResponseWriter, r *http.Request) {
	accent := strings.TrimSpace(fd.branding.load(fd.cfg).Accent)
	if !config.IsValidAccent(accent) {
		http.NotFound(w, r)
		return
	}
	dark := config.AccentForTheme(accent, "dark")
	light := config.AccentForTheme(accent, "light")
	css := fmt.Sprintf(":root{--color-primary:%s;--color-primary-bg:%s;--color-link:var(--color-primary)}\nhtml.theme-light{--color-primary:%s;--color-primary-bg:%s;--color-link:var(--color-primary)}\n@media(prefers-color-scheme:light){html.theme-auto{--color-primary:%s;--color-primary-bg:%s;--color-link:var(--color-primary)}}\n", dark, accentTint(dark, 15), light, accentTint(light, 12), light, accentTint(light, 12))
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", fmt.Sprint(len(css)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(css))
	}
}

func accentTint(value string, alpha int) string {
	color, err := strconv.ParseUint(strings.TrimPrefix(value, "#"), 16, 24)
	if err != nil {
		return "transparent"
	}
	return fmt.Sprintf("rgba(%d,%d,%d,%.2f)", (color>>16)&255, (color>>8)&255, color&255, float64(alpha)/100)
}
