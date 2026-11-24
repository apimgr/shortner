package httpserver

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/go-chi/chi/v5"
	"golang.org/x/net/html"
)

type healthyPageTestScheduler struct{}

func (healthyPageTestScheduler) Healthy() bool { return true }

// TestRegisteredServerPagesConform checks every static frontend page plus the
// registry-backed preference/help pages and representative not-found pages.
func TestRegisteredServerPagesConform(t *testing.T) {
	fd, ld := testFrontendDeps(t)
	fd.cfg.Server.Privacy.Data.Sold = true
	fd.cfg.Server.Compliance.GDPR = true
	router := chi.NewRouter()
	router.Use(frontendErrorContextMiddleware(fd))
	router.NotFound(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
	}))
	hd := &healthDeps{sqlDB: ld.sqlDB, dataDir: t.TempDir(), startTime: time.Now(), stats: &Stats{}, version: "test", buildDate: "2026-01-01", scheduler: healthyPageTestScheduler{}}
	fd.registerFrontendRoutes(router, hd, ld)
	router.Get("/server/preferences", fd.preferencesHandler)
	registerPreferencePages(router, fd, fd.cfg)

	paths := []string{
		"/", "/list", "/server/about", "/server/privacy", "/server/help",
		"/server/docs/swagger", "/server/docs/graphql", "/server/terms",
		"/server/contact", "/server/ccpa", "/server/security",
		"/server/security/policy", "/server/security/thanks", "/server/dpo",
		"/server/healthz", "/server/preferences",
	}
	for _, entry := range append(registeredPreferencePages(fd.cfg, "en"), registeredHelpPages(fd.cfg, "en")...) {
		paths = append(paths, entry.Path)
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Accept", "text/html")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Header().Get("Content-Type") == "" || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
				t.Fatalf("GET %s content type = %q, body %s", path, rec.Header().Get("Content-Type"), rec.Body.String())
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("registered/static GET %s status = %d, want 200: %s", path, rec.Code, rec.Body.String())
			}
			root, err := html.Parse(strings.NewReader(rec.Body.String()))
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			assertPageConformance(t, router, root, path)
		})
	}
	for _, path := range []string{"/not-a-real-short-code", "/server/security/report/not-a-real-report", "/server/not-registered"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Accept", "text/html")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("unknown route %s status = %d, want 404", path, rec.Code)
		}
		if !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
			t.Errorf("browser error %s content type = %q, want HTML", path, rec.Header().Get("Content-Type"))
			continue
		}
		root, err := html.Parse(strings.NewReader(rec.Body.String()))
		if err != nil {
			t.Errorf("parse error page %s: %v", path, err)
			continue
		}
		assertPageConformance(t, router, root, path)
	}
	for hub, entries := range map[string][]preferenceHubEntry{
		"/server/preferences": registeredPreferencePages(fd.cfg, "en"),
		"/server/help":        registeredHelpPages(fd.cfg, "en"),
	} {
		req := httptest.NewRequest(http.MethodGet, hub, nil)
		req.Header.Set("Accept", "text/html")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		for _, entry := range entries {
			if countHTMLAttribute(rec.Body.String(), "href", entry.Path) != 1 {
				t.Errorf("hub %s must link registered page %s exactly once", hub, entry.Path)
			}
		}
	}
}

func TestOperatorHeaderFragmentIsLastAndLeavesSharedHeaderAlone(t *testing.T) {
	plain := httptest.NewRecorder()
	if err := renderPage(plain, http.StatusOK, "home", homePageData{Base: PageData{SiteName: "Shortner", Lang: "en", Dir: "ltr", Theme: "dark", CurrentPath: "/", Title: "Shortner", TitleSeparator: " | ", Robots: "index,follow"}}); err != nil {
		t.Fatal(err)
	}
	withFragment := httptest.NewRecorder()
	data := homePageData{Base: PageData{SiteName: "Shortner", Lang: "en", Dir: "ltr", Theme: "dark", CurrentPath: "/", Title: "Shortner", TitleSeparator: " | ", Robots: "index,follow", OperatorHeader: template.HTML(`<span id="operator-fragment">Notice</span>`)}}
	if err := renderPage(withFragment, http.StatusOK, "home", data); err != nil {
		t.Fatal(err)
	}
	plainHeader := siteHeaderNode(t, plain.Body.String())
	fragmentHeader := siteHeaderNode(t, withFragment.Body.String())
	plainChildren := elementChildren(plainHeader)
	fragmentChildren := elementChildren(fragmentHeader)
	if len(fragmentChildren) != len(plainChildren)+1 || !nodeHasClass(fragmentChildren[len(fragmentChildren)-1], "operator-header-content") {
		t.Fatalf("operator header is not the final header child")
	}
	fragmentElements := elementChildren(fragmentChildren[len(fragmentChildren)-1])
	if len(fragmentElements) != 1 || attrValue(fragmentElements[0], "id") != "operator-fragment" {
		t.Fatal("operator header fragment was not preserved in its final wrapper")
	}
	for i := range plainChildren {
		if plainChildren[i].Data != fragmentChildren[i].Data || attrValue(plainChildren[i], "class") != attrValue(fragmentChildren[i], "class") || attrValue(plainChildren[i], "href") != attrValue(fragmentChildren[i], "href") {
			t.Fatalf("operator fragment changed shared header child %d", i)
		}
	}
}

