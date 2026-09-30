package cmd

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const agentTestSkill = "---\nname: spicrawl-web-data-api\ndescription: test skill\n---\n\n# Spicrawl\n"

// agentTestBase is the base URL agentRun passes; configs derive from it.
const (
	agentTestBase   = "http://127.0.0.1:1"
	agentTestMCPURL = agentTestBase + "/mcp"
)

// agentHostedHosts are the hosted service's hosts that the embedded skill and
// a docs site's skill carry as placeholders. None may survive into the files
// written for another deployment.
var agentHostedHosts = []string{"api.spicrawl.com", "mcp.spicrawl.com", "app.spicrawl.com", "docs.spicrawl.com"}

// agentHostedHost returns the first hosted-service host s names, or "".
func agentHostedHost(s string) string {
	for _, h := range agentHostedHosts {
		if strings.Contains(s, h) {
			return h
		}
	}
	return ""
}

// agentTestEnv gives each test a fresh project dir and HOME, and a fake
// skill server. It returns the project dir and home.
func agentTestEnv(t *testing.T) (string, string) {
	t.Helper()
	dir, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv(agentEnvMCPURL, "")
	t.Setenv(docsEnvURL, "")
	t.Setenv(docsEnvHost, "")
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte(agentTestSkill))
	})
	t.Setenv(agentEnvSkillURL, srv.URL+"/skill.md")
	return dir, home
}

func agentRun(t *testing.T, args ...string) ([]agentResult, string, int) {
	t.Helper()
	return agentRunBase(t, agentTestBase, args...)
}

func agentRunBase(t *testing.T, base string, args ...string) ([]agentResult, string, int) {
	t.Helper()
	out, errOut, code := runCLI(t, base, args...)
	var rs []agentResult
	if code == 0 && strings.HasPrefix(strings.TrimSpace(out), "[") {
		if err := json.Unmarshal([]byte(out), &rs); err != nil {
			t.Fatalf("stdout is not a result list: %v\n%s", err, out)
		}
	}
	return rs, errOut, code
}

func agentReadJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return m
}

func agentWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMCPInstallClaudeMergesAndIsIdempotent(t *testing.T) {
	dir, _ := agentTestEnv(t)
	path := filepath.Join(dir, ".mcp.json")
	agentWrite(t, path, `{"zeta": 1, "mcpServers": {"other": {"command": "x"}}, "alpha": {"keep": true}}`)

	rs, errOut, code := agentRun(t, "mcp", "install", "--client", "claude", "--dir", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if len(rs) != 1 || rs[0].Action != "updated" || rs[0].Secret {
		t.Fatalf("results = %+v", rs)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	if !(strings.Index(s, `"zeta"`) < strings.Index(s, `"mcpServers"`) && strings.Index(s, `"mcpServers"`) < strings.Index(s, `"alpha"`)) {
		t.Errorf("key order not preserved:\n%s", s)
	}
	if !strings.Contains(s, "\n  \"zeta\": 1,") {
		t.Errorf("not 2-space indented:\n%s", s)
	}
	m := agentReadJSON(t, path)
	servers := m["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Error("other server was dropped")
	}
	w := servers["spicrawl"].(map[string]any)
	if w["type"] != "http" || w["url"] != agentTestMCPURL {
		t.Errorf("entry = %v", w)
	}
	if got := w["headers"].(map[string]any)["Authorization"]; got != "Bearer ${SPICRAWL_API_KEY}" {
		t.Errorf("Authorization = %v", got)
	}

	rs, _, code = agentRun(t, "mcp", "install", "--client", "claude", "--dir", dir)
	if code != 0 || rs[0].Action != "unchanged" {
		t.Fatalf("second run: code %d, %+v", code, rs)
	}
	b2, _ := os.ReadFile(path)
	if string(b2) != s {
		t.Error("idempotent run rewrote the file")
	}
}

func TestMCPInstallMCPURLOverride(t *testing.T) {
	dir, _ := agentTestEnv(t)
	t.Setenv(agentEnvMCPURL, "https://env.example/mcp")
	if _, errOut, code := agentRun(t, "mcp", "install", "--client", "cursor", "--dir", dir); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	w := agentReadJSON(t, filepath.Join(dir, ".cursor", "mcp.json"))["mcpServers"].(map[string]any)["spicrawl"].(map[string]any)
	if w["url"] != "https://env.example/mcp" {
		t.Errorf("url = %v", w["url"])
	}
	if _, _, code := agentRun(t, "mcp", "install", "--client", "cursor", "--dir", dir, "--mcp-url", "https://flag.example/mcp"); code != 0 {
		t.Fatal(code)
	}
	w = agentReadJSON(t, filepath.Join(dir, ".cursor", "mcp.json"))["mcpServers"].(map[string]any)["spicrawl"].(map[string]any)
	if w["url"] != "https://flag.example/mcp" {
		t.Errorf("url = %v", w["url"])
	}
}

func TestMCPInstallCursorGlobalUsesEnvSyntax(t *testing.T) {
	dir, home := agentTestEnv(t)
	rs, errOut, code := agentRun(t, "mcp", "install", "--client", "cursor", "--global", "--dir", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	path := filepath.Join(home, ".cursor", "mcp.json")
	if rs[0].File != path || rs[0].Action != "created" {
		t.Fatalf("results = %+v", rs)
	}
	w := agentReadJSON(t, path)["mcpServers"].(map[string]any)["spicrawl"].(map[string]any)
	if _, has := w["type"]; has {
		t.Error("cursor entry should not carry type")
	}
	if got := w["headers"].(map[string]any)["Authorization"]; got != "Bearer ${env:SPICRAWL_API_KEY}" {
		t.Errorf("Authorization = %v", got)
	}
}

func TestMCPInstallVSCodeUsesInputPrompt(t *testing.T) {
	dir, _ := agentTestEnv(t)
	path := filepath.Join(dir, ".vscode", "mcp.json")
	agentWrite(t, path, `{"servers": {"x": {"type": "stdio", "command": "x"}}, "inputs": [{"type": "promptString", "id": "other"}]}`)
	if _, errOut, code := agentRun(t, "mcp", "install", "--client", "vscode", "--dir", dir); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	m := agentReadJSON(t, path)
	if _, ok := m["mcpServers"]; ok {
		t.Error("VS Code config must use servers, not mcpServers")
	}
	servers := m["servers"].(map[string]any)
	w := servers["spicrawl"].(map[string]any)
	if got := w["headers"].(map[string]any)["Authorization"]; got != "Bearer ${input:spicrawl-api-key}" {
		t.Errorf("Authorization = %v", got)
	}
	inputs := m["inputs"].([]any)
	if len(inputs) != 2 || inputs[1].(map[string]any)["id"] != agentVSCodeInputID || inputs[1].(map[string]any)["password"] != true {
		t.Errorf("inputs = %v", inputs)
	}
	rs, _, _ := agentRun(t, "mcp", "install", "--client", "vscode", "--dir", dir)
	if rs[0].Action != "unchanged" {
		t.Errorf("second run = %+v", rs)
	}
}

func TestMCPInstallCodexTOML(t *testing.T) {
	dir, _ := agentTestEnv(t)
	path := filepath.Join(dir, ".codex", "config.toml")
	agentWrite(t, path, `model = "gpt-5"

[mcp_servers.spicrawl]
url = "https://old.example/mcp"
http_headers = { "Authorization" = "Bearer old" }

[mcp_servers.spicrawl.tools.spicrawl_scrape]
approval_mode = "approve"

[mcp_servers.other]
command = "x"
`)
	rs, errOut, code := agentRun(t, "mcp", "install", "--client", "codex", "--dir", dir)
	if code != 0 || rs[0].Action != "updated" {
		t.Fatalf("exit %d %+v: %s", code, rs, errOut)
	}
	want := `model = "gpt-5"

[mcp_servers.spicrawl]
url = "http://127.0.0.1:1/mcp"
bearer_token_env_var = "SPICRAWL_API_KEY"

[mcp_servers.spicrawl.tools.spicrawl_scrape]
approval_mode = "approve"

[mcp_servers.other]
command = "x"
`
	b, _ := os.ReadFile(path)
	if string(b) != want {
		t.Errorf("config.toml =\n%s\nwant\n%s", b, want)
	}
	rs, _, _ = agentRun(t, "mcp", "install", "--client", "codex", "--dir", dir)
	if rs[0].Action != "unchanged" {
		t.Errorf("second run = %+v", rs)
	}
}

func TestMCPInstallCodexAppendsToGlobal(t *testing.T) {
	dir, home := agentTestEnv(t)
	path := filepath.Join(home, ".codex", "config.toml")
	agentWrite(t, path, "model = \"gpt-5\"\n")
	if _, errOut, code := agentRun(t, "mcp", "install", "--client", "codex", "--global", "--dir", dir); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	b, _ := os.ReadFile(path)
	want := "model = \"gpt-5\"\n\n[mcp_servers.spicrawl]\nurl = \"http://127.0.0.1:1/mcp\"\nbearer_token_env_var = \"SPICRAWL_API_KEY\"\n"
	if string(b) != want {
		t.Errorf("got\n%s", b)
	}
}

func TestMCPInstallClaudeGlobalEmbedsKeyAndWarns(t *testing.T) {
	dir, home := agentTestEnv(t)
	path := filepath.Join(home, ".claude.json")
	agentWrite(t, path, `{"numStartups": 3, "projects": {"/x": {}}}`)
	rs, errOut, code := agentRun(t, "mcp", "install", "--client", "claude", "--global", "--dir", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !rs[0].Secret || !strings.Contains(errOut, "API key in plain text") {
		t.Errorf("no secret warning: %+v %s", rs, errOut)
	}
	if strings.Contains(errOut, ".gitignore") {
		t.Errorf("gitignore advice for a home file: %s", errOut)
	}
	m := agentReadJSON(t, path)
	if m["numStartups"] != float64(3) {
		t.Error("existing keys lost")
	}
	w := m["mcpServers"].(map[string]any)["spicrawl"].(map[string]any)
	if got := w["headers"].(map[string]any)["Authorization"]; got != "Bearer spicrawl_test_key" {
		t.Errorf("Authorization = %v", got)
	}
}

func TestMCPInstallLiteralInProjectSuggestsGitignore(t *testing.T) {
	dir, _ := agentTestEnv(t)
	_, errOut, code := agentRun(t, "mcp", "install", "--client", "cursor", "--use-env=false", "--dir", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(errOut, ".cursor/mcp.json >> .gitignore") {
		t.Errorf("stderr = %s", errOut)
	}
	fi, _ := os.Stat(filepath.Join(dir, ".cursor", "mcp.json"))
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
}

func TestMCPInstallPrintWritesNothing(t *testing.T) {
	dir, _ := agentTestEnv(t)
	out, errOut, code := runCLI(t, "http://127.0.0.1:1", "mcp", "install", "--client", "claude,codex", "--print", "--dir", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var sn []struct{ Client, Format, Snippet string }
	if err := json.Unmarshal([]byte(out), &sn); err != nil || len(sn) != 2 {
		t.Fatalf("%v: %s", err, out)
	}
	if !strings.Contains(sn[0].Snippet, `"mcpServers"`) || !strings.Contains(sn[1].Snippet, "[mcp_servers.spicrawl]") {
		t.Errorf("snippets = %+v", sn)
	}
	if agentExists(filepath.Join(dir, ".mcp.json")) {
		t.Error("--print wrote a file")
	}
}

func TestMCPInstallNeedsClient(t *testing.T) {
	dir, _ := agentTestEnv(t)
	if _, _, code := agentRun(t, "mcp", "install", "--dir", dir); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if _, _, code := agentRun(t, "mcp", "install", "--client", "emacs", "--dir", dir); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
}

func TestMCPInstallRefusesInvalidJSON(t *testing.T) {
	dir, _ := agentTestEnv(t)
	path := filepath.Join(dir, ".vscode", "mcp.json")
	orig := "{\n  // comment\n  \"servers\": {}\n}\n"
	agentWrite(t, path, orig)
	if _, _, code := agentRun(t, "mcp", "install", "--client", "vscode", "--dir", dir); code == 0 {
		t.Fatal("want failure on JSONC")
	}
	b, _ := os.ReadFile(path)
	if string(b) != orig {
		t.Error("file was modified")
	}
}

func TestSkillInstallAllDeduplicates(t *testing.T) {
	dir, _ := agentTestEnv(t)
	rs, errOut, code := agentRun(t, "skill", "install", "--client", "all", "--dir", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	got := map[string]agentResult{}
	for _, r := range rs {
		got[r.Client] = r
	}
	if got["claude"].Action != "created" || got["claude"].File != filepath.Join(dir, ".claude", "skills", "spicrawl", "SKILL.md") {
		t.Errorf("claude = %+v", got["claude"])
	}
	if got["cursor"].Action != "skipped" || got["vscode"].Action != "skipped" {
		t.Errorf("cursor/vscode should reuse .claude/skills: %+v", rs)
	}
	if got["codex"].Action != "created" || got["codex"].File != filepath.Join(dir, ".agents", "skills", "spicrawl", "SKILL.md") {
		t.Errorf("codex = %+v", got["codex"])
	}
	b, _ := os.ReadFile(got["claude"].File)
	if !strings.HasPrefix(string(b), "---\nname: spicrawl\n") {
		t.Errorf("name not normalised:\n%s", b)
	}
	rs, _, _ = agentRun(t, "skill", "install", "--client", "claude", "--dir", dir)
	if rs[0].Action != "unchanged" {
		t.Errorf("second run = %+v", rs)
	}
}

func TestSkillInstallUpdatesExistingCopyInPlace(t *testing.T) {
	dir, _ := agentTestEnv(t)
	existing := filepath.Join(dir, ".agents", "skills", "spicrawl", "SKILL.md")
	agentWrite(t, existing, "old")
	rs, _, code := agentRun(t, "skill", "install", "--client", "cursor", "--dir", dir)
	if code != 0 || rs[0].File != existing || rs[0].Action != "updated" {
		t.Fatalf("code %d %+v", code, rs)
	}
	if agentExists(filepath.Join(dir, ".cursor", "skills")) {
		t.Error("wrote a duplicate skill")
	}
}

func TestSkillInstallFallsBackToEmbedded(t *testing.T) {
	dir, _ := agentTestEnv(t)
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) })
	t.Setenv(agentEnvSkillURL, srv.URL)
	rs, errOut, code := agentRun(t, "skill", "install", "--client", "codex", "--global", "--dir", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(errOut, "built into this CLI") {
		t.Errorf("stderr = %s", errOut)
	}
	b, _ := os.ReadFile(rs[0].File)
	if !strings.Contains(string(b), "name: spicrawl\n") || !strings.Contains(string(b), "http://127.0.0.1:1/v1/scrape") {
		t.Errorf("embedded skill not written:\n%.200s", b)
	}
	if h := agentHostedHost(string(b)); h != "" {
		t.Errorf("embedded skill still names the hosted service (%s):\n%s", h, b)
	}
}

func TestSkillInstallPrint(t *testing.T) {
	agentTestEnv(t)
	out, _, code := runCLI(t, "http://127.0.0.1:1", "skill", "install", "--print")
	if code != 0 || !strings.Contains(out, "name: spicrawl") {
		t.Errorf("code %d: %s", code, out)
	}
}

func TestSkillInstallAgentsMD(t *testing.T) {
	dir, _ := agentTestEnv(t)
	path := filepath.Join(dir, "AGENTS.md")
	agentWrite(t, path, "# Project\n\nRules.\n")
	for i := 0; i < 2; i++ {
		if _, errOut, code := agentRun(t, "skill", "install", "--client", "codex", "--agents-md", "--dir", dir); code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	if !strings.HasPrefix(s, "# Project\n\nRules.\n\n"+skillAgentsBegin) || strings.Count(s, skillAgentsBegin) != 1 {
		t.Errorf("AGENTS.md =\n%s", s)
	}
}

func TestInitRequiresYesWithoutTerminal(t *testing.T) {
	dir, _ := agentTestEnv(t)
	agentWrite(t, filepath.Join(dir, "CLAUDE.md"), "x")
	orig := initStdinIsTerminal
	initStdinIsTerminal = func() bool { return false }
	t.Cleanup(func() { initStdinIsTerminal = orig })
	_, errOut, code := agentRun(t, "init", "--dir", dir)
	if code != 2 || !strings.Contains(errOut, "--yes") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if agentExists(filepath.Join(dir, ".mcp.json")) {
		t.Error("wrote without --yes")
	}
}

func TestInitDetectsAndApplies(t *testing.T) {
	dir, _ := agentTestEnv(t)
	agentWrite(t, filepath.Join(dir, "CLAUDE.md"), "x")
	if err := os.MkdirAll(filepath.Join(dir, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	rs, errOut, code := agentRun(t, "init", "--yes", "--dir", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	seen := map[string]string{}
	for _, r := range rs {
		seen[r.Client+"/"+r.Kind] = r.Action
	}
	want := map[string]string{"claude/mcp": "created", "cursor/mcp": "created", "claude/skill": "created", "cursor/skill": "skipped"}
	for k, v := range want {
		if seen[k] != v {
			t.Errorf("%s = %q, want %q (all: %v)", k, seen[k], v, seen)
		}
	}
	if _, ok := seen["codex/mcp"]; ok {
		t.Errorf("codex was not detected but configured: %v", seen)
	}
	rs, _, _ = agentRun(t, "init", "--dir", dir)
	for _, r := range rs {
		if r.Action == "created" || r.Action == "updated" {
			t.Errorf("second run changed %+v", r)
		}
	}
}

func TestInitConfirmation(t *testing.T) {
	dir, _ := agentTestEnv(t)
	orig, origIn := initStdinIsTerminal, initStdin
	initStdinIsTerminal = func() bool { return true }
	t.Cleanup(func() { initStdinIsTerminal, initStdin = orig, origIn })

	initStdin = strings.NewReader("n\n")
	rs, errOut, code := agentRun(t, "init", "--client", "codex", "--dir", dir)
	if code != 0 || !strings.Contains(errOut, "plan:") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, r := range rs {
		if r.Action != "skipped" {
			t.Errorf("declined but %+v", r)
		}
	}
	if agentExists(filepath.Join(dir, ".codex")) {
		t.Error("wrote after decline")
	}

	initStdin = strings.NewReader("y\n")
	if _, errOut, code := agentRun(t, "init", "--client", "codex", "--dir", dir); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !agentExists(filepath.Join(dir, ".codex", "config.toml")) || !agentExists(filepath.Join(dir, ".agents", "skills", "spicrawl", "SKILL.md")) {
		t.Error("confirmed but not written")
	}
}

// ---- derived URLs (self-hosted deployments) ----

func TestMCPInstallDerivesURLFromBaseURL(t *testing.T) {
	dir, _ := agentTestEnv(t)
	if _, errOut, code := agentRunBase(t, "http://192.0.2.9:8080/", "mcp", "install", "--client", "claude,cursor,vscode,codex", "--dir", dir); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	const want = "http://192.0.2.9:8080/mcp"
	for _, f := range []struct{ path, servers string }{
		{".mcp.json", "mcpServers"}, {".cursor/mcp.json", "mcpServers"}, {".vscode/mcp.json", "servers"},
	} {
		w := agentReadJSON(t, filepath.Join(dir, f.path))[f.servers].(map[string]any)["spicrawl"].(map[string]any)
		if w["url"] != want {
			t.Errorf("%s url = %v, want %s", f.path, w["url"], want)
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".codex", "config.toml"))
	if !strings.Contains(string(b), `url = "`+want+`"`) {
		t.Errorf("config.toml =\n%s", b)
	}
	for _, f := range []string{".mcp.json", ".cursor/mcp.json", ".vscode/mcp.json", ".codex/config.toml"} {
		b, _ := os.ReadFile(filepath.Join(dir, f))
		if h := agentHostedHost(string(b)); h != "" {
			t.Errorf("%s names the hosted service (%s):\n%s", f, h, b)
		}
	}
}

func TestMCPInstallDerivesURLFromEnvBaseURL(t *testing.T) {
	dir, _ := agentTestEnv(t)
	t.Setenv("SPICRAWL_BASE_URL", "https://scrape.internal.example/api")
	out, errOut, code := runCLI(t, "", "mcp", "install", "--client", "cursor", "--print", "--dir", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, `https://scrape.internal.example/mcp`) {
		t.Errorf("snippet = %s", out)
	}
}

// The hosted API's MCP server has its own host; only the default base URL gets it.
func TestMCPInstallDefaultsToHostedMCPServer(t *testing.T) {
	dir, _ := agentTestEnv(t)
	t.Setenv("SPICRAWL_BASE_URL", "")
	out, errOut, code := runCLI(t, "", "mcp", "install", "--client", "cursor", "--print", "--dir", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, `https://mcp.spicrawl.com/mcp\"`) {
		t.Errorf("snippet = %s", out)
	}
	out, errOut, code = runCLI(t, "https://api.spicrawl.com/", "mcp", "install", "--client", "cursor", "--print", "--dir", dir)
	if code != 0 || !strings.Contains(out, `https://mcp.spicrawl.com/mcp\"`) {
		t.Errorf("explicit hosted base URL: exit %d, snippet = %s%s", code, out, errOut)
	}
}

func TestMCPInstallRejectsBadMCPURL(t *testing.T) {
	dir, _ := agentTestEnv(t)
	if _, _, code := agentRun(t, "mcp", "install", "--client", "cursor", "--mcp-url", "mcp.example", "--dir", dir); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if agentExists(filepath.Join(dir, ".cursor")) {
		t.Error("wrote a config with a bad URL")
	}
}

// agentSkillServer serves a skill that names the hosted service, recording
// the paths it is asked for. It is served at /docs/skill.md and /skill.md, so
// the same server stands in for a self-hosted API's docs and a docs host. The
// docs appear in their current form (the root of docs.spicrawl.com) and both
// older /docs forms, which a skill from an older docs build still carries.
func agentSkillServer(t *testing.T) (string, *[]recordedRequest) {
	t.Helper()
	body := "---\nname: x\ndescription: d\n---\n\nBase `https://api.spicrawl.com`, MCP `https://mcp.spicrawl.com/mcp`,\n" +
		"docs https://docs.spicrawl.com/errors, https://api.spicrawl.com/docs/quickstart and https://docs.spicrawl.com/docs/cli,\n" +
		"CDP `wss://api.spicrawl.com/v1/browser`.\n"
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/docs/skill.md" && r.URL.Path != "/skill.md" {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write([]byte(body))
	})
	return srv.URL, reqs
}

func TestSkillInstallDerivesURLAndRewrites(t *testing.T) {
	agentTestEnv(t)
	t.Setenv(agentEnvSkillURL, "")
	base, reqs := agentSkillServer(t)
	out, errOut, code := runCLI(t, base, "skill", "install", "--print")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if len(*reqs) != 1 || (*reqs)[0].Path != "/docs/skill.md" {
		t.Fatalf("requests = %+v", *reqs)
	}
	var res struct{ Source, Content string }
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.Source != base+"/docs/skill.md" {
		t.Errorf("source = %s", res.Source)
	}
	ws := "ws" + strings.TrimPrefix(base, "http")
	for _, want := range []string{"Base `" + base + "`", "MCP `" + base + "/mcp`", base + "/docs/errors",
		base + "/docs/quickstart", base + "/docs/cli,", ws + "/v1/browser"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("missing %q in\n%s", want, res.Content)
		}
	}
	if h := agentHostedHost(res.Content); h != "" {
		t.Errorf("hosted URLs (%s) left in\n%s", h, res.Content)
	}
}

// TestSkillInstallUsesDocsURL: $SPICRAWL_DOCS_URL is the docs base as is (here
// a docs host serving at its root), and wins over the legacy docs host.
func TestSkillInstallUsesDocsURL(t *testing.T) {
	agentTestEnv(t)
	t.Setenv(agentEnvSkillURL, "")
	docs, reqs := agentSkillServer(t)
	t.Setenv(docsEnvURL, docs+"/")
	t.Setenv(docsEnvHost, "http://127.0.0.1:1")
	out, errOut, code := runCLI(t, "http://192.0.2.9:8080", "skill", "install", "--print")
	if code != 0 || len(*reqs) != 1 || (*reqs)[0].Path != "/skill.md" {
		t.Fatalf("exit %d, requests %+v: %s", code, *reqs, errOut)
	}
	for _, want := range []string{"http://192.0.2.9:8080/mcp", docs + "/errors", docs + "/quickstart", docs + "/cli,", "ws://192.0.2.9:8080/v1/browser"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	if h := agentHostedHost(out); h != "" {
		t.Errorf("hosted URLs (%s) left in\n%s", h, out)
	}

	t.Setenv(docsEnvURL, "docs.example.test/docs")
	if _, _, code := runCLI(t, "http://192.0.2.9:8080", "skill", "install", "--print"); code != 2 {
		t.Errorf("a $%s without a scheme: exit %d, want 2", docsEnvURL, code)
	}
}

// TestSkillInstallHostedDocs: with the hosted API the docs are the root of
// docs.spicrawl.com, so the skill's older /docs links move there too.
func TestSkillInstallHostedDocs(t *testing.T) {
	agentTestEnv(t)
	docs, _ := agentSkillServer(t)
	out, errOut, code := runCLI(t, "https://api.spicrawl.com", "skill", "install", "--print", "--skill-url", docs+"/skill.md")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{"https://docs.spicrawl.com/errors", "https://docs.spicrawl.com/quickstart",
		"https://docs.spicrawl.com/cli,", "https://mcp.spicrawl.com/mcp"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	if strings.Contains(out, "spicrawl.com/docs") {
		t.Errorf("a /docs link survived in %s", out)
	}
}

// TestSkillInstallUsesDocsHost: the legacy $SPICRAWL_DOCS_HOST keeps its
// meaning, an origin whose /docs holds the docs.
func TestSkillInstallUsesDocsHost(t *testing.T) {
	agentTestEnv(t)
	t.Setenv(agentEnvSkillURL, "")
	docs, reqs := agentSkillServer(t)
	t.Setenv(docsEnvHost, docs+"/")
	out, errOut, code := runCLI(t, "http://192.0.2.9:8080", "skill", "install", "--print")
	if code != 0 || len(*reqs) != 1 || (*reqs)[0].Path != "/docs/skill.md" {
		t.Fatalf("exit %d, requests %+v: %s", code, *reqs, errOut)
	}
	for _, want := range []string{"http://192.0.2.9:8080/mcp", docs + "/docs/errors", "ws://192.0.2.9:8080/v1/browser"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	// --skill-url wins over everything.
	_, _, code = runCLI(t, "http://192.0.2.9:8080", "skill", "install", "--print", "--skill-url", docs+"/docs/skill.md?v=2")
	if code != 0 || len(*reqs) != 2 || (*reqs)[1].Query != "v=2" {
		t.Errorf("--skill-url not used: code %d %+v", code, *reqs)
	}
}

func TestInitWritesDerivedURLs(t *testing.T) {
	dir, _ := agentTestEnv(t)
	t.Setenv(agentEnvSkillURL, "")
	base, _ := agentSkillServer(t)
	if _, errOut, code := agentRunBase(t, base, "init", "--client", "codex", "--yes", "--agents-md", "--dir", dir); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	toml, _ := os.ReadFile(filepath.Join(dir, ".codex", "config.toml"))
	skill, _ := os.ReadFile(filepath.Join(dir, ".agents", "skills", "spicrawl", "SKILL.md"))
	agents, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if !strings.Contains(string(toml), base+"/mcp") || !strings.Contains(string(skill), base+"/mcp") || !strings.Contains(string(agents), base+"/docs/skill.md") {
		t.Errorf("toml:\n%s\nskill:\n%s\nAGENTS.md:\n%s", toml, skill, agents)
	}
}

// ---- Codex config.toml forms ----

func agentCodexRun(t *testing.T, dir, content string, extra ...string) (string, string, int) {
	t.Helper()
	path := filepath.Join(dir, ".codex", "config.toml")
	agentWrite(t, path, content)
	_, errOut, code := agentRun(t, append([]string{"mcp", "install", "--client", "codex", "--dir", dir}, extra...)...)
	b, _ := os.ReadFile(path)
	return string(b), errOut, code
}

func TestMCPInstallCodexPreservesCustomSettings(t *testing.T) {
	dir, _ := agentTestEnv(t)
	got, errOut, code := agentCodexRun(t, dir, `[mcp_servers.spicrawl]
# pinned by ops
url = "https://old.example/mcp" # old
enabled = false
startup_timeout_sec = 30
tool_timeout_sec = 120
http_headers = { "X-Team" = "data", Authorization = "Bearer old" }
`)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	want := `[mcp_servers.spicrawl]
# pinned by ops
url = "http://127.0.0.1:1/mcp" # old
bearer_token_env_var = "SPICRAWL_API_KEY"
enabled = false
startup_timeout_sec = 30
tool_timeout_sec = 120
http_headers = { "X-Team" = "data" }
`
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	// Literal key: the header comes back, the env var goes, custom keys stay.
	got, errOut, code = agentCodexRun(t, dir, got, "--use-env=false")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if strings.Contains(got, "bearer_token_env_var") || !strings.Contains(got, `http_headers = { "X-Team" = "data", "Authorization" = "Bearer spicrawl_test_key" }`) ||
		!strings.Contains(got, "startup_timeout_sec = 30") || !strings.Contains(got, "enabled = false") {
		t.Errorf("got\n%s", got)
	}
	if fi, _ := os.Stat(filepath.Join(dir, ".codex", "config.toml")); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
}

func TestMCPInstallCodexInlineForms(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{
			"inline in [mcp_servers]",
			"model = \"o5\"\n\n[mcp_servers]\nspicrawl = { url = \"https://mcp.spicrawl.com/mcp\", startup_timeout_sec = 20, bearer_token_env_var = \"SPICRAWL_API_KEY\" }\nother = { command = \"x\" }\n",
			"model = \"o5\"\n\n[mcp_servers]\nspicrawl = { url = \"http://127.0.0.1:1/mcp\", startup_timeout_sec = 20, bearer_token_env_var = \"SPICRAWL_API_KEY\" }\nother = { command = \"x\" }\n",
		},
		{
			"dotted inline at root",
			"mcp_servers.spicrawl = { url = \"https://old/mcp\", enabled = true } # mine\n\n[profiles.fast]\nmodel = \"o5-mini\"\n",
			"mcp_servers.spicrawl = { url = \"http://127.0.0.1:1/mcp\", bearer_token_env_var = \"SPICRAWL_API_KEY\", enabled = true } # mine\n\n[profiles.fast]\nmodel = \"o5-mini\"\n",
		},
		{
			"quoted table header",
			"[mcp_servers.\"spicrawl\"]\nurl = 'https://old/mcp'\n",
			"[mcp_servers.\"spicrawl\"]\nurl = \"http://127.0.0.1:1/mcp\"\nbearer_token_env_var = \"SPICRAWL_API_KEY\"\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, _ := agentTestEnv(t)
			got, errOut, code := agentCodexRun(t, dir, c.in)
			if code != 0 {
				t.Fatalf("exit %d: %s", code, errOut)
			}
			if got != c.want {
				t.Errorf("got\n%s\nwant\n%s", got, c.want)
			}
			if n := strings.Count(got, "webora"); n != 0 {
				t.Errorf("legacy name appears %d times", n)
			}
			rs, _, _ := agentRun(t, "mcp", "install", "--client", "codex", "--dir", dir)
			if rs[0].Action != "unchanged" {
				t.Errorf("second run = %+v", rs)
			}
		})
	}
}

func TestMCPInstallCodexRefusesUnsafeForms(t *testing.T) {
	cases := map[string]string{
		"dotted keys at root":      "mcp_servers.spicrawl.url = \"https://old/mcp\"\n",
		"dotted keys in table":     "[mcp_servers]\nspicrawl.url = \"https://old/mcp\"\n",
		"inline mcp_servers":       "mcp_servers = { spicrawl = { url = \"https://old/mcp\" } }\n",
		"inline without spicrawl":  "mcp_servers = { other = { command = \"x\" } }\n",
		"table and inline":         "[mcp_servers]\nspicrawl = { url = \"a\" }\n[mcp_servers.spicrawl]\nurl = \"b\"\n",
		"duplicate table":          "[mcp_servers.spicrawl]\nurl = \"a\"\n[mcp_servers.spicrawl]\nurl = \"b\"\n",
		"stdio server":             "[mcp_servers.spicrawl]\ncommand = \"npx\"\n",
		"multi-line http_headers":  "[mcp_servers.spicrawl]\nurl = \"a\"\nhttp_headers = [\n 1,\n]\n",
		"unterminated string":      "model = \"o5\n",
		"inline value not a table": "[mcp_servers]\nspicrawl = \"https://old/mcp\"\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			dir, _ := agentTestEnv(t)
			got, errOut, code := agentCodexRun(t, dir, in)
			if code == 0 {
				t.Fatalf("exit 0, file now\n%s", got)
			}
			if got != in {
				t.Errorf("file modified:\n%s", got)
			}
			if !strings.Contains(errOut, "config.toml") {
				t.Errorf("error does not name the file: %s", errOut)
			}
		})
	}
}

func TestMCPInstallCodexIgnoresLookalikes(t *testing.T) {
	dir, _ := agentTestEnv(t)
	in := "[profiles.x]\nmcp_servers.spicrawl.url = \"elsewhere\"\nnote = \"\"\"\n[mcp_servers.spicrawl]\nurl = 1\n\"\"\"\n"
	got, errOut, code := agentCodexRun(t, dir, in)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.HasPrefix(got, in+"\n[mcp_servers.spicrawl]\nurl = \"http://127.0.0.1:1/mcp\"\n") {
		t.Errorf("got\n%s", got)
	}
}

// ---- permissions of files holding a literal key ----

func TestMCPInstallSecretTightensExistingFile(t *testing.T) {
	dir, home := agentTestEnv(t)
	path := filepath.Join(home, ".claude.json")
	agentWrite(t, path, `{"numStartups": 3}`)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := agentRun(t, "mcp", "install", "--client", "claude", "--global", "--dir", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
	if !strings.Contains(errOut, "0644 to 0600") {
		t.Errorf("stderr does not mention the permission change: %s", errOut)
	}

	// Content already right but loosened again: still fixed, and reported.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	rs, errOut, code := agentRun(t, "mcp", "install", "--client", "claude", "--global", "--dir", dir)
	if code != 0 || rs[0].Action != "updated" || !strings.Contains(rs[0].Reason, "0600") {
		t.Fatalf("exit %d %+v: %s", code, rs, errOut)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
	rs, _, _ = agentRun(t, "mcp", "install", "--client", "claude", "--global", "--dir", dir)
	if rs[0].Action != "unchanged" {
		t.Errorf("third run = %+v", rs)
	}
}

func TestMCPInstallNonSecretKeepsMode(t *testing.T) {
	dir, _ := agentTestEnv(t)
	path := filepath.Join(dir, ".mcp.json")
	agentWrite(t, path, `{}`)
	if err := os.Chmod(path, 0o664); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := agentRun(t, "mcp", "install", "--client", "claude", "--dir", dir); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o664 {
		t.Errorf("mode = %v, want 0664 kept", fi.Mode().Perm())
	}
}
