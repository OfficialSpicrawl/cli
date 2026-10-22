package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/OfficialSpicrawl/cli/internal/schema"
)

// Product rule: a customer never reads an engine or browser name (internalNames,
// live_test.go) from the CLI. Engine routing is internal; the public vocabulary
// is the tier: fetch, js, browser. These tests lock that for the text the CLI
// writes, whatever the API sends. Machine output (--json, --jsonl) is the API's
// own data and stays as sent.

// wireField is the one name the API's own specs and skill keep: `headless` is a
// wire field of the API (deprecated and ignored on scrape and batch; a /v1/browser
// option). Everything else in them is tier vocabulary.
var wireField = regexp.MustCompile(`\bheadless\b`)

func TestHelpNeverNamesAnEngine(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		var buf bytes.Buffer
		c.SetOut(&buf)
		c.SetErr(&buf)
		if err := c.Help(); err != nil {
			t.Fatalf("%s: %v", c.CommandPath(), err)
		}
		c.SetOut(nil)
		c.SetErr(nil)
		if m := internalNames.FindString(buf.String()); m != "" {
			t.Errorf("%q --help names %q:\n%s", c.CommandPath(), m, buf.String())
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)
}

// The scrape flags the API ignores stay accepted, so no script breaks, but are
// not advertised.
func TestIgnoredEngineFlagsAreHidden(t *testing.T) {
	for _, c := range []*cobra.Command{scrapeCmd, batchSubmitCmd, browserURLCmd} {
		if fl := c.Flags().Lookup("headless"); fl == nil || !fl.Hidden {
			t.Errorf("%s --headless must exist and be hidden: %+v", c.CommandPath(), fl)
		}
	}
	for _, c := range []*cobra.Command{scrapeCmd, batchSubmitCmd} {
		if fl := c.Flags().Lookup("engine"); fl == nil || !fl.Hidden {
			t.Errorf("%s --engine must exist and be hidden: %+v", c.CommandPath(), fl)
		}
	}
	// sessions and browser url take a tier, and say so.
	for c, want := range map[*cobra.Command]string{sessionsCreateCmd: "fetch|js|browser", browserURLCmd: "browser|js"} {
		fl := c.Flags().Lookup("engine")
		if fl == nil || fl.Hidden || !strings.Contains(fl.Usage, want) {
			t.Errorf("%s --engine should list %s: %+v", c.CommandPath(), want, fl)
		}
	}
}

func TestEmbeddedTextNeverNamesAnEngine(t *testing.T) {
	if m := internalNames.FindString(wireField.ReplaceAllString(string(skillEmbedded), "")); m != "" {
		t.Errorf("the embedded skill names %q", m)
	}
	spec, err := os.ReadFile("../../openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if m := internalNames.FindString(wireField.ReplaceAllString(string(spec), "")); m != "" {
		t.Errorf("openapi.yaml names %q (make spec refreshes it from the docs site)", m)
	}
	for _, name := range schema.Names() {
		b, _ := schema.Get(name)
		if m := internalNames.FindString(wireField.ReplaceAllString(string(b), "")); m != "" {
			t.Errorf("schema %s names %q", name, m)
		}
	}
}

const leakyID = "01J9ZQ4M7R3T8VX2K5N6P0B1CD"

