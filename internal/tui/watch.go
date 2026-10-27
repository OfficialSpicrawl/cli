// Package tui is the CLI's full-screen live views (Bubble Tea + Lip Gloss):
// the request log of "spicrawl logs --watch" and the dashboard of
// "spicrawl batch watch". Both are one Model, a filterable table that polls a
// Source; they differ only in their columns, their Source and an optional
// header block. The package knows nothing about the API: the cmd package
// supplies rows already formatted for a person, so it also owns the rule
// that a customer sees tiers (fetch, js, browser) and never an engine name.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/OfficialSpicrawl/cli/internal/output"
)

// Row is one table line. Cells are already formatted and line up with the
// Config's Columns. Key identifies the row across polls (request id, item seq).
type Row struct {
	Key    string
	Cells  []string
	Status string // colours the status cell and drives the status filter
	Tier   string // fetch, js or browser; "" when unknown
	Search string // what the "/" search matches
}

// Column is one table column. A Width of 0 takes the space left over.
type Column struct {
	Title   string
	Width   int
	Right   bool
	Status  bool // coloured by the row's Status
	MinTerm int  // hidden when the terminal is narrower than this
}

// Update is what one poll brought.
type Update struct {
	Rows    []Row // new rows, newest first; with Replace, the whole table
	Replace bool
	// Header, when set, replaces the block drawn above the table (batch
	// progress). It is called on every redraw with the width and the clock.
	Header func(width int, now time.Time) []string
	Note   string // tail of the status bar
	Done   bool   // the thing watched has finished: the view exits
}

// Source is what a view watches. Poll and Detail are never called at the same
// time as themselves; they may run concurrently with each other.
type Source interface {
	// Poll returns what changed. known holds the keys of the newest rows the
	// view has, so a source can stop paging once it reaches them.
	Poll(ctx context.Context, known map[string]struct{}) (Update, error)
	// Detail renders one row's page (the content of "logs get"). It may
	// contain SGR colour codes; anything else non-printable is stripped.
	Detail(ctx context.Context, key string) (string, error)
}

// RetryAfter is implemented by an error that says when to try again (a 429).
type RetryAfter interface{ RetryAfter() time.Duration }

// Config describes one view.
type Config struct {
	Title      string // "spicrawl logs"
	Noun       string // "requests", "items"
	Empty      string // shown while there are no rows
	Columns    []Column
	Statuses   []string // what the "s" key cycles through
	BadKey     string   // the key that toggles the problems-only filter ("e", "f")
	BadLabel   string   // "errors", "failed"
	BadStatus  func(status string) bool
	Source     Source
	Initial    Update // the poll the caller already did
	InitialErr error  // or its failure, which the view retries
	Poll       time.Duration
	MaxRows    int
	Deadline   time.Time        // the view exits with Timeout after this; zero = never
	Now        func() time.Time // the clock; time.Now by default
}

// Reason says why a view returned.
type Reason int

const (
	Quit      Reason = iota // q or Esc
	Interrupt               // Ctrl-C
	Finished                // Update.Done
	Timeout                 // Config.Deadline passed
)

type mode int

const (
	modeList mode = iota
	modeSearch
	modeDetail
)

const (
	minW, minH  = 40, 10
	pollTimeout = 20 * time.Second
	maxBackoff  = 30 * time.Second
	knownKeys   = 200
)

// The messages the model sends itself.
type (
	tickMsg  struct{ gen int }
	clockMsg struct{}
	pollMsg  struct {
		u   Update
		err error
	}
	detailMsg struct {
		key  string
		text string
		err  error
	}
)

// Model is the Bubble Tea model of a view.
type Model struct {
	cfg Config
	ctx context.Context
	now func() time.Time

	rows   []Row
	seen   map[string]struct{}
	shown  []Row
	header func(int, time.Time) []string
	note   string
	added  int

	f   filters
	cur cursor

	mode       mode
	searchWas  string
	vp         viewport.Model
	detailKey  string
	detailText string
	detailErr  error
	loading    bool

	w, h     int
	paused   bool
	polling  bool
	gen      int
	failures int
	err      error
	nextAt   time.Time
	updated  time.Time

	done   bool
	result Reason
}

