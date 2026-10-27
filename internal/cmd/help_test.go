package cmd

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/OfficialSpicrawl/cli/internal/ui"
)

// helpOut runs `spicrawl args... --help` into a buffer, as cobra would write
// it to stdout.
func helpOut(t *testing.T, args ...string) string {
	t.Helper()
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs(append(args, "--help"))
	defer func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		resetFlags(rootCmd)
	}()
	if code := Execute(); code != 0 {
		t.Fatalf("--help exited %d: %s", code, buf.String())
	}
	return buf.String()
}

func TestHelpIsCobrasOwnUnlessLive(t *testing.T) {
	for _, args := range [][]string{nil, {"scrape"}, {"batch"}, {"batch", "submit"}} {
		out := helpOut(t, args...)
		noEscapes(t, "piped help", out)
		for _, want := range []string{"Usage:", "Flags:"} {
			if !strings.Contains(out, want) {
				t.Errorf("%v: piped help lost cobra's %q:\n%s", args, want, out)
			}
		}
		if strings.Contains(out, "USAGE") || strings.Contains(out, "EXTRACT DATA") {
			t.Errorf("%v: the terminal layout leaked into piped help:\n%s", args, out)
		}
	}
	root := helpOut(t)
	if !strings.Contains(root, "Available Commands:\n  auth ") || !strings.Contains(root, "\nUse \"spicrawl [command] --help\" for more information about a command.\n") {
		t.Errorf("piped root help is not cobra's:\n%s", root)
	}

	viewForce = true
	defer func() { viewForce = false }()
	live := helpOut(t)
	if !strings.Contains(live, "\x1b[") || !strings.Contains(stripANSI(live), "EXTRACT DATA") {
		t.Errorf("forced live help is not the terminal layout:\n%s", live)
	}
	// --json and --quiet promise no escape codes, even on a terminal.
	for _, flag := range []string{"--json", "--quiet"} {
		out := helpOut(t, flag)
		noEscapes(t, flag+" help", out)
		if !strings.Contains(out, "Available Commands:") {
			t.Errorf("%s: help is not cobra's:\n%s", flag, out)
		}
	}
}

func TestHelpLiveRootListsEveryCommandOnceInItsGroup(t *testing.T) {
	rootCmd.InitDefaultHelpCmd()
	rootCmd.InitDefaultCompletionCmd()
	out := stripANSI(helpRender(ui.Force(&bytes.Buffer{}), rootCmd))
	noEscapes(t, "stripped help", out)

	for _, sub := range rootCmd.Commands() {
		if !sub.IsAvailableCommand() && sub.Name() != "help" {
			continue
		}
		n := len(regexp.MustCompile(`(?m)^  `+regexp.QuoteMeta(sub.Name())+` {2,}\S`).FindAllString(out, -1))
		if n != 1 {
			t.Errorf("command %q is listed %d times in the live help:\n%s", sub.Name(), n, out)
		}
	}
	last := -1
	for _, h := range []string{"USAGE", "EXTRACT DATA", "SET UP", "MONITOR", "REFERENCE", "EXAMPLES", "FLAGS", "LEARN MORE"} {
		i := strings.Index(out, "\n"+h+"\n")
		if i < 0 || i < last {
			t.Fatalf("section %q missing or out of order (at %d, previous at %d):\n%s", h, i, last, out)
		}
		last = i
	}
	if !strings.Contains(out, "https://docs.spicrawl.com") {
		t.Error("the live help does not point to the docs")
	}
	if m := internalNames.FindString(out); m != "" {
		t.Errorf("the live help names an internal engine (%q):\n%s", m, out)
	}
	// One name column for every group.
	cols := map[int]bool{}
	for _, l := range strings.Split(out, "\n") {
		if m := regexp.MustCompile(`^  (scrape|login|status|docs|completion)( +)`).FindStringSubmatch(l); m != nil {
			cols[len(m[0])] = true
		}
	}
	if len(cols) != 1 {
		t.Errorf("groups do not share a name column: %v", cols)
	}
}

func TestHelpGroupsNameRealCommands(t *testing.T) {
	rootCmd.InitDefaultHelpCmd()
	rootCmd.InitDefaultCompletionCmd()
	have := map[string]bool{}
	for _, c := range rootCmd.Commands() {
		have[c.Name()] = true
	}
	seen := map[string]bool{}
	for _, g := range helpGroups {
		for _, n := range g.names {
			if !have[n] {
				t.Errorf("help group %q lists %q, which is not a command: update helpGroups in help.go", g.title, n)
			}
			if seen[n] {
				t.Errorf("%q is in two help groups", n)
			}
			seen[n] = true
		}
	}
}

