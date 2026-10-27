package cmd

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OfficialSpicrawl/cli/internal/config"
)

// The one-prompt setup: "init --all --yes --api-key K", then "status --json".

const onboardKey = "spicrawl_live_ONBOARDSECRET0123456789abcd"

// onboardAPI answers like a healthy API that accepts onboardKey only.
func onboardAPI(t *testing.T) string {
	t.Helper()
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/readyz":
			_, _ = w.Write([]byte(`{"status":"ready","checks":{"postgres":"ok"},"cdp":"enabled"}`))
		case "/v1/workers":
			_, _ = w.Write([]byte(`{"kinds":[{"kind":"browser","present":1,"ready":1}]}`))
		case "/v1/requests":
			if r.Header.Get("Authorization") != "Bearer "+onboardKey && r.Header.Get("Authorization") != "Bearer spicrawl_test_key" {
				writeProblem(w, 401, "ERR::AUTH::INVALID_KEY", false)
				return
			}
			_, _ = w.Write([]byte(`{"data":[],"page":{}}`))
		default:
			w.Header().Set("Content-Type", "text/markdown")
			_, _ = w.Write([]byte(agentTestSkill))
		}
	})
	t.Setenv(agentEnvSkillURL, srv.URL+"/skill.md")
	return srv.URL
}

// onboardProject is a project directory where Claude Code and Cursor are in use.
func onboardProject(t *testing.T) string {
	t.Helper()
	dir, _ := agentTestEnv(t)
	agentWrite(t, filepath.Join(dir, ".claude", "settings.json"), "{}")
	agentWrite(t, filepath.Join(dir, ".cursor", "rules", "x.mdc"), "x")
	return dir
}

// savedKey is the api_key in the config file, "" when there is none.
func savedKey(t *testing.T) string {
	t.Helper()
	f, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return f.APIKey
}

func TestInitAllYesAPIKey(t *testing.T) {
	base := onboardAPI(t)
	dir := onboardProject(t)
	out, errb, code := runCLI(t, base, "init", "--all", "--yes", "--api-key", onboardKey, "--dir", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	// Stdout stays the result list, one MCP entry per detected client.
	var rs []agentResult
	if err := json.Unmarshal([]byte(out), &rs); err != nil {
		t.Fatalf("stdout is not the result list: %v\n%s", err, out)
	}
	got := map[string]string{}
	for _, r := range rs {
		if r.Kind == "mcp" {
			got[r.Client] = r.Action
		}
	}
	if len(got) != 2 || got["claude"] != agentActionCreated || got["cursor"] != agentActionCreated {
		t.Errorf("mcp results %v", got)
	}
	for _, f := range []string{".mcp.json", ".cursor/mcp.json", ".claude/skills/spicrawl/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("not written: %v", err)
		}
	}
	// The key was saved like "login" does, and only masked in the report.
	if savedKey(t) != onboardKey {
		t.Errorf("key not saved to the config file")
	}
	for _, want := range []string{
		"connected: Claude Code, Cursor\n",
		"skipped: VS Code (Copilot) (not detected), Codex (not detected)\n",
		"api key: " + config.MaskKey(onboardKey) + " (checked, saved to ",
		"next: restart Claude Code, Cursor to load the MCP server and the skill\n",
		"next: run spicrawl status",
	} {
		if !strings.Contains(errb, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errb)
		}
	}
	noKeyIn(t, onboardKey, "stdout", out)
	noKeyIn(t, onboardKey, "stderr", errb)
	// The MCP configs reference the environment, never the literal key.
	b, _ := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	noKeyIn(t, onboardKey, ".mcp.json", string(b))
}

func noKeyIn(t *testing.T, key, where, s string) {
	t.Helper()
	if strings.Contains(s, key) {
		t.Errorf("%s contains the API key:\n%s", where, s)
	}
}

