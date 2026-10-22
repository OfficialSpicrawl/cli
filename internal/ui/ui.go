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
	"unicode/utf8"

	"github.com/mattn/go-isatty"
	"golang.org/x/term"
)

// Live reports whether w should get a spinner and colour: w is a terminal and
// nothing asked for plain output. NO_COLOR (any non-empty value), TERM=dumb
// and CI (anything but empty, 0 or false) all turn it off.
func Live(w io.Writer) bool { return live(w, os.Getenv, runtime.GOOS, isTTY) }

func isTTY(f *os.File) bool {
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

func live(w io.Writer, getenv func(string) string, goos string, tty func(*os.File) bool) bool {
	f, ok := w.(*os.File)
	if !ok || !tty(f) {
		return false
	}
	if getenv("NO_COLOR") != "" || getenv("TERM") == "dumb" {
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
	w  io.Writer
	on bool
}

// New returns a UI for w that is on only when Live(w).
func New(w io.Writer) *UI { return &UI{w: w, on: Live(w)} }

// Force returns a UI that is always on, whatever w is. For tests.
func Force(w io.Writer) *UI { return &UI{w: w, on: true} }

// On reports whether this UI draws anything.
func (u *UI) On() bool { return u != nil && u.on }

// ---- colour ----------------------------------------------------------------

// The palette is the terminal's own 8 colours plus bold and faint, so it
// follows the user's theme and stays readable on light and dark backgrounds.
// No white, black or yellow: those vanish on one of the two.

func (u *UI) style(code, s string) string {
	if !u.On() || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (u *UI) Bold(s string) string  { return u.style("1", s) }
func (u *UI) Dim(s string) string   { return u.style("2", s) }
func (u *UI) Red(s string) string   { return u.style("31", s) }
func (u *UI) Green(s string) string { return u.style("32", s) }
func (u *UI) Cyan(s string) string  { return u.style("36", s) }

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
// parts[0] is the subject (a URL); the rest are the facts, dimmed. ok picks a
// green ✓ or a red ✗.
func (u *UI) Done(ok bool, parts ...string) {
	if !u.On() || len(parts) == 0 {
		return
	}
	mark := u.Red("✗")
	if ok {
		mark = u.Green("✓")
	}
	line := mark + " " + parts[0]
	if len(parts) > 1 {
		line += "  " + u.Dim(strings.Join(parts[1:], " · "))
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
//
// Rows with an empty value are skipped.
func (u *UI) Fail(headline string, rows ...Row) {
	if !u.On() {
		return
	}
	var b strings.Builder
	b.WriteString(u.Red("✗") + " " + u.Bold(headline) + "\n")
	pad := 0
	for _, r := range rows {
		if r.Val != "" && len(r.Key) > pad {
			pad = len(r.Key)
		}
	}
	for _, r := range rows {
		if r.Val != "" {
			b.WriteString("  " + u.Dim(fmt.Sprintf("%-*s", pad, r.Key)) + "  " + r.Val + "\n")
		}
	}
	u.Printf("%s", b.String())
}

// Elapsed formats a duration for humans: 840ms is "0.8s", 12.14s is "12.1s",
// 64s is "1m04s".
func Elapsed(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	d = d.Round(time.Second)
	return fmt.Sprintf("%dm%02ds", int(d/time.Minute), int(d%time.Minute/time.Second))
}

// ---- spinner ---------------------------------------------------------------

// Delay is how long an operation runs before its spinner first appears, so a
// fast call never flashes one. Interval is the frame time. Tests change them.
var (
	Delay    = 300 * time.Millisecond
	Interval = 80 * time.Millisecond
)

var frames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Spinner is one live "⠹ label · 4.2s" line. At most one runs at a time: Spin
// while another is active returns nil, so the outermost operation (a whole
// batch wait) keeps its label over the calls inside it.
type Spinner struct {
	u     *UI
	start time.Time
	quit  chan struct{}
	done  chan struct{}

	mu      sync.Mutex
	label   string
	frame   int
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
	suffix := " · " + Elapsed(time.Since(s.start))
	// The visible line is 2 cells of glyph, the label and the suffix, and must
	// stay one short of the width: a line that wraps cannot be cleared by \r.
	label := truncate(s.label, width(s.u.w)-1-2-utf8.RuneCountInString(suffix))
	var b bytes.Buffer
	if !s.drawn {
		b.WriteString("\x1b[?25l") // hide the cursor; Stop shows it again
		s.drawn = true
	}
	b.WriteString("\r\x1b[K" + s.u.Cyan(frames[s.frame%len(frames)]) + " " + label + s.u.Dim(suffix))
	s.frame++
	s.u.w.Write(b.Bytes())
}

// width is the terminal's column count, 80 when it cannot be read.
func width(w io.Writer) int {
	if f, ok := w.(*os.File); ok {
		if cols, _, err := term.GetSize(int(f.Fd())); err == nil && cols > 0 {
			return cols
		}
	}
	return 80
}

// truncate shortens s to n cells with a trailing …. A rune is one cell; wide
// East Asian characters are not special-cased (ponytail: they may overshoot).
func truncate(s string, n int) string {
	n = max(n, 4)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
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
