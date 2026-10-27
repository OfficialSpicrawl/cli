package cmd

// Shared pieces of the human-facing output of the setup and read-only
// commands (init, auth, config, status, skill, mcp, docs, logs, sessions).
//
// The rule they all follow: the plain output is what it always was, byte for
// byte. Colour, marks, headings and spinners exist only for a person at a
// terminal, so each helper takes a *ui.UI and does nothing (or prints the old
// text) when it is nil or off. A nil *ui.UI and a nil *ui.Spinner are valid.

import (
	"io"
	"strings"

	"github.com/OfficialSpicrawl/cli/internal/output"
	"github.com/OfficialSpicrawl/cli/internal/ui"
)

// viewForce draws the live output whatever the streams are. Tests set it.
var viewForce bool

// errUI is the live output on stderr: spinner, hints, headings. Off when
// stderr is not a terminal, with --json or --quiet, and under NO_COLOR,
// TERM=dumb or CI (see ui.Live).
func errUI(p *output.Printer) *ui.UI {
	if viewForce && !p.Plain {
		return ui.Force(p.Err)
	}
	return p.UI()
}

// outUI is the styling of a human result on stdout. Off unless stdout is a
// terminal showing the human view (never JSON, never piped), and under the
// same switches as errUI.
func outUI(p *output.Printer) *ui.UI {
	switch {
	case p.JSON || p.Plain:
		return nil
	case viewForce:
		return ui.Force(p.Out)
	}
	return ui.New(p.Out)
}

// spin starts a spinner on stderr for a wait. It appears only after
// ui.Delay, so a fast call shows nothing. Stop it before printing anything.
func spin(p *output.Printer, format string, a ...any) *ui.Spinner {
	return errUI(p).Spin(format, a...)
}

// viewIntro prints a heading and key/value rows to stderr and reports true,
// or reports false when the caller should print its plain lines instead.
func viewIntro(p *output.Printer, title string, rows ...ui.Row) bool {
	u := errUI(p)
	if !u.On() {
		return false
	}
	var b strings.Builder
	b.WriteString(u.Bold(title) + "\n")
	b.WriteString(viewRows(u, "  ", rows))
	u.Printf("%s", b.String())
	return true
}

// viewNext prints a short "next" list to stderr for a person who just
// finished a setup step. Nothing in JSON mode, with --quiet or off a terminal.
func viewNext(p *output.Printer, steps ...string) {
	u := errUI(p)
	if p.JSON || !u.On() || len(steps) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString("\n" + u.Bold("next") + "\n")
	for _, s := range steps {
		b.WriteString("  " + u.Accent(u.Glyphs().Bullet) + " " + s + "\n")
	}
	u.Printf("%s", b.String())
}

// viewRows renders key/value rows with muted, aligned keys. Rows with an empty
// value are skipped. On a terminal a value longer than the line wraps under
// its own column, not under the key, and URLs in it become links.
func viewRows(u *ui.UI, indent string, rows []ui.Row) string {
	pad := 0
	for _, r := range rows {
		if r.Val != "" {
			pad = max(pad, ui.Width(r.Key))
		}
	}
	// 0 while u is off or its width is unknown: the value is printed as it is.
	room := 0
	if u.On() && u.Cols() > 0 {
		room = u.Cols() - 1 - ui.Width(indent) - pad - 2 // one short of the row: see ui.Fail
	}
	var b strings.Builder
	for _, r := range rows {
		if r.Val == "" {
			continue
		}
		lines := []string{r.Val}
		if room > 0 {
			lines = ui.Wrap(r.Val, room)
		}
		for i, l := range lines {
			key := viewKey(u, pad, r.Key)
			if i > 0 {
				key = strings.Repeat(" ", pad)
			}
			b.WriteString(indent + key + "  " + u.Linkify(l) + "\n")
		}
	}
	return b.String()
}

// viewKey is a key padded to width cells, muted when u is on. With u off it is
// the same text, padded.
func viewKey(u *ui.UI, width int, key string) string {
	return u.Dim(key + strings.Repeat(" ", max(width-ui.Width(key), 0)))
}

// Marks for a check list. Neutral is for "not applicable" or "unknown".
const (
	markOK = iota
	markBad
	markWarn
	markNeutral
)

func viewMark(u *ui.UI, m int) string {
	g := u.Glyphs()
	switch m {
	case markOK:
		return u.Green(g.OK)
	case markBad:
		return u.Red(g.Bad)
	case markWarn:
		return u.Amber(g.Warn)
	}
	return u.Dim(g.Neutral)
}

// viewLead is a mark and a space to put before a line, or "" while u is off,
// so a plain line keeps exactly the text it had.
func viewLead(u *ui.UI, m int) string {
	if !u.On() {
		return ""
	}
	return viewMark(u, m) + " "
}

// viewTable is p.Table, and exactly that while u is off. When it is on the
// header is bold and style, if given, colours each body cell. Padding is
// measured on the plain text, in terminal cells, so neither colour nor a wide
// character skews the columns.
func viewTable(p *output.Printer, u *ui.UI, headers []string, rows [][]string, style func(col int, cell string) string) {
	if !u.On() {
		p.Table(headers, rows)
		return
	}
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = ui.Width(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if i < len(widths) {
				widths[i] = max(widths[i], ui.Width(c))
			}
		}
	}
	line := func(cells []string, paint func(col int, cell string) string) {
		var b strings.Builder
		for i, c := range cells {
			shown := paint(i, c)
			b.WriteString(shown)
			if i < len(cells)-1 && i < len(widths) {
				b.WriteString(strings.Repeat(" ", max(widths[i]-ui.Width(c), 0)+2))
			}
		}
		b.WriteString("\n")
		io.WriteString(p.Out, b.String())
	}
	line(headers, func(_ int, c string) string { return u.Bold(c) })
	for _, r := range rows {
		line(r, func(col int, c string) string {
			if style == nil {
				return c
			}
			return style(col, c)
		})
	}
}

// viewStatus colours a status word: good green, bad red, anything else dim.
func viewStatus(u *ui.UI, s string, good, bad []string) string {
	for _, g := range good {
		if s == g {
			return u.Green(s)
		}
	}
	for _, b := range bad {
		if s == b {
			return u.Red(s)
		}
	}
	return u.Dim(s)
}
