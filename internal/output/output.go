// Package output writes command results in the format the reader needs.
//
// The contract (DESIGN.md): stdout carries data only; progress, warnings and
// errors go to stderr. JSON is chosen when --json is passed or stdout is not a
// terminal, so an agent piping the CLI gets JSON without asking.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/mattn/go-isatty"
)

type Printer struct {
	JSON  bool
	Quiet bool
	Out   io.Writer
	Err   io.Writer
}

// New picks JSON when forced or when stdout is not a terminal.
func New(forceJSON, quiet bool) *Printer {
	tty := isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
	return &Printer{JSON: forceJSON || !tty, Quiet: quiet, Out: os.Stdout, Err: os.Stderr}
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
func (p *Printer) RawJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		_, err := p.Out.Write(b)
		return err
	}
	return p.Value(v)
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

// Info writes a progress or status line to stderr unless --quiet.
func (p *Printer) Info(format string, a ...any) {
	if p.Quiet {
		return
	}
	fmt.Fprintf(p.Err, format+"\n", a...)
}

// Warn writes a warning to stderr, even with --quiet.
func (p *Printer) Warn(format string, a ...any) {
	fmt.Fprintf(p.Err, "warning: "+format+"\n", a...)
}
