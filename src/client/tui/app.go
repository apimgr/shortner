// Package tui implements the client's interactive terminal interface, the
// default face of shortner-cli when it is started with no command in an
// interactive terminal. See AI.md PART 32.
package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/apimgr/shortner/src/client/api"
	"github.com/apimgr/shortner/src/common/display"
	"github.com/apimgr/shortner/src/common/terminal"
)

// Options configures a TUI session.
type Options struct {
	Client      *api.Client
	Theme       string
	Mouse       bool
	Unicode     bool
	Lang        string
	ServerLabel string
	PageSize    int
}

// view identifies which screen the model is rendering.
type view int

const (
	viewList view = iota
	viewDetail
	viewStats
	viewHealth
	viewCreate
	viewHelp
)

// Message types delivered by the API commands.
type (
	linksMsg   struct{ list *api.LinkList }
	detailMsg  struct{ link *api.Link }
	statsMsg   struct{ stats *api.Stats }
	healthMsg  struct{ health *api.Health }
	createdMsg struct{ link *api.CreatedLink }
	deletedMsg struct{ slug string }
	errMsg     struct{ err error }
)

// model is the bubbletea model backing the whole interface.
type model struct {
	ctx      context.Context
	client   *api.Client
	styles   TUIStyles
	symbols  Symbols
	layout   LayoutConfig
	sizeMode terminal.SizeMode
	server   string

	width  int
	height int

	view     view
	prev     view
	links    []api.Link
	filtered []api.Link
	cursor   int
	scroll   int
	page     int
	pageSize int
	pages    int
	total    int64

	detail *api.Link
	stats  *api.Stats
	health *api.Health
	owner  string

	searching bool
	creating  bool
	query     string
	input     textinput.Model

	loading bool
	status  string
	err     error
	confirm string
}

// Run starts the interactive terminal interface and blocks until the user
// quits.
func Run(ctx context.Context, opts Options) error {
	if opts.Client == nil {
		return fmt.Errorf("no server configured")
	}

	size := terminal.GetTerminalSize()
	env := display.DetectDisplayEnv()

	// cli.yml's `tui.theme` picks the palette shared with the server frontend.
	SetTUITheme(opts.Theme)

	symbols := TUISymbolsASCII
	if opts.Unicode {
		symbols = GetTUISymbols(env)
	}

	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}

	input := textinput.New()
	input.Prompt = "> "
	input.CharLimit = 2048

	m := model{
		ctx:      ctx,
		client:   opts.Client,
		styles:   stylesForTheme(opts.Theme),
		symbols:  symbols,
		sizeMode: size.Mode,
		layout:   GetLayoutConfig(size.Mode),
		server:   opts.ServerLabel,
		width:    size.Cols,
		height:   size.Rows,
		page:     1,
		pageSize: pageSize,
		input:    input,
		loading:  true,
	}

	programOpts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithContext(ctx)}
	if opts.Mouse {
		programOpts = append(programOpts, tea.WithMouseCellMotion())
	}

	_, err := tea.NewProgram(m, programOpts...).Run()
	if err != nil && !isCancelled(err) {
		return err
	}
	return nil
}

// isCancelled reports whether a program error is just the context being
// cancelled, which is a normal quit rather than a failure.
func isCancelled(err error) bool {
	return err != nil && strings.Contains(err.Error(), context.Canceled.Error())
}

// Init loads the first page of links.
func (m model) Init() tea.Cmd {
	return m.loadLinks(1)
}

// loadLinks fetches one page of the public listing.
func (m model) loadLinks(page int) tea.Cmd {
	return func() tea.Msg {
		list, err := m.client.ListLinks(m.ctx, page, m.pageSize)
		if err != nil {
			return errMsg{err}
		}
		return linksMsg{list}
	}
}

// loadDetail fetches a single link.
func (m model) loadDetail(slug string) tea.Cmd {
	return func() tea.Msg {
		link, err := m.client.GetLink(m.ctx, slug)
		if err != nil {
			return errMsg{err}
		}
		return detailMsg{link}
	}
}

// loadStats fetches a link's click analytics.
func (m model) loadStats(slug string) tea.Cmd {
	return func() tea.Msg {
		stats, err := m.client.GetStats(m.ctx, slug)
		if err != nil {
			return errMsg{err}
		}
		return statsMsg{stats}
	}
}

