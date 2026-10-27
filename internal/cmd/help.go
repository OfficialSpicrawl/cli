package cmd

// The --help a person sees on a terminal. Cobra's own help is plain and
// alphabetical; here, when stdout is a live terminal (ui.Live: a TTY, and no
// NO_COLOR, TERM=dumb or CI), the root's commands are grouped by what they are
// for, headings and the names you type are coloured, and long descriptions
// wrap under their own column. Everywhere else, piped, redirected, in CI, for
// an agent, the help is cobra's, byte for byte.
//
// It is installed with SetHelpFunc from this init, so no command file changes:
// every command's help (spicrawl x --help, spicrawl help x, a bare group) goes
// through the root's help func.

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/OfficialSpicrawl/cli/internal/ui"
)

const helpDocsURL = "https://docs.spicrawl.com"

// helpMaxWidth caps the line length: a 250-column terminal does not get 250
// columns of prose.
const helpMaxWidth = 100

func init() {
	plain := rootCmd.HelpFunc() // cobra's own, kept for everything that is not a live terminal
	rootCmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		if u := helpUI(c.OutOrStdout()); u.On() {
			io.WriteString(c.OutOrStdout(), helpRender(u, c))
			return
		}
		plain(c, args)
	})
}

// helpUI is the live terminal for a help written to w: on only when w is a
// terminal and nothing asked for plain output: NO_COLOR, TERM=dumb, CI (see
// ui.Live), and --json or --quiet, which promise no escape codes at all. Tests
// force it.
func helpUI(w io.Writer) *ui.UI {
	switch {
	case flagJSON || flagQuiet:
		return nil
	case viewForce:
		return ui.Force(w)
	}
	return ui.New(w)
}

// helpGroups orders the root's commands. A command in none of them lands in a
// last "More commands" group, so a new command shows up without touching this.
var helpGroups = []struct {
	title string
	names []string
}{
	{"Extract data", []string{"scrape", "batch", "sessions", "browser"}},
	{"Set up", []string{"login", "logout", "auth", "init", "mcp", "skill", "config"}},
	{"Monitor", []string{"status", "logs", "usage"}},
	{"Reference", []string{"docs", "schema", "exit-codes", "version", "completion", "help"}},
}

// helpRootExamples are the first things to try. Every line parses (see
// TestHelpRootExamplesAreRealCommands), so they cannot rot.
const helpRootExamples = `  # Save your API key once
  spicrawl login

  # A page as clean markdown
  spicrawl scrape https://example.com --format markdown

  # A JavaScript-rendered page, waiting for the data to appear
  spicrawl scrape https://example.com/pricing --render --wait-for '.plans' --format markdown

  # Many URLs as one job, followed until it finishes
  spicrawl batch submit urls.txt --render --wait

  # Is everything reachable, and does my key work?
  spicrawl status`

// apiRoute matches the "(GET /v1/requests)" some Short texts end with: useful
// to a script author, noise to a reader, so the screen mutes it.
var apiRoute = regexp.MustCompile(`\s*\(((?:GET|POST|PUT|PATCH|DELETE) /v1/[^)]*)\)\s*$`)

func helpRender(u *ui.UI, c *cobra.Command) string {
	width := helpMaxWidth
	if cols := u.Cols(); cols > 0 {
		width = min(cols-1, helpMaxWidth) // one short of the row: see ui.Fail
	}
	var b strings.Builder
	section := func(title string) { b.WriteString("\n" + u.Bold(strings.ToUpper(title)) + "\n") }

	if text := strings.TrimRight(firstNonEmpty(c.Long, c.Short), " \n"); text != "" {
		b.WriteString(text + "\n")
	}

	section("Usage")
	switch {
	case c.HasAvailableSubCommands():
		fmt.Fprintf(&b, "  %s %s\n", c.CommandPath(), u.Dim("[command]"))
	case c.Runnable():
		b.WriteString("  " + c.UseLine() + "\n")
	}
	if len(c.Aliases) > 0 {
		fmt.Fprintf(&b, "  %s %s\n", u.Dim("aliases:"), strings.Join(c.Aliases, ", "))
	}

	var subs []*cobra.Command
	for _, s := range c.Commands() {
		if s.IsAvailableCommand() || s.Name() == "help" {
			subs = append(subs, s)
		}
	}
	pad := 0 // one name column for every group, so they line up
	for _, s := range subs {
		pad = max(pad, ui.Width(s.Name()))
	}
	if c.Parent() == nil {
		placed := map[string]bool{}
		for _, g := range helpGroups {
			var in []*cobra.Command
			for _, n := range g.names {
				for _, s := range subs {
					if s.Name() == n {
						in = append(in, s)
						placed[n] = true
					}
				}
			}
			helpCommands(&b, u, section, g.title, in, pad, width)
		}
		var rest []*cobra.Command
		for _, s := range subs {
			if !placed[s.Name()] {
				rest = append(rest, s)
			}
		}
		helpCommands(&b, u, section, "More commands", rest, pad, width)
	} else {
		helpCommands(&b, u, section, "Commands", subs, pad, width)
	}

	ex := c.Example
	if c.Parent() == nil {
		ex = helpRootExamples
	}
	if ex != "" {
		section("Examples")
		for _, l := range strings.Split(strings.TrimRight(ex, "\n"), "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "#") {
				l = u.Dim(l)
			}
			b.WriteString(l + "\n")
		}
	}

	if s := helpFlags(u, c.LocalFlags(), width); s != "" {
		section("Flags")
		b.WriteString(s)
	}
	if s := helpFlags(u, c.InheritedFlags(), width); s != "" {
		section("Global flags")
		b.WriteString(s)
	}

	if c.Parent() == nil {
		section("Learn more")
		b.WriteString("  " + u.Dim("Docs") + "          " + u.Link(helpDocsURL, helpDocsURL) + "\n")
		b.WriteString("  " + u.Dim("Exit codes") + "    spicrawl exit-codes\n")
	}
	if c.HasAvailableSubCommands() {
		fmt.Fprintf(&b, "\nUse %s for more information about a command.\n",
			u.Accent(fmt.Sprintf("%q", c.CommandPath()+" [command] --help")))
	}
	return b.String()
}

