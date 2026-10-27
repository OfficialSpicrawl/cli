package cmd

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OfficialSpicrawl/cli/internal/api"
	"github.com/OfficialSpicrawl/cli/internal/exitcode"
)

// witem renders one finished item as the results endpoint does.
func witem(seq int, status string) string {
	code, hs := "", 200
	if status == "failed" {
		code, hs = `,"error":{"code":"ERR::UPSTREAM::BLOCKED"}`, 403
	}
	return fmt.Sprintf(`{"seq":%d,"url":"https://docs.example.org/page/%d","status":%q,"attempts":1,"http_status":%d,"request_id":"RQ%d",`+
		`"credits_micro":1000000,"bytes":2048,"duration_ms":%d%s,"finished_at":"2026-09-22T06:%02d:00Z"}`+"\n",
		seq, seq, status, hs, seq, 400+seq*100, code, seq)
}

// batchFake serves one job whose state advances with every poll of it.
type batchFake struct {
	srv     *httptest.Server
	mu      sync.Mutex
	jobs    []string // served in order; the last repeats
	items   []string // finished items, in submission order
	engines map[string]string
	polls   atomic.Int32
	results atomic.Int32
	logs    atomic.Int32
	failRes atomic.Bool // results answers 503
	nextCur string      // X-Next-Cursor on the first results page, when set
	pages   atomic.Int32
}

func newBatchFake(t *testing.T, jobs []string, items ...string) *batchFake {
	t.Helper()
	b := &batchFake{jobs: jobs, items: items, engines: map[string]string{}}
	for i := range items {
		b.engines[fmt.Sprintf("RQ%d", i)] = []string{"fetch", "moli", "plain-chrome", "obscura"}[i%4]
	}
	b.srv = httptest.NewServer(http.HandlerFunc(b.serve))
	t.Cleanup(b.srv.Close)
	return b
}

func (b *batchFake) serve(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case strings.HasSuffix(r.URL.Path, "/results"):
		b.results.Add(1)
		if b.failRes.Load() {
			writeProblem(w, 503, "ERR::INTERNAL::UNAVAILABLE", true)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		if b.nextCur != "" {
			w.Header().Set("X-Next-Cursor", b.nextCur)
		}
		b.pages.Add(1)
		_, _ = fmt.Fprint(w, strings.Join(b.items, ""))
	case r.URL.Path == "/v1/requests":
		b.logs.Add(1)
		var data []string
		for id, eng := range b.engines {
			data = append(data, fmt.Sprintf(`{"id":%q,"created_at":"2026-09-22T06:00:00Z","project_id":"P","status":"success","engine":%q,"url":"u","credits_micro":0,"proxy":{},"spans":{}}`, id, eng))
		}
		_, _ = fmt.Fprintf(w, `{"data":[%s],"page":{"has_more":false,"retention_hours":24,"retained_since":"x"}}`, strings.Join(data, ","))
	case strings.HasPrefix(r.URL.Path, "/v1/requests/"):
		id := strings.TrimPrefix(r.URL.Path, "/v1/requests/")
		eng, ok := b.engines[id]
		if !ok {
			writeProblem(w, 404, "ERR::REQUEST::NOT_FOUND", false)
			return
		}
		_, _ = fmt.Fprintf(w, `{"id":%q,"created_at":"2026-09-22T06:00:00Z","project_id":"P","status":"success","engine":%q,"url":"https://docs.example.org/","credits_micro":1000000,"proxy":{},"spans":{"total_ms":900}}`, id, eng)
	default: // GET /v1/batch/{id}
		i := min(int(b.polls.Add(1))-1, len(b.jobs)-1)
		if b.jobs[i] == "" {
			writeProblem(w, 503, "ERR::INTERNAL::UNAVAILABLE", true)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, b.jobs[i])
	}
}

func running(done, failed int) string {
	return gJob{Status: "running", Done: done, Failed: failed, Total: 40, Name: "nightly"}.JSON()
}

func finishedJob(status string, failed int, errCode string) string {
	return gJob{Status: status, Done: 40, Failed: failed, Total: 40, Name: "nightly", Finished: true, Err: errCode}.JSON()
}

