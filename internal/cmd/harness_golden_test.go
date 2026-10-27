package cmd

// Golden tests: they lock what the CLI writes to stdout (byte for byte) and the
// exit code it returns, for every command and its main modes, so that terminal
// work (spinners, colour, TUI) cannot change what scripts and AI agents parse.
// DESIGN.md "Contract agents rely on" is the rule they enforce: stdout is
// data only, JSON when stdout is not a terminal, stable exit codes, and the
// problem document on stderr in JSON mode.
//
// Every case runs three times against its own fake API server, with the
// environment a non-interactive caller can have: nothing set, NO_COLOR=1 and
// CI=true. All three must produce the same stdout, the same exit code and a
// stderr (and stdout) with no ANSI escape, so a feature that colours or
// animates must stay off when the output is not a terminal.
//
// Files (testdata/golden/<case name>.<ext>):
//
//	.stdout         stdout, normalized (see goldenNormalize)
//	.stdout.sha256  instead of .stdout for outputs too big to review (digest case flag)
//	.exit           the exit code
//	.stderr         stderr, only for cases where stderr is itself machine output
//	                (JSON mode: the problem document); other stderr is prose
//	.file.<path>    a file the command was asked to write (-o, --screenshot-dir)
//
// Update them after an intended change, then review the diff:
//
//	go test ./internal/cmd -run TestGolden -update      (or GOLDEN_UPDATE=1 go test ./...)
//
// (-update is a flag of this package's test binary only, so use GOLDEN_UPDATE=1
// when running ./... .) -update also deletes golden files no case produces.
//
// The default mode of a case is what a script sees: stdout not a terminal, so
// JSON unless the command says otherwise (docs, raw documents). A case with
// human set runs the code path a person at a terminal gets (humanOutput), which
// is the only way to reach it without a pty.

import (
	"bytes"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/OfficialSpicrawl/cli/internal/api"
)

var goldenUpdate = flag.Bool("update", false, "rewrite the files under testdata/golden (or set GOLDEN_UPDATE=1)")

func goldenWantUpdate() bool { return *goldenUpdate || os.Getenv("GOLDEN_UPDATE") == "1" }

// goldenKey is the fake API key every case runs with (unless noKey).
const goldenKey = "spicrawl_test_0123456789abcdefghij"

// goldenFlagKey is the key cases pass with --api-key. No output may carry
// either key.
const goldenFlagKey = "spicrawl_live_GOLDENFLAG0123456789xyz"

// goldenCase is one invocation of the CLI.
type goldenCase struct {
	name  string   // "group/case": the golden file stem; unique
	args  []string // after the global flags; {server}, {dir} and {home} are replaced
	human bool     // run the human (terminal) code path instead of the script one

	explicitJSON bool                    // pass --json (the default for a script is the same)
	noKey        bool                    // no API key in the environment
	baseURL      string                  // --base-url, default: the fake server
	server       func() http.HandlerFunc // the fake API; built fresh for every run
	env          map[string]string       // extra environment; tokens as in args
	stdin        string                  // what os.Stdin holds (never a terminal)
	setup        func(t *testing.T, e *goldenEnv)

	stderr bool     // golden stderr too: it is machine-readable in this case
	digest bool     // golden a sha256 of stdout instead of the bytes (big outputs)
	files  []string // paths under {dir} the command writes; their bytes are golden too
}

// goldenEnv is the sandbox of one run.
type goldenEnv struct {
	Dir    string // working/project directory
	Home   string // $HOME
	CfgDir string // holds config.json ($SPICRAWL_CONFIG)
	Server string // fake API URL, "" when the case has none
}

var goldenCases []goldenCase

// goldenAdd registers cases; each *_golden_test.go calls it from init().
func goldenAdd(cs ...goldenCase) {
	seen := map[string]bool{}
	for _, c := range goldenCases {
		seen[c.name] = true
	}
	for _, c := range cs {
		if seen[c.name] {
			panic("duplicate golden case " + c.name)
		}
		seen[c.name] = true
		goldenCases = append(goldenCases, c)
	}
}

// goldenVariants are the environments every case must be insensitive to.
var goldenVariants = []struct {
	name string
	env  map[string]string
}{
	{"plain", nil},
	{"NO_COLOR", map[string]string{"NO_COLOR": "1"}},
	{"CI", map[string]string{"CI": "true"}},
}

func TestGolden(t *testing.T) {
	for _, c := range goldenCases {
		t.Run(strings.ReplaceAll(c.name, "/", "."), func(t *testing.T) {
			for _, v := range goldenVariants {
				t.Run(v.name, func(t *testing.T) {
					goldenRun(t, c, goldenWantUpdate() && v.name == "plain", v.env)
				})
			}
		})
	}
}

