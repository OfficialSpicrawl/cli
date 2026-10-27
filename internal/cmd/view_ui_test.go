package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/OfficialSpicrawl/cli/internal/output"
	"github.com/OfficialSpicrawl/cli/internal/ui"
)

func TestViewRowsWrapUnderTheirColumnOnlyOnATerminal(t *testing.T) {
	rows := []ui.Row{
		{Key: "key", Val: "the saved key, checked against the API a moment ago and still valid for every project"},
		{Key: "base url", Val: "https://api.spicrawl.com"},
		{Key: "empty", Val: ""},
	}
	want := "  key       the saved key, checked against\n" +
		"            the API a moment ago and still\n" +
		"            valid for every project\n" +
		"  base url  https://api.spicrawl.com\n"
	u := ui.ForceCaps(&bytes.Buffer{}, ui.Caps{Unicode: true, Width: 46})
	if got := stripANSI(viewRows(u, "  ", rows)); got != want {
		t.Errorf("wrapped rows:\n%q\nwant\n%q", got, want)
	}

	// Width unknown (a buffer, not a terminal): the value is printed whole, as
	// it always was, so a test or a log never sees a surprise line break.
	whole := stripANSI(viewRows(ui.Force(&bytes.Buffer{}), "  ", rows))
	if !strings.Contains(whole, "  key       the saved key, checked against the API a moment ago and still valid for every project\n") {
		t.Errorf("unknown width must not wrap:\n%s", whole)
	}
	// Off: the plain padded rows, no escapes.
	var off *ui.UI
	if got := viewRows(off, "  ", rows); got != whole {
		t.Errorf("off rows differ from live rows without colour:\n%q\n%q", got, whole)
	}
	// Links on a terminal that has them; the text is unchanged.
	linked := ui.ForceCaps(&bytes.Buffer{}, ui.Caps{Unicode: true, Links: true})
	got := viewRows(linked, "", []ui.Row{{Key: "docs", Val: "https://docs.spicrawl.com"}})
	if !strings.Contains(got, "\x1b]8;;https://docs.spicrawl.com\x1b\\") || ui.StripANSI(got) != "docs  https://docs.spicrawl.com\n" {
		t.Errorf("link row: %q", got)
	}
}

func TestViewMarksDegradeToASCII(t *testing.T) {
	unicode, ascii := ui.Force(&bytes.Buffer{}), ui.ForceCaps(&bytes.Buffer{}, ui.Caps{})
	for m, want := range map[int][2]string{
		markOK:      {"✓", "+"},
		markBad:     {"✗", "x"},
		markWarn:    {"!", "!"},
		markNeutral: {"–", "-"},
	} {
		if got := stripANSI(viewMark(unicode, m)); got != want[0] {
			t.Errorf("mark %d = %q, want %q", m, got, want[0])
		}
		if got := stripANSI(viewMark(ascii, m)); got != want[1] {
			t.Errorf("ASCII mark %d = %q, want %q", m, got, want[1])
		}
	}
	if !strings.Contains(viewMark(unicode, markWarn), "\x1b[33m") {
		t.Errorf("a warning is amber, not just bold: %q", viewMark(unicode, markWarn))
	}
	if got := viewLead(nil, markOK); got != "" {
		t.Errorf("off lead = %q", got)
	}
}

func TestViewTableAlignsWideCharacters(t *testing.T) {
	headers := []string{"NAME", "NOTE"}
	rows := [][]string{{"日本語", "x"}, {"abcdef", "y"}}
	var buf bytes.Buffer
	p := &output.Printer{Out: &buf}
	viewTable(p, ui.Force(&buf), headers, rows, nil)
	lines := strings.Split(strings.TrimRight(stripANSI(buf.String()), "\n"), "\n")
	// The second column starts at the same cell on every row.
	starts := map[int]bool{}
	for _, l := range lines {
		for _, marker := range []string{"NOTE", "x", "y"} {
			if i := strings.Index(l, marker); i >= 0 && (marker == "NOTE" || len(l) > 3) {
				starts[ui.Width(l[:i])] = true
			}
		}
	}
	if len(starts) != 1 {
		t.Errorf("columns are ragged (%v):\n%s", starts, strings.Join(lines, "\n"))
	}
}