// loadHealth fetches the server health document.
func (m model) loadHealth() tea.Cmd {
	return func() tea.Msg {
		health, err := m.client.Health(m.ctx)
		if err != nil {
			return errMsg{err}
		}
		return healthMsg{health}
	}
}

// createLink shortens a destination URL.
func (m model) createLink(destination string) tea.Cmd {
	return func() tea.Msg {
		created, err := m.client.CreateLink(m.ctx, destination, "", "")
		if err != nil {
			return errMsg{err}
		}
		return createdMsg{created}
	}
}

// deleteLink removes a link the client owns.
func (m model) deleteLink(slug string) tea.Cmd {
	return func() tea.Msg {
		if err := m.client.DeleteLink(m.ctx, slug); err != nil {
			return errMsg{err}
		}
		return deletedMsg{slug}
	}
}

// Update handles every message, including the window resize that AI.md
// PART 32 requires all modes to handle smoothly.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.sizeMode = modeForSize(msg.Width, msg.Height)
		m.layout = GetLayoutConfig(m.sizeMode)
		m.input.Width = maxInt(20, msg.Width-8)
		m.ensureVisible()
		return m, nil

	case linksMsg:
		m.loading = false
		m.err = nil
		m.links = msg.list.Data
		m.page = msg.list.Pagination.Page
		m.pages = msg.list.Pagination.Pages
		m.total = msg.list.Pagination.Total
		m.applyFilter()
		if m.cursor >= len(m.filtered) {
			m.cursor = maxInt(0, len(m.filtered)-1)
		}
		m.ensureVisible()
		return m, nil

	case detailMsg:
		m.loading = false
		m.err = nil
		m.detail = msg.link
		m.view = viewDetail
		return m, nil

	case statsMsg:
		m.loading = false
		m.err = nil
		m.stats = msg.stats
		m.view = viewStats
		return m, nil

	case healthMsg:
		m.loading = false
		m.err = nil
		m.health = msg.health
		m.view = viewHealth
		return m, nil

	case createdMsg:
		m.loading = false
		m.err = nil
		m.owner = msg.link.OwnerToken
		m.detail = &msg.link.Link
		m.view = viewDetail
		m.status = "Created " + msg.link.ShortCode + " — save the owner token now, it is shown once."
		return m, m.loadLinks(m.page)

	case deletedMsg:
		m.loading = false
		m.err = nil
		m.status = "Deleted " + msg.slug
		m.view = viewList
		return m, m.loadLinks(m.page)

	case errMsg:
		m.loading = false
		m.err = msg.err
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

