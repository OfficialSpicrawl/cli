package cmd

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Spicrawl/cli/internal/api"
	"github.com/Spicrawl/cli/internal/config"
	"github.com/Spicrawl/cli/internal/output"
)

// skillEmbedded is the fallback skill. Its https://api.spicrawl.com,
// https://mcp.spicrawl.com and https://docs.spicrawl.com URLs are placeholders
// that skillRewrite replaces with the resolved deployment's URLs.
//
//go:embed assets/skill.md
var skillEmbedded []byte

var skillFlags struct {
	client   string
	global   bool
	print    bool
	agentsMD bool
	dir      string
	url      string
	mcpURL   string
}

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Install the Spicrawl agent skill (SKILL.md) for AI agent clients",
}

var skillInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Write the Spicrawl agent skill where each client looks for skills",
	Long: `Download the Spicrawl agent skill and write it as spicrawl/SKILL.md in each
client's skill directory. If the download fails, a copy built into the CLI is
used instead.

The skill URL is --skill-url, else $` + agentEnvSkillURL + `, else <docs URL>/skill.md.
The docs URL is $` + docsEnvURL + `, else the legacy $` + docsEnvHost + ` + /docs, else
derived from the resolved base URL (--base-url, $SPICRAWL_BASE_URL, config
file): ` + docsPublicURL + ` for the hosted API, <API origin>/docs for a
self-hosted one. API, docs and MCP URLs in the skill are rewritten to this
deployment's.

Skill directories (project scope; --global for your home directory):
  claude   .claude/skills/spicrawl/SKILL.md
  cursor   .cursor/skills/spicrawl/SKILL.md   (also reads .claude/ and .agents/ skills)
  vscode   .github/skills/spicrawl/SKILL.md   (~/.copilot/skills; also reads .claude/ and .agents/)
  codex    .agents/skills/spicrawl/SKILL.md

A client that already reads a skill written for another selected client is
skipped, so agents never see the skill twice. --agents-md also adds a short
Spicrawl section to AGENTS.md (global: $CODEX_HOME/AGENTS.md).`,
	Example: `  spicrawl skill install --client claude
  spicrawl skill install --client all --global
  spicrawl skill install --print > SKILL.md`,
	Args: cobra.NoArgs,
	RunE: runSkillInstall,
}

func init() {
	f := skillInstallCmd.Flags()
	f.StringVar(&skillFlags.client, "client", "", "claude, cursor, vscode, codex or all (comma-separated; default: detected clients)")
	f.BoolVar(&skillFlags.global, "global", false, "install into your home directory instead of the project")
	f.BoolVar(&skillFlags.print, "print", false, "print the skill instead of writing it")
	f.BoolVar(&skillFlags.agentsMD, "agents-md", false, "also add a Spicrawl section to AGENTS.md")
	f.StringVar(&skillFlags.dir, "dir", "", "project directory (default: current directory)")
	f.StringVar(&skillFlags.url, "skill-url", "", "skill URL (default: $"+agentEnvSkillURL+", then <docs URL>/skill.md)")
	f.StringVar(&skillFlags.mcpURL, "mcp-url", "", "MCP server URL named in the skill (default: $"+agentEnvMCPURL+", then <API origin>/mcp; "+agentPublicMCPURL+" for the hosted API)")
	skillCmd.AddCommand(skillInstallCmd)
	rootCmd.AddCommand(skillCmd)
}

func runSkillInstall(cmd *cobra.Command, _ []string) error {
	p := Printer()
	ep, err := agentResolveEndpoints(skillFlags.mcpURL, skillFlags.url)
	if err != nil {
		return err
	}
	content, source := skillFetch(cmd.Context(), p, ep)
	if skillFlags.print {
		if p.JSON {
			return p.Value(map[string]any{"source": source, "content": string(content)})
		}
		_, err := p.Out.Write(content)
		return err
	}
	s, err := agentResolveScope(skillFlags.dir, skillFlags.global)
	if err != nil {
		return err
	}
	clients, err := agentSelectClients(skillFlags.client, s)
	if err != nil {
		return err
	}
	rs, err := skillPlan(clients, s, content)
	if err != nil {
		return err
	}
	if skillFlags.agentsMD {
		r, err := skillPlanAgentsMD(s, ep)
		if err != nil {
			return err
		}
		rs = append(rs, r)
	}
	if err := agentApply(rs); err != nil {
		return err
	}
	return agentReport(p, rs, s)
}

// skillFetch downloads the skill from ep.Skill, falling back to the embedded
// copy, and points its URLs at this deployment. The source is the URL, or
// "embedded".
func skillFetch(ctx context.Context, p *output.Printer, ep agentEndpoints) ([]byte, string) {
	b, err := skillDownload(ctx, ep.Skill)
	if err != nil {
		p.Warn("could not download the skill from %s (%v); using the copy built into this CLI", ep.Skill, err)
		return skillRewrite(skillNormalize(skillEmbedded), ep), "embedded"
	}
	return skillRewrite(skillNormalize(b), ep), ep.Skill
}