func threeItems() []string {
	return []string{witem(0, "succeeded"), witem(1, "failed"), witem(2, "succeeded")}
}

// ---- the plain fallback is "batch wait" --------------------------------------------------------

func TestBatchWatchOffATerminalIsBatchWait(t *testing.T) {
	for _, tc := range []struct {
		name string
		jobs []string
		code int
	}{
		{"completed", []string{running(10, 0), running(30, 1), finishedJob("completed", 1, "")}, 0},
		{"failed", []string{running(10, 0), finishedJob("failed", 5, "ERR::LIMIT::FAILURE_THRESHOLD")}, 4},
		{"cancelled", []string{finishedJob("cancelled", 0, "")}, 4},
	} {
		w := newBatchFake(t, tc.jobs)
		wo, we, wc := runHuman(t, w.srv.URL, false, "batch", "wait", gBatchID, "--poll", "5ms")
		g := newBatchFake(t, tc.jobs)
		go_, ge, gc := runHuman(t, g.srv.URL, false, "batch", "watch", gBatchID, "--poll", "5ms")
		if wc != tc.code || gc != wc || go_ != wo || ge != we {
			t.Errorf("%s: watch differs from wait\nwait  %d %q %q\nwatch %d %q %q", tc.name, wc, wo, we, gc, go_, ge)
		}
		if got := g.results.Load() + g.logs.Load(); got != 0 {
			t.Errorf("%s: the plain fallback read the item table (%d calls)", tc.name, got)
		}
	}
}

func TestBatchWatchJSONIsBatchWaitToo(t *testing.T) {
	a := newBatchFake(t, []string{running(5, 0), finishedJob("completed", 0, "")})
	wo, _, wc := runCLI(t, a.srv.URL, "batch", "wait", gBatchID, "--poll", "5ms")
	b := newBatchFake(t, []string{running(5, 0), finishedJob("completed", 0, "")})
	go_, _, gc := runCLI(t, b.srv.URL, "batch", "watch", gBatchID, "--poll", "5ms")
	if wc != 0 || gc != 0 || wo != go_ || !strings.Contains(go_, `"status": "completed"`) {
		t.Errorf("--json watch differs from wait:\n%s\n%s", wo, go_)
	}
}

// ---- the dashboard ------------------------------------------------------------------------------

func TestBatchWatchFullScreenCompletedExitsZeroAndPrintsTheJob(t *testing.T) {
	b := newBatchFake(t, []string{running(3, 1), running(3, 1), running(3, 1), finishedJob("completed", 1, "")}, threeItems()...)
	ft := newFakeTerminal(t)
	out, errs, code := runWatch(t, ft, b.srv.URL, nil, "batch", "watch", gBatchID, "--poll", "60ms")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{"status       completed", "progress     40/40"} {
		if !strings.Contains(out, want) {
			t.Errorf("the final job is missing %q:\n%s", want, out)
		}
	}
	if in, restored := ft.Left(); !in || !restored {
		t.Errorf("alt screen entered %v, restored %v", in, restored)
	}
	screen := ansi.ReplaceAllString(ft.Screen.String(), "")
	for _, want := range []string{"spicrawl batch watch", "nightly", "3/40 done", "1 failed", "https://docs.example.org/page/2", "ETA", "credits"} {
		if !strings.Contains(screen, want) {
			t.Errorf("the dashboard never showed %q:\n%s", want, screen)
		}
	}
	if internalNames.MatchString(screen) || internalNames.MatchString(out) {
		t.Errorf("an engine name reached the screen or stdout:\n%s", screen)
	}
}

