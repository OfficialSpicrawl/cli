// Package output writes command results in the format the reader needs.
//
// The contract (DESIGN.md): stdout carries data only; progress, warnings and
// errors go to stderr. JSON is chosen when --json is passed or stdout is not a
// terminal, so an agent piping the CLI gets JSON without asking.
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/mattn/go-isatty"

	"github.com/OfficialSpicrawl/cli/internal/ui"
)

type Printer struct {
	JSON  bool
	Quiet bool
	Out   io.Writer
	Err   io.Writer
	// Plain keeps stderr plain (no spinner, colour or summary lines): --json
	// and --quiet set it. The live output is also off whenever Err is not a
	// terminal, or NO_COLOR, TERM=dumb or CI say so (see ui.Live).
	Plain bool
	// Term overrides UI(). Tests set it to see the live output.
	Term *ui.UI
}

// New picks JSON when forced or when stdout is not a terminal.
func New(forceJSON, quiet bool) *Printer {
	tty := isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
	return &Printer{JSON: forceJSON || !tty, Quiet: quiet, Out: os.Stdout, Err: os.Stderr, Plain: forceJSON || quiet}
}

// UI is the live terminal output on stderr: spinner, colour, summary lines.
// It is off (every method a no-op) unless stderr is a terminal and neither
// --json, --quiet, NO_COLOR, TERM=dumb nor CI asked for plain output. Note
// that JSON picked only because stdout is piped does not turn it off: the
// spinner is stderr's, and a person watching a redirected run wants it.
func (p *Printer) UI() *ui.UI {
	switch {
	case p.Term != nil:
		return p.Term
	case p.Plain:
		return nil
	}
	return ui.New(p.Err)
}

// IsStdinTerminal reports whether stdin is interactive. Commands must never
// prompt when it is not.
func IsStdinTerminal() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
}

// Value writes v as indented JSON.
func (p *Printer) Value(v any) error {
	enc := json.NewEncoder(p.Out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// Line writes v as one compact JSON line (for --jsonl streams).
func (p *Printer) Line(v any) error {
	enc := json.NewEncoder(p.Out)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// RawJSON writes bytes that are already JSON, re-indented in JSON mode.
// It re-indents the bytes instead of decoding them, so numbers beyond
// float64 (ids, counters) and the API's key order survive, as in --jsonl.
func (p *Printer) RawJSON(b []byte) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, bytes.TrimSpace(b), "", "  "); err != nil {
		_, err := p.Out.Write(b)
		return err
	}
	buf.WriteByte('\n')
	_, err := p.Out.Write(buf.Bytes())
	return err
}

// Result prints v as JSON in JSON mode, otherwise calls human.
func (p *Printer) Result(v any, human func(w io.Writer)) error {
	if p.JSON {
		return p.Value(v)
	}
	human(p.Out)
	return nil
}

// Table writes rows with aligned columns (human mode only).
func (p *Printer) Table(headers []string, rows [][]string) {
	tw := tabwriter.NewWriter(p.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	tw.Flush()
}

// Info writes a progress or status line to stderr unless --quiet. Like Warn it
// shows server prose with tier names (PublicText), and like Warn it writes
// nothing in JSON mode.
func (p *Printer) Info(format string, a ...any) {
	if p.Quiet || p.JSON {
		return
	}
	ui.Fprintf(p.Err, "%s", PublicText(fmt.Sprintf(format+"\n", a...)))
}

// Warn writes a warning to stderr, even with --quiet. Server prose in it is
// shown with tier names, never engine names (PublicText).
//
// In JSON mode it writes nothing: stderr there is the problem document of a
// failure and otherwise empty, so `cmd --json 2>err.json; jq . err.json` always
// parses (DESIGN.md, "Errors are the API's problem document"). The API's own
// warnings are already in the JSON on stdout.
func (p *Printer) Warn(format string, a ...any) {
	if p.JSON {
		return
	}
	ui.Fprintf(p.Err, "%s", PublicText(fmt.Sprintf("warning: "+format+"\n", a...)))
}
