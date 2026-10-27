package ui

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestBar(t *testing.T) {
	u := Force(&bytes.Buffer{})
	for _, c := range []struct {
		width, done, total int
		want               string
	}{
		{10, 0, 20, "──────────"},
		{10, 10, 20, "━━━━━─────"},
		{10, 11, 20, "━━━━━╸────"}, // a half cell: the bar moves twice as finely as it is wide
		{10, 20, 20, "━━━━━━━━━━"},
		{10, 99, 20, "━━━━━━━━━━"}, // never past the end
		{10, -3, 20, "──────────"},
		{10, 5, 0, "──────────"}, // no total: empty, never a division by zero
		{4, 1, 4, "━───"},
	} {
		got := StripANSI(u.Bar(c.width, c.done, c.total))
		if got != c.want {
			t.Errorf("Bar(%d, %d, %d) = %q, want %q", c.width, c.done, c.total, got, c.want)
		}
		if Width(got) != c.width {
			t.Errorf("Bar(%d, %d, %d) is %d cells", c.width, c.done, c.total, Width(got))
		}
	}
	if u.Bar(0, 1, 2) != "" {
		t.Error("a zero-width bar must be empty")
	}
	ascii := ForceCaps(&bytes.Buffer{}, Caps{})
	if got := StripANSI(ascii.Bar(10, 5, 10)); got != "#####-----" {
		t.Errorf("ASCII bar = %q", got)
	}
}

func TestETA(t *testing.T) {
	if d, ok := ETA(10*time.Second, 5, 20); !ok || d != 30*time.Second {
		t.Errorf("ETA = %s, %v", d, ok)
	}
	for _, c := range [][3]int{{0, 20, 10}, {20, 20, 10}, {25, 20, 10}, {5, 20, 0}} {
		if _, ok := ETA(time.Duration(c[2])*time.Second, c[0], c[1]); ok {
			t.Errorf("ETA offered for done=%d total=%d elapsed=%ds", c[0], c[1], c[2])
		}
	}
}

func TestProgressLineFitsEveryWidth(t *testing.T) {
	u := Force(&bytes.Buffer{})
	label := "Batch job 01J9Z6V0Q8M4K2T7R3N5B1C9XA running on a very long label indeed"
	for cols := 12; cols <= 200; cols++ {
		line := u.progressLine("⠹", label, 7, 20, 12*time.Second, cols)
		if w := Width(line); w > cols-1 {
			t.Fatalf("cols=%d: line is %d cells (%q); it must stay one short of the terminal", cols, w, StripANSI(line))
		}
	}
	// Wide: everything is there.
	full := StripANSI(u.progressLine("⠹", "Batch job", 7, 20, 12*time.Second, 100))
	for _, want := range []string{"⠹ Batch job", "━", "7/20", "12.0s", "ETA 22s"} {
		if !strings.Contains(full, want) {
			t.Errorf("wide line lacks %q: %q", want, full)
		}
	}
	// Too early to guess: one item's rate is not an estimate.
	if early := StripANSI(u.progressLine("⠹", "Batch job", 1, 20, 900*time.Millisecond, 100)); strings.Contains(early, "ETA") {
		t.Errorf("an ETA after 0.9 s: %q", early)
	}
	// Narrower: the ETA goes first, the bar next, and the label is kept.
	mid := StripANSI(u.progressLine("⠹", "Batch job", 7, 20, 12*time.Second, 50))
	if strings.Contains(mid, "ETA") || !strings.Contains(mid, "━") || !strings.Contains(mid, "Batch job") {
		t.Errorf("50 cols should drop the ETA but keep the bar: %q", mid)
	}
	tight := StripANSI(u.progressLine("⠹", "Batch job", 7, 20, 12*time.Second, 28))
	if strings.Contains(tight, "━") || !strings.Contains(tight, "7/20") || !strings.Contains(tight, "Batch") {
		t.Errorf("28 cols should drop the bar and keep the count: %q", tight)
	}
	// No total: a count, no bar.
	open := StripANSI(u.progressLine("⠹", "Crawling", 7, 0, 3*time.Second, 80))
	if strings.Contains(open, "━") || strings.Contains(open, "/") || !strings.Contains(open, " 7 ") {
		t.Errorf("an open-ended count: %q", open)
	}
}