func goldenRun(t *testing.T, c goldenCase, update bool, variantEnv map[string]string) {
	t.Helper()
	r, e := goldenExec(t, c, variantEnv)
	norm := func(s string) string { return goldenNormalize(s, e) }

	// Not a terminal, so never colour or animation, whatever NO_COLOR/CI say.
	for name, s := range map[string]string{"stdout": r.stdout, "stderr": r.stderr} {
		if strings.Contains(s, "\x1b") {
			t.Errorf("%s has an ANSI escape on a non-terminal: %q", name, s)
		}
		for _, k := range []string{goldenKey, goldenFlagKey} {
			if strings.Contains(s, k) {
				t.Errorf("%s contains the API key: %q", name, s)
			}
		}
	}
	if strings.Contains(r.stderr, "\r") {
		t.Errorf("stderr has a carriage return (an in-place spinner or progress line) on a non-terminal: %q", r.stderr)
	}
	if r.stray != "" {
		t.Errorf("the command wrote to os.Stdout directly instead of the stdout writer: %q", r.stray)
	}

	out := norm(r.stdout)
	if c.digest {
		sum := sha256.Sum256([]byte(out))
		goldenCompare(t, update, c.name, ".stdout.sha256", []byte(fmt.Sprintf("sha256 %x\nbytes %d\n", sum, len(out))))
	} else {
		goldenCompare(t, update, c.name, ".stdout", []byte(out))
	}
	goldenCompare(t, update, c.name, ".exit", []byte(fmt.Sprintf("%d\n", r.code)))
	if c.stderr {
		goldenCompare(t, update, c.name, ".stderr", []byte(norm(r.stderr)))
	}
	for _, rel := range c.files {
		b, err := os.ReadFile(filepath.Join(e.Dir, rel))
		if err != nil {
			t.Errorf("expected the command to write %s: %v", rel, err)
			continue
		}
		goldenCompare(t, update, c.name, ".file."+strings.ReplaceAll(rel, "/", "__"), []byte(norm(string(b))))
	}
}

type goldenResult struct {
	stdout, stderr, stray string
	code                  int
}

