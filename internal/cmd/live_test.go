package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OfficialSpicrawl/cli/internal/exitcode"
	"github.com/OfficialSpicrawl/cli/internal/ui"
)

// lockedBuf is a stderr the spinner goroutine and the command both write to.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// runHuman runs the CLI in human mode (stdout is not a terminal, but the
// command behaves as if it were). With live, stderr is a stand-in terminal:
// the spinner, colour and summary lines are drawn on it, as they would be on a
// real one. Without it stderr is a plain pipe, which is what scripts and
// agents get.
func runHuman(t *testing.T, baseURL string, live bool, args ...string) (stdoutS, stderrS string, code int) {
	t.Helper()
	t.Setenv("SPICRAWL_CONFIG", t.TempDir()+"/config.json")
	t.Setenv("SPICRAWL_API_KEY", "spicrawl_test_key")
	d, i := ui.Delay, ui.Interval
	ui.Delay, ui.Interval = 20*time.Millisecond, 5*time.Millisecond
	var out bytes.Buffer
	var errb lockedBuf
	stdout, stderr, humanOutput = &out, &errb, true
	if live {
		liveTerm = ui.Force(&errb)
	}
	defer func() {
		stdout, stderr, humanOutput, liveTerm = os.Stdout, os.Stderr, false, nil
		ui.Delay, ui.Interval = d, i
		resetFlags(rootCmd)
	}()
	rootCmd.SetArgs(append([]string{"--base-url", baseURL}, args...))
	code = Execute()
	return out.String(), errb.String(), code
}

// slow answers after d, so the spinner has time to appear.
func slow(d time.Duration, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(d)
		h(w, r)
	}
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// internalNames must never reach a person's screen from the CLI's own text.
var internalNames = regexp.MustCompile(`(?i)moli|chromium|chrome|camoufox|obscura|browserless|headful|headless|playwright|puppeteer`)

// A handler that leaks an internal engine name in a header the CLI reads.
func scrapeLeakyHandler(w http.ResponseWriter, r *http.Request) {
	scrapeMarkdownHandler(w, r)
	w.Header().Set("X-Engine", "moli-chromium")
}

func TestLiveScrapeShowsSpinnerAndSummaryButNoNewStdout(t *testing.T) {
	srv, _ := newRecordingServer(t, slow(150*time.Millisecond, scrapeLeakyHandler))
	args := []string{"scrape", "https://example.com/pricing", "--render", "--format", "markdown"}

	plainOut, plainErr, code := runHuman(t, srv.URL, false, args...)
	if code != 0 {
		t.Fatalf("plain: exit %d: %s", code, plainErr)
	}
	liveOut, liveErr, code := runHuman(t, srv.URL, true, args...)
	if code != 0 {
		t.Fatalf("live: exit %d: %s", code, liveErr)
	}

	if liveOut != plainOut || liveOut != "# Hello\n" {
		t.Errorf("stdout changed: live %q, plain %q", liveOut, plainOut)
	}
	if strings.Contains(plainErr, "\x1b") || strings.Contains(plainErr, "✓") {
		t.Errorf("plain stderr has live output: %q", plainErr)
	}
	if plainErr != "warning: FORMAT_COERCED: one\n" {
		t.Errorf("plain stderr changed: %q", plainErr)
	}

	for _, want := range []string{
		"\x1b[?25l", // cursor hidden while it spins
		"Scraping example.com/pricing (rendering JavaScript)",
		"\x1b[?25h", // and shown again
		"✓", "example.com/pricing", "200", "1 credit",
		"warning: FORMAT_COERCED: one\n",
	} {
		if !strings.Contains(liveErr, want) {
			t.Errorf("live stderr lacks %q: %q", want, liveErr)
		}
	}
	if internalNames.MatchString(ansi.ReplaceAllString(liveErr, "")) {
		t.Errorf("live stderr names an internal engine: %q", liveErr)
	}
	// The spinner is gone before the summary: the cursor comes back first.
	if strings.LastIndex(liveErr, "\x1b[?25h") > strings.Index(liveErr, "✓") {
		t.Errorf("the spinner was still up when the summary printed: %q", liveErr)
	}
	// Without the escape codes the summary line is "✓ url  status · credits · time".
	line := regexp.MustCompile(`✓ example.com/pricing  200 · 1 credit · \d+\.\ds`)
	if !line.MatchString(ansi.ReplaceAllString(liveErr, "")) {
		t.Errorf("summary line not as designed: %q", ansi.ReplaceAllString(liveErr, ""))
	}
}

