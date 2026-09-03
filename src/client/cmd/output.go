package cmd

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/apimgr/shortner/src/client/api"
	"github.com/apimgr/shortner/src/common/theme"
)

// Printer renders command results in the format selected by --output.
type Printer struct {
	out    io.Writer
	errOut io.Writer
	format string
	color  bool
	quiet  bool
}

// NewPrinter builds a Printer for the given format.
func NewPrinter(out, errOut io.Writer, format string, colorEnabled, quiet bool) *Printer {
	return &Printer{
		out:    out,
		errOut: errOut,
		format: strings.ToLower(strings.TrimSpace(format)),
		color:  colorEnabled,
		quiet:  quiet,
	}
}

// Format reports the active output format.
func (p *Printer) Format() string { return p.format }

// colorize wraps text in an ANSI 256-color escape when color is enabled.
func (p *Printer) colorize(code, text string) string {
	if !p.color || code == "" {
		return text
	}
	return fmt.Sprintf("\033[38;5;%sm%s\033[0m", code, text)
}

// palette returns the terminal palette used for CLI coloring.
func (p *Printer) palette() theme.TerminalPalette {
	return theme.TerminalPaletteDark
}

// Message prints an informational line. Nothing is printed in quiet mode or
// in a machine-readable format, where stray prose would corrupt the output.
func (p *Printer) Message(format string, args ...any) {
	if p.quiet || p.format == "json" || p.format == "yaml" || p.format == "csv" {
		return
	}
	fmt.Fprintf(p.out, format+"\n", args...)
}

// Warn prints a warning to stderr. Warnings are never suppressed by the
// output format, only by --quiet.
func (p *Printer) Warn(format string, args ...any) {
	if p.quiet {
		return
	}
	pal := p.palette()
	fmt.Fprintln(p.errOut, p.colorize(pal.Warning, "warning: ")+fmt.Sprintf(format, args...))
}

// Error prints an error to stderr.
func (p *Printer) Error(format string, args ...any) {
	pal := p.palette()
	fmt.Fprintln(p.errOut, p.colorize(pal.Error, "error: ")+fmt.Sprintf(format, args...))
}

// structured renders a value as JSON or YAML, returning false when the
// active format is not machine-readable.
func (p *Printer) structured(value any) (bool, error) {
	switch p.format {
	case "json":
		encoder := json.NewEncoder(p.out)
		encoder.SetIndent("", "  ")
		return true, encoder.Encode(value)
	case "yaml":
		encoder := yaml.NewEncoder(p.out)
		encoder.SetIndent(2)
		if err := encoder.Encode(value); err != nil {
			return true, err
		}
		return true, encoder.Close()
	default:
		return false, nil
	}
}

// linkRow flattens a link into table/csv columns.
func linkRow(link api.Link) []string {
	expires := "never"
	if link.ExpiresAt != nil && *link.ExpiresAt != "" {
		expires = *link.ExpiresAt
	}
	return []string{
		link.ShortCode,
		link.DestinationURL,
		strconv.FormatInt(link.ClickCount, 10),
		link.CreatedAt,
		expires,
	}
}

// linkHeaders are the column titles for link tables.
var linkHeaders = []string{"CODE", "DESTINATION", "CLICKS", "CREATED", "EXPIRES"}

// PrintLink renders a single link.
func (p *Printer) PrintLink(link *api.Link) error {
	if done, err := p.structured(link); done {
		return err
	}
	switch p.format {
	case "csv":
		return p.writeCSV(linkHeaders, [][]string{linkRow(*link)})
	case "plain":
		fmt.Fprintln(p.out, strings.Join(linkRow(*link), "\t"))
		return nil
	default:
		p.writeKeyValue([][2]string{
			{"Short code", link.ShortCode},
			{"Short URL", link.ShortURL},
			{"Destination", link.DestinationURL},
			{"Clicks", strconv.FormatInt(link.ClickCount, 10)},
			{"Created", link.CreatedAt},
			{"Expires", linkRow(*link)[4]},
		})
		return nil
	}
}