// New builds the model of a view. ctx bounds every poll it starts.
func New(ctx context.Context, cfg Config) *Model {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Poll <= 0 {
		cfg.Poll = 2 * time.Second
	}
	if cfg.MaxRows <= 0 {
		cfg.MaxRows = 5000
	}
	if cfg.BadStatus == nil {
		cfg.BadStatus = func(string) bool { return false }
	}
	m := &Model{cfg: cfg, ctx: ctx, now: cfg.Now, seen: map[string]struct{}{}, w: 80, h: 24}
	m.vp = viewport.New(viewport.WithWidth(m.w), viewport.WithHeight(m.h-3))
	if cfg.InitialErr != nil {
		m.fail(cfg.InitialErr)
	} else {
		m.apply(cfg.Initial)
		m.added, m.updated = 0, m.now()
	}
	return m
}

// Reason is why the view returned; valid once the program has ended.
func (m *Model) Reason() Reason { return m.result }

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.tickAfter(m.delay()), tea.Tick(time.Second, func(time.Time) tea.Msg { return clockMsg{} }))
}

// ---- polling ---------------------------------------------------------------

func (m *Model) delay() time.Duration { return Backoff(m.cfg.Poll, m.failures, m.err) }

// Backoff is how long to wait before the next poll: poll while all is well,
// doubling per failure up to 30 s, or what a 429 asked for (at most a minute).
func Backoff(poll time.Duration, failures int, err error) time.Duration {
	if err == nil || failures <= 0 {
		return poll
	}
	d := poll
	for i := 0; i < failures && d < maxBackoff; i++ {
		d *= 2
	}
	d = min(d, maxBackoff)
	var ra RetryAfter
	if errors.As(err, &ra) && ra.RetryAfter() > 0 {
		d = min(ra.RetryAfter(), time.Minute)
	}
	return d
}

func (m *Model) tickAfter(d time.Duration) tea.Cmd {
	m.gen++
	gen := m.gen
	m.nextAt = m.now().Add(d)
	return tea.Tick(d, func(time.Time) tea.Msg { return tickMsg{gen} })
}

// fetch starts one poll. The caller has checked that none is running.
func (m *Model) fetch() tea.Cmd {
	m.polling = true
	known := make(map[string]struct{}, knownKeys)
	for _, r := range m.rows[:min(len(m.rows), knownKeys)] {
		known[r.Key] = struct{}{}
	}
	src, ctx := m.cfg.Source, m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, pollTimeout)
		defer cancel()
		u, err := src.Poll(ctx, known)
		return pollMsg{u, err}
	}
}

func (m *Model) fail(err error) {
	m.failures++
	m.err = err
}

// apply folds one poll into the table.
func (m *Model) apply(u Update) {
	if u.Header != nil {
		m.header = u.Header
	}
	m.note = u.Note
	m.done = m.done || u.Done
	m.added = 0
	switch {
	case u.Replace:
		seen := make(map[string]struct{}, len(u.Rows))
		m.rows = make([]Row, len(u.Rows))
		for i, r := range u.Rows {
			r = cleanRow(r)
			if _, ok := m.seen[r.Key]; !ok {
				m.added++
			}
			seen[r.Key] = struct{}{}
			m.rows[i] = r
		}
		m.seen = seen
	default:
		fresh := make([]Row, 0, len(u.Rows))
		for _, r := range u.Rows {
			if _, ok := m.seen[r.Key]; ok {
				continue
			}
			m.seen[r.Key] = struct{}{}
			fresh = append(fresh, cleanRow(r))
		}
		m.added = len(fresh)
		m.rows = append(fresh, m.rows...)
		if len(m.rows) > m.cfg.MaxRows {
			for _, r := range m.rows[m.cfg.MaxRows:] {
				delete(m.seen, r.Key)
			}
			m.rows = m.rows[:m.cfg.MaxRows]
		}
	}
	m.refilter()
}

