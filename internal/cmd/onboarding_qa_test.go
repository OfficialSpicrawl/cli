package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OfficialSpicrawl/cli/internal/config"
)

// Re-running init with another key, saving only once the plan is accepted, the
// .bak copies of key files, and the docs of the one-prompt flow.

// persistConfig keeps the CLI config file across the runCLI calls of a test.
func persistConfig(t *testing.T) {
	t.Helper()
	testConfig = filepath.Join(t.TempDir(), "config.json")
	testKey = "" // $SPICRAWL_API_KEY is not exported in this shell
	t.Cleanup(func() { testConfig, testKey = "", "spicrawl_test_key" })
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ---- B2: a new key over the old one ----

// Entries we wrote earlier differ from the new ones only in the token: init
// replaces them without --force, keeps a .bak, and saves the new key.
func TestInitNewKeyReplacesOldKeyWithoutForce(t *testing.T) {
	persistConfig(t)
	base := onboardAPI(t)
	dir := onboardProject(t)
	home := os.Getenv("HOME")
	run := func(key string) (string, string, int) {
		return runCLI(t, base, "init", "--all", "--yes", "--api-key", key, "--dir", dir)
	}
	if _, errb, code := run(onboardOldKey); code != 0 {
		t.Fatalf("first run: exit %d: %s", code, errb)
	}
	out, errb, code := run(onboardKey)
	if code != 0 {
		t.Fatalf("second run: exit %d: %s", code, errb)
	}
	var rs []agentResult
	if err := json.Unmarshal([]byte(out), &rs); err != nil {
		t.Fatal(err)
	}
	for _, r := range rs {
		if r.Kind == "mcp" && r.Action != agentActionUpdated {
			t.Errorf("%+v", r)
		}
	}
	for _, f := range []string{".claude.json", ".cursor/mcp.json"} {
		now, bak := readFile(t, filepath.Join(home, f)), readFile(t, filepath.Join(home, f+".bak"))
		if !strings.Contains(now, onboardKey) || strings.Contains(now, onboardOldKey) {
			t.Errorf("%s:\n%s", f, now)
		}
		if !strings.Contains(bak, onboardOldKey) {
			t.Errorf("%s.bak does not keep the old file:\n%s", f, bak)
		}
	}
	if savedKey(t) != onboardKey {
		t.Error("the new key was not saved")
	}
	for _, k := range []string{onboardKey, onboardOldKey} {
		noKeyIn(t, k, "stdout", out)
		noKeyIn(t, k, "stderr", errb)
	}
}

// The same for a Codex config, where the pairs are edited in place; a changed
// URL is still a refusal.
func TestInitNewKeyCodexWithoutForce(t *testing.T) {
	persistConfig(t)
	base := onboardAPI(t)
	dir, home := onboardProject(t), os.Getenv("HOME")
	toml := filepath.Join(home, ".codex", "config.toml")
	args := func(key string) []string {
		return []string{"init", "--client", "codex", "--yes", "--api-key", key, "--dir", dir}
	}
	// A reference we wrote becomes the key; so does an older key.
	agentWrite(t, toml, "[mcp_servers.spicrawl]\nurl = \""+base+"/mcp\"\nbearer_token_env_var = \"SPICRAWL_API_KEY\"\n")
	if _, errb, code := runCLI(t, base, args(onboardOldKey)...); code != 0 {
		t.Fatalf("reference to key: exit %d: %s", code, errb)
	}
	if got := readFile(t, toml); !strings.Contains(got, onboardOldKey) || strings.Contains(got, "bearer_token_env_var") {
		t.Errorf("config.toml:\n%s", got)
	}
	if _, errb, code := runCLI(t, base, args(onboardKey)...); code != 0 {
		t.Fatalf("key to key: exit %d: %s", code, errb)
	}
	if got := readFile(t, toml); !strings.Contains(got, onboardKey) || strings.Contains(got, onboardOldKey) {
		t.Errorf("config.toml:\n%s", got)
	}
	// Another URL is the user's own entry.
	agentWrite(t, toml, "[mcp_servers.spicrawl]\nurl = \"https://mine.example/mcp\"\nbearer_token_env_var = \"SPICRAWL_API_KEY\"\n")
	_, errb, code := runCLI(t, base, args(onboardKey)...)
	if code != 2 || !strings.Contains(errb, "--force") {
		t.Errorf("a customised URL: exit %d: %s", code, errb)
	}
	noKeyIn(t, onboardKey, "stderr", errb)
}

func TestMCPOnlyAuthDiffers(t *testing.T) {
	entry := func(url, auth string, extra string) json.RawMessage {
		return json.RawMessage(`{"type":"http","url":"` + url + `","headers":{"Authorization":"` + auth + `"` + extra + `}}`)
	}
	want := entry("https://x/mcp", "Bearer spicrawl_live_NEW", "")
	for _, tc := range []struct {
		name string
		cur  json.RawMessage
		want bool
	}{
		{"another key", entry("https://x/mcp", "Bearer spicrawl_live_OLD", ""), true},
		{"a reference", entry("https://x/mcp", "Bearer ${SPICRAWL_API_KEY}", ""), true},
		{"a Cursor reference", entry("https://x/mcp", "Bearer ${env:SPICRAWL_API_KEY}", ""), true},
		{"another URL", entry("https://mine/mcp", "Bearer spicrawl_live_OLD", ""), false},
		{"another header too", entry("https://x/mcp", "Bearer spicrawl_live_OLD", `,"X-Mine":"1"`), false},
		{"not a bearer token", entry("https://x/mcp", "Basic abc", ""), false},
		{"a token with spaces", entry("https://x/mcp", "Bearer a b", ""), false},
		{"another shape", json.RawMessage(`{"command":"x"}`), false},
	} {
		if got := mcpOnlyAuthDiffers(tc.cur, want); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A refused plan writes and saves nothing, not even the key.
func TestInitRefusedPlanSavesNothing(t *testing.T) {
	persistConfig(t)
	base := onboardAPI(t)
	dir, home := onboardProject(t), os.Getenv("HOME")
	mine := `{"mcpServers":{"spicrawl":{"url":"https://mine.example/mcp","headers":{"Authorization":"Bearer ` + onboardOldKey + `"}}}}`
	agentWrite(t, filepath.Join(home, ".cursor", "mcp.json"), mine)
	out, errb, code := runCLI(t, base, "init", "--all", "--yes", "--api-key", onboardKey, "--dir", dir)
	if code != 2 || !strings.Contains(errb, "--force") {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if savedKey(t) != "" {
		t.Error("the key was saved although the plan was refused")
	}
	if got := readFile(t, filepath.Join(home, ".cursor", "mcp.json")); got != mine {
		t.Errorf("the refused file changed:\n%s", got)
	}
	for _, f := range []string{".claude.json", ".claude/skills/spicrawl/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(home, f)); err == nil {
			t.Errorf("%s was written although the plan was refused", f)
		}
	}
	for _, k := range []string{onboardKey, onboardOldKey} {
		noKeyIn(t, k, "stdout", out)
		noKeyIn(t, k, "stderr", errb)
	}
}

// status sees an embedded key that is not the one in use, never prints either,
// and the repair fixes it.
func TestStatusStaleEmbeddedKey(t *testing.T) {
	usesSavedKey(t) // onboardKey is saved and in use
	base := onboardAPI(t)
	dir, home := onboardProject(t), os.Getenv("HOME")
	t.Chdir(dir)
	entry := func(key string) string {
		return `{"mcpServers":{"spicrawl":{"type":"http","url":"` + base + `/mcp","headers":{"Authorization":"Bearer ` + key + `"}}}}`
	}
	agentWrite(t, filepath.Join(home, ".claude.json"), entry(onboardOldKey))
	agentWrite(t, filepath.Join(home, ".cursor", "mcp.json"), strings.Replace(entry(onboardKey), `"type":"http",`, "", 1))

	out, errb, code := runCLI(t, base, "status")
	var rep statusReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	if code != 1 || strings.Join(rep.MCP.Stale, ",") != "claude" || strings.Join(rep.MCP.Connected, ",") != "cursor" {
		t.Fatalf("exit %d, mcp %+v\n%s", code, rep.MCP, errb)
	}
	r := repairFor(rep, "mcp")
	if r == nil || !r.UnattendedSafe || r.Command != "spicrawl init --client claude --yes" {
		t.Fatalf("repair %+v", r)
	}
	for _, k := range []string{onboardKey, onboardOldKey} {
		noKeyIn(t, k, "stdout", out)
		noKeyIn(t, k, "stderr", errb)
	}
	if _, errb, code := runCLI(t, base, strings.Fields(r.Command)[1:]...); code != 0 {
		t.Fatalf("%s: exit %d: %s", r.Command, code, errb)
	}
	if rep, _, code := statusJSON(t, base); code != 0 || len(rep.Repairs) != 0 {
		t.Errorf("after the repair: exit %d, %+v", code, rep)
	}

	// With no key saved the repair needs the user's, and is not safe to run unattended.
	agentWrite(t, filepath.Join(home, ".claude.json"), entry(onboardOldKey))
	testConfig = filepath.Join(t.TempDir(), "config.json")
	out, _, _ = runCLI(t, base, "status", "--api-key", onboardKey)
	_ = json.Unmarshal([]byte(out), &rep)
	r = repairFor(rep, "mcp")
	if r == nil || r.UnattendedSafe || r.Command != "spicrawl init --client claude --yes --api-key <YOUR_API_KEY>" {
		t.Errorf("repair without a saved key: %+v", r)
	}
	noKeyIn(t, onboardKey, "stdout", out)
}

// ---- B3: .bak files and tracked files ----

// A project file that held a key leaves a .bak that holds it too: it is
// ignored from the start, and a second run keeps .gitignore as it is.
func TestInitProjectBackupIsGitignored(t *testing.T) {
	persistConfig(t)
	base := onboardAPI(t)
	dir := onboardProject(t)
	agentWrite(t, filepath.Join(dir, ".gitignore"), "node_modules/\n")
	run := func(key string) {
		t.Helper()
		if _, errb, code := runCLI(t, base, "init", "--all", "--yes", "--api-key", key, "--global=false", "--dir", dir); code != 0 {
			t.Fatalf("exit %d: %s", code, errb)
		}
	}
	run(onboardOldKey)
	gi := readFile(t, filepath.Join(dir, ".gitignore"))
	run(onboardKey)
	if bak := readFile(t, filepath.Join(dir, ".mcp.json.bak")); !strings.Contains(bak, onboardOldKey) {
		t.Errorf(".mcp.json.bak:\n%s", bak)
	}
	for _, want := range []string{".mcp.json\n", ".mcp.json.bak\n", ".cursor/mcp.json.bak\n"} {
		if !strings.Contains(gi, want) {
			t.Errorf(".gitignore lacks %q:\n%s", want, gi)
		}
	}
	if again := readFile(t, filepath.Join(dir, ".gitignore")); again != gi {
		t.Errorf("the second run changed .gitignore:\n%s", again)
	}
}

func gitInDir(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// A key file git already tracks would be committed with the key: say so, in the
// warning for a person and in the result's reason for a script.
func TestInitWarnsWhenTheKeyFileIsTracked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	persistConfig(t)
	base := onboardAPI(t)
	setup := func() string {
		dir := onboardProject(t)
		gitInDir(t, dir, "init", "-q")
		agentWrite(t, filepath.Join(dir, ".mcp.json"),
			`{"mcpServers":{"spicrawl":{"type":"http","url":"`+base+`/mcp","headers":{"Authorization":"Bearer ${SPICRAWL_API_KEY}"}}}}`)
		gitInDir(t, dir, "add", ".mcp.json")
		return dir
	}
	args := func(dir string) []string {
		return []string{"init", "--client", "claude", "--yes", "--api-key", onboardKey, "--global=false", "--dir", dir}
	}

	dir := setup()
	_, errb, code := runCLIHuman(t, base, args(dir)...)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if !strings.Contains(errb, ".mcp.json is tracked by git") || !strings.Contains(errb, "git rm --cached .mcp.json") {
		t.Errorf("no warning:\n%s", errb)
	}
	noKeyIn(t, onboardKey, "stderr", errb)

	out, errb, code := runCLI(t, base, args(setup())...)
	if code != 0 || errb != "" {
		t.Fatalf("JSON: exit %d, stderr %q", code, errb)
	}
	var rs []agentResult
	_ = json.Unmarshal([]byte(out), &rs)
	if len(rs) == 0 || !strings.Contains(rs[0].Reason, "tracked by git: git rm --cached .mcp.json") {
		t.Errorf("reason: %+v", rs)
	}

	// A file git does not track gets no warning.
	untracked := onboardProject(t)
	gitInDir(t, untracked, "init", "-q")
	_, errb, code = runCLIHuman(t, base, args(untracked)...)
	if code != 0 || strings.Contains(errb, "tracked by git") {
		t.Errorf("untracked: exit %d\n%s", code, errb)
	}
}

// ---- B4: the key is saved once the changes are accepted ----

func TestInitKeyIsSavedOnlyAfterConfirmation(t *testing.T) {
	persistConfig(t)
	base := onboardAPI(t)

	// No terminal and no --yes: refused, nothing saved or written.
	dir := onboardProject(t)
	home := os.Getenv("HOME")
	_, errb, code := runCLI(t, base, "init", "--all", "--api-key", onboardKey, "--dir", dir)
	if code != 2 || !strings.Contains(errb, "--yes") {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if savedKey(t) != "" {
		t.Error("the key was saved before the confirmation")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); err == nil {
		t.Error("a config was written without --yes")
	}

	// At a terminal: "n" saves nothing, "y" saves and writes.
	orig, origIn := initStdinIsTerminal, initStdin
	initStdinIsTerminal = func() bool { return true }
	t.Cleanup(func() { initStdinIsTerminal, initStdin = orig, origIn })
	for _, tc := range []struct {
		answer string
		saved  bool
	}{{"n\n", false}, {"y\n", true}} {
		initStdin = strings.NewReader(tc.answer)
		out, errb, code := runCLIHuman(t, base, "init", "--all", "--api-key", onboardKey, "--dir", dir)
		if code != 0 {
			t.Fatalf("%q: exit %d: %s", tc.answer, code, errb)
		}
		if !strings.Contains(errb, "save    ") || !strings.Contains(errb, "key") {
			t.Errorf("%q: the plan does not mention saving the key:\n%s", tc.answer, errb)
		}
		if got := savedKey(t) == onboardKey; got != tc.saved {
			t.Errorf("%q: key saved = %v, want %v", tc.answer, got, tc.saved)
		}
		_, err := os.Stat(filepath.Join(home, ".claude.json"))
		if (err == nil) != tc.saved {
			t.Errorf("%q: config written = %v, want %v\n%s\n%s", tc.answer, err == nil, tc.saved, out, errb)
		}
		noKeyIn(t, onboardKey, "stdout", out)
		noKeyIn(t, onboardKey, "stderr", errb)
	}
}

// Nothing to change, but a key to save: it is saved.
func TestInitSavesTheKeyWhenNothingElseChanges(t *testing.T) {
	persistConfig(t)
	base := onboardAPI(t)
	dir := onboardProject(t)
	if _, errb, code := runCLI(t, base, "init", "--all", "--yes", "--api-key", onboardKey, "--dir", dir); code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if err := os.Remove(testConfig); err != nil {
		t.Fatal(err)
	}
	if _, errb, code := runCLI(t, base, "init", "--all", "--api-key", onboardKey, "--dir", dir); code != 0 {
		t.Fatalf("exit %d: %s", code, errb) // no --yes: nothing is pending
	}
	if savedKey(t) != onboardKey {
		t.Error("the key was not saved")
	}
}

// ---- B5: the docs of the flow ----

func TestOnePromptDocs(t *testing.T) {
	readme := readFile(t, filepath.Join("..", "..", "README.md"))
	i := strings.Index(readme, "Set up Spicrawl for yourself.")
	if i < 0 {
		t.Fatal("the prompt is not in the README")
	}
	prompt := readme[i : i+strings.Index(readme[i:], "\n")]
	for _, want := range []string{
		"npx -y @spicrawl/cli@latest init --all --yes --api-key <YOUR_API_KEY>",
		"npx -y @spicrawl/cli@latest status --json",
		"run npx -y @spicrawl/cli@latest scrape https://news.ycombinator.com and tell me today's top story",
		"Never repeat my API key back to me.",
		"New MCP tools appear only in a session started after the install.",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
	if strings.Contains(prompt, "spicrawl_live_") || strings.Contains(prompt, config.EnvAPIKey) {
		t.Error("the prompt holds a key, not the <YOUR_API_KEY> placeholder")
	}
	if strings.Contains(readme, "Without `--json`, `status` prints") {
		t.Error("the README claims status prints lines without --json; piped it prints JSON")
	}

	// The npm launcher looks SPICRAWL_BINARY up once.
	js := readFile(t, filepath.Join("..", "..", "npm", "bin", "spicrawl.js"))
	if n := strings.Count(js, "process.env.SPICRAWL_BINARY) return"); n != 1 {
		t.Errorf("the launcher reads SPICRAWL_BINARY %d times", n)
	}
	if strings.Contains(js, "fallback") {
		t.Error("the launcher comment still mentions a fallback name")
	}
}
