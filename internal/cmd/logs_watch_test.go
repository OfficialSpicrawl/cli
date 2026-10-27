package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OfficialSpicrawl/cli/internal/api"
	"github.com/OfficialSpicrawl/cli/internal/output"
	"github.com/OfficialSpicrawl/cli/internal/tui"
)

func init() { time.Local = time.UTC } // watchClock shows local time; the tests read it as UTC

func logsEntryJSON(id, created, status, engine, url string) string {
	return fmt.Sprintf(`{"id":%q,"created_at":%q,"project_id":"P-ONE","status":%q,"http_status":200,"engine":%q,"credits_micro":1000000,"url":%q,`+
		`"error_code":"","error_detail":"","attempts":1,"proxy":{},"spans":{"total_ms":120}}`, id, created, status, engine, url)
}

func logsList(more bool, entries ...string) string {
	page := `{"has_more":false,"retention_hours":24,"retained_since":"x"}`
	if more {
		page = `{"has_more":true,"next_before":"2026-09-22T10:00:00Z","next_before_id":"cursor","retention_hours":24,"retained_since":"x"}`
	}
	return `{"data":[` + strings.Join(entries, ",") + `],"page":` + page + `}`
}

// logsScripted answers /v1/requests with the next body each call (the last repeats).
func logsScripted(t *testing.T, bodies ...string) (string, func() []recordedRequest) {
	t.Helper()
	var n atomic.Int32
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		i := min(int(n.Add(1))-1, len(bodies)-1)
		_, _ = w.Write([]byte(bodies[i]))
	})
	return srv.URL, func() []recordedRequest { return *reqs }
}

var (
	e1 = logsEntryJSON("E1", "2026-09-22T10:00:01Z", "success", "fetch", "https://x.test/1")
	e2 = logsEntryJSON("E2", "2026-09-22T10:00:02Z", "failed", "moli", "https://x.test/2")
	e3 = logsEntryJSON("E3", "2026-09-22T10:00:03Z", "blocked", "plain-chrome", "https://x.test/3")
	e4 = logsEntryJSON("E4", "2026-09-22T10:00:04Z", "success", "fetch", "https://x.test/4")
	e5 = logsEntryJSON("E5", "2026-09-22T10:00:05Z", "success", "obscura", "https://x.test/5")
	e6 = logsEntryJSON("E6", "2026-09-22T10:00:06Z", "success", "fetch", "https://x.test/6")
)

// streamUntil runs the plain stream until out has n lines, lets it poll a few
// more times (nothing more may appear), then stops it.
func streamUntil(t *testing.T, base string, json bool, n int) (out, errs string, err error) {
	t.Helper()
	var ob, eb lockedBuf
	p := &output.Printer{JSON: json, Out: &ob, Err: &eb, Plain: true}
	src := &logsWatchSource{c: api.New(base, "k", 2*time.Second), q: map[string][]string{"limit": {"200"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- logsWatchPlain(ctx, p, src, 5*time.Millisecond) }()
	count := func() int {
		n := 0
		for _, l := range strings.Split(ob.String(), "\n") {
			if strings.TrimSpace(l) != "" {
				n++
			}
		}
		return n
	}
	for count() < n {
		select {
		case err := <-done:
			return ob.String(), eb.String(), err
		case <-time.After(5 * time.Millisecond):
		}
	}
	time.Sleep(60 * time.Millisecond)
	cancel()
	err = <-done
	return ob.String(), eb.String(), err
}

func lineIDs(out string) []string {
	var ids []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l == "" {
			continue
		}
		if i := strings.Index(l, "https://x.test/"); i >= 0 {
			ids = append(ids, "E"+l[i+len("https://x.test/"):])
		}
	}
	return ids
}

// ---- flags -------------------------------------------------------------------------------

func TestLogsWatchUsageErrors(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {})
	for _, args := range [][]string{
		{"logs", "--watch", "--all"},
		{"logs", "--watch", "--jsonl"},
		{"logs", "--poll", "5s"},
		{"logs", "--watch", "--poll", "0s"},
		{"logs", "--watch", "--limit", "500"},
		{"logs", "--watch", "--status", "error"},
		{"logs", "--watch", "--errors", "--status", "failed"},
		{"logs", "get", "R1", "--watch"},
	} {
		if _, errb, code := runCLI(t, srv.URL, args...); code != 2 {
			t.Errorf("%v: exit %d (stderr %q)", args, code, errb)
		}
	}
	if len(*reqs) != 0 {
		t.Errorf("a usage error sent %d requests", len(*reqs))
	}
}