func TestBatchWatchFullScreenExitCodesMatchBatchWait(t *testing.T) {
	for _, tc := range []struct {
		name, status, code string
		want               int
		msg                string
	}{
		{"failed", "failed", "ERR::LIMIT::FAILURE_THRESHOLD", exitcode.Request, "failed (40 of 40 items finished): ERR::LIMIT::FAILURE_THRESHOLD"},
		{"cancelled", "cancelled", "", exitcode.Request, "cancelled (40 of 40 items finished)"},
	} {
		b := newBatchFake(t, []string{running(5, 0), running(5, 0), finishedJob(tc.status, 2, tc.code)}, threeItems()...)
		ft := newFakeTerminal(t)
		out, errs, code := runWatch(t, ft, b.srv.URL, nil, "batch", "watch", gBatchID, "--poll", "40ms")
		if code != tc.want || !strings.Contains(errs, tc.msg) || !strings.Contains(out, "status       "+tc.status) {
			t.Errorf("%s: exit %d (want %d)\nstderr %q\nstdout %q", tc.name, code, tc.want, errs, out)
		}
		if _, restored := ft.Left(); !restored {
			t.Errorf("%s: screen not restored", tc.name)
		}
	}
}

func TestBatchWatchFullScreenMaxWaitExits11(t *testing.T) {
	b := newBatchFake(t, []string{running(5, 0)}, threeItems()...)
	ft := newFakeTerminal(t)
	out, errs, code := runWatch(t, ft, b.srv.URL, nil, "batch", "watch", gBatchID, "--poll", "20ms", "--max-wait", "150ms")
	if code != exitcode.Pending || !strings.Contains(errs, "still running after 150ms; resume with: spicrawl batch watch "+gBatchID) {
		t.Errorf("exit %d\n%s", code, errs)
	}
	if !strings.Contains(out, "status       running") {
		t.Errorf("the last job was not printed:\n%s", out)
	}
	if _, restored := ft.Left(); !restored {
		t.Error("screen not restored")
	}
}

func TestBatchWatchFullScreenQuitLeavesTheJobRunning(t *testing.T) {
	b := newBatchFake(t, []string{running(5, 1)}, threeItems()...)
	ft := newFakeTerminal(t)
	out, errs, code := runWatch(t, ft, b.srv.URL, func() {
		ft.Wait(t, "https://docs.example.org/page/2")
		ft.Press("q")
	}, "batch", "watch", gBatchID, "--poll", "20ms")
	if code != 0 || out != "" || !strings.Contains(errs, "the job keeps running: spicrawl batch watch "+gBatchID) {
		t.Errorf("exit %d\nstdout %q\nstderr %q", code, out, errs)
	}
}

func TestBatchWatchFullScreenCtrlCExits130(t *testing.T) {
	b := newBatchFake(t, []string{running(5, 1)}, threeItems()...)
	ft := newFakeTerminal(t)
	_, errs, code := runWatch(t, ft, b.srv.URL, func() {
		ft.Wait(t, "https://docs.example.org/page/2")
		ft.Press("\x03")
	}, "batch", "watch", gBatchID, "--poll", "20ms")
	if code != exitcode.Interrupted || !strings.Contains(errs, "interrupted") {
		t.Errorf("exit %d, stderr %q", code, errs)
	}
	if _, restored := ft.Left(); !restored {
		t.Error("screen not restored after Ctrl-C")
	}
}

func TestBatchWatchFullScreenFilterFailedAndOpenItem(t *testing.T) {
	b := newBatchFake(t, []string{running(5, 1)}, threeItems()...)
	ft := newFakeTerminal(t)
	_, errs, code := runWatch(t, ft, b.srv.URL, func() {
		ft.Wait(t, "https://docs.example.org/page/2")
		ft.Press("f")
		ft.Wait(t, "1 shown")
		ft.Press("\r") // the only failed item is seq 1
		ft.Wait(t, "request log entry:")
		ft.Press("\x1b[6~") // PgDn: the page is longer than 24 rows
		ft.Wait(t, "spans (ms):")
		ft.Press("q")
		time.Sleep(60 * time.Millisecond)
		ft.Press("q")
	}, "batch", "watch", gBatchID, "--poll", "20ms")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	page := ansi.ReplaceAllString(ft.Screen.String(), "")
	for _, want := range []string{"item:", "ERR::UPSTREAM::BLOCKED", "request id:", "RQ1", "tier:", "js"} {
		if !strings.Contains(page, want) {
			t.Errorf("item detail lacks %q", want)
		}
	}
	if internalNames.MatchString(page) {
		t.Errorf("engine name in the item detail:\n%s", page)
	}
}