func (m *Model) refilter() {
	m.shown = m.shown[:0]
	for _, r := range m.rows {
		if m.f.match(r, m.cfg.BadStatus) {
			m.shown = append(m.shown, r)
		}
	}
	m.cur.resolve(m.shown, m.listH())
}

// finish returns tea.Quit when the view is over and nothing is being read or
// typed, so a finished job never pulls a detail page out from under a reader.
func (m *Model) finish() tea.Cmd {
	if m.mode != modeList {
		return nil
	}
	switch {
	case m.done:
		m.result = Finished
	case !m.cfg.Deadline.IsZero() && m.now().After(m.cfg.Deadline):
		m.result = Timeout
	default:
		return nil
	}
	return tea.Quit
}

// ---- update ----------------------------------------------------------------

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.layout()
		return m, nil
	case clockMsg:
		return m, tea.Batch(m.finish(), tea.Tick(time.Second, func(time.Time) tea.Msg { return clockMsg{} }))
	case tickMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		if m.paused || m.polling {
			return m, m.tickAfter(m.cfg.Poll)
		}
		return m, m.fetch()
	case pollMsg:
		m.polling = false
		if msg.err != nil {
			m.fail(msg.err)
		} else {
			m.failures, m.err = 0, nil
			m.updated = m.now()
			m.apply(msg.u)
		}
		return m, tea.Batch(m.finish(), m.tickAfter(m.delay()))
	case detailMsg:
		if m.mode == modeDetail && msg.key == m.detailKey {
			m.loading, m.detailText, m.detailErr = false, msg.text, msg.err
			m.layout()
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *Model) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if k == "ctrl+c" {
		m.result = Interrupt
		return m, tea.Quit
	}
	switch m.mode {
	case modeSearch:
		switch k {
		case "enter":
			m.mode = modeList
			return m, m.finish()
		case "esc":
			m.f.text = m.searchWas
			m.mode = modeList
			m.refilter()
			return m, m.finish()
		case "backspace":
			if r := []rune(m.f.text); len(r) > 0 {
				m.f.text = string(r[:len(r)-1])
			}
		default:
			m.f.text += msg.Text
		}
		m.cur = cursor{}
		m.refilter()
		return m, nil
	case modeDetail:
		switch k {
		case "esc", "q", "enter", "backspace", "left", "h":
			m.mode = modeList
			return m, m.finish()
		}
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}

	switch k {
	case "q", "esc":
		m.result = Quit
		return m, tea.Quit
	case "up", "k":
		m.cur.move(-1, m.shown, m.listH())
	case "down", "j":
		m.cur.move(1, m.shown, m.listH())
	case "pgup", "ctrl+b":
		m.cur.move(-m.listH(), m.shown, m.listH())
	case "pgdown", "ctrl+f":
		m.cur.move(m.listH(), m.shown, m.listH())
	case "home", "g":
		m.cur.move(-len(m.shown), m.shown, m.listH())
	case "end", "G":
		m.cur.move(len(m.shown), m.shown, m.listH())
	case "enter":
		if len(m.shown) == 0 {
			return m, nil
		}
		m.mode, m.detailKey, m.detailText, m.detailErr, m.loading = modeDetail, m.shown[m.cur.idx].Key, "", nil, true
		m.layout()
		return m, m.fetchDetail(m.detailKey)
	case "/":
		m.mode, m.searchWas = modeSearch, m.f.text
	case "s":
		m.f.status = next(m.f.status, m.cfg.Statuses)
		m.cur = cursor{}
		m.refilter()
	case "t":
		m.f.tier = next(m.f.tier, []string{"fetch", "js", "browser"})
		m.cur = cursor{}
		m.refilter()
	case "c":
		m.f = filters{}
		m.cur = cursor{}
		m.refilter()
	case "p", " ":
		m.paused = !m.paused
	case "r":
		if !m.polling {
			return m, m.fetch()
		}
	default:
		if k == m.cfg.BadKey && k != "" {
			m.f.bad = !m.f.bad
			m.cur = cursor{}
			m.refilter()
		}
	}
	return m, nil
}