// goldenExec runs c in a fresh sandbox with the variant's extra environment.
func goldenExec(t *testing.T, c goldenCase, variantEnv map[string]string) (goldenResult, *goldenEnv) {
	t.Helper()
	e := &goldenEnv{Dir: t.TempDir(), Home: t.TempDir(), CfgDir: t.TempDir()}

	// A clean, deterministic environment, whatever the developer's shell has.
	for _, k := range []string{
		"NO_COLOR", "CI", "FORCE_COLOR", "CLICOLOR", "CLICOLOR_FORCE", "SPICRAWL_API_KEY", "SPICRAWL_BASE_URL",
		agentEnvMCPURL, agentEnvSkillURL, docsEnvURL, docsEnvHost,
	} {
		goldenUnsetenv(t, k)
	}
	t.Setenv("HOME", e.Home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("TERM", "xterm-256color") // a colour-capable TERM: only the non-terminal stdout may keep colour off
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("SPICRAWL_CONFIG", filepath.Join(e.CfgDir, "config.json"))
	if !c.noKey {
		t.Setenv("SPICRAWL_API_KEY", goldenKey)
	}

	if c.server != nil {
		srv := httptest.NewServer(c.server())
		t.Cleanup(srv.Close)
		e.Server = srv.URL
	}
	tokens := strings.NewReplacer("{server}", e.Server, "{dir}", e.Dir, "{home}", e.Home)
	for k, v := range c.env {
		t.Setenv(k, tokens.Replace(v))
	}
	for k, v := range variantEnv {
		t.Setenv(k, v)
	}
	if c.setup != nil {
		c.setup(t, e)
	}

	base := c.baseURL
	if base == "" {
		base = e.Server
	}
	args := []string{"--base-url", base}
	if c.explicitJSON {
		args = append(args, "--json")
	}
	for _, a := range c.args {
		args = append(args, tokens.Replace(a))
	}

	// Never a terminal: not even when `go test` itself runs in one.
	tmp := func(name string) *os.File {
		f, err := os.CreateTemp(t.TempDir(), name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		return f
	}
	in, so, se := tmp("stdin"), tmp("stdout"), tmp("stderr")
	_, _ = in.WriteString(c.stdin)
	_, _ = in.Seek(0, 0)

	var (
		ob, eb bytes.Buffer
		code   int
	)
	func() {
		prevIn, prevOut, prevErr := os.Stdin, os.Stdout, os.Stderr
		prevW, prevEW, prevHuman := stdout, stderr, humanOutput
		os.Stdin, os.Stdout, os.Stderr = in, so, se
		stdout, stderr, humanOutput = &ob, &eb, c.human
		defer func() {
			os.Stdin, os.Stdout, os.Stderr = prevIn, prevOut, prevErr
			stdout, stderr, humanOutput = prevW, prevEW, prevHuman
			resetFlags(rootCmd)
		}()
		rootCmd.SetArgs(args)
		code = Execute()
	}()
	stray, _ := os.ReadFile(so.Name())
	return goldenResult{stdout: ob.String(), stderr: eb.String(), stray: string(stray), code: code}, e
}

// goldenUnsetenv removes k for the test (t.Setenv alone can only set it).
func goldenUnsetenv(t *testing.T, k string) {
	t.Helper()
	t.Setenv(k, "")
	_ = os.Unsetenv(k)
}

// goldenCompare checks got against testdata/golden/<name><ext>, or rewrites
// the file when update is set.
func goldenCompare(t *testing.T, update bool, name, ext string, got []byte) {
	t.Helper()
	path := goldenPath(name, ext)
	if update {
		if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, got) {
			return
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no golden file %s (run: go test ./internal/cmd -run TestGolden -update)", path)
	}
	if !bytes.Equal(want, got) {
		t.Errorf("%s differs from the golden file (intended? run: go test ./internal/cmd -run TestGolden -update)\n%s", path, goldenDiff(want, got))
	}
}

func goldenPath(name, ext string) string {
	return filepath.Join("testdata", "golden", filepath.FromSlash(name)+ext)
}

// goldenDiff shows the first line where want and got differ.
func goldenDiff(want, got []byte) string {
	w, g := strings.Split(string(want), "\n"), strings.Split(string(got), "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl || i >= len(w) || i >= len(g) {
			return fmt.Sprintf("first difference at line %d:\n  want %q\n  got  %q\n(want %d bytes, got %d bytes)", i+1, wl, gl, len(want), len(got))
		}
	}
	return "(same lines, different bytes)"
}

// TestGoldenNoOrphans fails when testdata/golden holds a file no registered
// case produces (a renamed or deleted case); -update removes them.
func TestGoldenNoOrphans(t *testing.T) {
	want := map[string]bool{}
	for _, c := range goldenCases {
		stdoutExt := ".stdout"
		if c.digest {
			stdoutExt = ".stdout.sha256"
		}
		exts := []string{stdoutExt, ".exit"}
		if c.stderr {
			exts = append(exts, ".stderr")
		}
		for _, f := range c.files {
			exts = append(exts, ".file."+strings.ReplaceAll(f, "/", "__"))
		}
		for _, ext := range exts {
			want[filepath.ToSlash(goldenPath(c.name, ext))] = true
		}
	}
	var orphans []string
	_ = filepath.WalkDir(filepath.Join("testdata", "golden"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() != ".gitattributes" && !want[filepath.ToSlash(p)] {
			orphans = append(orphans, p)
		}
		return nil
	})
	sort.Strings(orphans)
	for _, p := range orphans {
		if goldenWantUpdate() {
			_ = os.Remove(p)
			continue
		}
		t.Errorf("orphan golden file %s: no case produces it (run: go test ./internal/cmd -run TestGolden -update)", p)
	}
}

// ---- normalization ----------------------------------------------------------

var (
	goldenTimestampRE = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})`)
	goldenReqIDJSONRE = regexp.MustCompile(`("request_id":\s*")[^"]+(")`)
	goldenReqIDTextRE = regexp.MustCompile(`(request id: )\S+( \(spicrawl logs get )\S+(\))`)
)

