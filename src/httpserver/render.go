// Frontend HTML rendering, per AI.md PART 16 "Technology Stack": server-
// side Go html/template only, no client-side rendering framework.
package httpserver

import (
	"bytes"
	"html/template"
	"net/http"
	"sync"

	"github.com/apimgr/shortner/src/server"
)

var (
	pageTmplMu    sync.Mutex
	pageTmplCache = map[string]*template.Template{}
)

// baseTemplateFiles are parsed into every page's *template.Template — the
// shared layout, all partials, and all components.
var baseTemplateFiles = []string{
	"template/layout/*.tmpl",
	"template/partial/*.tmpl",
	"template/partial/public/*.tmpl",
	"template/component/*.tmpl",
}

// pageTemplate returns the cached *template.Template for page name
// (without the .tmpl extension), parsing and caching it on first use.
func pageTemplate(name string) (*template.Template, error) {
	pageTmplMu.Lock()
	defer pageTmplMu.Unlock()

	if t, ok := pageTmplCache[name]; ok {
		return t, nil
	}

	t := template.New("base")
	for _, pattern := range baseTemplateFiles {
		var err error
		t, err = t.ParseFS(server.TemplateFS, pattern)
		if err != nil {
			return nil, err
		}
	}
	t, err := t.ParseFS(server.TemplateFS, "template/page/"+name+".tmpl")
	if err != nil {
		return nil, err
	}
	pageTmplCache[name] = t
	return t, nil
}

// renderPage executes the "layout" template for page name with data into
// an in-memory buffer first, so a template execution error never leaves a
// half-written response with a 200 already sent — only once execution
// succeeds does it write the real status and body to w. Content-Type
// defaults to text/html; charset UTF-8, matching every server-rendered
// frontend response.
func renderPage(w http.ResponseWriter, status int, name string, data any) error {
	t, err := pageTemplate(name)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", data); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, err = buf.WriteTo(w)
	return err
}
