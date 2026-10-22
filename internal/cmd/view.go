package cmd

// Shared pieces of the human-facing output of the setup and read-only
// commands (init, auth, config, status, skill, mcp, docs, logs, sessions).
//
// The rule they all follow: the plain output is what it always was, byte for
// byte. Colour, marks, headings and spinners exist only for a person at a
// terminal, so each helper takes a *ui.UI and does nothing (or prints the old
// text) when it is nil or off. A nil *ui.UI and a nil *ui.Spinner are valid.

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

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
		b.WriteString("  " + u.Cyan("›") + " " + s + "\n")
	}
	u.Printf("%s", b.String())
}

// viewRows renders key/value rows with dimmed, aligned keys. Rows with an
// empty value are skipped.
func viewRows(u *ui.UI, indent string, rows []ui.Row) string {
	pad := 0
	for _, r := range rows {
		if r.Val != "" && utf8.RuneCountInString(r.Key) > pad {
			pad = utf8.RuneCountInString(r.Key)
		}
	}
	var b strings.Builder
	for _, r := range rows {
		if r.Val != "" {
			b.WriteString(indent + viewKey(u, pad, r.Key) + "  " + r.Val + "\n")
		}
	}
	return b.String()
}

// viewKey is a key padded to width, dimmed when u is on. With u off it is
// exactly fmt's %-*s.
func viewKey(u *ui.UI, width int, key string) string {
	return u.Dim(fmt.Sprintf("%-*s", width, key))
}

// Marks for a check list. Neutral is for "not applicable" or "unknown".
const (
	markOK = iota
	markBad
	markWarn
	markNeutral
)

func viewMark(u *ui.UI, m int) string {
	switch m {
	case markOK:
		return u.Green("✓")
	case markBad:
		return u.Red("✗")
	case markWarn:
		return u.Bold("!")
	}
	return u.Dim("–")
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
// added around the plain text, so colour never skews the columns.
func viewTable(p *output.Printer, u *ui.UI, headers []string, rows [][]string, style func(col int, cell string) string) {
	if !u.On() {
		p.Table(headers, rows)
		return
	}
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if i < len(widths) {
				widths[i] = max(widths[i], utf8.RuneCountInString(c))
			}
		}
	}
	line := func(cells []string, paint func(col int, cell string) string) {
		var b strings.Builder
		for i, c := range cells {
			shown := paint(i, c)
			b.WriteString(shown)
			if i < len(cells)-1 && i < len(widths) {
				b.WriteString(strings.Repeat(" ", max(widths[i]-utf8.RuneCountInString(c), 0)+2))
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
