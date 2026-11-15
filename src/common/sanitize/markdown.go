package sanitize

import (
	"bytes"
	"html/template"
	"net/url"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark/v2/extension"
	"github.com/yuin/goldmark/v2/parser"
	markdownhtml "github.com/yuin/goldmark/v2/renderer/html"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var markdownParser = parser.New(parser.WithExtensions(extension.NewGFMParser()))
var markdownRenderer = markdownhtml.New(markdownhtml.WithExtensions(extension.NewGFMHTMLRenderer()))

func markdownPolicy() *bluemonday.Policy {
	policy := bluemonday.NewPolicy()
	policy.AllowElements("p", "br", "hr", "blockquote", "ul", "ol", "li", "h1", "h2", "h3", "h4", "h5", "h6", "em", "strong", "del", "a", "pre", "code", "table", "thead", "tbody", "tr", "th", "td")
	policy.AllowAttrs("href", "title", "rel").OnElements("a")
	policy.AllowAttrs("class").OnElements("code")
	policy.AllowAttrs("align").OnElements("th", "td")
	policy.AllowURLSchemes("http", "https", "mailto", "tel")
	policy.AllowRelativeURLs(true)
	policy.RequireParseableURLs(true)
	return policy
}

// RenderMarkdown converts operator-configured Markdown to a small, sanitized
// HTML subset suitable for html/template. Raw HTML is not enabled in the
// parser; the final output also passes through an explicit allow-list policy.
func RenderMarkdown(source, requestHost string) template.HTML {
	if strings.TrimSpace(source) == "" {
		return ""
	}
	input := []byte(source)
	var rendered bytes.Buffer
	doc := markdownParser.Parse(input)
	if err := markdownRenderer.Render(&rendered, input, doc); err != nil {
		return template.HTML(template.HTMLEscapeString(source))
	}
	safe := markdownPolicy().Sanitize(rendered.String())
	fragments, err := html.ParseFragment(strings.NewReader(safe), &html.Node{Type: html.ElementNode, DataAtom: atom.Div, Data: "div"})
	if err != nil {
		return template.HTML(template.HTMLEscapeString(source))
	}
	for _, fragment := range fragments {
		addExternalLinkRel(fragment, requestHost)
	}
	var output bytes.Buffer
	for _, fragment := range fragments {
		if err := html.Render(&output, fragment); err != nil {
			return template.HTML(template.HTMLEscapeString(source))
		}
	}
	return template.HTML(output.String())
}

func addExternalLinkRel(node *html.Node, requestHost string) {
	if node.Type == html.ElementNode && node.Data == "a" {
		for _, attr := range node.Attr {
			if attr.Key != "href" {
				continue
			}
			parsed, err := url.Parse(attr.Val)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || (requestHost != "" && strings.EqualFold(parsed.Host, requestHost)) {
				break
			}
			node.Attr = append(node.Attr, html.Attribute{Key: "rel", Val: "noopener noreferrer nofollow ugc"})
			break
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		addExternalLinkRel(child, requestHost)
	}
}
