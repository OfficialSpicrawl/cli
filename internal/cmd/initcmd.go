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
)

var initFlags struct {
	client   string
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
--help" for how the API key is referenced in each client's config.`,
	Example: `  spicrawl init
  spicrawl init --client claude,cursor --yes
  spicrawl init --client all --global --yes`,
	Args: cobra.NoArgs,
	RunE: runInit,
}

func init() {
	f := initCmd.Flags()
	f.StringVar(&initFlags.client, "client", "", "claude, cursor, vscode, codex or all (comma-separated; default: detected clients)")
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
	clients, err := agentSelectClients(initFlags.client, s)
	if err != nil {
		return err
	}
	names := make([]string, len(clients))
	for i, c := range clients {
		names[i] = c.Name
	}
	p.Info("clients: %s", strings.Join(names, ", "))
	p.Info("MCP server: %s", ep.MCP)

	key, _ := agentAPIKey()
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
		initPrintPlan(p.Err, rs, s)
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
	if key == "" {
		p.Warn("no API key is configured: run \"spicrawl login\" or set %s", config.EnvAPIKey)
	}
	return nil
}

func initPrintPlan(w io.Writer, rs []*agentResult, s agentScope) {
	fmt.Fprintln(w, "plan:")
	for _, r := range rs {
		verb := map[string]string{
			agentActionCreated:  "create",
			agentActionUpdated:  "update",
			agentActionUnchange: "keep  ",
			agentActionSkipped:  "skip  ",
		}[r.Action]
		line := fmt.Sprintf("  %s  %-7s %-6s %s", verb, r.Client, r.Kind, agentDisplayPath(r.File, s))
		if r.Reason != "" {
			line += "  (" + r.Reason + ")"
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
