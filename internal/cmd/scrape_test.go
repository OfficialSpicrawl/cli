package cmd

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Spicrawl/cli/internal/output"
)

// scrapeRunCLI wraps runCLI. cobra only gives a subcommand the root's
// context when it has none, so after one Execute the subcommand keeps the
// previous (cancelled) signal context; clear it so each run gets a fresh one.
func scrapeRunCLI(t *testing.T, baseURL string, args ...string) (string, string, int) {
	t.Helper()
	scrapeCmd.SetContext(nil) //nolint:staticcheck // reset so cobra re-inherits the root context
	return runCLI(t, baseURL, args...)
}

func scrapeTestBody(t *testing.T, reqs *[]recordedRequest) map[string]any {
	t.Helper()
	if len(*reqs) == 0 {
		t.Fatal("no request was sent")
	}
	var m map[string]any
	if err := json.Unmarshal((*reqs)[len(*reqs)-1].Body, &m); err != nil {
		t.Fatalf("request body: %v", err)
	}
	return m
}

func scrapeMarkdownHandler(w http.ResponseWriter, _ *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/markdown; charset=utf-8")
	h.Set("X-Request-Id", "01REQ")
	h.Set("X-Credits-Charged", "1")
	h.Set("X-Request-Cost", "1")
	h.Set("X-Engine", "fetch")
	h.Set("X-Target-Status", "200")
	h.Set("X-Proxy-Source", "pool")
	h.Set("X-Final-Url", "https://example.com/final")
	h.Set("Cache-State", "miss")
	h.Add("X-Warning", "FORMAT_COERCED: one")
	_, _ = w.Write([]byte("# Hello\n"))
}

func scrapeWithStdin(t *testing.T, content string) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(content)
	_, _ = f.Seek(0, 0)
	old := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = old; f.Close() })
}

func TestScrapeRequestBodyOnlySetFlags(t *testing.T) {
	srv, reqs := newRecordingServer(t, scrapeMarkdownHandler)
	_, stderr, code := scrapeRunCLI(t, srv.URL, "scrape", "https://example.com",
		"--format", "markdown", "--render", "--wait-for", ".plans", "--premium-proxy", "--country", "DE",
		"--header", "X-Test: a b", "--header", "Cookie: k=v", "--no-cache", "--allowed-status", "404,410",
		"--extract", `{"title":"h1"}`)
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
	r := (*reqs)[0]
	if r.Method != "POST" || r.Path != "/v1/scrape" {
		t.Fatalf("got %s %s", r.Method, r.Path)
	}
	body := scrapeTestBody(t, reqs)
	want := map[string]any{
		"url": "https://example.com", "response_format": "markdown", "js_render": true, "wait_for": ".plans",
		"premium_proxy": true, "proxy_country": "de", "cache": false,
		"custom_headers":       map[string]any{"X-Test": "a b", "Cookie": "k=v"},
		"allowed_status_codes": []any{404.0, 410.0},
		"extract":              map[string]any{"title": "h1"},
	}
	for k, v := range want {
		got, _ := json.Marshal(body[k])
		exp, _ := json.Marshal(v)
		if string(got) != string(exp) {
			t.Errorf("%s = %s, want %s", k, got, exp)
		}
	}
	if len(body) != len(want) {
		t.Errorf("unexpected fields sent: %v", body)
	}
}

func TestScrapeAIExclusive(t *testing.T) {
	srv, reqs := newRecordingServer(t, scrapeMarkdownHandler)
	_, _, code := scrapeRunCLI(t, srv.URL, "scrape", "https://example.com", "--ai", "prices", "--ai-schema", `{"type":"object"}`)
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if len(*reqs) != 0 {
		t.Fatal("request sent on usage error")
	}
}

func TestScrapeRawMarkdownWrapped(t *testing.T) {
	srv, _ := newRecordingServer(t, scrapeMarkdownHandler)
	out, stderr, code := scrapeRunCLI(t, srv.URL, "scrape", "https://example.com", "--format", "markdown")
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, out)
	}
	checks := map[string]any{
		"url": "https://example.com", "final_url": "https://example.com/final", "status": 200.0,
		"engine": "fetch", "proxy_source": "pool", "credits_charged": 1.0, "request_cost": 1.0,
		"cache_state": "miss", "request_id": "01REQ", "content": "# Hello\n",
		"content_type": "text/markdown; charset=utf-8",
	}
	for k, v := range checks {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	if w, _ := got["warnings"].([]any); len(w) != 1 || w[0] != "FORMAT_COERCED: one" {
		t.Errorf("warnings = %v", got["warnings"])
	}
}

func TestScrapeEnvelopePassthrough(t *testing.T) {
	env := `{"url":"https://example.com","final_url":"","status":200,"content":"x","headers":null,"truncated":false,"credits":1,"engine":"fetch","proxy_source":"pool","warnings":[],"data":{"title":"Hi"}}`
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(env))
	})
	out, stderr, code := scrapeRunCLI(t, srv.URL, "scrape", "https://example.com", "--extract", `{"title":"h1"}`)
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
	var got, want map[string]any
	_ = json.Unmarshal([]byte(env), &want)
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout not JSON: %v", err)
	}
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want)
	if string(a) != string(b) {
		t.Fatalf("envelope changed:\n%s\n%s", a, b)
	}
}