func TestLiveIsOffForJSONAndQuiet(t *testing.T) {
	srv, _ := newRecordingServer(t, slow(100*time.Millisecond, scrapeMarkdownHandler))
	_, wantErr, _ := runHuman(t, srv.URL, false, "scrape", "https://example.com", "--json")
	for _, flag := range []string{"--json", "--quiet"} {
		out, errOut, code := runHuman(t, srv.URL, true, "scrape", "https://example.com", flag)
		if code != 0 {
			t.Fatalf("%s: exit %d: %s", flag, code, errOut)
		}
		if strings.Contains(errOut, "\x1b") || strings.Contains(errOut, "✓") {
			t.Errorf("%s: live output on stderr: %q", flag, errOut)
		}
		if flag == "--json" {
			if errOut != wantErr {
				t.Errorf("--json stderr changed: %q vs %q", errOut, wantErr)
			}
			if !strings.HasPrefix(out, "{") {
				t.Errorf("--json stdout is not JSON: %q", out)
			}
		}
	}
}

func TestFastCallNeverFlashesASpinner(t *testing.T) {
	srv, _ := newRecordingServer(t, scrapeMarkdownHandler)
	d := ui.Delay
	defer func() { ui.Delay = d }()
	// runHuman lowers the delay to 20 ms; an instant answer is still faster.
	_, errOut, code := runHuman(t, srv.URL, true, "scrape", "https://example.com")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if strings.Contains(errOut, "\x1b[?25l") || strings.Contains(errOut, "Scraping") {
		t.Errorf("a fast call drew a spinner: %q", errOut)
	}
	if !strings.Contains(errOut, "✓") {
		t.Errorf("no summary line: %q", errOut)
	}
}

