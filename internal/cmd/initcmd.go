package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/OfficialSpicrawl/cli/internal/config"
	"github.com/OfficialSpicrawl/cli/internal/output"
	"github.com/OfficialSpicrawl/cli/internal/ui"
)

var initFlags struct {
	client   string
	all      bool
	yes      bool
	global   bool
	url      string
	skillURL string
	useEnv   bool
	agentsMD bool
	dir      string
	force    bool
}

// Swapped out by tests.
var (
	initStdinIsTerminal           = output.IsStdinTerminal
	initStdin           io.Reader = os.Stdin
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Set up AI agent clients: MCP server config and the Spicrawl skill",
	Long: `Detect the AI agent clients in use (Claude Code, Cursor, VS Code, Codex) in
this directory and your home directory, show the files that would be created
or changed, and after confirmation run "spicrawl mcp install" and "spicrawl skill
install" for each. The MCP URL defaults to <API origin>/mcp and the skill to
<API origin>/docs/skill.md, derived from the resolved base URL (--base-url,
$SPICRAWL_BASE_URL, config file), so self-hosted deployments need no extra flags.
With the default base URL (the hosted API) the MCP URL is ` + agentPublicMCPURL + `
and the skill ` + docsPublicURL + agentSkillFile + `. $` + docsEnvURL + ` sets the
docs URL the skill is read from.

Changed files are copied to <file>.bak first and symlinked configs are written
through. An existing "spicrawl" MCP entry that differs from the one to write is
not replaced unless you pass --force.

Without a terminal on stdin, --yes is required. See "spicrawl mcp install
--help" for how the API key is referenced in each client's config.

Unattended, in one command: "spicrawl init --all --yes --api-key <key>". --all
connects every detected client (and conflicts with --client); --yes skips the
confirmation. --api-key is checked with the API and saved to the config file as
"spicrawl login" does, before any client file is written; a rejected key writes
nothing. The key is never printed, only masked (spicrawl_live_ab12…cd34).
$SPICRAWL_API_KEY is used as before (referenced by the client configs) but is
neither checked nor saved here. A short report follows on stderr: connected and
skipped clients, the key, and the next step. The results stay on stdout, as
JSON when it is not a terminal.`,
	Example: `  spicrawl init
  spicrawl init --client claude,cursor --yes
  spicrawl init --client all --global --yes
  spicrawl init --all --yes --api-key spicrawl_live_...`,
	Args: cobra.NoArgs,
	RunE: runInit,
}

func init() {
	f := initCmd.Flags()
	f.StringVar(&initFlags.client, "client", "", "claude, cursor, vscode, codex or all (comma-separated; default: detected clients)")
	f.BoolVar(&initFlags.all, "all", false, "connect every detected client (the default selection, stated outright; conflicts with --client)")
	f.BoolVarP(&initFlags.yes, "yes", "y", false, "apply without asking")
	f.BoolVar(&initFlags.global, "global", false, "configure your user-level client settings instead of the project")
	f.StringVar(&initFlags.url, "mcp-url", "", "MCP server URL (default: $"+agentEnvMCPURL+", then <API origin>/mcp from the base URL; "+agentPublicMCPURL+" for the hosted API)")
	f.StringVar(&initFlags.skillURL, "skill-url", "", "skill URL (default: $"+agentEnvSkillURL+", then <docs URL>/skill.md)")
	f.BoolVar(&initFlags.useEnv, "use-env", true, "reference $SPICRAWL_API_KEY instead of embedding the key, where the client supports it")
	f.BoolVar(&initFlags.agentsMD, "agents-md", false, "also add a Spicrawl section to AGENTS.md")
	f.StringVar(&initFlags.dir, "dir", "", "project directory (default: current directory)")
	f.BoolVar(&initFlags.force, "force", false, "replace an existing spicrawl MCP entry that differs from the one to write")
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, _ []string) error {
	p := Printer()
	ep, err := agentResolveEndpoints(initFlags.url, initFlags.skillURL)
	if err != nil {
		return err
	}
	s, err := agentResolveScope(initFlags.dir, initFlags.global)
	if err != nil {
		return err
	}
	if initFlags.all && initFlags.client != "" {
		return Usagef("--all and --client are mutually exclusive: --all connects every detected client")
	}
	clients, err := agentSelectClients(initFlags.client, s)
	if err != nil {
		return err
	}
	names := make([]string, len(clients))
	for i, c := range clients {
		names[i] = c.Name
	}
	if !viewIntro(p, "Setting up Spicrawl",
		ui.Row{Key: "clients", Val: strings.Join(names, ", ")},
		ui.Row{Key: "MCP server", Val: ep.MCP},
	) {
		p.Info("clients: %s", strings.Join(names, ", "))
		p.Info("MCP server: %s", ep.MCP)
	}

	key, keySource := agentAPIKey()
	keyLine := "none configured"
	if key != "" {
		keyLine = config.MaskKey(key) + " (from " + keySource + ")"
	}
	if keySource == "flag" {
		// Check it and save it before writing any client file: a bad key must not leave a half-done setup.
		r, err := authSaveKey(p, key)
		if err != nil {
			return err
		}
		keyLine = config.MaskKey(key) + " (checked, saved to " + r.Path + ")"
	}
	opt := mcpOptions{URL: ep.MCP, UseEnv: initFlags.useEnv, Key: key, Force: initFlags.force}

	var rs []*agentResult
	for _, c := range clients {
		r, err := mcpPlan(c, s, opt)
		if err != nil {
			return err
		}
		rs = append(rs, r)
	}
	content, _ := skillFetch(cmd.Context(), p, ep)
	srs, err := skillPlan(clients, s, content)
	if err != nil {
		return err
	}
	rs = append(rs, srs...)
	if initFlags.agentsMD {
		r, err := skillPlanAgentsMD(s, ep)
		if err != nil {
			return err
		}
		rs = append(rs, r)
	}

	pending := 0
	for _, r := range rs {
		if r.Action == agentActionCreated || r.Action == agentActionUpdated {
			pending++
		}
	}
	if pending > 0 && !initFlags.yes {
		initPrintPlan(p.Err, errUI(p), rs, s)
		if !initStdinIsTerminal() {
			return Usagef("init changes files and stdin is not a terminal: re-run with --yes to apply the plan above")
		}
		ok, err := initConfirm(p.Err, initStdin)
		if err != nil {
			return err
		}
		if !ok {
			for _, r := range rs {
				if r.Action == agentActionCreated || r.Action == agentActionUpdated {
					r.Action, r.Reason, r.Secret = agentActionSkipped, "not confirmed", false
				}
			}
			p.Info("nothing written")
			return agentReport(p, rs, s)
		}
	}

	if err := agentApply(rs); err != nil {
		return err
	}
	if err := agentReport(p, rs, s); err != nil {
		return err
	}
	mcpEnvHint(p, rs)
	initPrintReport(p, rs, keyLine, key == "")
	return nil
}