func TestScrapeOutputFileAndScreenshots(t *testing.T) {
	png := []byte("\x89PNG fake")
	env := map[string]any{"url": "https://example.com", "content": "# Page", "status": 200, "engine": "chromium",
		"screenshots": []any{map[string]any{"label": "final", "encoding": "base64", "format": "png",
			"data": base64.StdEncoding.EncodeToString(png), "size_bytes": len(png)}}}
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(env)
	})
	dir := t.TempDir()
	file := filepath.Join(dir, "page.md")
	out, stderr, code := scrapeRunCLI(t, srv.URL, "scrape", "https://example.com", "--screenshot", "-o", file)
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
	if b, _ := os.ReadFile(file); string(b) != "# Page" {
		t.Errorf("document file = %q", b)
	}
	shot := filepath.Join(dir, "page.final.png")
	if b, _ := os.ReadFile(shot); string(b) != string(png) {
		t.Errorf("screenshot file = %q", b)
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(out), &got)
	if _, has := got["content"]; has || got["output"] != file {
		t.Errorf("stdout = %s", out)
	}
	if strings.Contains(out, `"data"`) || !strings.Contains(out, shot) {
		t.Errorf("screenshot data not replaced by file: %s", out)
	}
}

func TestScrapeProblemExitCode(t *testing.T) {
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeProblem(w, 502, "ERR::PROXY::EXHAUSTED", false)
	})
	out, stderr, code := scrapeRunCLI(t, srv.URL, "scrape", "https://example.com")
	if code != 7 {
		t.Fatalf("exit %d, want 7", code)
	}
	if out != "" {
		t.Errorf("stdout not empty: %s", out)
	}
	var prob map[string]any
	if err := json.Unmarshal([]byte(stderr), &prob); err != nil || prob["code"] != "ERR::PROXY::EXHAUSTED" {
		t.Errorf("stderr = %s", stderr)
	}
}

func TestScrapeStdinJSONL(t *testing.T) {
	// The recording server drains the body before the handler runs, so the
	// handler looks at the last recorded request (safe with --concurrency 1).
	var mixed *[]recordedRequest
	msrv, mreqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		last := (*mixed)[len(*mixed)-1]
		if strings.Contains(string(last.Body), "bad.example") {
			writeProblem(w, 429, "ERR::LIMIT::RATE", false)
			return
		}
		scrapeMarkdownHandler(w, r)
	})
	mixed = mreqs

	scrapeWithStdin(t, "https://a.example\n\n# comment\nhttps://bad.example\nhttps://c.example\n")
	out, _, code := scrapeRunCLI(t, msrv.URL, "scrape", "-", "--format", "markdown", "--concurrency", "1")
	if code != 5 {
		t.Fatalf("exit %d, want 5", code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d:\n%s", len(lines), out)
	}
	seen := map[float64]map[string]any{}
	for _, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("bad line %q: %v", l, err)
		}
		seen[m["index"].(float64)] = m
	}
	if seen[1]["url"] != "https://a.example" || seen[1]["content"] != "# Hello\n" {
		t.Errorf("line 1 = %v", seen[1])
	}
	if e, _ := seen[2]["error"].(map[string]any); e == nil || e["code"] != "ERR::LIMIT::RATE" || seen[2]["url"] != "https://bad.example" {
		t.Errorf("line 2 = %v", seen[2])
	}
	if seen[3]["url"] != "https://c.example" {
		t.Errorf("line 3 = %v", seen[3])
	}
	for _, r := range *mreqs {
		var b map[string]any
		_ = json.Unmarshal(r.Body, &b)
		if b["response_format"] != "markdown" {
			t.Errorf("shared flags not applied: %s", r.Body)
		}
	}
}

// scrapeHumanMode makes scrape print as on a terminal (runCLI forces --json).
func scrapeHumanMode(t *testing.T) {
	t.Helper()
	old := scrapePrinter
	scrapePrinter = func() *output.Printer { p := Printer(); p.JSON = false; return p }
	t.Cleanup(func() { scrapePrinter = old })
}

func scrapeTargetHandler(status int, httpStatus int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("X-Target-Status", strconv.Itoa(status))
		w.Header().Set("X-Credits-Charged", "0")
		w.WriteHeader(httpStatus)
		_, _ = w.Write([]byte("Service Unavailable\n"))
	}
}

func TestScrapeTargetFailureJSON(t *testing.T) {
	srv, _ := newRecordingServer(t, scrapeTargetHandler(503, 200))
	out, stderr, code := scrapeRunCLI(t, srv.URL, "scrape", "https://httpbin.org/status/503")
	if code != 6 {
		t.Fatalf("exit %d, want 6; stderr %s", code, stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, out)
	}
	if got["target_error"] != "target returned 503" || got["status"] != 503.0 || got["content"] != "Service Unavailable\n" {
		t.Errorf("stdout = %v", got)
	}
	if !strings.Contains(stderr, "target returned 503") {
		t.Errorf("stderr = %s", stderr)
	}
}