// helpCommands writes one titled list of commands: names in the accent colour,
// descriptions wrapped under their own column.
func helpCommands(b *strings.Builder, u *ui.UI, section func(string), title string, cmds []*cobra.Command, pad, width int) {
	if len(cmds) == 0 {
		return
	}
	section(title)
	for _, s := range cmds {
		short, route := s.Short, ""
		if m := apiRoute.FindStringSubmatchIndex(short); m != nil {
			short, route = short[:m[0]], short[m[2]:m[3]]
		}
		room := width - 2 - pad - 2
		lines := ui.Wrap(short, room)
		if route != "" {
			r := "(" + route + ")"
			if last := len(lines) - 1; ui.Width(lines[last])+1+ui.Width(r) <= room {
				lines[last] += " " + u.Dim(r)
			} else {
				lines = append(lines, u.Dim(r))
			}
		}
		for i, l := range lines {
			name := strings.Repeat(" ", pad)
			if i == 0 {
				name = u.Accent(s.Name()) + strings.Repeat(" ", pad-ui.Width(s.Name()))
			}
			b.WriteString("  " + name + "  " + l + "\n")
		}
	}
}

// helpFlags lays out a flag set as a table: "-q, --quiet", a muted value type,
// then the description wrapped under its own column with a muted default.
func helpFlags(u *ui.UI, fs *pflag.FlagSet, width int) string {
	type row struct{ plain, left, desc string }
	var rows []row
	left := 0
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		name, usage := pflag.UnquoteUsage(f)
		plain, styled := "    --"+f.Name, "    "+u.Accent("--"+f.Name)
		if f.Shorthand != "" {
			plain, styled = "-"+f.Shorthand+", --"+f.Name, u.Accent("-"+f.Shorthand)+", "+u.Accent("--"+f.Name)
		}
		if name != "" {
			plain += " " + name
			styled += " " + u.Dim(name)
		}
		if !zeroDefault(f) {
			d := f.DefValue
			if f.Value.Type() == "string" {
				d = fmt.Sprintf("%q", d)
			}
			// Joined with a private-use rune, which Wrap does not break at and
			// measures as one cell; swapped back for a space below.
			usage += " " + strings.ReplaceAll("(default "+d+")", " ", "\ue000")
		}
		rows = append(rows, row{plain, styled, usage})
		left = max(left, ui.Width(plain))
	})
	if len(rows) == 0 {
		return ""
	}
	// A very long flag name must not squeeze every description into a sliver.
	left = min(left, 34)
	room := width - 2 - left - 2
	var b strings.Builder
	for _, r := range rows {
		if room < 24 || ui.Width(r.plain) > left { // narrow terminal or long name: description below
			b.WriteString("  " + r.left + "\n")
			for _, l := range ui.Wrap(r.desc, max(width-8, 20)) {
				b.WriteString("        " + strings.ReplaceAll(l, "\ue000", " ") + "\n")
			}
			continue
		}
		for i, l := range ui.Wrap(r.desc, room) {
			l = strings.ReplaceAll(l, "\ue000", " ")
			lead := strings.Repeat(" ", left)
			if i == 0 {
				lead = r.left + strings.Repeat(" ", left-ui.Width(r.plain))
			}
			b.WriteString("  " + lead + "  " + helpMuteDefault(u, l) + "\n")
		}
	}
	return b.String()
}

// helpMuteDefault mutes a trailing "(default x)", which is never split across
// lines (see helpFlags).
func helpMuteDefault(u *ui.UI, l string) string {
	if i := strings.LastIndex(l, "(default "); i >= 0 && strings.HasSuffix(l, ")") {
		return l[:i] + u.Dim(l[i:])
	}
	return l
}

// zeroDefault mirrors pflag: a flag whose default is its type's zero value
// shows no "(default ...)".
func zeroDefault(f *pflag.Flag) bool {
	switch f.Value.Type() {
	case "bool":
		return f.DefValue == "false"
	case "duration":
		return f.DefValue == "0" || f.DefValue == "0s"
	case "int", "int8", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "count", "float32", "float64":
		return f.DefValue == "0"
	case "string":
		return f.DefValue == ""
	case "stringSlice", "stringArray", "intSlice":
		return f.DefValue == "[]"
	}
	switch f.Value.String() {
	case "false", "<nil>", "", "0":
		return true
	}
	return false
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