// PrintCreatedLink renders a newly created link plus its one-time owner
// token, which the server never returns again.
func (p *Printer) PrintCreatedLink(created *api.CreatedLink) error {
	if done, err := p.structured(created); done {
		return err
	}
	switch p.format {
	case "csv":
		row := append(linkRow(created.Link), created.OwnerToken)
		return p.writeCSV(append(append([]string{}, linkHeaders...), "OWNER_TOKEN"), [][]string{row})
	case "plain":
		fmt.Fprintln(p.out, created.ShortURL)
		if created.OwnerToken != "" {
			fmt.Fprintln(p.out, created.OwnerToken)
		}
		return nil
	default:
		rows := [][2]string{
			{"Short URL", created.ShortURL},
			{"Short code", created.ShortCode},
			{"Destination", created.DestinationURL},
			{"Expires", linkRow(created.Link)[4]},
		}
		if created.OwnerToken != "" {
			rows = append(rows, [2]string{"Owner token", created.OwnerToken})
		}
		p.writeKeyValue(rows)
		if created.OwnerToken != "" && !p.quiet {
			p.Message("")
			p.Message("Save the owner token now — it is shown once and cannot be recovered.")
		}
		return nil
	}
}

// PrintLinks renders a paginated listing.
func (p *Printer) PrintLinks(list *api.LinkList) error {
	if done, err := p.structured(list); done {
		return err
	}

	rows := make([][]string, 0, len(list.Data))
	for _, link := range list.Data {
		rows = append(rows, linkRow(link))
	}

	switch p.format {
	case "csv":
		return p.writeCSV(linkHeaders, rows)
	case "plain":
		for _, row := range rows {
			fmt.Fprintln(p.out, strings.Join(row, "\t"))
		}
		return nil
	default:
		if len(rows) == 0 {
			p.Message("No links found.")
			return nil
		}
		p.writeTable(linkHeaders, rows)
		if !p.quiet {
			p.Message("")
			p.Message("Page %d of %d (%d links total)", list.Pagination.Page, list.Pagination.Pages, list.Pagination.Total)
		}
		return nil
	}
}

// PrintStats renders a link's click analytics.
func (p *Printer) PrintStats(stats *api.Stats) error {
	if done, err := p.structured(stats); done {
		return err
	}

	switch p.format {
	case "csv":
		rows := make([][]string, 0, len(stats.TimeSeries))
		for _, point := range stats.TimeSeries {
			rows = append(rows, []string{point.Date, strconv.Itoa(point.Count)})
		}
		return p.writeCSV([]string{"DATE", "CLICKS"}, rows)
	case "plain":
		fmt.Fprintf(p.out, "%s\t%d\n", stats.ShortCode, stats.TotalClicks)
		for _, point := range stats.TimeSeries {
			fmt.Fprintf(p.out, "%s\t%d\n", point.Date, point.Count)
		}
		return nil
	default:
		p.writeKeyValue([][2]string{
			{"Short code", stats.ShortCode},
			{"Total clicks", strconv.FormatInt(stats.TotalClicks, 10)},
		})
		if len(stats.TimeSeries) > 0 {
			p.Message("")
			rows := make([][]string, 0, len(stats.TimeSeries))
			for _, point := range stats.TimeSeries {
				rows = append(rows, []string{point.Date, strconv.Itoa(point.Count)})
			}
			p.writeTable([]string{"DATE", "CLICKS"}, rows)
		}
		if len(stats.Referrers) > 0 {
			p.Message("")
			rows := make([][]string, 0, len(stats.Referrers))
			for referrer, count := range stats.Referrers {
				rows = append(rows, []string{referrer, strconv.Itoa(count)})
			}
			sortRows(rows)
			p.writeTable([]string{"REFERRER", "CLICKS"}, rows)
		}
		return nil
	}
}

// PrintHealth renders the server health document.
func (p *Printer) PrintHealth(health *api.Health) error {
	if done, err := p.structured(health); done {
		return err
	}
	rows := [][2]string{
		{"Status", health.Status},
		{"Project", health.Project.Name},
		{"Version", health.Version},
		{"Go version", health.GoVersion},
		{"Mode", health.Mode},
		{"Uptime", health.Uptime},
	}
	switch p.format {
	case "csv":
		flat := make([][]string, 0, len(rows))
		for _, row := range rows {
			flat = append(flat, []string{row[0], row[1]})
		}
		return p.writeCSV([]string{"FIELD", "VALUE"}, flat)
	case "plain":
		for _, row := range rows {
			fmt.Fprintf(p.out, "%s\t%s\n", row[0], row[1])
		}
		return nil
	default:
		p.writeKeyValue(rows)
		return nil
	}
}