func TestLogsWatchQueryAndFatalFirstPoll(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, 401, "ERR::AUTH::INVALID_KEY", false)
	})
	_, _, code := runCLI(t, srv.URL, "logs", "--watch", "--errors", "--all-projects")
	if code != 3 {
		t.Errorf("a wrong key must fail at once with exit 3, got %d", code)
	}
	if len(*reqs) != 1 || (*reqs)[0].Query != "all_projects=true&limit=200&only_errors=true" {
		t.Errorf("requests: %+v", *reqs)
	}

	_, _, code = runCLI(t, srv.URL, "logs", "--watch", "--limit", "20", "--status", "Blocked")
	if q := (*reqs)[1].Query; code != 3 || q != "limit=20&status=blocked" {
		t.Errorf("exit %d, query %q", code, q)
	}
}

func TestLogsWithoutWatchIsUnchanged(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(logsList(false, e1)))
	})
	out, _, code := runCLI(t, srv.URL, "logs")
	if code != 0 || (*reqs)[0].Query != "" || !strings.Contains(out, `"id": "E1"`) {
		t.Errorf("exit %d, query %q\n%s", code, (*reqs)[0].Query, out)
	}
}

// ---- the plain stream (stdout is not a terminal, or --json) ---------------------------------

func TestLogsWatchPlainStreamsNewRowsOldestFirst(t *testing.T) {
	srv, reqs := logsScripted(t,
		logsList(false, e3, e2),     // the newest page at the start
		logsList(false, e4, e3, e2), // one new row
		logsList(false, e4, e3, e2), // nothing new
	)
	out, _, err := streamUntil(t, srv, false, 3)
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err %v", err)
	}
	if got := strings.Join(lineIDs(out), " "); got != "E2 E3 E4" {
		t.Errorf("rows: %q\n%s", got, out)
	}
	if !strings.Contains(out, "failed") || !strings.Contains(out, "2026-09-22T10:00:02Z") {
		t.Errorf("plain line lacks status or time:\n%s", out)
	}
	if len(reqs()) < 3 {
		t.Errorf("polled %d times", len(reqs()))
	}
}

func TestLogsWatchPlainJSONLIsTheAPIsRecords(t *testing.T) {
	srv, _ := logsScripted(t, logsList(false, e2, e1), logsList(false, e3, e2, e1))
	out, _, _ := streamUntil(t, srv, true, 3)
	var ids []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		var v map[string]any
		if err := json.Unmarshal([]byte(l), &v); err != nil {
			t.Fatalf("line %q is not JSON: %v", l, err)
		}
		ids = append(ids, v["id"].(string))
		if v["engine"] == nil {
			t.Errorf("record was reshaped: %q", l)
		}
	}
	if got := strings.Join(ids, " "); got != "E1 E2 E3" {
		t.Errorf("ids %q", got)
	}
}

func TestLogsWatchPlainPagesBackToCloseAGap(t *testing.T) {
	srv, reqs := logsScripted(t,
		logsList(false, e2, e1),
		logsList(true, e6, e5),  // a burst: two pages of new rows...
		logsList(true, e4, e3),  // ...a third page...
		logsList(false, e2, e1), // ...and then the rows we already have
		logsList(false, e6, e5, e4, e3, e2, e1),
	)
	out, _, _ := streamUntil(t, srv, false, 6)
	if got := strings.Join(lineIDs(out), " "); got != "E1 E2 E3 E4 E5 E6" {
		t.Errorf("rows %q (a burst longer than one page left a hole or a duplicate)\n%s", got, out)
	}
	r := reqs()
	if r[1].Query != "limit=200" || !strings.Contains(r[2].Query, "before=2026-09-22T10%3A00%3A00Z") || !strings.Contains(r[2].Query, "before_id=cursor") {
		t.Errorf("second poll queries: %q then %q", r[1].Query, r[2].Query)
	}
}

