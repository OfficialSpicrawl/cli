package cmd

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/OfficialSpicrawl/cli/internal/output"
	"github.com/OfficialSpicrawl/cli/internal/ui"
)

var viewANSI = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")

// stripANSI removes colour and cursor codes.
func stripANSI(s string) string { return viewANSI.ReplaceAllString(s, "") }

// liveUI makes the commands draw their terminal output into the test's
// buffers, as if both streams were terminals. The spinner keeps its delay.
func liveUI(t *testing.T) {
	t.Helper()
	viewForce = true
	t.Cleanup(func() { viewForce = false })
}

// spinNow also lets the spinner appear at once, so a short slow call shows it.
func spinNow(t *testing.T) {
	t.Helper()
	liveUI(t)
	d, i := ui.Delay, ui.Interval
	ui.Delay, ui.Interval = 0, time.Millisecond
	t.Cleanup(func() { ui.Delay, ui.Interval = d, i })
}

// noEscapes fails when s carries any terminal control code.
func noEscapes(t *testing.T, what, s string) {
	t.Helper()
	if strings.Contains(s, "\x1b") {
		t.Errorf("%s has terminal escapes: %q", what, s)
	}
}

func TestViewGatingFollowsTheStream(t *testing.T) {
	var out, errb bytes.Buffer
	p := &output.Printer{Out: &out, Err: &errb}
	if outUI(p).On() || errUI(p).On() {
		t.Error("a buffer is not a terminal: the live output must be off")
	}

	liveUI(t)
	if !outUI(p).On() || !errUI(p).On() {
		t.Error("forced: both should be on")
	}
	p.JSON = true
	if outUI(p).On() {
		t.Error("JSON mode must never be styled on stdout")
	}
	if !errUI(p).On() {
		t.Error("JSON picked only because stdout is piped must keep stderr's spinner")
	}
	p.Plain = true // --json or --quiet
	if outUI(p).On() || errUI(p).On() {
		t.Error("--json and --quiet must keep both streams plain")
	}
}

func TestViewHelpersAreInertWhenOff(t *testing.T) {
	var out, errb bytes.Buffer
	p := &output.Printer{Out: &out, Err: &errb}
	if sp := spin(p, "waiting"); sp != nil {
		t.Error("spin returned a spinner while off")
	}
	if viewIntro(p, "title", ui.Row{Key: "k", Val: "v"}) {
		t.Error("viewIntro drew while off")
	}
	viewNext(p, "do a thing")
	if errb.Len() != 0 {
		t.Errorf("wrote %q while off", errb.String())
	}
	var u *ui.UI
	if viewKey(u, 6, "id:") != "id:   " || viewLead(u, markOK) != "" {
		t.Error("an off UI must leave keys padded and marks absent")
	}
}

func TestViewTableOffIsExactlyTheTabwriterTable(t *testing.T) {
	headers := []string{"A", "BB", "C"}
	rows := [][]string{{"one", "x", "—"}, {"two-two", "yy", "z"}}
	var plain, off bytes.Buffer
	(&output.Printer{Out: &plain}).Table(headers, rows)
	p := &output.Printer{Out: &off}
	viewTable(p, nil, headers, rows, nil)
	if off.String() != plain.String() {
		t.Errorf("off table differs:\n%q\n%q", off.String(), plain.String())
	}

	// On: coloured, and the columns line up exactly as the plain table's do.
	var live bytes.Buffer
	p = &output.Printer{Out: &live}
	viewTable(p, ui.Force(&live), headers, rows, func(col int, cell string) string {
		return ui.Force(&live).Red(cell)
	})
	if !strings.Contains(live.String(), "\x1b[") {
		t.Fatal("no colour in the live table")
	}
	if got := stripANSI(live.String()); got != plain.String() {
		t.Errorf("live table misaligned:\n%q\n%q", got, plain.String())
	}
}
