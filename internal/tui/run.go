package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/mattn/go-isatty"
)

// Interactive reports whether a full-screen view may run: stdin and stdout
// are terminals, noTUI (--json or --quiet) is off, and neither TERM=dumb nor
// CI says the session is not a person's. NO_COLOR is not here: it takes the
// colour away, not the view.
func Interactive(noTUI bool) bool {
	return interactive(isTerminal(os.Stdin), isTerminal(os.Stdout), os.Getenv, noTUI)
}

func isTerminal(f *os.File) bool {
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

func interactive(inTTY, outTTY bool, getenv func(string) string, noTUI bool) bool {
	if noTUI || !inTTY || !outTTY || getenv("TERM") == "dumb" {
		return false
	}
	switch strings.ToLower(getenv("CI")) {
	case "", "0", "false":
		return true
	}
	return false
}

// Colour reports whether the view may use colour (NO_COLOR is unset).
func Colour() bool { return os.Getenv("NO_COLOR") == "" }

// Run shows the view until it ends and returns why. The terminal is restored
// however it ends: a key, Ctrl-C (the context), an error or a panic. A
// cancelled ctx returns ctx.Err(). opts are extra tea.ProgramOptions, for tests.
func Run(ctx context.Context, cfg Config, opts ...tea.ProgramOption) (Reason, error) {
	if !Colour() {
		opts = append([]tea.ProgramOption{tea.WithColorProfile(colorprofile.ASCII)}, opts...) // text styles stay, colours go
	}
	ctx, cancel := context.WithCancel(ctx) // stops a poll still in flight when the view ends
	defer cancel()
	m := New(ctx, cfg)
	if _, err := runProgram(ctx, m, opts...); err != nil {
		return 0, err
	}
	return m.Reason(), nil
}

func runProgram(ctx context.Context, m tea.Model, opts ...tea.ProgramOption) (tea.Model, error) {
	final, err := tea.NewProgram(m, append([]tea.ProgramOption{tea.WithContext(ctx)}, opts...)...).Run()
	switch {
	case err == nil:
		return final, nil
	case ctx.Err() != nil:
		return final, ctx.Err()
	case errors.Is(err, tea.ErrInterrupted):
		return final, context.Canceled
	case errors.Is(err, tea.ErrProgramPanic):
		return final, fmt.Errorf("the live view crashed (the terminal was restored): %w", err)
	}
	return final, err
}