// leakyAPI answers like an API from before tiers (or one with a bug): the engine
// fields and the prose all name engines.
func leakyAPI(t *testing.T) *httptest.Server {
	t.Helper()
	reply := func(status int, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			fmt.Fprint(w, body)
		}
	}
	session := func(engine string) string {
		return `{"id":"` + leakyID + `","project_id":"p","engine":"` + engine + `","status":"active","sticky_key":"s1",` +
			`"proxy":{"tier":"residential","country":"de"},"created_at":"2026-09-22T00:00:00Z","last_used_at":null,` +
			`"expires_at":"2026-09-22T02:00:00Z","usage_count":0,"context":null,"warnings":["Moli cannot persist IndexedDB, use Chromium"]}`
	}
	entry := func(engine string) string {
		return `{"id":"01M0FK1EP2E7FBMWAR7W10SMXW","created_at":"2026-09-22T10:00:00Z","project_id":"p","status":"failed","http_status":502,` +
			`"engine":"` + engine + `","credits_micro":0,"url":"https://example.com/a","error_code":"ERR::ENGINE::FAILED",` +
			`"error_detail":"Camoufox crashed; retried on Obscura","spans":{"total_ms":210},"proxy":{"source":"pool"},` +
			`"blocked":{"vendor":"cloudflare","rule":"headless-check","signal":"Browserless"}}`
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/scrape", func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "text/markdown; charset=utf-8")
		h.Set("X-Engine", "chromium")
		h.Set("X-Credits-Charged", "8")
		h.Set("X-Request-Cost", "8")
		h.Set("X-Target-Status", "200")
		h.Set("X-Proxy-Source", "pool")
		h.Set("Cache-State", "miss")
		h.Set("X-Request-Id", "01REQ")
		h.Add("X-Warning", "ENGINE_FALLBACK: Camoufox was busy, served by Moli")
		fmt.Fprint(w, "# Hello\n")
	})
	mux.HandleFunc("POST /v1/sessions", reply(201, session("obscura")))
	mux.HandleFunc("GET /v1/sessions", reply(200, `{"sessions":[`+session("camoufox")+`,`+session("moli")+`]}`))
	mux.HandleFunc("GET /v1/sessions/{id}", reply(200, session("chromium")))
	mux.HandleFunc("GET /v1/requests", reply(200, `{"data":[`+entry("moli")+`],"page":{"has_more":false,"retention_hours":24}}`))
	mux.HandleFunc("GET /v1/requests/{id}", reply(200, entry("chromium")))
	mux.HandleFunc("GET /v1/usage", reply(200, `{"period":{"start":"2026-08-01","end":"2026-09-01","days":31},"group_by":"engine","groups":[`+
		`{"key":"fetch","label":"Fetch","metrics":{"requests":40}},{"key":"chromium","label":"","metrics":{"requests":5}},`+
		`{"key":"moli","label":"Moli renders","metrics":{"requests":2}}],"totals":{"requests":47},"unattributed":{"requests":0},"warnings":["no rows for Obscura"]}`))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestHumanOutputNeverNamesAnEngine(t *testing.T) {
	srv := leakyAPI(t)
	cases := []struct {
		name string
		args []string
		want []string // tier names the person should read instead
	}{
		{"scrape --meta", []string{"scrape", "https://example.com", "--meta"}, []string{"engine: browser", "served by js"}},
		{"sessions create", []string{"sessions", "create"}, []string{"engine:", "js"}},
		{"sessions list", []string{"sessions", "list"}, []string{"ENGINE", "browser", "js"}},
		{"sessions get", []string{"sessions", "get", leakyID}, []string{"engine:", "browser"}},
		{"logs", []string{"logs"}, []string{"ENGINE", "js"}},
		{"logs get", []string{"logs", "get", "x"}, []string{"engine:", "browser", "browser crashed; retried on js"}},
		{"usage by engine", []string{"usage", "--group-by", "engine"}, []string{"browser", "js renders", "no rows for js"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, errb, code := runHuman(t, srv.URL, false, tc.args...)
			if code != 0 {
				t.Fatalf("exit %d: %s", code, errb)
			}
			all := ansi.ReplaceAllString(out+errb, "")
			if m := internalNames.FindString(all); m != "" {
				t.Errorf("names %q:\n%s", m, all)
			}
			for _, w := range tc.want {
				if !strings.Contains(all, w) {
					t.Errorf("lacks %q:\n%s", w, all)
				}
			}
		})
	}
}

// The live terminal output (spinner, summary line) and the error block too.
func TestLiveOutputAndErrorsNeverNameAnEngine(t *testing.T) {
	srv := leakyAPI(t)
	_, errb, code := runHuman(t, srv.URL, true, "scrape", "https://example.com", "--render")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	plain := ansi.ReplaceAllString(errb, "")
	if m := internalNames.FindString(plain); m != "" || !strings.Contains(plain, "✓ example.com") {
		t.Errorf("live stderr (%q):\n%s", m, plain)
	}

	fail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(502)
		fmt.Fprint(w, `{"title":"Bad Gateway","status":502,"code":"ERR::ENGINE::FAILED","detail":"Chromium did not answer","retryable":false,`+
			`"diagnostics":{"hint":"retry, or drop stealth: Camoufox is busy"}}`)
	}))
	t.Cleanup(fail.Close)
	for _, live := range []bool{false, true} {
		_, errb, code = runHuman(t, fail.URL, live, "scrape", "https://example.com")
		if code == 0 {
			t.Fatal("want a failure")
		}
		plain = ansi.ReplaceAllString(errb, "")
		if m := internalNames.FindString(plain); m != "" || !strings.Contains(plain, "browser did not answer") {
			t.Errorf("live=%v error output (%q):\n%s", live, m, plain)
		}
	}
}

// Machine output is the API's data: nothing is rewritten, so a script sees what
// the API sent.
func TestJSONOutputStaysAsTheAPISentIt(t *testing.T) {
	srv := leakyAPI(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"sessions", "get", leakyID}, `"engine": "chromium"`},
		{[]string{"scrape", "https://example.com"}, `"engine": "chromium"`},
		{[]string{"logs", "--jsonl"}, `"engine":"moli"`},
		{[]string{"usage", "--group-by", "engine"}, `"key": "chromium"`},
	} {
		out, errb, code := runCLI(t, srv.URL, tc.args...)
		if code != 0 || !strings.Contains(out, tc.want) {
			t.Errorf("%v: exit %d, want %s in:\n%s\n%s", tc.args, code, tc.want, out, errb)
		}
	}
}