func TestScrapeTargetFailureHuman(t *testing.T) {
	scrapeHumanMode(t)
	srv, _ := newRecordingServer(t, scrapeTargetHandler(503, 200))
	out, stderr, code := scrapeRunCLI(t, srv.URL, "scrape", "https://httpbin.org/status/503")
	if code != 6 {
		t.Fatalf("exit %d, want 6", code)
	}
	if out != "Service Unavailable\n" {
		t.Errorf("stdout = %q, want the document", out)
	}
	if !strings.Contains(stderr, "target returned 503") {
		t.Errorf("stderr = %s", stderr)
	}
}

func TestScrapeTargetFailureOriginalStatus(t *testing.T) {
	srv, _ := newRecordingServer(t, scrapeTargetHandler(503, 503))
	out, stderr, code := scrapeRunCLI(t, srv.URL, "scrape", "https://httpbin.org/status/503", "--original-status")
	if code != 6 {
		t.Fatalf("exit %d, want 6; stderr %s", code, stderr)
	}
	if !strings.Contains(out, `"target_error": "target returned 503"`) || !strings.Contains(out, "Service Unavailable") {
		t.Errorf("stdout = %s", out)
	}
}

func TestScrapeTargetAllowedStatus(t *testing.T) {
	srv, _ := newRecordingServer(t, scrapeTargetHandler(503, 200))
	out, stderr, code := scrapeRunCLI(t, srv.URL, "scrape", "https://httpbin.org/status/503", "--allowed-status", "503")
	if code != 0 || strings.Contains(out, "target_error") {
		t.Fatalf("exit %d, stdout %s stderr %s", code, out, stderr)
	}
	// allowed_status_codes given through --body counts too.
	_, _, code = scrapeRunCLI(t, srv.URL, "scrape", "https://httpbin.org/status/503", "--body", `{"allowed_status_codes":[503]}`)
	if code != 0 {
		t.Fatalf("--body allowed: exit %d", code)
	}
}

func TestScrapeTargetFailureEnvelope(t *testing.T) {
	env := `{"url":"https://example.com/x","status":404,"content":"gone","links":[]}`
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(env))
	})
	out, _, code := scrapeRunCLI(t, srv.URL, "scrape", "https://example.com/x", "--links")
	if code != 6 || !strings.Contains(out, `"target_error": "target returned 404"`) {
		t.Fatalf("exit %d, stdout %s", code, out)
	}
	if _, _, code = scrapeRunCLI(t, srv.URL, "scrape", "https://example.com/x", "--links", "--allowed-status", "404"); code != 0 {
		t.Fatalf("allowed 404: exit %d", code)
	}
}

func TestScrapeJSONLSingle(t *testing.T) {
	scrapeHumanMode(t) // --jsonl applies on a terminal too
	srv, _ := newRecordingServer(t, scrapeMarkdownHandler)
	out, stderr, code := scrapeRunCLI(t, srv.URL, "scrape", "https://example.com", "--jsonl")
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Fatalf("want one line, got %q", out)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil || got["content"] != "# Hello\n" {
		t.Fatalf("line = %s (%v)", out, err)
	}

	env := "{\n  \"url\": \"https://example.com\",\n  \"status\": 200,\n  \"content\": \"x\"\n}\n"
	esrv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(env))
	})
	out, _, code = scrapeRunCLI(t, esrv.URL, "scrape", "https://example.com", "--format", "json", "--jsonl")
	if code != 0 || out != `{"url":"https://example.com","status":200,"content":"x"}`+"\n" {
		t.Fatalf("exit %d, envelope line %q", code, out)
	}
}

func TestScrapeHumanLinksAndNetwork(t *testing.T) {
	scrapeHumanMode(t)
	env := map[string]any{"url": "https://example.com", "status": 200, "content": "# Page\n",
		"links": []any{"https://example.com/a", "https://example.com/b"},
		"network": []any{map[string]any{"url": "https://example.com/api", "status": 200,
			"resource_type": "xhr", "content_type": "application/json", "body": map[string]any{"k": 1}}}}
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(env)
	})
	out, stderr, code := scrapeRunCLI(t, srv.URL, "scrape", "https://example.com", "--links",
		"--network-capture", `{"resource_types":["xhr"]}`)
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
	want := "# Page\n--- links (2) ---\nhttps://example.com/a\nhttps://example.com/b\n" +
		"--- network (1) ---\n200 xhr application/json https://example.com/api\n"
	if out != want {
		t.Fatalf("stdout =\n%s\nwant\n%s", out, want)
	}
}

func TestScrapeStdinTargetFailure(t *testing.T) {
	srv, _ := newRecordingServer(t, scrapeTargetHandler(503, 200))
	scrapeWithStdin(t, "https://a.example\n")
	out, _, code := scrapeRunCLI(t, srv.URL, "scrape", "-")
	if code != 6 {
		t.Fatalf("exit %d, want 6", code)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil || m["target_error"] != "target returned 503" || m["index"] != 1.0 {
		t.Fatalf("line = %s (%v)", out, err)
	}
}