func TestLiveErrorKeepsCodeAndHint(t *testing.T) {
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"title":"blocked","status":403,"code":"ERR::TARGET::BLOCKED","detail":"the site refused the request","retryable":true,"request_id":"01REQ","doc_url":"https://docs.spicrawl.com/errors/target-blocked","target_status":403,"diagnostics":{"hint":"try --render"}}`)
	})
	args := []string{"scrape", "https://example.com/x"}
	_, plainErr, pc := runHuman(t, srv.URL, false, args...)
	_, liveErr, lc := runHuman(t, srv.URL, true, args...)
	if pc != lc || pc == 0 {
		t.Fatalf("exit codes differ: plain %d, live %d", pc, lc)
	}
	for _, want := range []string{"error: ERR::TARGET::BLOCKED: the site refused the request\n", "hint: try --render\n", "request id: 01REQ"} {
		if !strings.Contains(plainErr, want) {
			t.Errorf("plain stderr lost %q: %q", want, plainErr)
		}
	}
	if strings.Contains(plainErr, "\x1b") {
		t.Errorf("plain error has escape codes: %q", plainErr)
	}
	flat := ansi.ReplaceAllString(liveErr, "")
	for _, want := range []string{"✗ ERR::TARGET::BLOCKED: the site refused the request", "try --render", "spicrawl logs get 01REQ", "docs", "https://docs.spicrawl.com/errors/target-blocked", "https://example.com/x", "after"} {
		if !strings.Contains(flat, want) {
			t.Errorf("live error lacks %q: %q", want, flat)
		}
	}
	if n := strings.Count(flat, "01REQ"); n != 1 {
		t.Errorf("request id printed %d times, want once: %q", n, flat)
	}
}

func TestLiveBatchWaitShowsProgress(t *testing.T) {
	gets := 0
	var mu sync.Mutex
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		gets++
		n := gets
		mu.Unlock()
		time.Sleep(40 * time.Millisecond)
		switch {
		case n < 3:
			fmt.Fprint(w, batchJobJSON("J9", "running", n, 4))
		default:
			fmt.Fprint(w, batchJobJSON("J9", "completed", 4, 4))
		}
	})
	args := []string{"batch", "wait", "J9", "--poll", "10ms"}
	plainOut, plainErr, _ := runHuman(t, srv.URL, false, args...)
	mu.Lock()
	gets = 0
	mu.Unlock()
	liveOut, liveErr, code := runHuman(t, srv.URL, true, args...)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, liveErr)
	}
	if liveOut != plainOut {
		t.Errorf("stdout changed:\nlive  %q\nplain %q", liveOut, plainOut)
	}
	flat := ansi.ReplaceAllString(liveErr, "")
	for _, want := range []string{"J9", "━", "1/4", "✓ batch J9  completed · 4/4 items"} {
		if !strings.Contains(flat, want) {
			t.Errorf("live stderr lacks %q: %q", want, flat)
		}
	}
	// The plain path keeps its per-change progress lines (stderr is not a terminal).
	if !strings.Contains(plainErr, "batch J9: running 1/4") || strings.Contains(plainErr, "\x1b") {
		t.Errorf("plain progress changed: %q", plainErr)
	}
}

func TestLiveScrapeManyKeepsStdoutLines(t *testing.T) {
	srv, _ := newRecordingServer(t, slow(60*time.Millisecond, scrapeMarkdownHandler))
	run := func(live bool) (string, string) {
		scrapeWithStdin(t, "https://example.com/a\n# skip\nhttps://example.com/b\n")
		out, errOut, code := runHuman(t, srv.URL, live, "scrape", "-", "--concurrency", "1")
		if code != 0 {
			t.Fatalf("live=%v exit %d: %s", live, code, errOut)
		}
		return out, errOut
	}
	plainOut, plainErr := run(false)
	liveOut, liveErr := run(true)
	if strings.Count(liveOut, "\n") != 2 || strings.Contains(liveOut, "\x1b") {
		t.Errorf("live stdout: %q", liveOut)
	}
	// Lines come in completion order, which with one worker is input order; the
	// request ids and timings are identical, so the bytes match.
	if liveOut != plainOut {
		t.Errorf("stdout changed:\nlive  %q\nplain %q", liveOut, plainOut)
	}
	if !strings.Contains(plainErr, "2 ok, 0 failed") {
		t.Errorf("plain summary changed: %q", plainErr)
	}
	flat := ansi.ReplaceAllString(liveErr, "")
	for _, want := range []string{"Scraping 1/2 done", "✓ 2 URLs  2 ok"} {
		if !strings.Contains(flat, want) {
			t.Errorf("live stderr lacks %q: %q", want, flat)
		}
	}
}

func TestLiveBareCallGetsALabel(t *testing.T) {
	srv, _ := newRecordingServer(t, slow(100*time.Millisecond, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data":[],"page":{"has_more":false,"retention_hours":24}}`)
	}))
	_, errOut, code := runHuman(t, srv.URL, true, "logs")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(errOut, "Fetching the request log") {
		t.Errorf("no spinner label for a slow call: %q", errOut)
	}
}

// Ctrl-C while the spinner is up: the cursor comes back, the line is cleared,
// and the run ends with exit 130 and "interrupted".
func TestLiveInterruptRestoresTheCursor(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("cannot signal self on Windows")
	}
	inflight, release := make(chan struct{}), make(chan struct{})
	srv, _ := newRecordingServer(t, func(_ http.ResponseWriter, _ *http.Request) {
		select {
		case inflight <- struct{}{}:
		default:
		}
		<-release
	})
	defer close(release)
	go func() {
		<-inflight
		time.Sleep(80 * time.Millisecond) // long enough for the spinner to be drawn
		p, _ := os.FindProcess(os.Getpid())
		_ = p.Signal(os.Interrupt)
	}()
	_, errOut, code := runHuman(t, srv.URL, true, "scrape", "https://example.com")
	if code != exitcode.Interrupted {
		t.Fatalf("exit %d, want %d: %q", code, exitcode.Interrupted, errOut)
	}
	if !strings.Contains(errOut, "\x1b[?25l") {
		t.Errorf("the spinner never drew: %q", errOut)
	}
	if strings.LastIndex(errOut, "\x1b[?25h") < strings.LastIndex(errOut, "\x1b[?25l") {
		t.Errorf("cursor left hidden: %q", errOut)
	}
	if !strings.Contains(ansi.ReplaceAllString(errOut, ""), "✗ interrupted") {
		t.Errorf("no interrupted line: %q", errOut)
	}
}