// handleKey routes a keystroke, giving the text input priority whenever it
// has focus so typing never triggers a shortcut.
func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.searching || m.creating {
		switch msg.Type {
		case tea.KeyEsc:
			m.searching = false
			m.creating = false
			m.input.Blur()
			m.input.SetValue("")
			return m, nil
		case tea.KeyEnter:
			value := strings.TrimSpace(m.input.Value())
			m.input.Blur()
			m.input.SetValue("")
			if m.creating {
				m.creating = false
				if value == "" {
					return m, nil
				}
				m.loading = true
				return m, m.createLink(value)
			}
			m.searching = false
			m.query = value
			m.applyFilter()
			m.cursor = 0
			m.ensureVisible()
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}

	if m.confirm != "" {
		switch strings.ToLower(msg.String()) {
		case "y":
			slug := m.confirm
			m.confirm = ""
			m.loading = true
			return m, m.deleteLink(slug)
		default:
			m.confirm = ""
			m.status = "Aborted."
			return m, nil
		}
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "?":
		if m.view == viewHelp {
			m.view = m.prev
			return m, nil
		}
		m.prev = m.view
		m.view = viewHelp
		return m, nil
	case "esc", "h", "left":
		if m.view != viewList {
			m.view = viewList
			m.owner = ""
		}
		return m, nil
	case "j", "down":
		if m.view == viewList && m.cursor < len(m.filtered)-1 {
			m.cursor++
		}
		m.ensureVisible()
		return m, nil
	case "k", "up":
		if m.view == viewList && m.cursor > 0 {
			m.cursor--
		}
		m.ensureVisible()
		return m, nil
	case "g", "home":
		m.cursor = 0
		m.ensureVisible()
		return m, nil
	case "G", "end":
		m.cursor = maxInt(0, len(m.filtered)-1)
		m.ensureVisible()
		return m, nil
	case "n", "pgdown":
		if m.page < m.pages {
			m.loading = true
			return m, m.loadLinks(m.page + 1)
		}
		return m, nil
	case "p", "pgup":
		if m.page > 1 {
			m.loading = true
			return m, m.loadLinks(m.page - 1)
		}
		return m, nil
	case "r":
		m.loading = true
		m.status = ""
		return m, m.loadLinks(m.page)
	case "/":
		m.searching = true
		m.input.Placeholder = "search short code or destination"
		m.input.SetValue("")
		m.input.Focus()
		return m, textinput.Blink
	case "c":
		m.creating = true
		m.input.Placeholder = "https://example.com/page-to-shorten"
		m.input.SetValue("")
		m.input.Focus()
		return m, textinput.Blink
	case "enter", "l", "right":
		if link := m.selected(); link != nil {
			m.loading = true
			return m, m.loadDetail(link.ShortCode)
		}
		return m, nil
	case "s":
		if link := m.currentLink(); link != nil {
			m.loading = true
			return m, m.loadStats(link.ShortCode)
		}
		return m, nil
	case "H":
		m.loading = true
		return m, m.loadHealth()
	case "d":
		if link := m.currentLink(); link != nil {
			m.confirm = link.ShortCode
		}
		return m, nil
	}

	return m, nil
}

// selected returns the highlighted list row, if any.
func (m model) selected() *api.Link {
	if m.view != viewList || m.cursor < 0 || m.cursor >= len(m.filtered) {
		return nil
	}
	link := m.filtered[m.cursor]
	return &link
}

// currentLink returns whichever link the user is acting on: the highlighted
// row in the list, or the one already open in a detail view.
func (m model) currentLink() *api.Link {
	if link := m.selected(); link != nil {
		return link
	}
	return m.detail
}

// applyFilter narrows the loaded page by the active search query.
func (m *model) applyFilter() {
	if m.query == "" {
		m.filtered = m.links
		return
	}
	needle := strings.ToLower(m.query)
	filtered := make([]api.Link, 0, len(m.links))
	for _, link := range m.links {
		if strings.Contains(strings.ToLower(link.ShortCode), needle) ||
			strings.Contains(strings.ToLower(link.DestinationURL), needle) {
			filtered = append(filtered, link)
		}
	}
	m.filtered = filtered
}

// View renders the current screen.
func (m model) View() string {
	var b strings.Builder

	if m.layout.ShowHeader {
		b.WriteString(m.header())
		b.WriteString("\n")
	}

	switch m.view {
	case viewDetail:
		b.WriteString(m.detailView())
	case viewStats:
		b.WriteString(m.statsView())
	case viewHealth:
		b.WriteString(m.healthView())
	case viewHelp:
		b.WriteString(m.helpView())
	default:
		b.WriteString(m.listView())
	}

	if m.searching || m.creating {
		b.WriteString("\n" + m.input.View() + "\n")
	}
	if m.confirm != "" {
		b.WriteString("\n" + m.styles.Warning.Render("Delete "+m.confirm+" permanently? [y/N]") + "\n")
	}
	if m.err != nil {
		b.WriteString("\n" + m.styles.Error.Render(m.symbols.Error+" "+m.err.Error()) + "\n")
	} else if m.status != "" {
		b.WriteString("\n" + m.styles.Success.Render(m.symbols.Success+" "+m.status) + "\n")
	}

	if m.layout.ShowFooter {
		b.WriteString("\n" + m.footer())
	}
	return b.String()
}

// header renders the title bar.
func (m model) header() string {
	title := m.styles.Title.Render("shortner")
	server := m.styles.Muted.Render(m.server)
	if m.layout.UseAbbrev {
		return title
	}
	return title + "  " + server
}