func TestBatchWatchFullScreenAlreadyFinishedNeverOpensTheView(t *testing.T) {
	b := newBatchFake(t, []string{finishedJob("failed", 3, "ERR::LIMIT::FAILURE_THRESHOLD")}, threeItems()...)
	ft := newFakeTerminal(t)
	out, _, code := runWatch(t, ft, b.srv.URL, nil, "batch", "watch", gBatchID)
	if code != exitcode.Request || !strings.Contains(out, "status       failed") {
		t.Errorf("exit %d\n%s", code, out)
	}
	if in, _ := ft.Left(); in {
		t.Error("opened the dashboard for a job that was already over")
	}
	if b.results.Load() != 0 {
		t.Error("read the item table of a finished job")
	}
}

func TestBatchWatchFullScreenUnknownJobFailsAtOnce(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, 404, "ERR::REQUEST::NOT_FOUND", false)
	})
	ft := newFakeTerminal(t)
	_, errs, code := runWatch(t, ft, srv.URL, nil, "batch", "watch", "nope")
	if code != exitcode.Request || !strings.Contains(errs, "ERR::REQUEST::NOT_FOUND") {
		t.Errorf("exit %d: %s", code, errs)
	}
	if in, _ := ft.Left(); in || len(*reqs) != 1 {
		t.Errorf("entered the view %v, %d requests", in, len(*reqs))
	}
}

func TestBatchWatchFullScreenKeepsRetryingAndShowsTheError(t *testing.T) {
	// 503 on the first poll, then a good one, a failing one, and the end.
	b := newBatchFake(t, []string{"", running(3, 1), "", finishedJob("completed", 1, "")}, threeItems()...)
	ft := newFakeTerminal(t)
	_, errs, code := runWatch(t, ft, b.srv.URL, nil, "batch", "watch", gBatchID, "--poll", "15ms")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	screen := ansi.ReplaceAllString(ft.Screen.String(), "")
	if !strings.Contains(screen, "ERR::INTERNAL::UNAVAILABLE") || !strings.Contains(screen, "retrying in") {
		t.Errorf("the error never reached the status bar:\n%s", screen)
	}
}

// ---- the source ---------------------------------------------------------------------------------------

func newSource(b *batchFake) *batchWatchSource {
	return &batchWatchSource{c: api.New(b.srv.URL, "k", 2*time.Second), id: gBatchID, tiers: map[string]string{}}
}

func TestBatchWatchSourceRowsNewestFirstWithTiersFromTheRequestLog(t *testing.T) {
	b := newBatchFake(t, []string{running(3, 1)}, threeItems()...)
	src := newSource(b)
	u, err := src.Poll(context.Background(), nil)
	if err != nil || !u.Replace || len(u.Rows) != 3 || u.Header == nil || u.Done {
		t.Fatalf("%v %+v", err, u)
	}
	// finished_at is 06:02, 06:01, 06:00 for seq 2, 1, 0
	if u.Rows[0].Key != "2" || u.Rows[2].Key != "0" {
		t.Errorf("not newest first: %s %s %s", u.Rows[0].Key, u.Rows[1].Key, u.Rows[2].Key)
	}
	r := u.Rows[1] // seq 1: failed, engine "moli" in the log
	if got := strings.Join(r.Cells, "|"); got != "1|failed|403|js|0.5s|https://docs.example.org/page/1" || r.Tier != "js" || r.Status != "failed" {
		t.Errorf("row %q tier %q", got, r.Tier)
	}
	if got := u.Rows[0].Tier; got != "browser" { // seq 2 -> plain-chrome
		t.Errorf("tier of seq 2 = %q", got)
	}
	for _, row := range u.Rows {
		for _, c := range row.Cells {
			if internalNames.MatchString(c) {
				t.Errorf("cell %q", c)
			}
		}
	}
	if b.logs.Load() != 1 {
		t.Errorf("%d request-log reads for one table", b.logs.Load())
	}
}

