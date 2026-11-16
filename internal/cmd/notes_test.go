package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// humanRunCLI runs the CLI the way a person at a terminal gets it: humanOutput
// set and no --json. (--json=false does NOT select human mode: the printer
// also goes JSON whenever os.Stdout is not a terminal, which it never is in tests.)
func humanRunCLI(t *testing.T, baseURL string, args ...string) (string, string, int) {
	t.Helper()
	scrapeCmd.SetContext(nil) //nolint:staticcheck // see scrapeRunCLI
	t.Setenv("SPICRAWL_CONFIG", t.TempDir()+"/config.json")
	t.Setenv("SPICRAWL_API_KEY", testKey)
	var out, errb bytes.Buffer
	prevW, prevE, prevH := stdout, stderr, humanOutput
	stdout, stderr, humanOutput = &out, &errb, true
	defer func() {
		stdout, stderr, humanOutput = prevW, prevE, prevH
		resetFlags(rootCmd)
	}()
	rootCmd.SetArgs(append([]string{"--base-url", baseURL}, args...))
	code := Execute()
	return out.String(), errb.String(), code
}

const noteDoc = "# Hello\n"

// noteRaw serves a raw markdown document with the given target status and X-Note headers.
func noteRaw(target int, notes ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "text/markdown; charset=utf-8")
		h.Set("X-Request-Id", "01REQ")
		h.Set("X-Credits-Charged", "1")
		h.Set("X-Engine", "fetch")
		h.Set("X-Target-Status", fmt.Sprint(target))
		for _, n := range notes {
			h.Add("X-Note", n)
		}
		_, _ = io.WriteString(w, noteDoc)
	}
}

func noteServer(t *testing.T, h http.HandlerFunc) string {
	srv, _ := newRecordingServer(t, h)
	return srv.URL
}

func TestNoteForHuman(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Set `js_render` to true to get the page as a browser shows it.", "Set --render to get the page as a browser shows it."},
		{"`js_render=true`", "--render"},
		{"set `premium_proxy` to true for a residential exit, or pass your own in `proxy`.", "set --premium-proxy for a residential exit, or pass your own in --proxy."},
		{"`proxy_verify=true`", "--proxy-verify"},
		// regex: (=true)? only matches "=true", so "=html" is not consumed and no
		// backtick pair matches ("`response_format=html`" has "=html" before the
		// closing tick): the text stays unchanged.
		{"`response_format=html`", "`response_format=html`"},
		{"`response_format`", "--format"},
		{"use js_render for this", "use js_render for this"},
		{"try `render_all` instead", "try `render_all` instead"},
		{"nothing to translate here.", "nothing to translate here."},
		{"`stealth` to true", "--stealth"},
		{"", ""},
	} {
		if got := noteForHuman(c.in); got != c.want {
			t.Errorf("noteForHuman(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestScrapeHumanNoteStdoutUnchanged(t *testing.T) {
	const n1 = "Set `js_render` to true to render the page."
	const n2 = "Or use `premium_proxy=true`."
	u := "https://example.com"
	baseOut, baseErr, baseCode := humanRunCLI(t, noteServer(t, noteRaw(200)), "scrape", u, "--format", "markdown")
	if baseOut != noteDoc || baseCode != 0 || strings.Contains(baseErr, "note:") {
		t.Fatalf("baseline: %q %q %d", baseOut, baseErr, baseCode)
	}

	out, errs, code := humanRunCLI(t, noteServer(t, noteRaw(200, n1)), "scrape", u, "--format", "markdown")
	if out != baseOut || code != 0 {
		t.Errorf("stdout %q code %d, want %q", out, code, baseOut)
	}
	if want := baseErr + "note: Set --render to render the page.\n"; errs != want {
		t.Errorf("stderr %q, want %q", errs, want)
	}

	out, errs, _ = humanRunCLI(t, noteServer(t, noteRaw(200, n1, n2)), "scrape", u, "--format", "markdown")
	if out != baseOut {
		t.Errorf("stdout %q", out)
	}
	if want := baseErr + "note: Set --render to render the page.\nnote: Or use --premium-proxy.\n"; errs != want {
		t.Errorf("stderr %q, want %q", errs, want)
	}
}

func TestScrapeJSONRawNotes(t *testing.T) {
	const n = "Set `js_render` to true to render."
	u := "https://example.com"
	out, errs, code := scrapeRunCLI(t, noteServer(t, noteRaw(200, n)), "scrape", u, "--format", "markdown")
	if code != 0 || strings.Contains(errs, "note") {
		t.Fatalf("code %d stderr %q", code, errs)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	got, _ := m["notes"].([]any)
	if len(got) != 1 || got[0] != n {
		t.Errorf("notes = %v, want the untranslated %q", m["notes"], n)
	}

	out, _, _ = scrapeRunCLI(t, noteServer(t, noteRaw(200)), "scrape", u, "--format", "markdown")
	if !strings.Contains(out, `"notes": []`) {
		t.Errorf("no header: want \"notes\": [] in %s", out)
	}
}

func noteEnvelope(withHeader bool) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if withHeader {
			w.Header().Set("X-Note", "header note")
		}
		_, _ = io.WriteString(w, `{"url":"https://example.com","status":200,"content":"x","warnings":[],"notes":["body note"]}`)
	}
}

func TestScrapeJSONEnvelopeNotes(t *testing.T) {
	u := "https://example.com"
	out, _, code := scrapeRunCLI(t, noteServer(t, noteEnvelope(false)), "scrape", u, "--format", "json")
	if code != 0 || strings.Count(out, "body note") != 1 || strings.Count(out, `"notes"`) != 1 {
		t.Errorf("code %d out %s", code, out)
	}
	// Envelope with its own notes AND an X-Note header: the envelope body is
	// printed as the API sent it (RawJSON), so the body's notes win and the
	// header's text is not merged in.
	out, _, _ = scrapeRunCLI(t, noteServer(t, noteEnvelope(true)), "scrape", u, "--format", "json")
	if strings.Contains(out, "header note") || strings.Count(out, "body note") != 1 {
		t.Errorf("recorded behaviour changed (body notes should win): %s", out)
	}
	// Human mode, same response: the header wins (notes() prefers X-Note).
	_, errs, _ := humanRunCLI(t, noteServer(t, noteEnvelope(true)), "scrape", u, "--format", "json")
	if !strings.Contains(errs, "note: header note\n") || strings.Contains(errs, "body note") {
		t.Errorf("human stderr %q", errs)
	}
	// Human mode, envelope notes only.
	_, errs, _ = humanRunCLI(t, noteServer(t, noteEnvelope(false)), "scrape", u, "--format", "json")
	if !strings.Contains(errs, "note: body note\n") {
		t.Errorf("human stderr %q", errs)
	}
}

func TestScrapeRefusedTargetNote(t *testing.T) {
	const n = "The target refused us; set `premium_proxy` to true."
	u := "https://example.com"
	srv := noteServer(t, noteRaw(403, n))

	// before the feature: same refusal without a note
	_, plainErr, plainCode := humanRunCLI(t, noteServer(t, noteRaw(403)), "scrape", u, "--format", "markdown")
	if plainErr != "error: target returned 403 (pass --allowed-status 403 to accept it)\n" {
		t.Fatalf("baseline stderr %q", plainErr)
	}

	out, errs, code := humanRunCLI(t, srv, "scrape", u, "--format", "markdown")
	want := "error: target returned 403 (pass --allowed-status 403 to accept it)\nnote: The target refused us; set --premium-proxy.\n"
	if errs != want || strings.Count(errs, "note:") != 1 {
		t.Errorf("stderr %q, want %q", errs, want)
	}
	if code != plainCode || code == 0 {
		t.Errorf("exit %d, before-feature exit %d", code, plainCode)
	}
	if out != noteDoc {
		t.Errorf("stdout %q", out)
	}

	_, jerr, jcode := scrapeRunCLI(t, srv, "scrape", u, "--format", "markdown")
	if jcode != code || !strings.Contains(jerr, n) {
		t.Errorf("json: exit %d stderr %q (want untranslated %q)", jcode, jerr, n)
	}

	out, errs, code = humanRunCLI(t, srv, "scrape", u, "--format", "markdown", "--allowed-status", "403")
	if code != 0 || out != noteDoc || errs != "note: The target refused us; set --premium-proxy.\n" {
		t.Errorf("allowed: exit %d out %q err %q", code, out, errs)
	}
}

func noteProblem(diag, notes string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(502)
		fmt.Fprintf(w, `{"title":"Bad Gateway","status":502,"code":"ERR::SCRAPE::FAILED","detail":"fetch failed","retryable":false,"request_id":"01REQ"%s%s}`, diag, notes)
	}
}

