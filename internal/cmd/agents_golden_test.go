package cmd

import (
	"net/http"
	"testing"
)

// The agent commands (init, mcp install, skill install) write config files and
// print what they did. Their base URL is an unreachable address so the MCP and
// docs URLs they write are fixed; the skill comes from a fake docs server.
const (
	gAgentBase  = "http://127.0.0.1:1"
	gAgentSkill = "---\nname: spicrawl-web-data-api\ndescription: golden test skill\n---\n\n# Spicrawl\n\nUse https://api.spicrawl.com for scraping.\n"
)

func gSkillAPI() func() http.HandlerFunc {
	return goldenServe(map[string]http.HandlerFunc{"GET /skill.md": func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte(gAgentSkill))
	}})
}

func init() {
	env := map[string]string{agentEnvSkillURL: "{server}/skill.md"}
	// agent builds a case that runs in a fresh project directory.
	agent := func(name string, human bool, files []string, setup func(t *testing.T, e *goldenEnv), args ...string) goldenCase {
		return goldenCase{name: name, human: human, args: args, baseURL: gAgentBase, env: env, server: gSkillAPI(), files: files, setup: setup}
	}
	allMCP := []string{".mcp.json", ".cursor/mcp.json", ".vscode/mcp.json", ".codex/config.toml"}
	existing := func(t *testing.T, e *goldenEnv) {
		goldenWrite(t, e.Dir, ".mcp.json", `{"zeta": 1, "mcpServers": {"other": {"command": "x"}}}`)
	}

	c := agent("agents/mcp_print_all", false, nil, nil, "mcp", "install", "--client", "all", "--dir", "{dir}", "--print")
	c2 := agent("agents/mcp_install_claude", false, []string{".mcp.json"}, nil, "mcp", "install", "--client", "claude", "--dir", "{dir}")
	c3 := agent("agents/mcp_install_all", false, allMCP, nil, "mcp", "install", "--client", "all", "--dir", "{dir}")
	c4 := agent("agents/mcp_install_merges_existing", false, []string{".mcp.json"}, existing, "mcp", "install", "--client", "claude", "--dir", "{dir}")
	c5 := agent("agents/mcp_err_no_client_detected", false, nil, nil, "mcp", "install", "--dir", "{dir}")
	c5.stderr = true
	c6 := agent("agents/skill_print", false, nil, nil, "skill", "install", "--print", "--dir", "{dir}")
	c7 := agent("agents/skill_install_claude", false, []string{".claude/skills/spicrawl/SKILL.md"}, nil, "skill", "install", "--client", "claude", "--dir", "{dir}")
	c8 := agent("agents/skill_install_codex_agents_md", false, []string{".agents/skills/spicrawl/SKILL.md", "AGENTS.md"}, nil, "skill", "install", "--client", "codex", "--agents-md", "--dir", "{dir}")
	c9 := agent("agents/init_yes", false, []string{".mcp.json", ".claude/skills/spicrawl/SKILL.md", ".cursor/mcp.json"}, nil, "init", "--client", "claude,cursor", "--yes", "--dir", "{dir}")
	c10 := agent("agents/init_needs_yes_without_terminal", false, nil, nil, "init", "--client", "claude", "--dir", "{dir}")
	c10.stderr = true
	c11 := agent("agents/init_err_no_client_detected", false, nil, nil, "init", "--yes", "--dir", "{dir}")
	c11.stderr = true

	// One prompt, no questions: --all --yes --api-key against a fake API that
	// accepts or rejects the key. The report on stderr is machine-readable here.
	setupClients := func(t *testing.T, e *goldenEnv) {
		goldenWrite(t, e.Dir, ".claude/settings.json", `{}`)
		goldenWrite(t, e.Dir, ".cursor/rules/x.mdc", `x`)
	}
	oneShot := func(name string, key int, noKey bool, args ...string) goldenCase {
		return goldenCase{
			name: name, args: append([]string{"init", "--all", "--yes", "--dir", "{dir}"}, args...), noKey: noKey, stderr: true,
			env: env, setup: setupClients,
			server: goldenServe(map[string]http.HandlerFunc{
				"GET /skill.md": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(gAgentSkill)) },
				"GET /v1/requests": func(w http.ResponseWriter, r *http.Request) {
					if key == 401 {
						gProblem(401, "ERR::AUTH::INVALID_KEY", "the API key is not valid")(w, r)
						return
					}
					gJSON(200, `{"data":[],`+gLogPageEnd+`}`)(w, r)
				},
			}),
			files: []string{".mcp.json", ".cursor/mcp.json", ".claude/skills/spicrawl/SKILL.md"},
		}
	}
	allKey := oneShot("agents/init_all_yes_api_key", 200, true, "--api-key", goldenFlagKey)
	rejected := oneShot("agents/init_api_key_rejected_exit3", 401, true, "--api-key", goldenFlagKey)
	rejected.files = nil
	noKeyAtAll := oneShot("agents/init_all_yes_no_key", 200, true)
	noKeyAtAll.files = nil
	envKey := oneShot("agents/init_all_yes_env_key", 200, false)
	envKey.files = nil
	both := agent("agents/init_all_conflicts_with_client", false, nil, nil, "init", "--all", "--client", "claude", "--yes", "--dir", "{dir}")
	both.stderr = true

	goldenAdd(allKey, rejected, noKeyAtAll, envKey, both)
	goldenAdd(c, c2, c3, c4, c5, c6, c7, c8, c9, c10, c11,
		agent("agents_human/mcp_print_all", true, nil, nil, "mcp", "install", "--client", "all", "--dir", "{dir}", "--print"),
		agent("agents_human/mcp_install_all", true, nil, nil, "mcp", "install", "--client", "all", "--dir", "{dir}"),
		agent("agents_human/mcp_install_merges_existing", true, nil, existing, "mcp", "install", "--client", "claude", "--dir", "{dir}"),
		agent("agents_human/skill_print", true, nil, nil, "skill", "install", "--print", "--dir", "{dir}"),
		agent("agents_human/skill_install_claude", true, nil, nil, "skill", "install", "--client", "claude", "--dir", "{dir}"),
		agent("agents_human/init_yes", true, nil, nil, "init", "--client", "claude,cursor", "--yes", "--dir", "{dir}"),
	)
}