// A rejected key stops init before anything is written or saved.
func TestInitAPIKeyRejectedWritesNothing(t *testing.T) {
	base := onboardAPI(t)
	dir := onboardProject(t)
	bad := "spicrawl_live_REJECTEDSECRET0123456789wxyz"
	out, errb, code := runCLI(t, base, "init", "--all", "--yes", "--api-key", bad, "--dir", dir)
	if code != 3 {
		t.Fatalf("exit %d, want 3: %s", code, errb)
	}
	if _, err := os.Stat(filepath.Join(dir, ".mcp.json")); err == nil {
		t.Error(".mcp.json written for a rejected key")
	}
	if savedKey(t) != "" {
		t.Error("a rejected key was saved")
	}
	noKeyIn(t, bad, "stdout", out)
	noKeyIn(t, bad, "stderr", errb)
}

// The key never reaches stdout or stderr, even when something upstream puts it
// in an error: a problem document that echoes it, or a config diff.
func TestInitNeverEchoesTheKeyInErrors(t *testing.T) {
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(401)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "https://docs.spicrawl.com/errors", "title": "bad key " + onboardKey, "status": 401,
			"code": "ERR::AUTH::INVALID_KEY", "detail": "the key " + onboardKey + " is not valid", "retryable": false,
			"diagnostics": map[string]string{"hint": "key " + onboardKey},
		})
	})
	for _, human := range []bool{false, true} {
		dir := onboardProject(t)
		run := runCLI
		if human {
			run = runCLIHuman
		}
		out, errb, code := run(t, srv.URL, "init", "--all", "--yes", "--api-key", onboardKey, "--dir", dir)
		if code != 3 {
			t.Fatalf("human=%v: exit %d: %s", human, code, errb)
		}
		noKeyIn(t, onboardKey, "stdout", out)
		noKeyIn(t, onboardKey, "stderr", errb)
		if !strings.Contains(errb, config.MaskKey(onboardKey)) {
			t.Errorf("human=%v: expected the masked key in the error:\n%s", human, errb)
		}
	}

	// A usage error and an unreachable API: the key is not in the message either.
	dir := onboardProject(t)
	for _, args := range [][]string{
		{"init", "--all", "--client", "claude", "--api-key", onboardKey, "--dir", dir},
		{"init", "--all", "--yes", "--api-key", onboardKey, "--dir", dir, "--mcp-url", "not a url " + onboardKey},
	} {
		out, errb, code := runCLI(t, "http://127.0.0.1:1", args...)
		if code == 0 {
			t.Fatalf("%v: exit 0", args)
		}
		noKeyIn(t, onboardKey, "stdout", out)
		noKeyIn(t, onboardKey, "stderr", errb)
	}
}

func TestScrubKey(t *testing.T) {
	if got := scrubKey("a "+onboardKey+" b "+onboardKey, onboardKey); strings.Contains(got, onboardKey) || strings.Count(got, config.MaskKey(onboardKey)) != 2 {
		t.Errorf("scrubKey = %q", got)
	}
	if got := scrubKey("short key", "key"); got != "short key" {
		t.Errorf("a short key must not be scrubbed: %q", got)
	}
}

func TestInitAllConflictsWithClient(t *testing.T) {
	dir := onboardProject(t)
	_, errb, code := runCLI(t, "http://127.0.0.1:1", "init", "--all", "--client", "claude", "--yes", "--dir", dir)
	if code != 2 || !strings.Contains(errb, "mutually exclusive") {
		t.Errorf("exit %d: %s", code, errb)
	}
}