// writeCSV emits RFC 4180 CSV.
func (p *Printer) writeCSV(headers []string, rows [][]string) error {
	writer := csv.NewWriter(p.out)
	if err := writer.Write(headers); err != nil {
		return err
	}
	for _, row := range rows {
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

// writeKeyValue renders an aligned label/value block.
func (p *Printer) writeKeyValue(rows [][2]string) {
	width := 0
	for _, row := range rows {
		if len(row[0]) > width {
			width = len(row[0])
		}
	}
	pal := p.palette()
	for _, row := range rows {
		label := fmt.Sprintf("%-*s", width, row[0])
		fmt.Fprintf(p.out, "%s  %s\n", p.colorize(pal.Muted, label), row[1])
	}
}

// writeTable renders a box-drawing table sized to its content.
func (p *Printer) writeTable(headers []string, rows [][]string) {
	widths := make([]int, len(headers))
	for i, header := range headers {
		widths[i] = len(header)
	}
	for _, row := range rows {
		for i := range headers {
			if i < len(row) && len(row[i]) > widths[i] {
				widths[i] = len(row[i])
			}
		}
	}
	// Long destination URLs would otherwise blow past any terminal width.
	for i := range widths {
		if widths[i] > 60 {
			widths[i] = 60
		}
	}

	pal := p.palette()
	fmt.Fprintln(p.out, p.colorize(pal.Border, border("┌", "┬", "┐", widths)))

	cells := make([]string, len(headers))
	for i, header := range headers {
		cells[i] = pad(header, widths[i])
	}
	fmt.Fprintln(p.out, p.colorize(pal.Border, "│")+strings.Join(colorCells(p, pal.Primary, cells), p.colorize(pal.Border, "│"))+p.colorize(pal.Border, "│"))
	fmt.Fprintln(p.out, p.colorize(pal.Border, border("├", "┼", "┤", widths)))

	for _, row := range rows {
		for i := range headers {
			value := ""
			if i < len(row) {
				value = row[i]
			}
			cells[i] = pad(truncate(value, widths[i]), widths[i])
		}
		fmt.Fprintln(p.out, p.colorize(pal.Border, "│")+strings.Join(cells, p.colorize(pal.Border, "│"))+p.colorize(pal.Border, "│"))
	}

	fmt.Fprintln(p.out, p.colorize(pal.Border, border("└", "┴", "┘", widths)))
}

// colorCells applies a color to every cell of a row.
func colorCells(p *Printer, code string, cells []string) []string {
	colored := make([]string, len(cells))
	for i, cell := range cells {
		colored[i] = p.colorize(code, cell)
	}
	return colored
}

// border builds one horizontal table rule.
func border(left, middle, right string, widths []int) string {
	segments := make([]string, len(widths))
	for i, width := range widths {
		segments[i] = strings.Repeat("─", width+2)
	}
	return left + strings.Join(segments, middle) + right
}

// pad left-aligns a cell inside its column with one space of padding.
func pad(value string, width int) string {
	return " " + value + strings.Repeat(" ", width-runeLen(value)) + " "
}

// truncate shortens a value to width, marking the cut with an ellipsis.
func truncate(value string, width int) string {
	if runeLen(value) <= width {
		return value
	}
	if width <= 1 {
		return string([]rune(value)[:width])
	}
	return string([]rune(value)[:width-1]) + "…"
}

// runeLen counts runes rather than bytes so multi-byte values align.
func runeLen(value string) int {
	return len([]rune(value))
}

// sortRows orders rows by their first column, descending by numeric second
// column when both parse as numbers.
func sortRows(rows [][]string) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && lessRow(rows[j], rows[j-1]); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}

// lessRow reports whether a should sort before b.
func lessRow(a, b []string) bool {
	aCount, aErr := strconv.Atoi(a[1])
	bCount, bErr := strconv.Atoi(b[1])
	if aErr == nil && bErr == nil && aCount != bCount {
		return aCount > bCount
	}
	return a[0] < b[0]
}