// goldenNormalize turns the parts of an output that differ between runs and
// machines into fixed placeholders, so a golden file changes only when the
// CLI's output does. It is applied to stdout, stderr and written files:
//
//   - the fake server's address (random port)      -> SERVER (127.0.0.1:41233 -> SERVER)
//   - the sandbox directories (random temp paths)  -> <DIR>, <HOME>, <CONFIG_DIR>
//   - build identity: CLI version, Go version, OS and architecture
//     (api.Version is stamped by -ldflags; the others are the host's)
//     -> <VERSION>, <GOVERSION>, <GOOS>, <GOARCH>
//   - RFC 3339 timestamps (the fixtures' and any the CLI might stamp)  -> <TIMESTAMP>
//   - non-empty request ids ("request_id" fields, "request id:" lines) -> <REQUEST_ID>
//
// Empty values are left alone, so a field that goes missing or empty still
// shows. The CLI prints no measured durations today (the durations in its output
// are flags echoed back, e.g. "after 30m0s"), so there is nothing to mask; if
// one is ever added, add its pattern here rather than letting goldens flake.
func goldenNormalize(s string, e *goldenEnv) string {
	if e != nil {
		for _, d := range []struct{ path, name string }{{e.Dir, "<DIR>"}, {e.Home, "<HOME>"}, {e.CfgDir, "<CONFIG_DIR>"}} {
			s = strings.ReplaceAll(s, d.path, d.name)
			if real, err := filepath.EvalSymlinks(d.path); err == nil && real != d.path { // macOS: /var -> /private/var
				s = strings.ReplaceAll(s, real, d.name)
			}
		}
		if e.Server != "" {
			s = strings.ReplaceAll(s, strings.TrimPrefix(e.Server, "http://"), "SERVER")
		}
	}
	for _, r := range [][2]string{
		{`"version": "` + api.Version + `"`, `"version": "<VERSION>"`},
		{"spicrawl " + api.Version + " (", "spicrawl <VERSION> ("},
		{runtime.Version(), "<GOVERSION>"},
		{`"os": "` + runtime.GOOS + `"`, `"os": "<GOOS>"`},
		{`"arch": "` + runtime.GOARCH + `"`, `"arch": "<GOARCH>"`},
		{runtime.GOOS + "/" + runtime.GOARCH + ")", "<GOOS>/<GOARCH>)"},
	} {
		s = strings.ReplaceAll(s, r[0], r[1])
	}
	s = goldenTimestampRE.ReplaceAllString(s, "<TIMESTAMP>")
	s = goldenReqIDJSONRE.ReplaceAllString(s, "${1}<REQUEST_ID>${2}")
	s = goldenReqIDTextRE.ReplaceAllString(s, "${1}<REQUEST_ID>${2}<REQUEST_ID>${3}")
	return s
}

func TestGoldenNormalize(t *testing.T) {
	e := &goldenEnv{Dir: "/tmp/x/001", Home: "/tmp/x/002", CfgDir: "/tmp/x/003", Server: "http://127.0.0.1:41233"}
	in := "ws://127.0.0.1:41233/v1 /tmp/x/001/a /tmp/x/003/config.json 2026-09-22T06:00:00.5Z 2026-09-22T06:00:00+02:00 " +
		`"request_id": "01J9ZQ5N" "request_id": "" request id: 01ABC (spicrawl logs get 01ABC) ` +
		`"version": "` + api.Version + `" ` + runtime.Version() + " 2026-09-22"
	want := "ws://SERVER/v1 <DIR>/a <CONFIG_DIR>/config.json <TIMESTAMP> <TIMESTAMP> " +
		`"request_id": "<REQUEST_ID>" "request_id": "" request id: <REQUEST_ID> (spicrawl logs get <REQUEST_ID>) ` +
		`"version": "<VERSION>" <GOVERSION> 2026-09-22`
	if got := goldenNormalize(in, e); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// ---- fake API building blocks -------------------------------------------------

// goldenServe builds a fake API from "METHOD /path" patterns (net/http ServeMux
// syntax, so "GET /v1/batch/{id}" works). Anything else is a 404 problem.
func goldenServe(routes map[string]http.HandlerFunc) func() http.HandlerFunc {
	return func() http.HandlerFunc {
		mux := http.NewServeMux()
		for pattern, h := range routes {
			mux.HandleFunc(pattern, h)
		}
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			writeProblem(w, http.StatusNotFound, "ERR::REQUEST::NOT_FOUND", false)
		})
		return mux.ServeHTTP
	}
}

// gJSON answers with a JSON body.
func gJSON(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// gProblem answers with an RFC 7807 problem document (a full one: detail,
// request id and a hint, so the human error renderer has every line to show).
func gProblem(status int, code, detail string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.Header().Set("X-Request-Id", "01J9ZQ9ERR0000000000000000")
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"type":"https://docs.spicrawl.com/errors","title":%q,"status":%d,"code":%q,"detail":%q,"retryable":false,"doc_url":"https://docs.spicrawl.com/errors","request_id":"01J9ZQ9ERR0000000000000000","target_status":null,"diagnostics":{"hint":"check the request and try again"}}`,
			http.StatusText(status), status, code, detail)
	}
}

// goldenWrite creates path (relative to dir) with content, making parent
// directories, for a case's setup.
func goldenWrite(t *testing.T, dir, rel, content string) string {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
