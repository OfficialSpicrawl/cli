package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OfficialSpicrawl/cli/internal/output"
)

func TestScrapeBodyNullIsUsageError(t *testing.T) {
	_, stderr, code := scrapeRunCLI(t, "http://127.0.0.1:1", "scrape", "https://example.com", "--body", "null")
	if code != 2 || !strings.Contains(stderr, "--body") {
		t.Fatalf("exit %d, stderr %s; want usage error 2", code, stderr)
	}
}

func TestScrapeBodyDeepMerge(t *testing.T) {
	srv, reqs := newRecordingServer(t, scrapeMarkdownHandler)
	_, stderr, code := scrapeRunCLI(t, srv.URL, "scrape", "https://example.com",
		"--body", `{"custom_headers":{"A":"1","B":"2"},"extract":{"title":{"selector":"h1","attr":"x"},"price":".p"}}`,
		"--header", "B: 9", "--extract", `{"title":{"selector":"h2"}}`)
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
	got, _ := json.Marshal(scrapeTestBody(t, reqs))
	for _, want := range []string{
		`"custom_headers":{"A":"1","B":"9"}`,
		`"extract":{"price":".p","title":{"attr":"x","selector":"h2"}}`,
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("body %s lacks %s", got, want)
		}
	}
}

// A write error on stdout must stop the run (no more billing) and exit non-zero.
func TestScrapeStdinStdoutWriteError(t *testing.T) {
	old := scrapePrinter
	scrapePrinter = func() *output.Printer { p := Printer(); p.Out = failingWriter{}; return p }
	t.Cleanup(func() { scrapePrinter = old })
	srv, reqs := newRecordingServer(t, scrapeMarkdownHandler)
	scrapeWithStdin(t, "https://a.example\nhttps://b.example\nhttps://c.example\n")
	_, stderr, code := scrapeRunCLI(t, srv.URL, "scrape", "-", "--concurrency", "1")
	if code == 0 || !strings.Contains(stderr, "write results to stdout") {
		t.Fatalf("exit %d, stderr %s; want a write error", code, stderr)
	}
	if len(*reqs) != 1 {
		t.Errorf("%d URLs were requested after stdout failed, want 1", len(*reqs))
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("no space left on device") }

// Ctrl-C mid-run: exit 130 and a summary that does not call unfinished URLs ok.
func TestScrapeStdinInterruptedSummary(t *testing.T) {
	scrapeHumanMode(t)
	n := 0
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if n++; n == 1 {
			scrapeMarkdownHandler(w, r)
			return
		}
		self, _ := os.FindProcess(os.Getpid())
		_ = self.Signal(os.Interrupt)
		select { // the client aborts once the signal cancels its context
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	scrapeWithStdin(t, "https://a.example\nhttps://b.example\nhttps://c.example\n")
	_, stderr, code := scrapeRunCLI(t, srv.URL, "scrape", "-", "--concurrency", "1")
	if code != 130 {
		t.Errorf("exit %d, want 130", code)
	}
	if !strings.Contains(stderr, "1 ok, 1 failed, 1 not run") {
		t.Errorf("stderr = %s", stderr)
	}
}

// Ctrl-C used to be ignored while stdin was idle.
func TestReadStdinLinesCtxCancelWhileIdle(t *testing.T) {
	r, w, _ := os.Pipe()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old; r.Close(); w.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	done := make(chan error, 1)
	go func() { _, err := readStdinLinesCtx(ctx); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("readStdinLinesCtx still blocked on stdin after cancel")
	}
}

func TestReadLinesStripsBOM(t *testing.T) {
	f := filepath.Join(t.TempDir(), "urls.txt")
	_ = os.WriteFile(f, []byte("\ufeffhttps://a.example\n\ufeff# c\nhttps://b.example\n"), 0o644)
	got, err := readLines(f)
	if err != nil || strings.Join(got, "|") != "https://a.example|https://b.example" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestScrapeScreenshotLabelsUnique(t *testing.T) {
	data := base64.StdEncoding.EncodeToString([]byte("x"))
	var shots []map[string]any
	for _, l := range []any{"a", "a-2", "a", "A", nil} {
		shots = append(shots, map[string]any{"label": l, "data": data})
	}
	dir := t.TempDir()
	paths, err := scrapeSaveScreenshots(shots, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range paths {
		k := strings.ToLower(filepath.Base(p))
		if seen[k] {
			t.Errorf("duplicate file %s in %v", k, paths)
		}
		seen[k] = true
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 5 {
		t.Errorf("%d files written, want 5", len(ents))
	}
}

func TestScrapeOutputKeepsGoodFileOnTargetFailure(t *testing.T) {
	srv, _ := newRecordingServer(t, scrapeTargetHandler(503, 200))
	dir := t.TempDir()
	file := filepath.Join(dir, "page.md")
	_ = os.WriteFile(file, []byte("good"), 0o600)
	out, _, code := scrapeRunCLI(t, srv.URL, "scrape", "https://example.com", "-o", file)
	if b, _ := os.ReadFile(file); code != 6 || string(b) != "good" {
		t.Fatalf("exit %d, file %q: an error page replaced the good file", code, b)
	}
	if !strings.Contains(out, "Service Unavailable") {
		t.Errorf("JSON mode must keep the unwritten document: %s", out)
	}
	if _, _, code = scrapeRunCLI(t, srv.URL, "scrape", "https://example.com", "-o", file, "--allowed-status", "503"); code != 0 {
		t.Fatalf("allowed: exit %d", code)
	}
	b, _ := os.ReadFile(file)
	st, _ := os.Stat(file)
	if string(b) != "Service Unavailable\n" || st.Mode().Perm() != 0o600 {
		t.Errorf("file = %q mode %v, want the page with the old mode", b, st.Mode().Perm())
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Errorf("leftover temp files: %v", ents)
	}
}

func TestScrapeOutputDashIsStdout(t *testing.T) {
	scrapeHumanMode(t)
	t.Chdir(t.TempDir())
	srv, _ := newRecordingServer(t, scrapeMarkdownHandler)
	out, stderr, code := scrapeRunCLI(t, srv.URL, "scrape", "https://example.com", "-o", "-")
	if code != 0 || out != "# Hello\n" {
		t.Fatalf("exit %d, stdout %q, stderr %s", code, out, stderr)
	}
	if _, err := os.Stat("-"); err == nil {
		t.Error(`a file named "-" was written`)
	}
}

func TestScrapeOutputUnwritablePrintsResult(t *testing.T) {
	srv, _ := newRecordingServer(t, scrapeMarkdownHandler)
	file := filepath.Join(t.TempDir(), "missing", "page.md")
	out, stderr, code := scrapeRunCLI(t, srv.URL, "scrape", "https://example.com", "-o", file)
	if code != 1 || !strings.Contains(stderr, "printed to stdout instead") {
		t.Errorf("exit %d, stderr %s", code, stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil || got["content"] != "# Hello\n" || got["output"] != nil {
		t.Errorf("billed result lost or mislabelled: %s", out)
	}
}

func TestScrapeImpersonateToggle(t *testing.T) {
	srv, reqs := newRecordingServer(t, scrapeMarkdownHandler)
	if _, stderr, code := scrapeRunCLI(t, srv.URL, "scrape", "https://example.com", "--impersonate=false"); code != 0 {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
	if v, ok := scrapeTestBody(t, reqs)["impersonate"]; !ok || v != false {
		t.Errorf("impersonate = %v, want false sent", v)
	}
	if u := scrapeCmd.Flags().Lookup("impersonate").Usage; !strings.Contains(u, "on by default") {
		t.Errorf("help does not state the default: %q", u)
	}
}

func TestScrapeHumanDataNotHTMLEscaped(t *testing.T) {
	scrapeHumanMode(t)
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"u","status":200,"content":"","data":{"q":"a<b>&c"}}`))
	})
	out, stderr, code := scrapeRunCLI(t, srv.URL, "scrape", "https://example.com", "--extract", `{"q":"h1"}`)
	if code != 0 || !strings.Contains(out, `"a<b>&c"`) {
		t.Fatalf("exit %d, stdout %q, stderr %s", code, out, stderr)
	}
}

func TestScrapeWrapperCreditsAlias(t *testing.T) {
	srv, _ := newRecordingServer(t, scrapeMarkdownHandler)
	out, _, _ := scrapeRunCLI(t, srv.URL, "scrape", "https://example.com", "--format", "markdown")
	var got map[string]any
	_ = json.Unmarshal([]byte(out), &got)
	if got["credits"] != 1.0 || got["credits"] != got["credits_charged"] {
		t.Errorf("credits = %v, credits_charged = %v", got["credits"], got["credits_charged"])
	}
}