func TestLogsWatchPlainRetriesAndReports(t *testing.T) {
	var n atomic.Int32
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch n.Add(1) {
		case 1:
			_, _ = w.Write([]byte(logsList(false, e1)))
		case 2:
			writeProblem(w, 503, "ERR::INTERNAL::UNAVAILABLE", true)
		default:
			_, _ = w.Write([]byte(logsList(false, e2, e1)))
		}
	})
	out, errs, _ := streamUntil(t, srv.URL, false, 2)
	if got := strings.Join(lineIDs(out), " "); got != "E1 E2" {
		t.Errorf("rows %q", got)
	}
	if !strings.Contains(errs, "warning: request log:") || !strings.Contains(errs, "retrying in") {
		t.Errorf("the failed poll was not reported on stderr: %q", errs)
	}
}

func TestLogsWatchPlainFirstPollFatalAndRetryable(t *testing.T) {
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, 403, "ERR::AUTH::INSUFFICIENT_SCOPE", false)
	})
	_, _, err := streamUntil(t, srv.URL, false, 1)
	if ExitCode(err) != 3 {
		t.Errorf("exit code %d for %v", ExitCode(err), err)
	}
}

func TestLogsWatchPlainStopsWhenStdoutCloses(t *testing.T) {
	srv, _ := logsScripted(t, logsList(false, e1))
	p := &output.Printer{Out: failWriter{}, Err: &lockedBuf{}, Plain: true}
	src := &logsWatchSource{c: api.New(srv, "k", time.Second), q: map[string][]string{}}
	err := logsWatchPlain(context.Background(), p, src, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "closed pipe") {
		t.Errorf("a failed write must end the stream, got %v", err)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("write: closed pipe") }

func TestLogsWatchShowsTiersNeverEngines(t *testing.T) {
	srv, _ := logsScripted(t, logsList(false, e5, e3, e2, e1))
	out, _, _ := streamUntil(t, srv, false, 4)
	if internalNames.MatchString(out) {
		t.Errorf("an engine name reached the stream:\n%s", out)
	}
	for _, tier := range []string{"fetch", "js", "browser"} {
		if !strings.Contains(out, tier) {
			t.Errorf("tier %q missing:\n%s", tier, out)
		}
	}
}

func TestLogsWatchSourceRowsAreTiersAndLocalTimes(t *testing.T) {
	srv, _ := logsScripted(t, logsList(false, e3, e2))
	src := &logsWatchSource{c: api.New(srv, "k", time.Second), q: map[string][]string{}, project: true}
	u, err := src.Poll(context.Background(), nil)
	if err != nil || len(u.Rows) != 2 {
		t.Fatalf("%v %+v", err, u)
	}
	r := u.Rows[0]
	if r.Key != "E3" || r.Tier != "browser" || r.Status != "blocked" {
		t.Errorf("row %+v", r)
	}
	if got := strings.Join(r.Cells, "|"); got != "10:00:03|P-ONE|blocked|200|browser|1|120|https://x.test/3" {
		t.Errorf("cells %q", got)
	}
	if u.Rows[1].Tier != "js" {
		t.Errorf("moli must show as js, got %q", u.Rows[1].Tier)
	}
	if cols := src.columns(); len(cols) != len(r.Cells) || cols[1].Title != "PROJECT" {
		t.Errorf("columns do not line up with cells: %+v", cols)
	}
	for _, c := range r.Cells {
		if internalNames.MatchString(c) {
			t.Errorf("cell %q", c)
		}
	}
}

func TestWatchErrMarks429WithRetryAfter(t *testing.T) {
	err := watchErr(&api.Problem{Status: 429, Code: "ERR::LIMIT::RATE", RetryAfterSeconds: 7})
	if d := tui.Backoff(time.Second, 3, err); d != 7*time.Second {
		t.Errorf("backoff ignored Retry-After: %s", d)
	}
	var plain = &api.Problem{Status: 500, Code: "ERR::INTERNAL::ERROR"}
	if watchErr(plain) != error(plain) {
		t.Error("only a 429 with Retry-After is wrapped")
	}
	if !watchRetryable(err) || watchRetryable(&api.Problem{Status: 401}) || !watchRetryable(&api.NetworkError{Err: errors.New("refused")}) {
		t.Error("watchRetryable")
	}
}

// ---- the full-screen path ---------------------------------------------------------------------

func logsTUIServer(t *testing.T) (string, func() []recordedRequest) {
	e := logsEntryJSON("R1", "2026-09-22T10:00:00.5Z", "blocked", "moli", "https://example.com/pricing")
	e = strings.Replace(e, `"error_detail":""`, `"error_detail":"the site served a plain-chrome challenge"`, 1)
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/requests/") {
			_, _ = w.Write([]byte(e))
			return
		}
		_, _ = w.Write([]byte(logsList(false, e)))
	})
	return srv.URL, func() []recordedRequest { return *reqs }
}