// $SPICRAWL_API_KEY is used for the client configs as before; init does not
// check it (no network) or copy it into the config file.
func TestInitEnvKeyIsNotCheckedOrSaved(t *testing.T) {
	dir := onboardProject(t)
	_, errb, code := runCLI(t, "http://127.0.0.1:1", "init", "--all", "--yes", "--dir", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if savedKey(t) != "" {
		t.Error("the environment key was saved")
	}
	if !strings.Contains(errb, "(from env)") {
		t.Errorf("report lacks the key source:\n%s", errb)
	}
}

func TestInitNextStepUsesNpxWhenStartedByNpx(t *testing.T) {
	t.Setenv("npm_command", "exec")
	dir := onboardProject(t)
	_, errb, code := runCLI(t, "http://127.0.0.1:1", "init", "--all", "--yes", "--dir", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if !strings.Contains(errb, "next: run npx -y @spicrawl/cli@latest status") {
		t.Errorf("stderr:\n%s", errb)
	}
}

// ---- status repairs ----

func statusJSON(t *testing.T, base string, args ...string) (statusReport, string, int) {
	t.Helper()
	out, errb, code := runCLI(t, base, append([]string{"status"}, args...)...)
	var rep statusReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("stdout is not a status report: %v\n%s\n%s", err, out, errb)
	}
	return rep, out, code
}

// Every repair has the four documented fields and nothing else.
func TestStatusRepairsJSONShape(t *testing.T) {
	srv, _ := statusServer(t, false) // not ready, key rejected
	out, _, code := runCLI(t, srv.URL, "status")
	if code != 3 {
		t.Fatalf("exit %d", code)
	}
	var raw struct {
		Repairs []map[string]any `json:"repairs"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw.Repairs) != 2 {
		t.Fatalf("repairs %v", raw.Repairs)
	}
	for _, r := range raw.Repairs {
		if len(r) != 4 {
			t.Errorf("repair has %d fields: %v", len(r), r)
		}
		for _, k := range []string{"check", "command", "unattended_safe", "why"} {
			if _, ok := r[k]; !ok {
				t.Errorf("repair lacks %q: %v", k, r)
			}
		}
		if _, ok := r["unattended_safe"].(bool); !ok {
			t.Errorf("unattended_safe is not a boolean: %v", r)
		}
	}
}

func repairFor(rep statusReport, check string) *statusRepair {
	for i := range rep.Repairs {
		if rep.Repairs[i].Check == check {
			return &rep.Repairs[i]
		}
	}
	return nil
}

// A missing key is the user's to supply; an MCP config not written is safe to
// write; and the key itself is never in a repair command.
func TestStatusRepairsSafety(t *testing.T) {
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/readyz":
			_, _ = w.Write([]byte(`{"status":"ready","checks":{},"cdp":"enabled"}`))
		default:
			writeProblem(w, 401, "ERR::AUTH::INVALID_KEY", false)
		}
	})
	dir := onboardProject(t)
	t.Chdir(dir)

	rep, _, code := statusJSON(t, srv.URL, "--api-key", onboardKey)
	if code != 3 {
		t.Fatalf("exit %d, want 3 (key rejected)", code)
	}
	key := repairFor(rep, "key")
	if key == nil || key.UnattendedSafe || !strings.Contains(key.Command, "<YOUR_API_KEY>") || strings.Contains(key.Command, onboardKey) {
		t.Errorf("key repair %+v", key)
	}
	mcp := repairFor(rep, "mcp")
	if mcp == nil || !mcp.UnattendedSafe || mcp.Command != "spicrawl init --all --yes" {
		t.Errorf("mcp repair %+v", mcp)
	}
	if !strings.Contains(mcp.Why, "Claude Code") || !strings.Contains(mcp.Why, "Cursor") {
		t.Errorf("mcp why %q", mcp.Why)
	}
	if got := rep.MCP.Detected; len(got) != 2 || len(rep.MCP.Connected) != 0 {
		t.Errorf("mcp %+v", rep.MCP)
	}
}

func TestStatusRepairsNoKeyAtAll(t *testing.T) {
	srv, _ := statusServer(t, true)
	testKey = "" // a shell without $SPICRAWL_API_KEY
	t.Cleanup(func() { testKey = "spicrawl_test_key" })
	out, _, code := runCLI(t, srv.URL, "status")
	if code != 3 {
		t.Fatalf("exit %d", code)
	}
	var rep statusReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	key := repairFor(rep, "key")
	if key == nil || key.UnattendedSafe || key.Command != "spicrawl login --api-key <YOUR_API_KEY>" || !strings.Contains(key.Why, "no API key") {
		t.Errorf("key repair %+v", key)
	}
}

// Started through npx, a repair names npx: the program may not be on PATH.
func TestStatusRepairsUseNpx(t *testing.T) {
	t.Setenv("npm_command", "exec")
	dir := onboardProject(t)
	t.Chdir(dir)
	rep, _, code := statusJSON(t, onboardAPI(t))
	if code != 1 {
		t.Fatalf("exit %d, want 1 (mcp not configured)", code)
	}
	mcp := repairFor(rep, "mcp")
	if mcp == nil || mcp.Command != "npx -y @spicrawl/cli@latest init --all --yes" {
		t.Errorf("mcp repair %+v", mcp)
	}
}

// The loop the prompt relies on: status fails with an mcp repair, running that
// repair fixes it, status passes and exits 0.
func TestStatusPassesAfterInit(t *testing.T) {
	base := onboardAPI(t)
	dir := onboardProject(t)
	t.Chdir(dir)

	rep, _, code := statusJSON(t, base)
	if code != 1 || repairFor(rep, "mcp") == nil {
		t.Fatalf("before init: exit %d, repairs %+v", code, rep.Repairs)
	}
	if _, errb, code := runCLI(t, base, "init", "--all", "--yes", "--api-key", onboardKey); code != 0 {
		t.Fatalf("init: exit %d: %s", code, errb)
	}
	rep, _, code = statusJSON(t, base)
	if code != 0 || len(rep.Repairs) != 0 {
		t.Fatalf("after init: exit %d, repairs %+v", code, rep.Repairs)
	}
	if got := strings.Join(rep.MCP.Connected, ","); got != "claude,cursor" {
		t.Errorf("connected %q", got)
	}
}

func TestStatusFailedChecksExitNonZero(t *testing.T) {
	// Not ready, with the key fine: exit 1, one repair per failed check.
	srv, _ := statusServer(t, true)
	rep, _, code := statusJSON(t, srv.URL)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if r := repairFor(rep, "ready"); r == nil || !r.UnattendedSafe {
		t.Errorf("ready repair %+v", r)
	}
	if len(rep.Repairs) != 1 {
		t.Errorf("repairs %+v", rep.Repairs)
	}
}

func TestMCPConfigured(t *testing.T) {
	dir := t.TempDir()
	w := func(name, body string) string {
		p := filepath.Join(dir, name)
		agentWrite(t, p, body)
		return p
	}
	claude, vscode, codex := agentClientByID("claude"), agentClientByID("vscode"), agentClientByID("codex")
	for _, tc := range []struct {
		name string
		c    *agentClient
		file string
		want bool
	}{
		{"claude entry", claude, w("a.json", `{"mcpServers":{"other":{},"spicrawl":{"url":"x"}}}`), true},
		{"claude other server", claude, w("b.json", `{"mcpServers":{"other":{}}}`), false},
		{"claude empty", claude, w("c.json", `{}`), false},
		{"claude broken", claude, w("d.json", `{`), false},
		{"claude missing file", claude, filepath.Join(dir, "none.json"), false},
		{"vscode entry (jsonc)", vscode, w("e.json", "// c\n{\"servers\":{\"spicrawl\":{\"url\":\"x\"},},}"), true},
		{"vscode wrong key", vscode, w("f.json", `{"mcpServers":{"spicrawl":{}}}`), false},
		{"codex table", codex, w("g.toml", "[mcp_servers.spicrawl]\nurl = \"x\"\n"), true},
		{"codex inline", codex, w("h.toml", "mcp_servers.spicrawl = { url = \"x\" }\n"), true},
		{"codex other", codex, w("i.toml", "[mcp_servers.other]\nurl = \"x\"\n"), false},
	} {
		if got := mcpConfigured(tc.c, tc.file); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}