func (m *Model) fetchDetail(key string) tea.Cmd {
	src, ctx := m.cfg.Source, m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, pollTimeout)
		defer cancel()
		text, err := src.Detail(ctx, key)
		return detailMsg{key, text, err}
	}
}

// ---- layout ----------------------------------------------------------------

func (m *Model) headerLines() []string {
	if m.header == nil {
		return nil
	}
	return m.header(m.w, m.now())
}

// listH is the number of table rows that fit: the screen less the title, the
// header block, the filter line, the column titles, the status bar and help.
func (m *Model) listH() int {
	return max(1, m.h-5-len(m.headerLines()))
}

func (m *Model) layout() {
	m.vp.SetWidth(m.w)
	m.vp.SetHeight(max(1, m.h-3))
	m.vp.SetContent(m.detailBody())
	m.cur.resolve(m.shown, m.listH())
}

func (m *Model) detailBody() string {
	switch {
	case m.loading:
		return "loading…"
	case m.detailErr != nil:
		return lipgloss.NewStyle().Width(m.w).Render("! " + clean(output.PublicText(m.detailErr.Error())))
	}
	return lipgloss.NewStyle().Width(m.w).Render(Sanitize(m.detailText))
}

type colLayout struct {
	cols   []Column
	widths []int
	at     []int // each shown column's index in Config.Columns, and so in Row.Cells
}

// columns picks the columns that fit and gives the flexible one the rest.
func (m *Model) columns() colLayout {
	var l colLayout
	used := 2 // the cursor mark
	flex := -1
	for i, c := range m.cfg.Columns {
		if m.w < c.MinTerm {
			continue
		}
		if c.Width == 0 {
			flex = len(l.cols)
		}
		l.cols = append(l.cols, c)
		l.at = append(l.at, i)
		l.widths = append(l.widths, c.Width)
		used += c.Width + 1
	}
	if flex >= 0 {
		l.widths[flex] = max(8, m.w-used+1)
	}
	return l
}