func TestScrapeFailureHints(t *testing.T) {
	u := "https://example.com"
	const hint = `,"diagnostics":{"hint":"Try ` + "`js_render=true`" + ` or ` + "`stealth` to true" + `."}`
	const notes = `,"notes":["First ` + "`premium_proxy` to true" + `.","second"]`

	_, errs, code := humanRunCLI(t, noteServer(t, noteProblem(hint, notes)), "scrape", u)
	if !strings.Contains(errs, "hint: Try --render or --stealth.\n") || code == 0 {
		t.Errorf("hint+notes: code %d %q", code, errs)
	}
	_, errs, _ = humanRunCLI(t, noteServer(t, noteProblem("", notes)), "scrape", u)
	if !strings.Contains(errs, "hint: First --premium-proxy.\n") || strings.Contains(errs, "second") {
		t.Errorf("notes only: %q", errs)
	}
	_, base, baseCode := humanRunCLI(t, noteServer(t, noteProblem("", "")), "scrape", u)
	if strings.Contains(base, "hint:") || strings.Contains(base, "note") || baseCode != code {
		t.Errorf("neither: code %d %q", baseCode, base)
	}
	if want := "error: ERR::SCRAPE::FAILED"; !strings.Contains(base, want) {
		t.Errorf("neither: %q", base)
	}

	// JSON mode: the problem document, notes intact and untranslated.
	_, jerr, jcode := scrapeRunCLI(t, noteServer(t, noteProblem(hint, notes)), "scrape", u)
	var m map[string]any
	if err := json.Unmarshal([]byte(jerr), &m); err != nil {
		t.Fatalf("%v: %q", err, jerr)
	}
	ns, _ := m["notes"].([]any)
	if jcode != code || len(ns) != 2 || ns[0] != "First `premium_proxy` to true." {
		t.Errorf("json: exit %d notes %v", jcode, m["notes"])
	}
	if strings.Contains(jerr, "--render") {
		t.Errorf("json stderr was translated: %q", jerr)
	}
}

// --json=false does not give human mode: stdout is not a terminal, so errors
// and documents are still JSON.
func TestJSONFalseDoesNotForceHuman(t *testing.T) {
	srv := noteServer(t, noteRaw(200, "Set `js_render` to true."))
	out, errs, code := scrapeRunCLI(t, srv, "--json=false", "scrape", "https://example.com", "--format", "markdown")
	if code != 0 || !strings.HasPrefix(strings.TrimSpace(out), "{") || strings.Contains(errs, "note:") {
		t.Errorf("code %d out %q err %q", code, out, errs)
	}
}