// The examples on the root help are typed by people; every one must parse.
func TestHelpRootExamplesAreRealCommands(t *testing.T) {
	n := 0
	for _, line := range strings.Split(helpRootExamples, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		args := splitShell(line)
		if args[0] != "spicrawl" {
			t.Errorf("example does not start with the program name: %q", line)
			continue
		}
		c, rest, err := rootCmd.Find(args[1:])
		if err != nil || c == rootCmd {
			t.Errorf("example %q names no command: %v", line, err)
			continue
		}
		if err := c.ParseFlags(rest); err != nil {
			t.Errorf("example %q does not parse: %v", line, err)
		}
		resetFlags(rootCmd)
		n++
	}
	if n < 4 {
		t.Errorf("only %d examples checked", n)
	}
}

// splitShell splits on spaces, keeping single-quoted text together.
func splitShell(s string) []string {
	var out []string
	var cur strings.Builder
	quoted := false
	for _, r := range s {
		switch {
		case r == '\'':
			quoted = !quoted
		case r == ' ' && !quoted:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func TestHelpFlagsWrapUnderTheirColumnAndMuteDefaults(t *testing.T) {
	c := &cobra.Command{Use: "x"}
	c.Flags().BoolP("quiet", "q", false, "suppress progress messages on stderr")
	c.Flags().String("base-url", "https://api.spicrawl.com", "API base URL, from the environment or the config file or this flag")
	c.Flags().Int("concurrency", 4, "with '-': parallel requests")
	c.Flags().Duration("timeout", 0, "HTTP timeout per API call")
	c.Flags().String("hidden", "", "never shown")
	_ = c.Flags().MarkHidden("hidden")

	u := ui.Force(&bytes.Buffer{})
	out := helpFlags(u, c.Flags(), 100)
	plain := stripANSI(out)
	if strings.Contains(plain, "hidden") {
		t.Errorf("a hidden flag is listed:\n%s", plain)
	}
	for _, want := range []*regexp.Regexp{
		regexp.MustCompile(`(?m)^  -q, --quiet {13}suppress progress messages on stderr$`),
		regexp.MustCompile(`\(default "https://api.spicrawl.com"\)`),
		regexp.MustCompile(`(?m)with '-': parallel requests \(default 4\)$`),
	} {
		if !want.MatchString(plain) {
			t.Errorf("flags lack %v:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "(default 0") || strings.Contains(plain, "(default false") || strings.Contains(plain, "\ue000") {
		t.Errorf("a zero default is shown, or a placeholder leaked:\n%s", plain)
	}
	if !strings.Contains(out, "\x1b[2m(default 4)\x1b[0m") {
		t.Errorf("the default is not muted: %q", out)
	}

	// Tight: lines wrap under the description column, and a "(default ...)" is
	// never split.
	tight := stripANSI(helpFlags(u, c.Flags(), 60))
	for _, l := range strings.Split(strings.TrimRight(tight, "\n"), "\n") {
		if ui.Width(l) > 60 && !strings.Contains(l, "https://") {
			t.Errorf("line %q is %d cells in a 60-cell help", l, ui.Width(l))
		}
	}
	for _, want := range []string{"(default 4)", `(default "https://api.spicrawl.com")`} {
		if !strings.Contains(tight, want) {
			t.Errorf("a default was split across lines (%q):\n%s", want, tight)
		}
	}

	// Narrow: the description drops below the flag instead of into a sliver.
	narrow := stripANSI(helpFlags(u, c.Flags(), 30))
	if !strings.Contains(narrow, "  -q, --quiet\n        suppress progress") {
		t.Errorf("narrow layout:\n%s", narrow)
	}
}

func TestHelpMutesTheAPIRouteAndKeepsIt(t *testing.T) {
	sub := &cobra.Command{Use: "logs", Short: "Recent requests in the key's project (GET /v1/requests)", Run: func(*cobra.Command, []string) {}}
	var b strings.Builder
	u := ui.Force(&bytes.Buffer{})
	helpCommands(&b, u, func(string) {}, "x", []*cobra.Command{sub}, 4, 100)
	if !strings.Contains(b.String(), "\x1b[2m(GET /v1/requests)\x1b[0m") {
		t.Errorf("route not muted: %q", b.String())
	}
	if got := stripANSI(b.String()); got != "  logs  Recent requests in the key's project (GET /v1/requests)\n" {
		t.Errorf("route lost or moved: %q", got)
	}
}

// The live layout reuses every command's own text, so the engine-name rule that
// locks the plain help (engine_names_test.go) must hold for it too, on every
// command.
func TestHelpLiveNeverNamesAnEngine(t *testing.T) {
	rootCmd.InitDefaultHelpCmd()
	rootCmd.InitDefaultCompletionCmd()
	u := ui.Force(&bytes.Buffer{})
	n := 0
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		out := stripANSI(helpRender(u, c))
		n++
		if m := internalNames.FindString(out); m != "" {
			t.Errorf("%s: the live help names an internal engine (%q)", c.CommandPath(), m)
		}
		if strings.Contains(out, "\x1b") {
			t.Errorf("%s: escape codes left after stripping", c.CommandPath())
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)
	if n < 30 {
		t.Errorf("only %d help screens walked", n)
	}
}
