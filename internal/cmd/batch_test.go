package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// batchRunCLI wraps runCLI, first clearing the context cobra cached on each
// command by a previous run: Execute cancels its signal context on return,
// and cobra only hands a fresh one to commands whose ctx is nil.
func batchRunCLI(t *testing.T, baseURL string, args ...string) (string, string, int) {
	t.Helper()
	var clear func(c *cobra.Command)
	clear = func(c *cobra.Command) {
		c.SetContext(nil) //nolint:staticcheck // nil lets cobra propagate the new root context
		for _, sub := range c.Commands() {
			clear(sub)
		}
	}
	clear(rootCmd)
	return runCLI(t, baseURL, args...)
}

func batchWriteFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func batchJobJSON(id, status string, done, total int) string {
	return fmt.Sprintf(`{"id":%q,"project_id":"p","status":%q,"status_url":"/v1/batch/%s","results_url":"/v1/batch/%s/results","params":{},"total_items":%d,"concurrency":10,"priority":100,"max_attempts":3,"estimated_credits":0,"estimated_credits_micro":0,"progress":{"total":%d,"completed":%d,"succeeded":%d,"failed":0,"cancelled":0,"skipped":0,"remaining":%d,"percent_complete":0,"credits_charged":0,"credits_charged_micro":0,"bytes":0},"submitted_at":"2026-09-22T06:00:00Z"}`,
		id, status, id, id, total, total, done, done, total-done)
}

func batchDecodeBody(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("body %q: %v", b, err)
	}
	return m
}

func TestBatchSubmitURLList(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, batchJobJSON("J1", "queued", 0, 2))
	})
	file := batchWriteFile(t, "urls.txt", "# targets\nhttps://example.com/1\n\nhttps://example.com/2\n")
	out, errOut, code := batchRunCLI(t, srv.URL, "batch", "submit", file, "--render", "--country", "US",
		"--block", "images,fonts", "--name", "nightly", "--concurrency", "20", "--priority", "5",
		"--max-attempts", "4", "--failure-threshold", "10", "--credit-budget", "500", "--open")
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, errOut)
	}
	if len(*reqs) != 1 || (*reqs)[0].Method != "POST" || (*reqs)[0].Path != "/v1/batch" {
		t.Fatalf("requests: %+v", *reqs)
	}
	body := batchDecodeBody(t, (*reqs)[0].Body)
	want := map[string]any{
		"urls": []any{"https://example.com/1", "https://example.com/2"}, "js_render": true, "proxy_country": "us",
		"block_resources": []any{"images", "fonts"}, "name": "nightly", "concurrency": 20.0, "priority": 5.0,
		"max_attempts": 4.0, "failure_threshold": 10.0, "credit_budget": 500.0, "open": true,
	}
	for k, v := range want {
		if fmt.Sprint(body[k]) != fmt.Sprint(v) {
			t.Errorf("body[%s] = %v, want %v", k, body[k], v)
		}
	}
	if len(body) != len(want) {
		t.Errorf("unexpected fields in body: %v", body)
	}
	if batchDecodeBody(t, []byte(out))["id"] != "J1" {
		t.Errorf("stdout: %s", out)
	}
}

func TestBatchSubmitJSONLItems(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, batchJobJSON("J2", "queued", 0, 2))
	})
	file := batchWriteFile(t, "items.jsonl",
		`{"url":"https://example.com/a","external_id":"sku-1"}`+"\n\n"+
			`{"url":"https://example.com/b","external_id":"sku-2","js_render":false,"wait_for":"#x"}`+"\n")
	_, errOut, code := batchRunCLI(t, srv.URL, "batch", "submit", file, "--stealth", "--format", "markdown",
		"--body", `{"premium_proxy":true}`)
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, errOut)
	}
	body := batchDecodeBody(t, (*reqs)[0].Body)
	items, _ := body["items"].([]any)
	if len(items) != 2 || body["urls"] != nil {
		t.Fatalf("items: %v", body)
	}
	second := items[1].(map[string]any)
	if second["external_id"] != "sku-2" || second["js_render"] != false || second["wait_for"] != "#x" {
		t.Errorf("item overrides lost: %v", second)
	}
	if body["stealth"] != true || body["response_format"] != "markdown" || body["premium_proxy"] != true {
		t.Errorf("job-wide flags not merged: %v", body)
	}
	if strings.Contains(errOut, "note:") {
		t.Errorf("batch applies every field it accepts; no note expected: %s", errOut)
	}
}