// footer renders the key hints, abbreviated on narrow terminals.
func (m model) footer() string {
	if m.layout.UseAbbrev {
		return m.styles.Muted.Render("?:help q:quit")
	}
	return m.styles.Muted.Render(
		"j/k move  enter open  c create  s stats  d delete  / search  n/p page  r refresh  H health  ?:help  q:quit")
}

// listView renders the paginated link list.
func (m model) listView() string {
	if m.loading && len(m.filtered) == 0 {
		return m.styles.Muted.Render("Loading ...")
	}
	if len(m.filtered) == 0 {
		return m.styles.Muted.Render("No links yet. Press 'c' to create one.")
	}

	width := m.truncateWidth()
	visible := maxInt(1, m.viewportHeight()-1)
	start := m.scroll
	if start > len(m.filtered)-1 {
		start = maxInt(0, len(m.filtered)-1)
	}
	end := start + visible
	if end > len(m.filtered) {
		end = len(m.filtered)
	}

	var b strings.Builder
	for i := start; i < end; i++ {
		link := m.filtered[i]
		marker := "  "
		if i == m.cursor {
			marker = m.symbols.Arrow + " "
		}
		row := fmt.Sprintf("%-14s %-*s %8d", link.ShortCode, width, truncateMiddle(link.DestinationURL, width), link.ClickCount)
		if i == m.cursor {
			b.WriteString(m.styles.Selected.Render(marker + row))
		} else {
			b.WriteString(m.styles.Base.Render(marker + row))
		}
		b.WriteString("\n")
	}
	b.WriteString(m.styles.Muted.Render(fmt.Sprintf("page %d/%d  %d links", m.page, maxInt(m.pages, 1), m.total)))
	return b.String()
}

// detailView renders a single link, including the one-time owner token when
// this session just created it.
func (m model) detailView() string {
	if m.detail == nil {
		return m.styles.Muted.Render("No link selected.")
	}
	rows := [][2]string{
		{"Short code", m.detail.ShortCode},
		{"Short URL", m.detail.ShortURL},
		{"Destination", m.detail.DestinationURL},
		{"Created", m.detail.CreatedAt},
		{"Clicks", fmt.Sprintf("%d", m.detail.ClickCount)},
	}
	if m.detail.ExpiresAt != nil {
		rows = append(rows, [2]string{"Expires", *m.detail.ExpiresAt})
	}

	var b strings.Builder
	for _, row := range rows {
		b.WriteString(m.styles.Muted.Render(fmt.Sprintf("%-14s", row[0])))
		b.WriteString(m.styles.Base.Render(row[1]))
		b.WriteString("\n")
	}
	if m.owner != "" {
		b.WriteString("\n" + m.styles.Warning.Render("Owner token: "+m.owner) + "\n")
		b.WriteString(m.styles.Warning.Render("Save it now — it is shown once and cannot be recovered.") + "\n")
	}
	return b.String()
}

// statsView renders click analytics for the selected link.
func (m model) statsView() string {
	if m.stats == nil {
		return m.styles.Muted.Render("No statistics loaded.")
	}
	var b strings.Builder
	b.WriteString(m.styles.Title.Render("Stats for "+m.stats.ShortCode) + "\n")
	b.WriteString(m.styles.Base.Render(fmt.Sprintf("Total clicks: %d", m.stats.TotalClicks)) + "\n")

	if len(m.stats.TimeSeries) > 0 {
		b.WriteString("\n" + m.styles.Muted.Render("Recent days") + "\n")
		for _, point := range m.stats.TimeSeries {
			b.WriteString(fmt.Sprintf("  %-12s %s %d\n", point.Date, m.symbols.Bullet, point.Count))
		}
	}
	if len(m.stats.Referrers) > 0 {
		b.WriteString("\n" + m.styles.Muted.Render("Referrers") + "\n")
		keys := make([]string, 0, len(m.stats.Referrers))
		for key := range m.stats.Referrers {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			if m.stats.Referrers[keys[i]] != m.stats.Referrers[keys[j]] {
				return m.stats.Referrers[keys[i]] > m.stats.Referrers[keys[j]]
			}
			return keys[i] < keys[j]
		})
		for _, key := range keys {
			b.WriteString(fmt.Sprintf("  %-30s %d\n", truncate(key, 30), m.stats.Referrers[key]))
		}
	}
	if len(m.stats.Recent) > 0 {
		b.WriteString("\n" + m.styles.Muted.Render("Recent clicks") + "\n")
		for _, click := range m.stats.Recent {
			b.WriteString(fmt.Sprintf("  %-24s %-16s %s\n", click.Timestamp, click.Country, truncate(click.Referrer, 40)))
		}
	}
	return b.String()
}

