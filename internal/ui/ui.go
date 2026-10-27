// Package ui is the CLI's live terminal output: a spinner with a label and
// elapsed time, a one-line ✓/✗ summary, a readable error block and a small
// colour palette. It exists only for a person at a terminal.
//
// Everything here writes to the stream it is given (stderr, never stdout) and
// is OFF unless that stream is a terminal and nothing asked for plain output
// (NO_COLOR, TERM=dumb, CI). When it is off every method is a no-op, so a
// caller never branches on it and the plain output stays byte-for-byte what it
// was. A nil *UI and a nil *Spinner are valid and off.
//
// When it is on it adapts to the terminal (Caps): colours are semantic roles
// (Accent, Success, Warn, Danger, Muted) that resolve to the 16-colour palette,
// the 256-colour cube or 24-bit colour; glyphs fall back to ASCII outside a
// UTF-8 locale; links are OSC 8 only where the terminal has them.
//
// Callers that print to the same stream while a spinner runs must go through
// Fprintf (output.Printer.Info and Warn already do), so the line is not
// written into the middle of a frame.
package ui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-isatty"
	"golang.org/x/term"
)

// Live reports whether w should get a spinner and colour: w is a terminal and
// nothing asked for plain output. NO_COLOR (any non-empty value), CLICOLOR=0,
// TERM=dumb and CI (anything but empty, 0 or false) all turn it off.
func Live(w io.Writer) bool { return live(w, os.Getenv, runtime.GOOS, isTTY) }