func TestLogsWatchFullScreenQuitsOnQ(t *testing.T) {
	base, _ := logsTUIServer(t)
	ft := newFakeTerminal(t)
	out, errs, code := runWatch(t, ft, base, func() {
		ft.Wait(t, "https://example.com/pricing")
		ft.Press("q")
	}, "logs", "--watch", "--poll", "20ms")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if out != "" {
		t.Errorf("stdout must stay empty under the full-screen view: %q", out)
	}
	if in, restored := ft.Left(); !in || !restored {
		t.Errorf("alt screen entered %v, restored %v", in, restored)
	}
	if s := ft.Screen.String(); internalNames.MatchString(s) || !strings.Contains(s, "spicrawl logs") {
		t.Errorf("screen:\n%s", ansi.ReplaceAllString(s, ""))
	}
	if errs != "" {
		t.Errorf("stderr must stay quiet under the view (a spinner would be drawn over it): %q", errs)
	}
}

func TestLogsWatchFullScreenCtrlCExits130(t *testing.T) {
	base, _ := logsTUIServer(t)
	ft := newFakeTerminal(t)
	_, errs, code := runWatch(t, ft, base, func() {
		ft.Wait(t, "https://example.com/pricing")
		ft.Press("\x03")
	}, "logs", "--watch")
	if code != 130 || !strings.Contains(errs, "interrupted") {
		t.Errorf("exit %d, stderr %q", code, errs)
	}
	if _, restored := ft.Left(); !restored {
		t.Error("the screen was not restored after Ctrl-C")
	}
}

func TestLogsWatchFullScreenEnterShowsLogsGet(t *testing.T) {
	base, reqs := logsTUIServer(t)
	ft := newFakeTerminal(t)
	_, errs, code := runWatch(t, ft, base, func() {
		ft.Wait(t, "https://example.com/pricing")
		ft.Press("\r")
		ft.Wait(t, "spans (ms):")
		ft.Press("q") // back to the list, not out
		time.Sleep(60 * time.Millisecond)
		ft.Press("q")
	}, "logs", "--watch", "--poll", "50ms")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var detail bool
	for _, r := range reqs() {
		detail = detail || r.Path == "/v1/requests/R1"
	}
	if !detail {
		t.Error("the detail did not read GET /v1/requests/R1")
	}
	s := ft.Screen.String()
	plain := ansi.ReplaceAllString(s, "")
	for _, want := range []string{"error detail:", "blocked", "upstream"} {
		if !strings.Contains(plain, want) {
			t.Errorf("detail lacks %q", want)
		}
	}
	if internalNames.MatchString(plain) {
		t.Errorf("an engine name reached the detail page:\n%s", plain)
	}
}

func TestLogsWatchFullScreenFirstPollFatalNeverOpensTheView(t *testing.T) {
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, 401, "ERR::AUTH::INVALID_KEY", false)
	})
	ft := newFakeTerminal(t)
	_, _, code := runWatch(t, ft, srv.URL, nil, "logs", "--watch")
	if code != 3 {
		t.Errorf("exit %d", code)
	}
	if in, _ := ft.Left(); in {
		t.Error("the alternate screen was entered for a fatal error")
	}
}

func TestLogsWatchFullScreenRetriesATransientFirstPoll(t *testing.T) {
	var n atomic.Int32
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) <= 2 {
			writeProblem(w, 503, "ERR::INTERNAL::UNAVAILABLE", true)
			return
		}
		_, _ = w.Write([]byte(logsList(false, e1)))
	})
	ft := newFakeTerminal(t)
	_, errs, code := runWatch(t, ft, srv.URL, func() {
		ft.Wait(t, "retrying in")
		ft.Wait(t, "https://x.test/1")
		ft.Press("q")
	}, "logs", "--watch", "--poll", "20ms")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
}