// healthView renders the server's health document.
func (m model) healthView() string {
	if m.health == nil {
		return m.styles.Muted.Render("No health data loaded.")
	}
	rows := [][2]string{
		{"Project", m.health.Project.Name},
		{"Status", m.health.Status},
		{"Version", m.health.Version},
		{"Go", m.health.GoVersion},
		{"Mode", m.health.Mode},
		{"Uptime", m.health.Uptime},
	}
	var b strings.Builder
	for _, row := range rows {
		b.WriteString(m.styles.Muted.Render(fmt.Sprintf("%-10s", row[0])))
		b.WriteString(m.styles.Base.Render(row[1]))
		b.WriteString("\n")
	}
	return b.String()
}

// helpView lists every keyboard shortcut.
func (m model) helpView() string {
	rows := [][2]string{
		{"j / k, up / down", "move the selection"},
		{"g / G", "jump to first / last"},
		{"enter, l", "open the selected link"},
		{"esc, h", "back to the list"},
		{"c", "create a short link"},
		{"s", "click statistics"},
		{"d", "delete (asks to confirm)"},
		{"/", "search the loaded page"},
		{"n / p", "next / previous page"},
		{"r", "refresh"},
		{"H", "server health"},
		{"?", "toggle this help"},
		{"q, ctrl+c", "quit"},
	}
	var b strings.Builder
	b.WriteString(m.styles.Title.Render("Keyboard") + "\n")
	for _, row := range rows {
		b.WriteString(m.styles.Muted.Render(fmt.Sprintf("  %-18s", row[0])))
		b.WriteString(m.styles.Base.Render(row[1]))
		b.WriteString("\n")
	}
	return b.String()
}

// truncateWidth is the destination column width for the current layout.
func (m model) truncateWidth() int {
	if m.layout.TruncateAt == 0 {
		return maxInt(20, m.width-40)
	}
	if m.width > 0 && m.width-40 < m.layout.TruncateAt {
		return maxInt(20, m.width-40)
	}
	return m.layout.TruncateAt
}

// viewportHeight is the number of content rows left after the header, the
// footer, and any border the current size mode draws. AI.md PART 32 requires
// the TUI to stay usable on a phone-sized terminal, so the result never drops
// below three rows.
func (m model) viewportHeight() int {
	const headerHeight = 1
	const footerHeight = 1

	borderHeight := 0
	if m.layout.ShowBorders {
		borderHeight = 2
	}

	height := m.height - headerHeight - footerHeight - borderHeight
	if height < 3 {
		height = 3
	}
	return height
}

// ensureVisible scrolls the list so the cursor stays inside the viewport.
func (m *model) ensureVisible() {
	// The list is the only scrolling view; every other view fits its content.
	if m.view != viewList {
		m.scroll = 0
		return
	}

	// One row of the viewport belongs to the pagination line.
	visible := maxInt(1, m.viewportHeight()-1)
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	}
	if m.cursor >= m.scroll+visible {
		m.scroll = m.cursor - visible + 1
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

// truncate shortens a string to width runes, ending with an ellipsis.
func truncate(value string, width int) string {
	runes := []rune(value)
	if width <= 0 || len(runes) <= width {
		return value
	}
	if width <= 3 {
		return string(runes[:width])
	}
	return string(runes[:width-3]) + "..."
}

// truncateMiddle shortens a string by removing its middle, which keeps both
// ends of a URL readable — the host at the front and the file name at the end.
func truncateMiddle(value string, width int) string {
	runes := []rune(value)
	if width <= 0 || len(runes) <= width {
		return value
	}
	if width <= 5 {
		return string(runes[:width])
	}
	half := (width - 3) / 2
	return string(runes[:half]) + "..." + string(runes[len(runes)-half:])
}

// maxInt returns the larger of two ints.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