// skillRewrite replaces the hosted-service URLs a skill may carry (the
// embedded copy always does; a docs site that does not know its MCP URL
// serves https://mcp.spicrawl.com/mcp) with the resolved deployment's.
//
// The hosted docs are at the root of docs.spicrawl.com. Their older forms, the
// /docs path there and on the API host, are rewritten too, and before the
// bare hosts: at each position the first matching pattern wins.
func skillRewrite(b []byte, ep agentEndpoints) []byte {
	ws := "ws" + strings.TrimPrefix(ep.API, "http") // https:// -> wss://, http:// -> ws://
	return []byte(strings.NewReplacer(
		"https://mcp.spicrawl.com/mcp", ep.MCP,
		"https://api.spicrawl.com/mcp", ep.MCP,
		"https://docs.spicrawl.com/docs", ep.Docs,
		"https://api.spicrawl.com/docs", ep.Docs,
		docsPublicURL, ep.Docs,
		"wss://api.spicrawl.com", ws,
		"https://api.spicrawl.com", ep.API,
	).Replace(string(b)))
}

func skillDownload(ctx context.Context, u string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/markdown, text/plain;q=0.9")
	req.Header.Set("User-Agent", "spicrawl-cli/"+api.Version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(bytes.TrimLeft(b, "\ufeff \t\r\n"), []byte("---")) || !bytes.Contains(b, []byte("\ndescription:")) {
		return nil, fmt.Errorf("response is not a SKILL.md (no frontmatter)")
	}
	return b, nil
}

var skillNameLine = regexp.MustCompile(`(?m)^name:.*$`)

// skillNormalize sets the frontmatter name to the directory name (the Agent
// Skills format requires them to match) and normalises line endings.
func skillNormalize(b []byte) []byte {
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	s = strings.TrimLeft(s, "\ufeff \t\n")
	// Only touch the frontmatter block.
	if strings.HasPrefix(s, "---\n") {
		if end := strings.Index(s[4:], "\n---"); end >= 0 {
			fm := s[:4+end]
			fm = skillNameLine.ReplaceAllLiteralString(fm, "name: "+agentSkillDirName)
			s = fm + s[4+end:]
		}
	}
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return []byte(s)
}

// skillPlan decides where each client's skill goes, writing each file once:
// a client that already reads a skill planned for another client is skipped,
// and an existing spicrawl skill in any directory a client reads is updated in
// place instead of duplicated.
func skillPlan(clients []*agentClient, s agentScope, content []byte) ([]*agentResult, error) {
	var rs []*agentResult
	planned := map[string]bool{}
	for _, c := range clients {
		r := &agentResult{Client: c.ID, Kind: "skill"}
		var target string
		for _, root := range c.skillRoots(s) {
			p := filepath.Join(root, agentSkillDirName, "SKILL.md")
			if planned[p] {
				r.File, r.Action, r.Reason = p, agentActionSkipped, "already reads this skill"
				break
			}
			if target == "" && agentExists(p) {
				target = p
			}
		}
		if r.Action == agentActionSkipped {
			rs = append(rs, r)
			continue
		}
		if target == "" {
			target = c.skillPath(s)
		}
		r.File = target
		planned[target] = true
		if err := agentPlanFile(r, content, 0o644); err != nil {
			return nil, err
		}
		rs = append(rs, r)
	}
	return rs, nil
}

const (
	skillAgentsBegin = "<!-- spicrawl:begin -->"
	skillAgentsEnd   = "<!-- spicrawl:end -->"
)

func skillAgentsSection(skillURL string) string {
	return skillAgentsBegin + `
## Web data (Spicrawl)

To fetch, render or extract data from web pages, use Spicrawl instead of ad-hoc
HTTP requests: the ` + "`spicrawl`" + ` MCP server (tools ` + "`spicrawl_*`" + `) or the CLI
(` + "`spicrawl scrape <url> --format markdown`" + `, ` + "`spicrawl batch submit urls.txt`" + `).
The API key is read from ` + "`$" + config.EnvAPIKey + "`" + `; never print or commit it.
Start cheap (plain fetch), then add ` + "`--render`" + `, ` + "`--premium-proxy`" + `, ` + "`--stealth`" + `
only when the page needs it. Full guide: the ` + "`spicrawl`" + ` skill, or ` + skillURL + `.
` + skillAgentsEnd + "\n"
}

// skillPlanAgentsMD adds or refreshes the marked Spicrawl section in AGENTS.md.
func skillPlanAgentsMD(s agentScope, ep agentEndpoints) (*agentResult, error) {
	path := filepath.Join(s.Dir, "AGENTS.md")
	if s.Global {
		path = filepath.Join(agentCodexHome(s), "AGENTS.md")
	}
	r := &agentResult{Client: "codex", Kind: "agents_md", File: path}
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	section := skillAgentsSection(ep.Skill)
	text := string(old)
	var out string
	if i := strings.Index(text, skillAgentsBegin); i >= 0 {
		j := strings.Index(text[i:], skillAgentsEnd)
		if j < 0 {
			return nil, fmt.Errorf("%s has %q without %q; fix it by hand", path, skillAgentsBegin, skillAgentsEnd)
		}
		rest := strings.TrimPrefix(text[i+j+len(skillAgentsEnd):], "\n")
		out = text[:i] + section + rest
	} else {
		body := strings.TrimRight(text, "\n")
		if body != "" {
			body += "\n\n"
		}
		out = body + section
	}
	if err := agentPlanFile(r, []byte(out), 0o644); err != nil {
		return nil, err
	}
	return r, nil
}