// initPrintReport is the short report after a run: the clients connected and
// skipped (with why), the key (masked) and the next step. It goes to stderr,
// as plain "name: value" lines unless a terminal gets the live view; stdout
// stays the result list. noKey: no API key is configured anywhere.
func initPrintReport(p *output.Printer, rs []*agentResult, keyLine string, noKey bool) {
	var connected, skipped []string
	for _, c := range agentClients {
		var mcp *agentResult
		for _, r := range rs {
			if r.Client == c.ID && r.Kind == "mcp" {
				mcp = r
			}
		}
		switch {
		case mcp == nil && initFlags.client == "":
			skipped = append(skipped, c.Name+" (not detected)")
		case mcp == nil:
			skipped = append(skipped, c.Name+" (not selected)")
		case mcp.Action == agentActionSkipped:
			skipped = append(skipped, c.Name+" ("+mcp.Reason+")")
		default:
			connected = append(connected, c.Name)
		}
	}
	list := func(l []string) string {
		if len(l) == 0 {
			return "none"
		}
		return strings.Join(l, ", ")
	}
	if !viewIntro(p, "Result",
		ui.Row{Key: "connected", Val: list(connected)},
		ui.Row{Key: "skipped", Val: list(skipped)},
		ui.Row{Key: "api key", Val: keyLine},
	) {
		p.Info("connected: %s", list(connected))
		p.Info("skipped: %s", list(skipped))
		p.Info("api key: %s", keyLine)
	}

	cli := cliInvocation()
	var steps []string
	if noKey {
		steps = append(steps, cli+" login --api-key <YOUR_API_KEY> (no API key is configured)")
	}
	if env := mcpEnvClients(rs); len(env) > 0 {
		steps = append(steps, "export "+config.EnvAPIKey+"=<YOUR_API_KEY> in the shell or profile that starts "+strings.Join(env, ", ")+": their MCP config reads the key from it")
	}
	names := agentChanged(rs)
	if len(names) > 0 {
		steps = append(steps, "restart "+strings.Join(names, ", ")+" to load the MCP server and the skill")
	}
	steps = append(steps, "run "+cli+" status, then ask your agent to scrape a page (or try: "+cli+" scrape https://example.com)")
	if len(names) > 0 || noKey { // on a terminal, an unchanged re-run needs no list
		viewNext(p, steps...)
	}
	if !errUI(p).On() {
		for _, st := range steps {
			p.Info("next: %s", st)
		}
	}
}

func initPrintPlan(w io.Writer, u *ui.UI, rs []*agentResult, s agentScope) {
	fmt.Fprintln(w, u.Bold("plan:"))
	for _, r := range rs {
		verb := map[string]string{
			agentActionCreated:  "create",
			agentActionUpdated:  "update",
			agentActionUnchange: "keep  ",
			agentActionSkipped:  "skip  ",
		}[r.Action]
		switch r.Action {
		case agentActionCreated, agentActionUpdated:
			verb = u.Green(verb)
		default:
			verb = u.Dim(verb)
		}
		line := fmt.Sprintf("  %s  %-7s %-6s %s", verb, r.Client, r.Kind, agentDisplayPath(r.File, s))
		if r.Reason != "" {
			line += "  " + u.Dim("("+r.Reason+")")
		}
		fmt.Fprintln(w, line)
	}
}

func initConfirm(w io.Writer, r io.Reader) (bool, error) {
	fmt.Fprint(w, "apply these changes? [y/N] ")
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(w)
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}
