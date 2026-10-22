// Package cmd holds the cobra command tree.
//
// Each command lives in its own file and registers itself from init() with
// rootCmd.AddCommand, so adding a command never touches this file.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/OfficialSpicrawl/cli/internal/api"
	"github.com/OfficialSpicrawl/cli/internal/config"
	"github.com/OfficialSpicrawl/cli/internal/exitcode"
	"github.com/OfficialSpicrawl/cli/internal/output"
	"github.com/OfficialSpicrawl/cli/internal/ui"
)

// Global flags.
var (
	flagJSON    bool
	flagQuiet   bool
	flagAPIKey  string
	flagBaseURL string
	flagTimeout time.Duration
)

var rootCmd = &cobra.Command{
	Use:   "spicrawl",
	Short: "Scrape, render and extract web data from the command line",
	Long: `spicrawl is the command-line client for the Spicrawl web data API.

Output is JSON whenever stdout is not a terminal (or with --json), so agents and
scripts can pipe it. Data goes to stdout; progress and errors go to stderr.
Exit codes are stable; run "spicrawl exit-codes".`,
	SilenceUsage:  true,
	SilenceErrors: true,
	Version:       api.Version,
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.BoolVar(&flagJSON, "json", false, "force JSON output (default when stdout is not a terminal)")
	pf.BoolVarP(&flagQuiet, "quiet", "q", false, "suppress progress messages on stderr")
	pf.StringVar(&flagAPIKey, "api-key", "", "API key (default: $SPICRAWL_API_KEY, then the config file)")
	pf.StringVar(&flagBaseURL, "base-url", "", "API base URL (default: $SPICRAWL_BASE_URL, then the config file, then "+config.DefaultBase+")")
	pf.DurationVar(&flagTimeout, "timeout", 3*time.Minute, "HTTP timeout per API call")

	rootCmd.AddCommand(&cobra.Command{
		Use:   "exit-codes",
		Short: "Exit codes and what they mean",
		Long: `Exit codes are stable; scripts and agents may branch on them.
With --json (or when stdout is not a terminal) prints [{code, name, meaning}].
In JSON mode a failed command writes the API's problem document to stderr.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return Printer().Result(exitcode.Table, printExitCodes)
		},
	})
}

// stdout and stderr are swapped out by tests; humanOutput lets a test see
// human mode although its stdout is not a terminal.
var (
	stdout      io.Writer = os.Stdout
	stderr      io.Writer = os.Stderr
	humanOutput bool
	// liveTerm, when set by a test, stands in for the stderr terminal: spinner,
	// colour and summary lines are drawn on it unless --json or --quiet.
	liveTerm *ui.UI
)

// Printer returns the output printer for the current invocation.
func Printer() *output.Printer {
	p := output.New(flagJSON, flagQuiet)
	if humanOutput && !flagJSON {
		p.JSON = false
	}
	p.Out, p.Err = stdout, stderr
	if liveTerm != nil && !p.Plain {
		p.Term = liveTerm
	}
	return p
}

// Client builds an API client, failing with an auth error when no key is set.
func Client() (*api.Client, error) {
	r, err := config.Resolve(flagAPIKey, flagBaseURL)
	if err != nil {
		return nil, err
	}
	if r.APIKey == "" {
		return nil, ErrNoKey
	}
	c := api.New(r.BaseURL, r.APIKey, flagTimeout)
	p := Printer()
	c.Notify = p.Info // retry waits; muted by --quiet
	if u := p.UI(); u.On() {
		// Every call shows a spinner once it has been outstanding for a moment.
		// A command that already runs a spinner (scrape, batch wait) keeps its
		// own label: only one spinner runs at a time.
		c.Wait = func(method, path string) func() {
			s := u.Spin("%s", waitLabel(method, path))
			return func() { s.Stop() }
		}
	}
	return c, nil
}

// waitLabel says what a bare API call is doing, for its spinner.
func waitLabel(method, path string) string {
	switch {
	case strings.HasPrefix(path, "/v1/requests"):
		return "Fetching the request log"
	case strings.HasPrefix(path, "/v1/usage"):
		return "Fetching usage"
	case strings.HasPrefix(path, "/v1/sessions"):
		if method == http.MethodPost {
			return "Creating a session"
		}
		return "Fetching sessions"
	case strings.HasPrefix(path, "/v1/batch"):
		return "Contacting the batch API"
	case strings.HasPrefix(path, "/v1/browser"):
		return "Requesting a browser URL"
	case strings.HasPrefix(path, "/v1/workers"):
		return "Checking the workers"
	}
	return "Waiting for the Spicrawl API"
}

// ErrNoKey is returned when no API key is configured anywhere.
var ErrNoKey = errors.New(`no API key: run "spicrawl login", set SPICRAWL_API_KEY, or pass --api-key`)

// UsageError marks a problem with flags or arguments (exit 2).
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// Usagef builds a UsageError.
func Usagef(format string, a ...any) error { return &UsageError{Msg: fmt.Sprintf(format, a...)} }

// ExitError carries an explicit exit code (e.g. exitcode.Pending).
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	// cobra copies the context to a subcommand only when it has none, so a
	// second Execute in one process (tests) would inherit the first run's
	// cancelled context. Set it on every command explicitly.
	setContextTree(rootCmd, ctx)
	rootCmd.InitDefaultCompletionCmd() // cobra adds it lazily; groups are fixed up below
	requireKnownSubcommand(rootCmd)
	err := rootCmd.ExecuteContext(ctx)
	ui.StopAll() // a spinner a command left running (Ctrl-C) must not outlive it
	if err == nil {
		return exitcode.OK
	}
	if ctx.Err() != nil { // Ctrl-C: whatever error the command made of it, say so briefly
		if p := Printer(); p.UI().On() {
			p.UI().Done(false, "interrupted")
		} else {
			p.Info("interrupted")
		}
		return exitcode.Interrupted
	}
	return report(err)
}

// requireKnownSubcommand makes a typo under a group command (batch, sessions,
// mcp, ...) a usage error. cobra prints help and exits 0 for a group with no
// Run, whatever follows it, and only suggests corrections for the root.
func requireKnownSubcommand(c *cobra.Command) {
	for _, sub := range c.Commands() {
		requireKnownSubcommand(sub)
	}
	if c.Parent() == nil || !c.HasSubCommands() || c.Runnable() {
		return
	}
	c.Args = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}
		msg := fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath())
		if cmd.SuggestionsMinimumDistance <= 0 {
			cmd.SuggestionsMinimumDistance = 2 // cobra's own default, which it only applies for the root
		}
		if s := cmd.SuggestionsFor(args[0]); len(s) > 0 {
			msg += "; did you mean " + strings.Join(s, " or ") + "?"
		}
		return Usagef("%s", msg)
	}
	c.RunE = func(cmd *cobra.Command, _ []string) error { return cmd.Help() }
	// Runnable would add a "spicrawl batch [flags]" line to the help; keep it as before.
	c.SetUsageTemplate(strings.Replace(c.UsageTemplate(), "{{if .Runnable}}", "{{if and .Runnable (not .HasAvailableSubCommands)}}", 1))
}

func setContextTree(c *cobra.Command, ctx context.Context) {
	c.SetContext(ctx)
	for _, sub := range c.Commands() {
		setContextTree(sub, ctx)
	}
}

func printExitCodes(w io.Writer) {
	fmt.Fprintln(w, "Exit codes (stable; scripts and agents may branch on them):")
	fmt.Fprintln(w)
	for _, e := range exitcode.Table {
		fmt.Fprintf(w, "  %-3d %-9s %s\n", e.Code, e.Name, e.Meaning)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "In JSON mode a failed command writes the API's problem document to stderr.")
}

// ExitCode returns the exit code report would use for err. Commands that
// report per-item failures (e.g. scrape with -) can use it to stay in step
// with the process exit code.
func ExitCode(err error) int {
	if err == nil {
		return exitcode.OK
	}
	if prob, ok := api.AsProblem(err); ok {
		return exitcode.ForProblem(prob.Code, prob.Retryable)
	}
	var (
		ue *UsageError
		ne *api.NetworkError
		te *api.TimeoutError
		ce *api.ConnectionLostError
		re *api.RedirectError
		fe *config.FileError
		ee *ExitError
	)
	switch {
	case errors.As(err, &ee):
		return ee.Code
	case errors.As(err, &ue), errors.As(err, &fe), errors.As(err, &re):
		return exitcode.Usage
	case errors.Is(err, ErrNoKey):
		return exitcode.Auth
	case errors.As(err, &te), errors.As(err, &ce):
		return exitcode.Timeout
	case errors.As(err, &ne):
		return exitcode.Network
	case isCobraUsage(err):
		return exitcode.Usage
	}
	return exitcode.Internal
}

// report prints err to stderr in the active format and returns its exit code.
func report(err error) int {
	p := Printer()
	if prob, ok := api.AsProblem(err); ok {
		if p.JSON {
			p.Out = p.Err
			_ = p.Value(prob)
		} else if u := p.UI(); u.On() {
			u.Fail(output.PublicText(prob.Error()), append(problemRows(prob), failRows...)...)
		} else {
			fmt.Fprintf(stderr, "error: %s\n", output.PublicText(prob.Error()))
			if h := output.PublicText(prob.Hint()); h != "" {
				fmt.Fprintf(stderr, "hint: %s\n", h)
			}
			if prob.TargetStatus != nil {
				fmt.Fprintf(stderr, "target status: %d\n", *prob.TargetStatus)
			}
			if prob.Retryable {
				fmt.Fprintln(stderr, "retryable: yes")
			}
			if prob.RequestID != "" {
				fmt.Fprintf(stderr, "request id: %s (spicrawl logs get %s)\n", prob.RequestID, prob.RequestID)
			}
		}
		return ExitCode(err)
	}

	code := ExitCode(err)
	if p.JSON {
		p.Out = p.Err
		_ = p.Value(map[string]any{"error": err.Error(), "exit_code": code})
	} else if u := p.UI(); u.On() {
		u.Fail(err.Error(), failRows...)
	} else {
		fmt.Fprintf(stderr, "error: %s\n", err)
	}
	return code
}

// failRows are extra facts a command wants under its error block on a
// terminal (the URL it was on, how long it ran). One command runs per process.
var failRows []ui.Row

func failContext(rows ...ui.Row) { failRows = append(failRows, rows...) }

// problemRows are the facts under an API error's headline on a terminal: the
// same ones the plain output lists, the ERR:: code stays in the headline.
func problemRows(prob *api.Problem) []ui.Row {
	rows := []ui.Row{{Key: "hint", Val: output.PublicText(prob.Hint())}}
	if prob.TargetStatus != nil {
		rows = append(rows, ui.Row{Key: "target", Val: fmt.Sprintf("returned %d", *prob.TargetStatus)})
	}
	if prob.Retryable {
		rows = append(rows, ui.Row{Key: "retryable", Val: "yes"})
	}
	if prob.RequestID != "" {
		rows = append(rows, ui.Row{Key: "request", Val: fmt.Sprintf("%s (spicrawl logs get %s)", prob.RequestID, prob.RequestID)})
	}
	return rows
}

// isCobraUsage recognises cobra's own flag/arg errors, which are plain errors.
func isCobraUsage(err error) bool {
	msg := err.Error()
	for _, prefix := range []string{"unknown command", "unknown flag", "unknown shorthand", "invalid argument", "accepts ", "requires at least", "requires at most", "flag needs an argument", "required flag"} {
		if len(msg) >= len(prefix) && msg[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