func TestBatchSubmitJSONArrayOfStrings(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, batchJobJSON("J3", "queued", 0, 1))
	})
	file := batchWriteFile(t, "t.json", `["https://example.com/1"]`)
	if _, e, code := batchRunCLI(t, srv.URL, "batch", "submit", file); code != 0 {
		t.Fatalf("exit %d: %s", code, e)
	}
	if u := batchDecodeBody(t, (*reqs)[0].Body)["urls"]; fmt.Sprint(u) != "[https://example.com/1]" {
		t.Errorf("urls = %v", u)
	}
}

func TestBatchSubmitUsageErrors(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {})
	urls := batchWriteFile(t, "urls.txt", "https://example.com/1\n")
	cases := [][]string{
		{"batch", "submit", urls, "--body", `{"url":"https://example.com"}`},
		{"batch", "submit", batchWriteFile(t, "empty.txt", "# nothing\n")},
		{"batch", "submit", batchWriteFile(t, "bad.jsonl", `{"external_id":"x"}`)},
		{"batch", "submit", urls, "--open", "--wait"},
		{"batch", "submit", filepath.Join(t.TempDir(), "missing.txt")},
	}
	for _, args := range cases {
		if _, e, code := batchRunCLI(t, srv.URL, args...); code != 2 {
			t.Errorf("%v: exit %d (%s), want 2", args, code, e)
		}
	}
	if len(*reqs) != 0 {
		t.Errorf("usage errors must not send requests: %d sent", len(*reqs))
	}
}

func TestBatchSubmitWaitPollsUntilCompleted(t *testing.T) {
	gets := 0
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, batchJobJSON("J4", "queued", 0, 2))
			return
		}
		gets++
		switch gets {
		case 1:
			w.Header().Set("Retry-After", "0")
			writeProblem(w, http.StatusTooManyRequests, "ERR::LIMIT::RATE", true)
		case 2:
			fmt.Fprint(w, batchJobJSON("J4", "running", 1, 2))
		default:
			fmt.Fprint(w, batchJobJSON("J4", "completed", 2, 2))
		}
	})
	file := batchWriteFile(t, "urls.txt", "https://example.com/1\nhttps://example.com/2\n")
	out, errOut, code := batchRunCLI(t, srv.URL, "batch", "submit", file, "--wait", "--poll", "5ms")
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, errOut)
	}
	if got := batchDecodeBody(t, []byte(out))["status"]; got != "completed" {
		t.Errorf("final status %v; stdout %s", got, out)
	}
	if gets != 3 || (*reqs)[1].Path != "/v1/batch/J4" {
		t.Errorf("gets=%d reqs=%+v", gets, *reqs)
	}
	if _, err := json.Marshal(out); err != nil || strings.Count(out, `"id"`) != 1 {
		t.Errorf("stdout should hold exactly one job: %s", out)
	}
}

func TestBatchWaitTimeoutExitsPending(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, batchJobJSON("J5", "running", 1, 3))
	})
	out, errOut, code := batchRunCLI(t, srv.URL, "batch", "wait", "J5", "--poll", "10ms", "--max-wait", "60ms")
	if code != 11 {
		t.Fatalf("exit %d, want 11; stderr %s", code, errOut)
	}
	if batchDecodeBody(t, []byte(out))["status"] != "running" {
		t.Errorf("last job not printed: %s", out)
	}
	if !strings.Contains(errOut, `"exit_code": 11`) || !strings.Contains(errOut, "spicrawl batch wait J5") {
		t.Errorf("stderr: %s", errOut)
	}
	if len(*reqs) < 2 {
		t.Errorf("polled only %d times", len(*reqs))
	}
}

func TestBatchWaitProblemExitCode(t *testing.T) {
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, http.StatusUnauthorized, "ERR::AUTH::INVALID_KEY", false)
	})
	_, errOut, code := batchRunCLI(t, srv.URL, "batch", "wait", "J6", "--poll", "5ms")
	if code != 3 || !strings.Contains(errOut, "ERR::AUTH::INVALID_KEY") {
		t.Errorf("exit %d stderr %s", code, errOut)
	}
}