func (l colLayout) line(cells []string, style func(i int, cell string) string) string {
	var b strings.Builder
	for i, c := range l.cols {
		cell := ""
		if at := l.at[i]; at < len(cells) {
			cell = cells[at]
		}
		b.WriteString(style(i, fit(cell, l.widths[i], c.Right)))
		if i < len(l.cols)-1 {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// ---- view ------------------------------------------------------------------

var (
	sBar    = lipgloss.NewStyle().Bold(true).Reverse(true)
	sSel    = lipgloss.NewStyle().Reverse(true)
	sDim    = lipgloss.NewStyle().Faint(true)
	sBold   = lipgloss.NewStyle().Bold(true)
	sGreen  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	sRed    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	sCyan   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	goodSet = map[string]bool{"success": true, "succeeded": true}
	badSet  = map[string]bool{"failed": true, "timeout": true, "blocked": true, "rejected": true}
)

func statusStyle(s string) lipgloss.Style {
	switch {
	case goodSet[s]:
		return sGreen
	case badSet[s]:
		return sRed
	}
	return sDim
}

func (m *Model) View() tea.View {
	v := tea.NewView(m.screen())
	v.AltScreen = true
	return v
}

// screen is the whole screen as a styled string, exactly m.h lines.
func (m *Model) screen() string {
	if m.w < minW || m.h < minH {
		return fmt.Sprintf("terminal too small\n%dx%d, need %dx%d\nq quits", m.w, m.h, minW, minH)
	}
	if m.mode == modeDetail {
		return m.detailView()
	}
	lines := []string{m.titleBar(m.cfg.Title)}
	lines = append(lines, m.headerLines()...)
	lines = append(lines, m.filterLine())

	l := m.columns()
	titles := make([]string, len(m.cfg.Columns))
	for i, c := range m.cfg.Columns {
		titles[i] = c.Title
	}
	lines = append(lines, sBold.Render("  "+l.line(titles, func(_ int, c string) string { return c })))

	h := m.listH()
	for i := 0; i < h; i++ {
		idx := m.cur.top + i
		switch {
		case idx < len(m.shown):
			lines = append(lines, m.rowLine(l, m.shown[idx], idx == m.cur.idx))
		case i == 0 && len(m.shown) == 0:
			lines = append(lines, "  "+sDim.Render(m.emptyText()))
		default:
			lines = append(lines, "")
		}
	}
	lines = append(lines, m.statusBar(), m.helpLine())
	return strings.Join(lines, "\n")
}

func (m *Model) rowLine(l colLayout, r Row, selected bool) string {
	if selected {
		return sSel.Render(fit("> "+l.line(r.Cells, func(_ int, c string) string { return c }), m.w, false))
	}
	return "  " + l.line(r.Cells, func(i int, c string) string {
		if l.cols[i].Status {
			return statusStyle(r.Status).Render(c)
		}
		return c
	})
}

func (m *Model) emptyText() string {
	switch {
	case m.f.active() && len(m.rows) > 0:
		return "no " + m.cfg.Noun + " match the filters (c clears them)"
	case m.err != nil:
		return "waiting for the API…"
	}
	return m.cfg.Empty
}

func (m *Model) titleBar(left string) string {
	right := "● live"
	if m.paused {
		right = "‖ paused"
	}
	if m.err != nil {
		right = "! offline"
	}
	pad := max(1, m.w-ansi.StringWidth(left)-ansi.StringWidth(right)-2)
	return sBar.Render(fit(" "+left+strings.Repeat(" ", pad)+right+" ", m.w, false))
}

func (m *Model) filterLine() string {
	item := func(name, val string, on bool) string {
		if on {
			return sDim.Render(name+": ") + sCyan.Render(sBold.Render(val))
		}
		return sDim.Render(name + ": " + val)
	}
	if m.mode == modeSearch {
		return ansi.Truncate(" "+sDim.Render("search: ")+sCyan.Render(sBold.Render(clean(m.f.text)))+"▏", m.w, "…")
	}
	return ansi.Truncate(" "+strings.Join([]string{
		item("status", or(m.f.status, "all"), m.f.status != ""),
		item("tier", or(m.f.tier, "all"), m.f.tier != ""),
		item(m.cfg.BadLabel, onOff(m.f.bad), m.f.bad),
		item("search", or(clean(m.f.text), "-"), m.f.text != ""),
	}, "  "), m.w, "…")
}

func (m *Model) statusBar() string {
	var s string
	if m.err != nil {
		retry := max(1, int((m.nextAt.Sub(m.now())+time.Second-1)/time.Second)) // rounded up: "0s" would look like a hang
		s = sRed.Render(fit(fmt.Sprintf(" ! %s · retrying in %ds", clean(output.PublicText(m.err.Error())), retry), m.w, false))
		return s
	}
	parts := []string{fmt.Sprintf("%d %s", len(m.rows), m.cfg.Noun)}
	if m.f.active() {
		parts = append(parts, fmt.Sprintf("%d shown", len(m.shown)))
	}
	if m.added > 0 {
		parts = append(parts, fmt.Sprintf("+%d new", m.added))
	}
	if !m.updated.IsZero() {
		parts = append(parts, "updated "+m.updated.Format("15:04:05"))
	}
	if m.note != "" {
		parts = append(parts, clean(m.note))
	}
	return sDim.Render(fit(" "+strings.Join(parts, " · "), m.w, false))
}

func (m *Model) helpLine() string {
	keys := [][2]string{{"↑↓", "move"}, {"⏎", "detail"}, {"/", "search"}, {"s", "status"}, {"t", "tier"}}
	if m.cfg.BadKey != "" {
		keys = append(keys, [2]string{m.cfg.BadKey, m.cfg.BadLabel})
	}
	keys = append(keys, [2]string{"c", "clear"}, [2]string{"p", "pause"}, [2]string{"r", "refresh"})
	if m.mode == modeSearch {
		return sDim.Render(" type to filter · ⏎ keep · esc cancel")
	}
	const quit = "q quit"
	s := " "
	for _, k := range keys {
		item := k[0] + " " + k[1] + "  "
		if ansi.StringWidth(s+item+quit) > m.w {
			break
		}
		s += item
	}
	return sDim.Render(fit(s+quit, m.w, false))
}

func (m *Model) detailView() string {
	title := m.titleBar(m.cfg.Title + " · " + clean(m.detailKey))
	foot := fmt.Sprintf(" %3.0f%% · ↑↓ PgUp PgDn scroll · esc back · ctrl+c quit", m.vp.ScrollPercent()*100)
	return title + "\n" + padLines(m.vp.View(), m.vp.Height()) + "\n\n" + sDim.Render(fit(foot, m.w, false))
}

// padLines pads a block to exactly h lines so the footer stays on the last line.
func padLines(s string, h int) string {
	lines := strings.Split(s, "\n")
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines[:h], "\n")
}

// ---- helpers ---------------------------------------------------------------

type filters struct {
	status, tier string
	bad          bool
	text         string
}

func (f filters) active() bool { return f.status != "" || f.tier != "" || f.bad || f.text != "" }

func (f filters) match(r Row, bad func(string) bool) bool {
	switch {
	case f.status != "" && r.Status != f.status,
		f.tier != "" && r.Tier != f.tier,
		f.bad && !bad(r.Status),
		f.text != "" && !strings.Contains(strings.ToLower(r.Search), strings.ToLower(f.text)):
		return false
	}
	return true
}

// next cycles "" -> opts[0] -> ... -> "" -> ...
func next(cur string, opts []string) string {
	if cur == "" {
		return opts[0]
	}
	for i, o := range opts {
		if o == cur && i+1 < len(opts) {
			return opts[i+1]
		}
	}
	return ""
}

// cursor is the selected row. It follows the newest row while it is on the
// first one, and otherwise stays on its row as new ones arrive above it.
type cursor struct {
	key      string
	idx, top int
}

func (c *cursor) resolve(rows []Row, h int) {
	if len(rows) == 0 {
		*c = cursor{}
		return
	}
	if c.idx > 0 && c.key != "" {
		c.idx = min(c.idx, len(rows)-1)
		for i, r := range rows {
			if r.Key == c.key {
				c.idx = i
				break
			}
		}
	} else {
		c.idx = 0
	}
	c.key = rows[c.idx].Key
	if c.idx < c.top {
		c.top = c.idx
	}
	if c.idx >= c.top+h {
		c.top = c.idx - h + 1
	}
	c.top = max(0, min(c.top, len(rows)-h))
}

func (c *cursor) move(d int, rows []Row, h int) {
	if len(rows) == 0 {
		return
	}
	c.idx = max(0, min(c.idx+d, len(rows)-1))
	c.key = rows[c.idx].Key
	c.resolve(rows, h)
}

// fit truncates s to w cells (with …) and pads it to exactly w.
func fit(s string, w int, right bool) string {
	s = ansi.Truncate(s, w, "…")
	if pad := w - ansi.StringWidth(s); pad > 0 {
		if right {
			return strings.Repeat(" ", pad) + s
		}
		return s + strings.Repeat(" ", pad)
	}
	return s
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// clean makes a server string safe for one terminal line: control characters,
// which could move the cursor or retitle the window, become spaces.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

func cleanRow(r Row) Row {
	cells := make([]string, len(r.Cells))
	for i, c := range r.Cells {
		cells[i] = clean(c)
	}
	r.Cells, r.Search, r.Key = cells, clean(r.Search), clean(r.Key)
	return r
}

// Sanitize keeps a multi-line page safe to print: colour codes (SGR) and
// newlines pass, every other control character or escape sequence is dropped.
func Sanitize(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			if j := sgrEnd(s, i); j > 0 {
				b.WriteString(s[i:j])
				i = j
				continue
			}
			i++
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	return b.String()
}

// sgrEnd is the end of the colour sequence ESC [ digits/; m at i, or 0.
func sgrEnd(s string, i int) int {
	if i+1 >= len(s) || s[i+1] != '[' {
		return 0
	}
	for j := i + 2; j < len(s); j++ {
		switch c := s[j]; {
		case c >= '0' && c <= '9', c == ';':
		case c == 'm':
			return j + 1
		default:
			return 0
		}
	}
	return 0
}