func TestBatchWatchSourceOnlyRereadsTheTableWhenProgressMoves(t *testing.T) {
	b := newBatchFake(t, []string{running(3, 1), running(3, 1), running(4, 1), finishedJob("completed", 1, "")}, threeItems()...)
	src := newSource(b)
	ctx := context.Background()
	u1, _ := src.Poll(ctx, nil)
	u2, _ := src.Poll(ctx, nil) // same counters: header only
	if len(u1.Rows) != 3 || u2.Replace || len(u2.Rows) != 0 || u2.Header == nil {
		t.Errorf("unchanged progress: %+v", u2)
	}
	if b.results.Load() != 1 {
		t.Errorf("%d result reads after an unchanged poll", b.results.Load())
	}
	u3, _ := src.Poll(ctx, nil)
	if !u3.Replace || b.results.Load() != 2 {
		t.Errorf("moved progress did not refresh the table: %+v", u3)
	}
	u4, _ := src.Poll(ctx, nil) // terminal: Done, and no download of the inline bodies
	if !u4.Done || b.results.Load() != 2 {
		t.Errorf("done=%v results=%d", u4.Done, b.results.Load())
	}
}

func TestBatchWatchSourceKeepsProgressWhenTheTableFails(t *testing.T) {
	b := newBatchFake(t, []string{running(3, 1), running(4, 1)}, threeItems()...)
	b.failRes.Store(true)
	src := newSource(b)
	u, err := src.Poll(context.Background(), nil)
	if err != nil || u.Header == nil || u.Replace || !strings.HasPrefix(u.Note, "items: ") {
		t.Fatalf("err %v update %+v", err, u)
	}
	b.failRes.Store(false)
	u, _ = src.Poll(context.Background(), nil)
	if !u.Replace || len(u.Rows) != 3 {
		t.Errorf("the table did not come back: %+v", u)
	}
}

func TestBatchWatchSourceSaysWhenTheTableIsCut(t *testing.T) {
	b := newBatchFake(t, []string{running(3, 1)}, threeItems()...)
	b.nextCur = "more" // every page claims another follows: the table stops at two pages
	src := newSource(b)
	u, err := src.Poll(context.Background(), nil)
	if err != nil || !strings.Contains(u.Note, "first") {
		t.Errorf("err %v note %q", err, u.Note)
	}
	if b.pages.Load() != batchWatchPages {
		t.Errorf("%d pages read, want %d", b.pages.Load(), batchWatchPages)
	}
}

func TestBatchWatchSourceDetail(t *testing.T) {
	b := newBatchFake(t, []string{running(3, 1)}, threeItems()...)
	src := newSource(b)
	if _, err := src.Poll(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	text, err := src.Detail(context.Background(), "1")
	if err != nil {
		t.Fatal(err)
	}
	plain := ansi.ReplaceAllString(text, "")
	for _, want := range []string{"item:", "failed", "https://docs.example.org/page/1", "403", "tier:          js", "attempts:      1",
		"ERR::UPSTREAM::BLOCKED", "request id:    RQ1", "request log entry:", "spans (ms):", "total            900"} {
		if !strings.Contains(plain, want) {
			t.Errorf("detail lacks %q:\n%s", want, plain)
		}
	}
	if internalNames.MatchString(plain) {
		t.Errorf("engine name in detail:\n%s", plain)
	}
	if _, err := src.Detail(context.Background(), "999"); err == nil {
		t.Error("an unknown item must be an error")
	}
	delete(b.engines, "RQ1") // the request log no longer has it: the item page still renders
	text, err = src.Detail(context.Background(), "1")
	if err != nil || !strings.Contains(text, "unavailable:") {
		t.Errorf("err %v\n%s", err, text)
	}
}

func TestBatchSnapshotCounts(t *testing.T) {
	var j batchJob
	if err := (&api.Response{Body: []byte(running(7, 2))}).Decode(&j); err != nil {
		t.Fatal(err)
	}
	s := batchSnapshot(j)
	if s.Total != 40 || s.Completed != 7 || s.Failed != 2 || s.Succeeded != 5 || s.Started.IsZero() || s.ID != gBatchID {
		t.Errorf("%+v", s)
	}
}