func siteHeaderNode(t *testing.T, source string) *html.Node {
	t.Helper()
	root, err := html.Parse(strings.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	var found *html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "header" && nodeHasClass(node, "site-header") {
			found = node
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	if found == nil {
		t.Fatal("page has no shared site header")
	}
	return found
}

func elementChildren(node *html.Node) []*html.Node {
	var children []*html.Node
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode {
			children = append(children, child)
		}
	}
	return children
}

func countHTMLAttribute(source, key, value string) int {
	root, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return 0
	}
	count := 0
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		for _, attr := range node.Attr {
			if node.Type == html.ElementNode && attr.Key == key && attr.Val == value {
				count++
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return count
}

func assertPageConformance(t *testing.T, router chi.Router, root *html.Node, path string) {
	t.Helper()
	var all []*html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		all = append(all, node)
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	count := func(tag string) int {
		n := 0
		for _, node := range all {
			if node.Type == html.ElementNode && node.Data == tag {
				n++
			}
		}
		return n
	}
	if count("main") != 1 || count("h1") != 1 {
		t.Errorf("%s has %d main elements and %d h1 elements; want one each", path, count("main"), count("h1"))
	}
	pageHeaders, pageLeads := 0, 0
	for _, node := range all {
		if node.Type != html.ElementNode {
			continue
		}
		if node.Data == "header" && nodeHasClass(node, "page-header") {
			pageHeaders++
		}
		if node.Data == "p" && nodeHasClass(node, "page-lead") {
			pageLeads++
		}
		if node.Data == "h2" {
			section := nearestElement(node.Parent, "section")
			if section == nil || !nodeHasClass(section, "section-card") || attrValue(section, "aria-labelledby") != attrValue(node, "id") {
				t.Errorf("%s h2 is not the accessible heading of a shared section card", path)
			}
		}
	}
	if pageHeaders != 1 || pageLeads != 1 {
		t.Errorf("%s has %d page headers and %d leads, want one each", path, pageHeaders, pageLeads)
	}
	containerModifiers := 0
	for _, node := range all {
		if node.Type == html.ElementNode && node.Data == "div" && nodeHasClass(node, "container") {
			for _, modifier := range []string{"container-sm", "container-md", "container-lg"} {
				if nodeHasClass(node, modifier) {
					containerModifiers++
				}
			}
		}
	}
	if containerModifiers != 1 {
		t.Errorf("%s has %d shared width modifiers, want exactly one", path, containerModifiers)
	}
	for _, node := range all {
		if node.Type == html.ElementNode && node.Data == "h1" && !nodeHasClass(node, "page-title") {
			t.Errorf("%s h1 is missing .page-title", path)
		}
	}
	for _, node := range all {
		if node.Type != html.ElementNode {
			continue
		}
		attrs := map[string]string{}
		for _, attr := range node.Attr {
			attrs[strings.ToLower(attr.Key)] = attr.Val
			key := strings.ToLower(attr.Key)
			if key == "style" || strings.HasPrefix(key, "on") {
				t.Errorf("%s contains forbidden %s attribute on <%s>", path, key, node.Data)
			}
		}
		switch node.Data {
		case "style":
			t.Errorf("%s contains a style element", path)
		case "img":
			if _, ok := attrs["alt"]; !ok {
				t.Errorf("%s has an image without alt", path)
			}
		case "script":
			if attrs["type"] != "application/ld+json" && strings.TrimSpace(nodeText(node)) != "" {
				t.Errorf("%s contains an executable inline script", path)
			}
		case "form":
			if strings.EqualFold(attrs["method"], http.MethodPost) && !hasDescendantInput(node, "csrf_token") {
				t.Errorf("%s has a POST form without csrf_token", path)
			}
		case "input", "select", "textarea":
			if attrs["type"] != "hidden" && attrs["type"] != "submit" && !controlHasLabel(node, attrs, all) {
				t.Errorf("%s has an unlabeled <%s> control", path, node.Data)
			}
		case "table":
			if !hasDescendantTag(node, "thead") || !hasClassAncestor(node, "table-wrapper") {
				t.Errorf("%s has a table without thead or .table-wrapper", path)
			}
		}
	}
	assertSkipLinkOrder(t, all, path)
	assertPageBreadcrumb(t, all, path)
	assertHeaderConformance(t, all, path)
	assertInternalLinks(t, router, all, path)
}

func nearestElement(node *html.Node, tag string) *html.Node {
	for current := node; current != nil; current = current.Parent {
		if current.Type == html.ElementNode && current.Data == tag {
			return current
		}
	}
	return nil
}

func nodeHasClass(node *html.Node, class string) bool {
	for _, attr := range node.Attr {
		if attr.Key == "class" && containsClass(attr.Val, class) {
			return true
		}
	}
	return false
}

func controlHasLabel(node *html.Node, attrs map[string]string, all []*html.Node) bool {
	if id := attrs["id"]; id != "" {
		for _, candidate := range all {
			if candidate.Type == html.ElementNode && candidate.Data == "label" {
				for _, attr := range candidate.Attr {
					if attr.Key == "for" && attr.Val == id {
						return true
					}
				}
			}
		}
	}
	for parent := node.Parent; parent != nil; parent = parent.Parent {
		if parent.Type == html.ElementNode && parent.Data == "label" {
			return true
		}
	}
	return false
}

func assertHeaderConformance(t *testing.T, nodes []*html.Node, path string) {
	t.Helper()
	var header *html.Node
	for _, node := range nodes {
		if node.Type == html.ElementNode && node.Data == "header" {
			for _, attr := range node.Attr {
				if attr.Key == "class" && containsClass(attr.Val, "site-header") {
					header = node
				}
			}
		}
	}
	if header == nil {
		t.Errorf("%s has no shared site header", path)
		return
	}
	var children []*html.Node
	for child := header.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode {
			children = append(children, child)
		}
	}
	if len(children) != 4 || children[0].Data != "a" || !nodeHasClass(children[0], "brand") || children[1].Data != "nav" || !nodeHasClass(children[1], "nav-links") || children[2].Data != "label" || !nodeHasClass(children[2], "nav-toggle") || children[3].Data != "div" || !nodeHasClass(children[3], "header-actions") {
		t.Errorf("%s shared header elements do not follow brand, navigation, hamburger, actions order", path)
	}
	for _, node := range nodes {
		if node.Type == html.ElementNode && node.Data == "a" {
			var href, current, class string
			for _, attr := range node.Attr {
				switch attr.Key {
				case "href":
					href = attr.Val
				case "aria-current":
					current = attr.Val
				case "class":
					class = attr.Val
				}
			}
			if current == "page" && !containsClass(class, "active") {
				t.Errorf("%s current navigation link %q lacks .active", path, href)
			}
		}
	}
	var nav *html.Node
	for _, node := range nodes {
		if node.Type == html.ElementNode && node.Data == "nav" && nodeHasID(node, "navigation") {
			nav = node
		}
	}
	if nav == nil {
		t.Errorf("%s header has no navigation landmark", path)
		return
	}
	activeFound := false
	for child := nav.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && (child.Data != "a" || (attrValue(child, "href") != "/" && attrValue(child, "href") != "/list")) {
			t.Errorf("%s navigation includes non-section item <%s href=%q>", path, child.Data, attrValue(child, "href"))
		}
		if child.Type == html.ElementNode && child.Data == "a" && attrValue(child, "href") == path && attrValue(child, "aria-current") == "page" && nodeHasClass(child, "active") {
			activeFound = true
		}
	}
	if (path == "/" || path == "/list") && !activeFound {
		t.Errorf("%s navigation has no active current-page link", path)
	}
	if !hasDescendantInput(header, "csrf_token") || !hasDescendantNamedSubmit(header, "theme") {
		t.Errorf("%s header theme switch is not a CSRF-protected POST form", path)
	}
	if attrValue(findByID(nodes, "nav-toggle"), "type") != "checkbox" || !hasLabelFor(nodes, "nav-toggle") {
		t.Errorf("%s mobile navigation toggle is not a labeled checkbox", path)
	}
}

