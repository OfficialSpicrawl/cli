package ui

import (
	"strings"
	"testing"
)

func TestWidth(t *testing.T) {
	for s, want := range map[string]int{
		"":                         0,
		"hello":                    5,
		"✓ ok":                     4,
		"⠹ wait":                   6,
		"日本語":                      6,
		"a日b":                      4,
		"é":                       1, // e + combining acute
		"\x1b[31mred\x1b[0m":       3,
		"\x1b[38;2;1;2;3mx\x1b[0m": 1,
		"\x1b]8;;https://x.example\x1b\\link\x1b]8;;\x1b\\": 4,
		"\x1b]8;;https://x.example\x07link\x1b]8;;\x07":     4,
		"🚀 go": 5,
	} {
		if got := Width(s); got != want {
			t.Errorf("Width(%q) = %d, want %d", s, got, want)
		}
	}
	if got := StripANSI("\x1b[1mbold\x1b[0m \x1b]8;;u\x1b\\l\x1b]8;;\x1b\\"); got != "bold l" {
		t.Errorf("StripANSI = %q", got)
	}
}

func TestTruncateCountsCells(t *testing.T) {
	for _, c := range []struct {
		in   string
		n    int
		ell  string
		want string
	}{
		{"https://example.com/a/very/long/path", 12, "…", "https://exa…"},
		{"short", 12, "…", "short"},
		{"exactly12chr", 12, "…", "exactly12chr"},
		{"日本語のテキスト", 7, "…", "日本語…"}, // 3 wide runes = 6 cells + the ellipsis = 7, never 8
		{"日本語のテキスト", 8, "…", "日本語…"},
		{"https://example.com/long", 10, "...", "https:/..."},
		{"anything", 0, "…", "…"},
	} {
		got := Truncate(c.in, c.n, c.ell)
		if got != c.want {
			t.Errorf("Truncate(%q, %d, %q) = %q, want %q", c.in, c.n, c.ell, got, c.want)
		}
		if c.n > 0 && Width(got) > c.n {
			t.Errorf("Truncate(%q, %d) is %d cells wide", c.in, c.n, Width(got))
		}
	}
}

func TestWrap(t *testing.T) {
	got := Wrap("the site refused the request after three attempts", 20)
	want := []string{"the site refused the", "request after three", "attempts"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("Wrap = %q", got)
	}
	// A word wider than the line stays whole, on its own line.
	got = Wrap("see https://example.com/a/very/long/url/that/does/not/fit now", 20)
	if len(got) != 3 || got[1] != "https://example.com/a/very/long/url/that/does/not/fit" {
		t.Errorf("a long word was split: %q", got)
	}
	// Paragraphs survive; an empty line stays one.
	if got := Wrap("a b\n\nc", 10); strings.Join(got, "|") != "a b||c" {
		t.Errorf("Wrap paragraphs = %q", got)
	}
	// A line keeps its own indentation, on every piece of it.
	if got := Wrap("  one two three four", 12); strings.Join(got, "|") != "  one two|  three four" {
		t.Errorf("Wrap indent = %q", got)
	}
	// Unknown width: untouched.
	if got := Wrap("a long line that would wrap", 0); len(got) != 1 {
		t.Errorf("unknown width must not wrap: %q", got)
	}
	// Wide characters count two cells.
	for _, l := range Wrap("日本語 日本語 日本語", 7) {
		if Width(l) > 7 {
			t.Errorf("line %q is %d cells", l, Width(l))
		}
	}
}
