package httputil

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// HTML2TextConverter converts rendered HTML to readable, width-wrapped text.
// Non-interactive clients do not receive forms or scripts because they cannot
// use them.
func HTML2TextConverter(source string, width int) string {
	if width < 20 {
		width = 80
	}
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return strings.TrimSpace(stripHTMLTags(source))
	}
	var out strings.Builder
	convertNode(&out, doc, width)
	return strings.TrimSpace(out.String()) + "\n"
}

func convertNode(out *strings.Builder, node *html.Node, width int) {
	if node.Type != html.ElementNode {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			convertNode(out, child, width)
		}
		return
	}
	switch node.Data {
	case "script", "style", "form", "button", "input", "select", "textarea", "noscript":
		return
	case "h1":
		line := strings.Repeat("═", width)
		writeBlock(out, line+"\n"+centerText(strings.ToUpper(inlineText(node)), width)+"\n"+line, 2)
	case "h2":
		writeBlock(out, "─── "+inlineText(node)+" ───", 2)
	case "h3":
		writeBlock(out, "► "+inlineText(node), 2)
	case "p":
		if text := inlineText(node); text != "" {
			writeBlock(out, wrapText(text, width), 2)
		}
	case "div", "header", "footer", "section", "article", "main", "aside":
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			convertNode(out, child, width)
		}
	case "ul", "ol":
		convertList(out, node, width, node.Data == "ol")
	case "a":
		label := nodeText(node)
		if href := nodeAttr(node, "href"); href != "" && href != label {
			label += " [" + href + "]"
		}
		out.WriteString(label)
	case "strong", "b":
		out.WriteString("*" + nodeText(node) + "*")
	case "em", "i":
		out.WriteString("_" + nodeText(node) + "_")
	case "code":
		out.WriteString("`" + nodeText(node) + "`")
	case "pre":
		for _, line := range strings.Split(nodeText(node), "\n") {
			out.WriteString("    " + line + "\n")
		}
		out.WriteByte('\n')
	case "table":
		convertTable(out, node, width)
	case "hr":
		writeBlock(out, strings.Repeat("─", width), 2)
	case "blockquote":
		for _, line := range strings.Split(wrapText(nodeText(node), width-2), "\n") {
			out.WriteString("│ " + line + "\n")
		}
		out.WriteByte('\n')
	case "br":
		out.WriteByte('\n')
	case "li":
		out.WriteString(nodeText(node))
	default:
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			convertNode(out, child, width)
		}
	}
}

func inlineText(node *html.Node) string {
	var out strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			out.WriteString(current.Data)
			return
		}
		if current.Type != html.ElementNode {
			for child := current.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
			return
		}
		switch current.Data {
		case "script", "style", "form", "button", "input", "select", "textarea", "noscript":
			return
		case "strong", "b":
			out.WriteByte('*')
			for child := current.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
			out.WriteByte('*')
		case "em", "i":
			out.WriteByte('_')
			for child := current.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
			out.WriteByte('_')
		case "code":
			out.WriteByte('`')
			for child := current.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
			out.WriteByte('`')
		case "br":
			out.WriteByte(' ')
		default:
			for child := current.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
		}
	}
	walk(node)
	return strings.Join(strings.Fields(out.String()), " ")
}

func convertList(out *strings.Builder, node *html.Node, width int, ordered bool) {
	index := 0
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != html.ElementNode || child.Data != "li" {
			continue
		}
		index++
		prefix := "  • "
		if ordered {
			prefix = "  " + strconv.Itoa(index) + ". "
		}
		wrapped := strings.Split(wrapText(nodeText(child), width-len([]rune(prefix))), "\n")
		for i, line := range wrapped {
			if i == 0 {
				out.WriteString(prefix)
			} else {
				out.WriteString(strings.Repeat(" ", len([]rune(prefix))))
			}
			out.WriteString(line + "\n")
		}
	}
	out.WriteByte('\n')
}

func convertTable(out *strings.Builder, node *html.Node, width int) {
	for row := node.FirstChild; row != nil; row = row.NextSibling {
		if row.Type != html.ElementNode || (row.Data != "thead" && row.Data != "tbody" && row.Data != "tr") {
			continue
		}
		if row.Data == "tr" {
			writeTableRow(out, row)
			continue
		}
		for tr := row.FirstChild; tr != nil; tr = tr.NextSibling {
			if tr.Type == html.ElementNode && tr.Data == "tr" {
				writeTableRow(out, tr)
			}
		}
	}
	if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n\n") {
		out.WriteByte('\n')
	}
	_ = width
}

func writeTableRow(out *strings.Builder, row *html.Node) {
	var cells []string
	for cell := row.FirstChild; cell != nil; cell = cell.NextSibling {
		if cell.Type == html.ElementNode && (cell.Data == "td" || cell.Data == "th") {
			cells = append(cells, nodeText(cell))
		}
	}
	if len(cells) > 0 {
		out.WriteString("│ " + strings.Join(cells, " │ ") + " │\n")
	}
}

func nodeText(node *html.Node) string {
	var out strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			out.WriteString(current.Data)
			out.WriteByte(' ')
			return
		}
		if current.Type == html.ElementNode && (current.Data == "script" || current.Data == "style") {
			return
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	text := strings.Join(strings.Fields(out.String()), " ")
	return strings.NewReplacer(" .", ".", " ,", ",", " !", "!", " ?", "?", " ;", ";", " :", ":", " )", ")", " ]", "]").Replace(text)
}

func nodeAttr(node *html.Node, key string) string {
	for _, attr := range node.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func wrapText(text string, width int) string {
	words := strings.Fields(text)
	var lines []string
	line := ""
	for _, word := range words {
		if line == "" {
			line = word
		} else if len([]rune(line))+1+len([]rune(word)) <= width {
			line += " " + word
		} else {
			lines = append(lines, line)
			line = word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func centerText(text string, width int) string {
	padding := width - len([]rune(text))
	if padding <= 0 {
		return text
	}
	left := padding / 2
	return strings.Repeat(" ", left) + text
}

func writeBlock(out *strings.Builder, value string, newlines int) {
	trimmed := strings.TrimRight(out.String(), "\n")
	if trimmed != "" {
		out.Reset()
		out.WriteString(trimmed)
		out.WriteString(strings.Repeat("\n", newlines))
	}
	out.WriteString(value)
	out.WriteString(strings.Repeat("\n", newlines))
}

func stripHTMLTags(source string) string {
	var out strings.Builder
	inTag := false
	for _, char := range source {
		if char == '<' {
			inTag = true
			continue
		}
		if char == '>' {
			inTag = false
			out.WriteByte(' ')
			continue
		}
		if !inTag {
			out.WriteRune(char)
		}
	}
	return strings.Join(strings.Fields(out.String()), " ")
}
