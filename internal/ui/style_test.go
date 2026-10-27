package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestRolesResolvePerColourLevel(t *testing.T) {
	cases := []struct {
		caps Caps
		role Role
		want string
	}{
		{Caps{}, Accent, "36"},
		{Caps{}, Success, "32"},
		{Caps{}, Danger, "31"},
		{Caps{}, Warn, "33"},
		{Caps{}, Muted, "2"},
		{Caps{}, Strong, "1"},
		{Caps{Color: Color256}, Accent, "38;5;30"},
		{Caps{Color: Color256}, Warn, "38;5;172"},
		{Caps{Color: ColorTrue}, Accent, "38;2;13;148;136"},
		{Caps{Color: ColorTrue, Light: true}, Accent, "38;2;15;118;110"},
		{Caps{Color: ColorTrue}, Warn, "38;2;217;119;6"},
		// The signals stay in the terminal's own palette at every level, so a
		// theme tunes them for its background.
		{Caps{Color: ColorTrue}, Danger, "31"},
		{Caps{Color: Color256}, Success, "32"},
		{Caps{Color: ColorTrue}, Muted, "2"},
	}
	for _, c := range cases {
		if got := c.caps.sgr(c.role); got != c.want {
			t.Errorf("sgr(%+v, %d) = %q, want %q", c.caps, c.role, got, c.want)
		}
	}
	u := ForceCaps(&bytes.Buffer{}, Caps{Color: ColorTrue})
	if got := u.Accent("x"); got != "\x1b[38;2;13;148;136mx\x1b[0m" {
		t.Errorf("Accent = %q", got)
	}
	if u.Cyan("x") != u.Accent("x") || u.Red("x") != u.Paint(Danger, "x") {
		t.Error("the old names must keep resolving to their roles")
	}
}

func TestGlyphsFallBackToASCII(t *testing.T) {
	var buf bytes.Buffer
	u := ForceCaps(&buf, Caps{}) // not a UTF-8 terminal
	u.Done(true, "https://example.com", "200", "1 credit")
	u.Done(false, "boom")
	u.Fail("ERR::X::Y: broke", Row{"hint", "try again"})
	out := StripANSI(buf.String())
	for _, want := range []string{"+ https://example.com  200 - 1 credit\n", "x boom\n", "x ERR::X::Y: broke\n", "  hint  try again\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("ASCII output lacks %q: %q", want, out)
		}
	}
	for _, r := range out {
		if r > 0x7e {
			t.Fatalf("a non-ASCII rune %q on an ASCII terminal: %q", r, out)
		}
	}
	if g := (*UI)(nil).Glyphs(); g.OK != "+" {
		t.Errorf("a nil UI must have the safe glyphs, got %+v", g)
	}
	if g := Force(&buf).Glyphs(); g.OK != "✓" || len(g.Frames) != 10 {
		t.Errorf("unicode glyphs = %+v", g)
	}
}

func TestLinks(t *testing.T) {
	const url = "https://docs.spicrawl.com/errors/target-blocked"
	on := ForceCaps(&bytes.Buffer{}, Caps{Links: true})
	off := ForceCaps(&bytes.Buffer{}, Caps{})
	if got := on.Link(url, "docs"); got != "\x1b]8;;"+url+"\x1b\\docs\x1b]8;;\x1b\\" {
		t.Errorf("Link = %q", got)
	}
	if got := on.Link(url, ""); !strings.HasSuffix(got, url+"\x1b]8;;\x1b\\") {
		t.Errorf("an empty text means the URL: %q", got)
	}
	if got := off.Link(url, url); got != url {
		t.Errorf("no hyperlinks: %q", got)
	}
	if got := off.Link(url, "docs"); got != "docs ("+url+")" {
		t.Errorf("a label must not swallow the address: %q", got)
	}
	var nilUI *UI
	if got := nilUI.Link(url, "docs"); got != "docs" {
		t.Errorf("off UI: %q", got)
	}

	// Linkify wraps whole URLs and leaves the sentence's punctuation outside.
	got := on.Linkify("see https://a.example/x, or https://b.example.")
	want := "see \x1b]8;;https://a.example/x\x1b\\https://a.example/x\x1b]8;;\x1b\\, or \x1b]8;;https://b.example\x1b\\https://b.example\x1b]8;;\x1b\\."
	if got != want {
		t.Errorf("Linkify = %q\nwant      %q", got, want)
	}
	if StripANSI(got) != "see https://a.example/x, or https://b.example." {
		t.Errorf("stripping the escapes must leave the text: %q", StripANSI(got))
	}
	if got := off.Linkify("see https://a.example/x"); got != "see https://a.example/x" {
		t.Errorf("no hyperlinks, no change: %q", got)
	}
}