func TestSpinnerProgressAndResize(t *testing.T) {
	d := Delay
	Delay = time.Hour // draw by hand, so the test controls every frame
	defer func() { Delay = d }()
	var buf syncBuf
	u := ForceCaps(&buf, Caps{Unicode: true, Width: 80})
	s := u.Spin("Batch job 01J9Z6V0Q8M4K2T7R3N5B1C9XA")
	s.Progress(3, 10)
	draw := func() string {
		before := len(buf.String())
		s.mu.Lock()
		s.draw()
		s.mu.Unlock()
		return StripANSI(buf.String()[before:])
	}
	wide := draw()
	if !strings.Contains(wide, "3/10") || !strings.Contains(wide, "━") {
		t.Errorf("progress not drawn: %q", wide)
	}
	// The terminal shrinks mid-run: the very next frame fits it.
	u.caps.Width = 30
	narrow := strings.TrimPrefix(draw(), "\r")
	if Width(narrow) > 29 || !strings.Contains(narrow, "3/10") {
		t.Errorf("after a resize to 30 columns the line is %d cells: %q", Width(narrow), narrow)
	}
	// Taking the total away takes the bar away.
	u.caps.Width = 80
	s.Progress(0, 0)
	if plain := draw(); strings.Contains(plain, "━") || strings.Contains(plain, "/10") {
		t.Errorf("bar still drawn: %q", plain)
	}
	s.Stop()
	(*Spinner)(nil).Progress(1, 2) // nil-safe
}

func TestSpinnerASCIIFrames(t *testing.T) {
	d := Delay
	Delay = time.Hour
	defer func() { Delay = d }()
	var buf syncBuf
	s := ForceCaps(&buf, Caps{}).Spin("work")
	for range 4 {
		s.mu.Lock()
		s.draw()
		s.mu.Unlock()
	}
	s.Stop()
	out := StripANSI(buf.String())
	for _, r := range out {
		if r > 0x7e {
			t.Fatalf("non-ASCII %q on an ASCII terminal: %q", r, out)
		}
	}
	if !strings.Contains(out, "| work - 0.0s") || !strings.Contains(out, "/ work") || !strings.Contains(out, "\\ work") {
		t.Errorf("ASCII spinner frames: %q", out)
	}
}

func TestFailWrapsUnderItsOwnColumn(t *testing.T) {
	var buf bytes.Buffer
	u := ForceCaps(&buf, Caps{Unicode: true, Width: 50, Links: true})
	u.Fail("ERR::TARGET::BLOCKED: the site refused the request after three attempts through the pool",
		Row{"hint", "retry with --render to run the page's JavaScript, or add --country to change the exit"},
		Row{"docs", "https://docs.spicrawl.com/errors/target-blocked"},
		Row{"request", "01J9Z6V0Q8M4K2T7R3N5B1C9XA"})
	out := StripANSI(buf.String())
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if Width(l) > 50 && !strings.Contains(l, "https://") {
			t.Errorf("line %q is %d cells wide", l, Width(l))
		}
	}
	if !strings.HasPrefix(out, "✗ ERR::TARGET::BLOCKED: the site refused the\n  request after three attempts through the pool\n") {
		t.Errorf("headline did not wrap under itself: %q", out)
	}
	if !strings.Contains(out, "  hint     retry with --render to run the page's\n           JavaScript, or add --country to change\n           the exit\n") {
		t.Errorf("hint did not wrap under its value column: %q", out)
	}
	if !strings.Contains(buf.String(), "\x1b]8;;https://docs.spicrawl.com/errors/target-blocked\x1b\\") {
		t.Error("the docs URL is not a hyperlink on a terminal with them")
	}
	// A message with its own lines keeps them, and their indentation.
	buf.Reset()
	u.Fail("cannot read the config file\n  line 3: bad value", Row{"file", "/home/me/.config/spicrawl/config.json"})
	if got := StripANSI(buf.String()); !strings.HasPrefix(got, "✗ cannot read the config file\n    line 3: bad value\n  file  /home/me") {
		t.Errorf("multi-line headline: %q", got)
	}
	// Without hyperlinks the URL is plain text, nothing else.
	var plain bytes.Buffer
	ForceCaps(&plain, Caps{Unicode: true}).Fail("boom", Row{"docs", "https://docs.spicrawl.com/x"})
	if strings.Contains(plain.String(), "\x1b]") {
		t.Errorf("OSC 8 on a terminal without hyperlinks: %q", plain.String())
	}
}
