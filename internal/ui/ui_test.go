package ui

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func fast(t *testing.T) {
	t.Helper()
	d, i := Delay, Interval
	Delay, Interval = 20*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { Delay, Interval = d, i })
}

func TestLiveGating(t *testing.T) {
	tty := func(*os.File) bool { return true }
	notTTY := func(*os.File) bool { return false }
	env := func(kv ...string) func(string) string {
		m := map[string]string{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) string { return m[k] }
	}
	cases := []struct {
		name string
		w    io.Writer
		env  func(string) string
		goos string
		tty  func(*os.File) bool
		want bool
	}{
		{"terminal", os.Stderr, env(), "linux", tty, true},
		{"not a terminal", os.Stderr, env(), "linux", notTTY, false},
		{"buffer is never live", &bytes.Buffer{}, env(), "linux", tty, false},
		{"NO_COLOR", os.Stderr, env("NO_COLOR", "1"), "linux", tty, false},
		{"empty NO_COLOR is ignored", os.Stderr, env("NO_COLOR", ""), "linux", tty, true},
		{"TERM=dumb", os.Stderr, env("TERM", "dumb"), "linux", tty, false},
		{"TERM=xterm-256color", os.Stderr, env("TERM", "xterm-256color"), "linux", tty, true},
		{"CI=true", os.Stderr, env("CI", "true"), "linux", tty, false},
		{"CI=1", os.Stderr, env("CI", "1"), "linux", tty, false},
		{"CI=false", os.Stderr, env("CI", "false"), "linux", tty, true},
		{"legacy windows console", os.Stderr, env(), "windows", tty, false},
		{"windows terminal", os.Stderr, env("WT_SESSION", "x"), "windows", tty, true},
	}
	for _, c := range cases {
		if got := live(c.w, c.env, c.goos, c.tty); got != c.want {
			t.Errorf("%s: live = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestOffIsInert(t *testing.T) {
	for _, u := range []*UI{nil, {}, New(&bytes.Buffer{})} {
		if u.On() {
			t.Fatal("expected off")
		}
		if got := u.Green("x") + u.Bold("y") + u.Dim("z"); got != "xyz" {
			t.Errorf("off UI styled text: %q", got)
		}
		s := u.Spin("work")
		if s != nil {
			t.Fatal("off UI returned a spinner")
		}
		s.Label("x") // nil-safe
		s.Around(func() {})
		if s.Stop() != 0 {
			t.Error("nil Stop")
		}
		u.Done(true, "a", "b")
		u.Fail("boom", Row{"k", "v"})
	}
	var buf bytes.Buffer
	New(&buf).Done(true, "x")
	if buf.Len() != 0 {
		t.Errorf("off UI wrote %q", buf.String())
	}
}

func TestSpinnerLifecycle(t *testing.T) {
	fast(t)
	var buf syncBuf
	u := Force(&buf)
	s := u.Spin("Scraping %s", "example.com")
	if s == nil {
		t.Fatal("no spinner")
	}
	if u.Spin("nested") != nil {
		t.Error("a second spinner started while one runs")
	}
	time.Sleep(60 * time.Millisecond)
	s.Label("Scraping example.com · rendering JavaScript")
	time.Sleep(30 * time.Millisecond)
	if d := s.Stop(); d < 60*time.Millisecond {
		t.Errorf("Stop returned %s", d)
	}
	out := buf.String()
	for _, want := range []string{"\x1b[?25l", "Scraping example.com", "rendering JavaScript", "s\x1b[0m"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q: %q", want, out)
		}
	}
	if !strings.HasSuffix(out, "\r\x1b[K\x1b[?25h") {
		t.Errorf("output must end by clearing the line and showing the cursor: %q", out)
	}
	if strings.Contains(out, "\n") {
		t.Errorf("a spinner must never emit a newline: %q", out)
	}
	if strings.Count(out, "\x1b[?25l") != 1 {
		t.Errorf("cursor hidden more than once: %q", out)
	}
	n := len(out)
	s.Stop() // idempotent
	time.Sleep(30 * time.Millisecond)
	if got := buf.String(); len(got) != n {
		t.Errorf("wrote after Stop: %q", got[n:])
	}
	if u.Spin("again") == nil {
		t.Error("cannot start a spinner after Stop")
	} else {
		StopAll()
	}
}

func TestFastCallShowsNothing(t *testing.T) {
	var buf syncBuf
	s := Force(&buf).Spin("quick")
	time.Sleep(20 * time.Millisecond) // well under the 300 ms Delay
	s.Stop()
	if buf.String() != "" {
		t.Errorf("a call under the delay drew %q", buf.String())
	}
}

func TestFprintfClearsTheSpinnerLine(t *testing.T) {
	fast(t)
	var buf syncBuf
	s := Force(&buf).Spin("waiting")
	time.Sleep(50 * time.Millisecond)
	Fprintf(&buf, "retrying in 1s\n")
	s.Stop()
	if !strings.Contains(buf.String(), "\r\x1b[Kretrying in 1s\n") {
		t.Errorf("the note was not written on a cleared line: %q", buf.String())
	}
	// With no spinner it is a plain write.
	var plain bytes.Buffer
	Fprintf(&plain, "x %d\n", 1)
	if plain.String() != "x 1\n" {
		t.Errorf("plain Fprintf = %q", plain.String())
	}
}

func TestStopAllRestoresTheCursor(t *testing.T) {
	fast(t)
	var buf syncBuf
	Force(&buf).Spin("work")
	time.Sleep(40 * time.Millisecond)
	StopAll()
	if !strings.HasSuffix(buf.String(), "\x1b[?25h") {
		t.Errorf("cursor not restored: %q", buf.String())
	}
}

func TestDoneAndFail(t *testing.T) {
	var buf bytes.Buffer
	u := Force(&buf)
	u.Done(true, "https://example.com", "200", "1 credit", "4.2s")
	u.Done(false, "https://example.com", "503")
	u.Fail("ERR::TARGET::BLOCKED: refused", Row{"hint", "use --render"}, Row{"skipped", ""}, Row{"request", "01J9"})
	out := buf.String()
	for _, want := range []string{
		"\x1b[32m✓\x1b[0m https://example.com  \x1b[2m200 · 1 credit · 4.2s\x1b[0m\n",
		"\x1b[31m✗\x1b[0m https://example.com  \x1b[2m503\x1b[0m\n",
		"\x1b[31m✗\x1b[0m \x1b[1mERR::TARGET::BLOCKED: refused\x1b[0m\n",
		"  \x1b[2mhint   \x1b[0m  use --render\n",
		"  \x1b[2mrequest\x1b[0m  01J9\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	if strings.Contains(out, "skipped") {
		t.Error("an empty row was printed")
	}
}

func TestElapsedAndTruncate(t *testing.T) {
	for d, want := range map[time.Duration]string{
		840 * time.Millisecond:   "0.8s",
		12140 * time.Millisecond: "12.1s",
		64 * time.Second:         "1m04s",
		601 * time.Second:        "10m01s",
	} {
		if got := Elapsed(d); got != want {
			t.Errorf("Elapsed(%s) = %q, want %q", d, got, want)
		}
	}
	if got := truncate("https://example.com/a/very/long/path", 12); got != "https://exa…" {
		t.Errorf("truncate = %q", got)
	}
	if got := truncate("short", 12); got != "short" {
		t.Errorf("truncate = %q", got)
	}
}