func TestBatchResultsAllFollowsCursor(t *testing.T) {
	pages := map[string]string{
		"":   `{"seq":0,"url":"https://example.com/1","status":"succeeded","attempts":1,"credits_micro":0,"bytes":10,"duration_ms":1}` + "\n",
		"0a": `{"seq":1,"url":"https://example.com/2","status":"failed","attempts":3,"credits_micro":0,"bytes":0,"duration_ms":1,"error":{"code":"ERR::UPSTREAM::TIMEOUT","retryable":true}}` + "\n",
	}
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		cur := r.URL.Query().Get("cursor")
		w.Header().Set("Content-Type", "application/x-ndjson")
		if cur == "" {
			w.Header().Set("X-Next-Cursor", "0a")
		}
		fmt.Fprint(w, pages[cur])
	})
	out, errOut, code := batchRunCLI(t, srv.URL, "batch", "results", "J7", "--all", "--limit", "1", "--status", "failed")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if out != pages[""]+pages["0a"] {
		t.Errorf("stdout not the unchanged concatenation:\n%s", out)
	}
	if len(*reqs) != 2 {
		t.Fatalf("requests: %d", len(*reqs))
	}
	q := (*reqs)[1]
	if q.Path != "/v1/batch/J7/results" || !strings.Contains(q.Query, "cursor=0a") ||
		!strings.Contains(q.Query, "limit=1") || !strings.Contains(q.Query, "status=failed") {
		t.Errorf("second request: %+v", q)
	}
	if (*reqs)[0].Header.Get("Accept") != "application/x-ndjson" {
		t.Errorf("accept: %s", (*reqs)[0].Header.Get("Accept"))
	}
}

func TestBatchResultsToFileSinglePage(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", `</v1/batch/J8/results?limit=500&cursor=ff>; rel="next"`)
		fmt.Fprint(w, `{"seq":0,"url":"u","status":"succeeded","attempts":1,"credits_micro":0,"bytes":1,"duration_ms":1}`)
	})
	path := filepath.Join(t.TempDir(), "r.jsonl")
	out, errOut, code := batchRunCLI(t, srv.URL, "batch", "results", "J8", "-o", path)
	if code != 0 || out != "" {
		t.Fatalf("exit %d stdout %q stderr %s", code, out, errOut)
	}
	b, _ := os.ReadFile(path)
	if !strings.HasSuffix(string(b), "}\n") {
		t.Errorf("file: %q", b)
	}
	if len(*reqs) != 1 || !strings.Contains(errOut, "--cursor ff") {
		t.Errorf("reqs=%d stderr=%s", len(*reqs), errOut)
	}
}

func TestBatchResultsExpiredProblem(t *testing.T) {
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, http.StatusGone, "ERR::REQUEST::BEYOND_RETENTION", false)
	})
	out, errOut, code := batchRunCLI(t, srv.URL, "batch", "results", "J9")
	if code != 4 || out != "" || !strings.Contains(errOut, "BEYOND_RETENTION") {
		t.Errorf("exit %d stdout %q stderr %s", code, out, errOut)
	}
}