func isTTY(f *os.File) bool {
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

func live(w io.Writer, getenv func(string) string, goos string, tty func(*os.File) bool) bool {
	f, ok := w.(*os.File)
	if !ok || !tty(f) {
		return false
	}
	if getenv("NO_COLOR") != "" || getenv("CLICOLOR") == "0" || getenv("TERM") == "dumb" {
		return false
	}
	switch strings.ToLower(getenv("CI")) {
	case "", "0", "false":
	default:
		return false
	}
	// A Windows console only understands escape codes when VT processing is
	// on; Windows Terminal turns it on, the legacy console does not.
	return goos != "windows" || getenv("WT_SESSION") != ""
}

// UI draws on one stream. The zero value and nil are off.
type UI struct {
	w    io.Writer
	on   bool
	caps Caps
}

// New returns a UI for w that is on only when Live(w), drawing for whatever the
// terminal in the environment can do (Detect).
func New(w io.Writer) *UI {
	u := &UI{w: w, on: Live(w)}
	if u.on {
		u.caps = Detect()
	}
	return u
}

// Force returns a UI that is always on, whatever w is, drawing for a plain
// UTF-8 terminal with 16 colours. For tests.
func Force(w io.Writer) *UI { return ForceCaps(w, Caps{Unicode: true}) }

// ForceCaps is Force for a terminal with the given capabilities.
func ForceCaps(w io.Writer, c Caps) *UI { return &UI{w: w, on: true, caps: c} }

// On reports whether this UI draws anything.
func (u *UI) On() bool { return u != nil && u.on }

// Caps is what the terminal this UI draws on can do.
func (u *UI) Caps() Caps {
	if u == nil {
		return Caps{}
	}
	return u.caps
}

// Cols is the width of the terminal in cells, or 0 when it is not known (the
// stream is not a terminal). It is read every call, so it follows a resize.
func (u *UI) Cols() int {
	if u == nil {
		return 0
	}
	if u.caps.Width > 0 {
		return u.caps.Width
	}
	return termCols(u.w)
}

// ---- lines -----------------------------------------------------------------

// Printf writes to the UI's stream without breaking a running spinner. It
// prints even when the UI is off (it is how plain lines are written).
func (u *UI) Printf(format string, a ...any) {
	if u == nil || u.w == nil {
		return
	}
	Fprintf(u.w, format, a...)
}

// Done prints the summary line of one operation, only when the UI is on:
//
//	✓ https://example.com  200 · 1 credit · 4.2s
//
// parts[0] is the subject (a URL); the rest are the facts, muted. ok picks a
// green ✓ or a red ✗.
func (u *UI) Done(ok bool, parts ...string) {
	if !u.On() || len(parts) == 0 {
		return
	}
	g := u.Glyphs()
	mark := u.Red(g.Bad)
	if ok {
		mark = u.Green(g.OK)
	}
	line := mark + " " + parts[0]
	if len(parts) > 1 {
		line += "  " + u.Dim(strings.Join(parts[1:], " "+g.Dot+" "))
	}
	u.Printf("%s\n", line)
}

// Row is one "key  value" line under a failure headline.
type Row struct{ Key, Val string }

// Fail prints an error block, only when the UI is on:
//
//	✗ ERR::TARGET::BLOCKED: the site refused the request
//	  hint     retry with --render
//	  request  01J9Z... (spicrawl logs get 01J9Z...)
//	  docs     https://docs.spicrawl.com/errors/target-blocked
//
// The ERR:: code is muted so the sentence leads; the hint is the accent, since
// it is the line to act on; the keys line up; long text wraps under its own
// column instead of under the key; URLs become links where the terminal has
// them. Rows with an empty value are skipped.
func (u *UI) Fail(headline string, rows ...Row) {
	if !u.On() {
		return
	}
	cols := u.Cols()
	if cols > 0 {
		cols-- // a line that fills the row exactly can leave a blank one after it on some terminals
	}
	var b strings.Builder
	var code string
	if i := strings.Index(headline, ": "); i > 0 && strings.HasPrefix(headline, "ERR::") && !strings.ContainsAny(headline[:i], " \t") {
		code, headline = headline[:i+1], headline[i+2:] // "ERR::X::Y:" and the sentence
	}
	lead := u.Red(u.Glyphs().Bad) + " "
	// The first line shares its row with the code; the rest have the full width.
	var text []string
	for i, para := range strings.Split(headline, "\n") {
		if i > 0 {
			text = append(text, Wrap(para, cols-2)...)
			continue
		}
		ls := Wrap(para, cols-2-Width(code)-1)
		if len(ls) > 1 {
			rest := strings.Fields(para)[len(strings.Fields(ls[0])):]
			ls = append(ls[:1], Wrap(strings.Join(rest, " "), cols-2)...)
		}
		text = append(text, ls...)
	}
	if code != "" {
		text[0] = u.Dim(code) + " " + u.Bold(text[0])
	} else {
		text[0] = u.Bold(text[0])
	}
	for i, l := range text {
		if i > 0 {
			l = u.Bold(l)
		}
		b.WriteString(lead + l + "\n")
		lead = "  "
	}
	pad := 0
	for _, r := range rows {
		if r.Val != "" {
			pad = max(pad, Width(r.Key))
		}
	}
	for _, r := range rows {
		if r.Val == "" {
			continue
		}
		key := r.Key + strings.Repeat(" ", pad-Width(r.Key))
		if r.Key == "hint" {
			key = u.Accent(key)
		} else {
			key = u.Dim(key)
		}
		for i, l := range Wrap(r.Val, cols-2-pad-2) {
			if i > 0 {
				key = strings.Repeat(" ", pad)
			}
			b.WriteString("  " + key + "  " + u.Linkify(l) + "\n")
		}
	}
	u.Printf("%s", b.String())
}

// Elapsed formats a duration for humans: 840ms is "0.8s", 12.14s is "12.1s",
// 64s is "1m04s", 3900s is "1h05m".
func Elapsed(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return Span(d)
}

// ---- spinner ---------------------------------------------------------------

// Delay is how long an operation runs before its spinner first appears, so a
// fast call never flashes one. Interval is the frame time. Tests change them.
var (
	Delay    = 300 * time.Millisecond
	Interval = 80 * time.Millisecond
)

// Spinner is one live "⠹ label · 4.2s" line. At most one runs at a time: Spin
// while another is active returns nil, so the outermost operation (a whole
// batch wait) keeps its label over the calls inside it.
//
// Progress turns it into a determinate bar: "⠹ label  ━━━╸──  7/20 · 12.1s ·
// ETA 22s". The line is measured against the terminal on every frame, so a
// resize is picked up within one Interval and the label shortens to fit.
type Spinner struct {
	u     *UI
	start time.Time
	quit  chan struct{}
	done  chan struct{}

	mu      sync.Mutex
	label   string
	frame   int
	items   int // finished, of total; total 0 means no bar
	total   int
	drawn   bool // something is on the line and the cursor is hidden
	stopped bool
	elapsed time.Duration
}

var (
	curMu sync.Mutex
	cur   *Spinner
)

// Spin starts a spinner with the given label, or returns nil when the UI is
// off or a spinner already runs. Always Stop it (defer is fine; Stop is
// idempotent and nil-safe).
func (u *UI) Spin(format string, a ...any) *Spinner {
	if !u.On() {
		return nil
	}
	curMu.Lock()
	defer curMu.Unlock()
	if cur != nil {
		return nil
	}
	s := &Spinner{u: u, label: fmt.Sprintf(format, a...), start: time.Now(),
		quit: make(chan struct{}), done: make(chan struct{})}
	cur = s
	go s.run()
	return s
}

// Label changes the text shown; the next frame picks it up.
func (s *Spinner) Label(format string, a ...any) {
	if s == nil {
		return
	}
	l := fmt.Sprintf(format, a...)
	s.mu.Lock()
	s.label = l
	s.mu.Unlock()
}

// Progress shows "done of total" as a bar with a count and an ETA next to the
// label. Call it as often as the count changes; it is cheap. A total of 0 (or
// less) takes the bar away again.
func (s *Spinner) Progress(done, total int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.items, s.total = done, total
	s.mu.Unlock()
}

// Around runs fn with the spinner line cleared, then redraws it. Use it for
// writes to another stream that shares the terminal (results on stdout).
func (s *Spinner) Around(fn func()) {
	if s == nil {
		fn()
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clear()
	fn()
	if s.drawn {
		s.draw()
	}
}

// Stop removes the line, shows the cursor again and returns the time since
// Spin. Do it before printing a result or an error.
func (s *Spinner) Stop() time.Duration {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	if s.stopped {
		defer s.mu.Unlock()
		return s.elapsed
	}
	s.stopped = true
	s.elapsed = time.Since(s.start)
	close(s.quit)
	s.mu.Unlock()
	<-s.done // the drawing goroutine is gone: nothing writes after this
	s.mu.Lock()
	s.clear()
	if s.drawn {
		io.WriteString(s.u.w, "\x1b[?25h")
		s.drawn = false
	}
	d := s.elapsed
	s.mu.Unlock()
	curMu.Lock()
	if cur == s {
		cur = nil
	}
	curMu.Unlock()
	return d
}

func (s *Spinner) run() {
	defer close(s.done)
	select {
	case <-time.After(Delay):
	case <-s.quit:
		return
	}
	t := time.NewTicker(Interval)
	defer t.Stop()
	for {
		s.mu.Lock()
		s.draw()
		s.mu.Unlock()
		select {
		case <-t.C:
		case <-s.quit:
			return
		}
	}
}

// clear wipes the spinner line. Callers hold s.mu.
func (s *Spinner) clear() {
	if s.drawn {
		io.WriteString(s.u.w, "\r\x1b[K")
	}
}

// draw writes one frame in a single Write. Callers hold s.mu.
func (s *Spinner) draw() {
	if s.stopped {
		return
	}
	g := s.u.Glyphs()
	frame := g.Frames[s.frame%len(g.Frames)]
	// The visible line must stay one cell short of the width: a line that
	// wraps cannot be cleared by \r.
	cols := s.u.Cols()
	if cols == 0 {
		cols = 80
	}
	elapsed := time.Since(s.start)
	var line string
	if s.total > 0 {
		line = s.u.progressLine(frame, s.label, s.items, s.total, elapsed, cols)
	} else {
		suffix := " " + g.Dot + " " + Elapsed(elapsed)
		label := Truncate(s.label, max(cols-1-2-Width(suffix), 4), g.Ellipsis)
		line = s.u.Accent(frame) + " " + label + s.u.Dim(suffix)
	}
	var b bytes.Buffer
	if !s.drawn {
		b.WriteString("\x1b[?25l") // hide the cursor; Stop shows it again
		s.drawn = true
	}
	b.WriteString("\r\x1b[K" + line)
	s.frame++
	s.u.w.Write(b.Bytes())
}

// termCols is the terminal's column count, 0 when w is not a terminal.
func termCols(w io.Writer) int {
	if f, ok := w.(*os.File); ok {
		if cols, _, err := term.GetSize(int(f.Fd())); err == nil && cols > 0 {
			return cols
		}
	}
	return 0
}

// Fprintf writes to w like fmt.Fprintf, but when a spinner is drawing on w it
// clears the spinner line first and redraws it after, so a retry note or a
// warning lands on its own line above the spinner.
func Fprintf(w io.Writer, format string, a ...any) {
	curMu.Lock()
	s := cur
	curMu.Unlock()
	if s != nil && s.u.w == w {
		s.Around(func() { fmt.Fprintf(w, format, a...) })
		return
	}
	fmt.Fprintf(w, format, a...)
}

// StopAll stops the running spinner, if any. Execute calls it once the command
// returns so a cancelled run (Ctrl-C) cannot leave the cursor hidden.
func StopAll() {
	curMu.Lock()
	s := cur
	curMu.Unlock()
	s.Stop()
}