func findByID(nodes []*html.Node, id string) *html.Node {
	for _, node := range nodes {
		if node.Type == html.ElementNode && attrValue(node, "id") == id {
			return node
		}
	}
	return nil
}

func hasLabelFor(nodes []*html.Node, id string) bool {
	for _, node := range nodes {
		if node.Type == html.ElementNode && node.Data == "label" && attrValue(node, "for") == id {
			return true
		}
	}
	return false
}

func hasDescendantNamedSubmit(node *html.Node, name string) bool {
	if node.Type == html.ElementNode && node.Data == "button" && attrValue(node, "name") == name && attrValue(node, "type") == "submit" {
		return true
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if hasDescendantNamedSubmit(child, name) {
			return true
		}
	}
	return false
}

func nodeHasID(node *html.Node, id string) bool {
	return attrValue(node, "id") == id
}

func attrValue(node *html.Node, key string) string {
	for _, attr := range node.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func assertInternalLinks(t *testing.T, router chi.Router, nodes []*html.Node, page string) {
	t.Helper()
	for _, node := range nodes {
		if node.Type != html.ElementNode || node.Data != "a" {
			continue
		}
		href := attrValue(node, "href")
		parsed, err := url.Parse(href)
		if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Path == "" {
			continue
		}
		path := parsed.EscapedPath()
		if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/static/") || strings.HasPrefix(path, "/.well-known/") {
			continue
		}
		ctx := chi.NewRouteContext()
		if !router.Match(ctx, http.MethodGet, path) {
			t.Errorf("%s has unresolved internal link %q", page, href)
		}
	}
}