func TestBatchListAll(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "" {
			fmt.Fprintf(w, `{"batches":[%s],"next_cursor":"c2"}`, batchJobJSON("A", "running", 0, 1))
			return
		}
		fmt.Fprintf(w, `{"batches":[%s]}`, batchJobJSON("B", "completed", 1, 1))
	})
	out, errOut, code := batchRunCLI(t, srv.URL, "batch", "list", "--all", "--status", "running")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var got struct {
		Batches    []map[string]any `json:"batches"`
		NextCursor string           `json:"next_cursor"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got.Batches) != 2 || got.NextCursor != "" {
		t.Fatalf("stdout %s (%v)", out, err)
	}
	if len(*reqs) != 2 || !strings.Contains((*reqs)[1].Query, "cursor=c2") || !strings.Contains((*reqs)[1].Query, "status=running") {
		t.Errorf("reqs: %+v", *reqs)
	}
}

func TestBatchJobActions(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/retry"):
			fmt.Fprintf(w, `{"job":%s,"items_reset":1,"items_dispatched":1}`, batchJobJSON("K", "queued", 0, 1))
		case strings.HasSuffix(r.URL.Path, "/items"):
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprintf(w, `{"job":%s,"items_added":1,"items_dispatched":1}`, batchJobJSON("K", "running", 0, 2))
		default:
			fmt.Fprint(w, batchJobJSON("K", "cancelling", 0, 1))
		}
	})
	urls := batchWriteFile(t, "u.txt", "https://example.com/new\n")
	for _, tc := range []struct {
		args         []string
		method, path string
	}{
		{[]string{"batch", "get", "K"}, "GET", "/v1/batch/K"},
		{[]string{"batch", "cancel", "K", "--yes"}, "POST", "/v1/batch/K/cancel"},
		{[]string{"batch", "retry", "K"}, "POST", "/v1/batch/K/retry"},
		{[]string{"batch", "close", "K"}, "POST", "/v1/batch/K/close"},
		{[]string{"batch", "append", "K", urls}, "POST", "/v1/batch/K/items"},
	} {
		n := len(*reqs)
		out, errOut, code := batchRunCLI(t, srv.URL, tc.args...)
		if code != 0 || !json.Valid([]byte(out)) {
			t.Errorf("%v: exit %d stdout %q stderr %s", tc.args, code, out, errOut)
			continue
		}
		r := (*reqs)[n]
		if r.Method != tc.method || r.Path != tc.path {
			t.Errorf("%v: sent %s %s", tc.args, r.Method, r.Path)
		}
	}
	last := (*reqs)[len(*reqs)-1]
	if fmt.Sprint(batchDecodeBody(t, last.Body)["urls"]) != "[https://example.com/new]" {
		t.Errorf("append body: %s", last.Body)
	}
}

func TestBatchSubmitRegistersOnlyAcceptedScrapeFlags(t *testing.T) {
	f := batchSubmitCmd.Flags()
	for name := range batchSubmitScrapeFlags {
		if f.Lookup(name) == nil {
			t.Errorf("accepted flag --%s is not registered", name)
		}
	}
	refused := []string{
		"screenshot", "screenshot-full-page", "screenshot-selector", "screenshot-format", "screenshot-quality",
		"extract", "ai", "ai-schema", "autoparse", "links", "session", "actions", "network-capture",
		"no-cache", "cache-ttl", "allowed-status", "original-status", "no-parse-pdf", "mode",
	}
	for _, name := range refused {
		if f.Lookup(name) != nil {
			t.Errorf("--%s is registered, but the batch API refuses its field", name)
		}
	}
	if fl := f.Lookup("wait"); fl == nil || fl.Value.Type() != "bool" {
		t.Errorf("--wait must stay the boolean job wait: %+v", fl)
	}
	if u := f.Lookup("engine").Usage; strings.Contains(u, "chromium|") || !strings.Contains(u, "refused") {
		t.Errorf("--engine help should not offer chromium: %q", u)
	}

	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {})
	urls := batchWriteFile(t, "u.txt", "https://example.com/1\n")
	for _, args := range [][]string{
		{"batch", "submit", urls, "--screenshot"},
		{"batch", "submit", urls, "--extract", `{"t":"h1"}`},
		{"batch", "submit", urls, "--session", "s1"},
	} {
		if _, e, code := batchRunCLI(t, srv.URL, args...); code != 2 || !strings.Contains(e, "unknown flag") {
			t.Errorf("%v: exit %d stderr %s, want 2 unknown flag", args, code, e)
		}
	}
	if len(*reqs) != 0 {
		t.Errorf("refused flags must not send requests: %d sent", len(*reqs))
	}
}

func TestBatchSubmitAcceptedFlagsMapToFields(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, batchJobJSON("JR", "queued", 0, 1))
	})
	urls := batchWriteFile(t, "u.txt", "https://example.com/1\n")
	_, errOut, code := batchRunCLI(t, srv.URL, "batch", "submit", urls, "--render", "--render-wait", "1500",
		"--wait-for", "#price", "--wait-for-timeout", "4000", "--format", "markdown", "--main-content",
		"--include", "main", "--exclude", "nav", "--header", "X-Trace: abc", "--headless=false",
		"--impersonate", "--sticky-key", "k1", "--max-cost", "5", "--engine", "obscura")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	body := batchDecodeBody(t, (*reqs)[0].Body)
	want := map[string]any{
		"urls": "[https://example.com/1]", "js_render": true, "wait": 1500.0, "wait_for": "#price",
		"wait_for_timeout": 4000.0, "response_format": "markdown", "main_content_only": true,
		"include_tags": "[main]", "exclude_tags": "[nav]", "custom_headers": "map[X-Trace:abc]",
		"headless": false, "impersonate": true, "sticky_key": "k1", "max_cost": 5.0, "engine": "obscura",
	}
	for k, v := range want {
		if fmt.Sprint(body[k]) != fmt.Sprint(v) {
			t.Errorf("body[%s] = %v, want %v", k, body[k], v)
		}
	}
	if len(body) != len(want) {
		t.Errorf("unexpected fields in body: %v", body)
	}
}

func TestBatchSubmitRenderWaitOverridesBody(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, batchJobJSON("JW", "queued", 0, 1))
	})
	urls := batchWriteFile(t, "u.txt", "https://example.com/1\n")
	if _, e, code := batchRunCLI(t, srv.URL, "batch", "submit", urls, "--stealth",
		"--body", `{"wait":100}`, "--render-wait", "250"); code != 0 {
		t.Fatalf("exit %d: %s", code, e)
	}
	if w := batchDecodeBody(t, (*reqs)[0].Body)["wait"]; w != 250.0 {
		t.Errorf("wait = %v, want 250 (flag overrides --body)", w)
	}
}

func batchWithStdin(t *testing.T, tty bool, input string) {
	t.Helper()
	oldTTY, oldIn := batchStdinIsTerminal, batchStdin
	batchStdinIsTerminal = func() bool { return tty }
	batchStdin = strings.NewReader(input)
	t.Cleanup(func() { batchStdinIsTerminal, batchStdin = oldTTY, oldIn })
}

func TestBatchCancelRequiresYesWithoutTTY(t *testing.T) {
	batchWithStdin(t, false, "")
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, batchJobJSON("C1", "cancelling", 0, 1))
	})
	_, errOut, code := batchRunCLI(t, srv.URL, "batch", "cancel", "C1")
	if code != 2 || !strings.Contains(errOut, "--yes") {
		t.Errorf("exit %d stderr %s, want 2 naming --yes", code, errOut)
	}
	if len(*reqs) != 0 {
		t.Errorf("cancel without --yes sent %d requests", len(*reqs))
	}
	if _, e, code := batchRunCLI(t, srv.URL, "batch", "cancel", "C1", "-y"); code != 0 || len(*reqs) != 1 {
		t.Errorf("-y: exit %d stderr %s reqs %d", code, e, len(*reqs))
	}
}

func TestBatchCancelPromptsOnTTY(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, batchJobJSON("C2", "cancelling", 0, 1))
	})
	batchWithStdin(t, true, "n\n")
	_, errOut, code := batchRunCLI(t, srv.URL, "batch", "cancel", "C2")
	if code != 2 || len(*reqs) != 0 || !strings.Contains(errOut, "cannot be undone") {
		t.Errorf("declined: exit %d reqs %d stderr %s", code, len(*reqs), errOut)
	}
	batchWithStdin(t, true, "yes\n")
	out, errOut, code := batchRunCLI(t, srv.URL, "batch", "cancel", "C2")
	if code != 0 || len(*reqs) != 1 || (*reqs)[0].Path != "/v1/batch/C2/cancel" || !json.Valid([]byte(out)) {
		t.Errorf("confirmed: exit %d reqs %+v stderr %s", code, *reqs, errOut)
	}
}

func TestBatchContent(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tasks/3/content") {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Content-Truncated", "true")
			fmt.Fprint(w, `"<html><p>hi</p></html>"`)
			return
		}
		writeProblem(w, http.StatusConflict, "ERR::REQUEST::CONFLICT", false)
	})
	out, errOut, code := batchRunCLI(t, srv.URL, "batch", "content", "J1", "3")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var doc string
	if err := json.Unmarshal([]byte(out), &doc); err != nil || doc != "<html><p>hi</p></html>" {
		t.Errorf("JSON stdout %q (%v)", out, err)
	}
	if (*reqs)[0].Path != "/v1/batch/J1/tasks/3/content" || !strings.Contains(errOut, "512 KiB") {
		t.Errorf("path %s stderr %s", (*reqs)[0].Path, errOut)
	}

	path := filepath.Join(t.TempDir(), "page.html")
	out, errOut, code = batchRunCLI(t, srv.URL, "batch", "content", "J1", "3", "-o", path)
	if b, _ := os.ReadFile(path); code != 0 || out != "" || string(b) != "<html><p>hi</p></html>" {
		t.Errorf("-o: exit %d stdout %q file %q stderr %s", code, out, b, errOut)
	}

	if _, e, code := batchRunCLI(t, srv.URL, "batch", "content", "J1", "three"); code != 2 || !strings.Contains(e, "seq") {
		t.Errorf("bad seq: exit %d %s", code, e)
	}
	n := len(*reqs)
	if _, e, code := batchRunCLI(t, srv.URL, "batch", "content", "J1", "0"); code != 4 || !strings.Contains(e, "CONFLICT") {
		t.Errorf("409: exit %d %s", code, e)
	}
	if len(*reqs) != n+1 {
		t.Errorf("409 must not be retried: %d requests", len(*reqs)-n)
	}
}