func nodeText(node *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return b.String()
}

func hasDescendantInput(node *html.Node, name string) bool {
	if node.Type == html.ElementNode && node.Data == "input" {
		for _, attr := range node.Attr {
			if attr.Key == "name" && attr.Val == name {
				return true
			}
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if hasDescendantInput(child, name) {
			return true
		}
	}
	return false
}

func hasDescendantTag(node *html.Node, tag string) bool {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.Data == tag || hasDescendantTag(child, tag) {
			return true
		}
	}
	return false
}

func hasClassAncestor(node *html.Node, class string) bool {
	for parent := node.Parent; parent != nil; parent = parent.Parent {
		if parent.Type != html.ElementNode {
			continue
		}
		for _, attr := range parent.Attr {
			if attr.Key == "class" && containsClass(attr.Val, class) {
				return true
			}
		}
	}
	return false
}

func containsClass(classList, class string) bool {
	for _, item := range strings.Fields(classList) {
		if item == class {
			return true
		}
	}
	return false
}

func assertSkipLinkOrder(t *testing.T, nodes []*html.Node, path string) {
	t.Helper()
	var first []string
	for _, node := range nodes {
		if node.Type != html.ElementNode {
			continue
		}
		href := ""
		for _, attr := range node.Attr {
			if attr.Key == "href" {
				href = attr.Val
			}
		}
		focusable := node.Data == "button" || node.Data == "input" || node.Data == "select" || node.Data == "textarea" || (node.Data == "a" && href != "")
		if focusable {
			first = append(first, href)
			if len(first) == 2 {
				break
			}
		}
	}
	if len(first) != 2 || first[0] != "#main-content" || first[1] != "#navigation" {
		t.Errorf("%s first focusable links = %v, want main then navigation skip links", path, first)
	}
}

func assertPageBreadcrumb(t *testing.T, nodes []*html.Node, path string) {
	t.Helper()
	var expected string
	switch path {
	case "/server/preferences/tokens", "/server/preferences/sync":
		expected = "/server/preferences"
	case "/server/help/mcp":
		expected = "/server/help"
	}
	count, correct := 0, false
	for _, node := range nodes {
		if node.Type != html.ElementNode || node.Data != "nav" {
			continue
		}
		for _, attr := range node.Attr {
			if attr.Key == "class" && containsClass(attr.Val, "breadcrumb") {
				count++
				correct = correct || hasDescendantHref(node, expected)
			}
		}
	}
	if expected == "" && (path == "/server/preferences" || path == "/server/help") && count != 0 {
		t.Errorf("hub %s has a breadcrumb", path)
	}
	if expected != "" && (count != 1 || !correct) {
		t.Errorf("sub-page %s has %d breadcrumbs to parent %s", path, count, expected)
	}
}

func hasDescendantHref(node *html.Node, href string) bool {
	if node.Type == html.ElementNode && node.Data == "a" {
		for _, attr := range node.Attr {
			if attr.Key == "href" && attr.Val == href {
				return true
			}
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if hasDescendantHref(child, href) {
			return true
		}
	}
	return false
}
