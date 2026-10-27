package cmd

// "spicrawl batch watch <id>": a live dashboard for a batch job. On a terminal
// it is the full-screen view of internal/tui; anywhere else it is "batch wait".
// Either way it ends with the job printed and batch wait's exit code: 0 for
// completed, 4 for failed or cancelled, 11 when --max-wait ran out.

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/OfficialSpicrawl/cli/internal/api"
	"github.com/OfficialSpicrawl/cli/internal/exitcode"
	"github.com/OfficialSpicrawl/cli/internal/output"
	"github.com/OfficialSpicrawl/cli/internal/tui"
	"github.com/OfficialSpicrawl/cli/internal/ui"
)

var (
	batchWatchPoll time.Duration
	batchWatchMax  time.Duration
)

var batchWatchCmd = &cobra.Command{
	Use:   "watch <id>",
	Short: "Live dashboard for a batch job (exit codes as batch wait)",
	Long: `Follow a batch job on a full-screen dashboard: a progress bar with done, failed
and total, the ETA and credits, and a table of the finished items (status,
HTTP status, tier, duration, URL), newest first. Enter opens an item (its
record and the request log entry behind it), f shows only failed items, s and
t filter by status and tier, / searches, p pauses, q or Esc leaves.

The table lists finished items, newest first, up to the first 1000 in
submission order; the job's own counters are always complete. A tier shows once
the request log has the item's request.

The dashboard closes when the job finishes and prints the final job, then exits
as "batch wait" does: 0 if completed, 4 if failed or cancelled, 11 if --max-wait
elapsed first. Quitting with q or Esc leaves the job running (exit 0), Ctrl-C
exits 130. A failed poll is retried with backoff and shown on the status bar.

When stdin or stdout is not a terminal, or with --json, --quiet, CI or
TERM=dumb, there is no dashboard: this is exactly "batch wait". NO_COLOR is
honoured.`,
	Example: `  spicrawl batch watch 01J9Z6V0Q8M4K2T7R3N5B1C9XA
  spicrawl batch submit urls.txt --name nightly | jq -r .id | xargs spicrawl batch watch`,
	Args: cobra.ExactArgs(1),
	RunE: batchRunWatch,
}

func init() {
	f := batchWatchCmd.Flags()
	f.DurationVar(&batchWatchPoll, "poll", 2*time.Second, "poll interval")
	f.DurationVar(&batchWatchMax, "max-wait", 30*time.Minute, "give up after this long (exit 11); 0 = never")
	batchCmd.AddCommand(batchWatchCmd)
}

func batchRunWatch(cmd *cobra.Command, args []string) error {
	id := args[0]
	c, err := Client()
	if err != nil {
		return err
	}
	p, ctx := Printer(), cmd.Context()
	if !watchInteractive() {
		return batchWaitAndPrint(cmd, c, p, id, batchWatchPoll, batchWatchMax)
	}

	src := &batchWatchSource{c: watchClient(c), id: id, tiers: map[string]string{}}
	sp := spin(p, "Fetching batch job %s", id)
	first, err := src.Poll(ctx, nil)
	sp.Stop()
	if err == nil && first.Done {
		return batchWaitAndPrint(cmd, c, p, id, batchWatchPoll, batchWatchMax) // already over: print it
	}
	cfg := tui.Config{
		Title: "spicrawl batch watch", Noun: "items", Empty: "no item has finished yet",
		Columns: []tui.Column{
			{Title: "SEQ", Width: 6, Right: true},
			{Title: "STATUS", Width: 9, Status: true},
			{Title: "HTTP", Width: 4, Right: true},
			{Title: "TIER", Width: 7, MinTerm: 60},
			{Title: "TOOK", Width: 6, Right: true},
			{Title: "URL"},
		},
		Statuses: []string{"succeeded", "failed", "cancelled", "skipped"},
		BadKey:   "f", BadLabel: "failed",
		BadStatus: func(s string) bool { return s == "failed" },
		Source:    src, Initial: first, Poll: batchWatchPoll,
	}
	if batchWatchMax > 0 {
		cfg.Deadline = time.Now().Add(batchWatchMax)
	}
	if err != nil {
		if !watchRetryable(err) {
			return err
		}
		cfg.InitialErr = err
	}

	reason, err := tui.Run(ctx, cfg, tuiOptions...)
	if err != nil {
		return err
	}
	switch reason {
	case tui.Finished:
		return batchWaitAndPrint(cmd, c, p, id, batchWatchPoll, batchWatchMax)
	case tui.Timeout:
		raw, job := src.latest()
		if err := batchPrintJob(p, raw, job); err != nil {
			return err
		}
		return &ExitError{Code: exitcode.Pending, Err: fmt.Errorf(
			"batch %s is still %s after %s; resume with: spicrawl batch watch %s", id, job.Status, batchWatchMax, id)}
	case tui.Interrupt:
		return &ExitError{Code: exitcode.Interrupted, Err: errors.New("interrupted")}
	}
	p.Info("stopped watching; the job keeps running: spicrawl batch watch %s", id)
	return nil
}

// batchWatchItem is one result line with the fields the dashboard adds to
// batchResultLine.
type batchWatchItem struct {
	batchResultLine
	Attempts     int      `json:"attempts"`
	RequestID    string   `json:"request_id"`
	CreditsMicro int64    `json:"credits_micro"`
	DurationMS   int64    `json:"duration_ms"`
	Warnings     []string `json:"warnings"`
	FinishedAt   string   `json:"finished_at"`
}

const (
	batchWatchPage  = 500 // result lines per request
	batchWatchPages = 2   // so the table holds at most 1000 items
	batchWatchTiers = 200 // newest items whose tier is looked up
)

// batchWatchSource reads a job and its finished items. It is the tui.Source of
// the dashboard.
type batchWatchSource struct {
	c  *api.Client
	id string

	mu       sync.Mutex
	raw      []byte // the last job, as sent
	job      batchJob
	itemsAt  batchProgressCounts // progress the table was built for
	haveRows bool
	items    map[string]batchWatchItem // by seq, for Detail
	tiers    map[string]string         // request id -> tier
}

func (s *batchWatchSource) latest() ([]byte, batchJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.raw, s.job
}

func (s *batchWatchSource) Poll(ctx context.Context, _ map[string]struct{}) (tui.Update, error) {
	resp, err := s.c.Do(ctx, api.Request{Path: "/v1/batch/" + url.PathEscape(s.id)})
	if err != nil {
		return tui.Update{}, watchErr(err)
	}
	var job batchJob
	if err := resp.Decode(&job); err != nil {
		return tui.Update{}, err
	}
	s.mu.Lock()
	s.raw, s.job = resp.Body, job
	stale := !s.haveRows || s.itemsAt != job.Progress
	s.mu.Unlock()

	u := tui.Update{Header: tui.BatchHeader(batchSnapshot(job)), Done: batchTerminal[job.Status]}
	if u.Done || !stale {
		return u, nil // a finished job ends the view; an unchanged one has the same table
	}
	rows, truncated, err := s.finished(ctx)
	if err != nil {
		u.Note = "items: " + err.Error() // keep the progress current, try the table again next poll
		return u, nil
	}
	s.mu.Lock()
	s.itemsAt, s.haveRows = job.Progress, true
	s.mu.Unlock()
	u.Rows, u.Replace = rows, true
	if truncated {
		u.Note = fmt.Sprintf("table: first %d finished items", len(rows))
	}
	return u, nil
}

func batchSnapshot(j batchJob) tui.BatchSnapshot {
	pr := j.Progress
	total := pr.Total
	if total == 0 {
		total = j.TotalItems
	}
	return tui.BatchSnapshot{
		ID: j.ID, Name: j.Name, Status: j.Status,
		Total: total, Completed: pr.Completed, Succeeded: pr.Succeeded, Failed: pr.Failed,
		Cancelled: pr.Cancelled, Skipped: pr.Skipped, Credits: pr.CreditsCharged, Reserved: j.EstimatedCredits,
		Started: watchTime(j.StartedAt), Finished: watchTime(j.FinishedAt),
		Error: strings.TrimSpace(j.ErrorCode + " " + j.ErrorMessage),
	}
}

func watchTime(ts string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, ts)
	return t
}

// finished reads the job's finished items and returns them as rows, newest
// first. truncated says there were more than the table holds.
func (s *batchWatchSource) finished(ctx context.Context) (rows []tui.Row, truncated bool, err error) {
	q := url.Values{"limit": {strconv.Itoa(batchWatchPage)}}
	var items []batchWatchItem
	for page := 1; ; page++ {
		resp, err := s.c.Do(ctx, api.Request{Path: "/v1/batch/" + url.PathEscape(s.id) + "/results", Query: q, Accept: "application/x-ndjson"})
		if err != nil {
			return nil, false, watchErr(err)
		}
		dec := json.NewDecoder(bytes.NewReader(resp.Body))
		for {
			var it batchWatchItem
			if err := dec.Decode(&it); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				return nil, false, fmt.Errorf("decode results: %w", err)
			}
			items = append(items, it)
		}
		next := batchNextCursor(resp)
		if next == "" {
			break
		}
		if page == batchWatchPages {
			truncated = true
			break
		}
		q.Set("cursor", next)
	}
	slices.SortStableFunc(items, func(a, b batchWatchItem) int {
		if c := watchTime(b.FinishedAt).Compare(watchTime(a.FinishedAt)); c != 0 {
			return c
		}
		return cmp.Compare(b.Seq, a.Seq)
	})
	s.learnTiers(ctx, items[:min(len(items), batchWatchTiers)])

	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = make(map[string]batchWatchItem, len(items))
	for _, it := range items {
		key := strconv.FormatInt(it.Seq, 10)
		s.items[key] = it
		dur := "—"
		if it.DurationMS > 0 {
			dur = ui.Elapsed(time.Duration(it.DurationMS) * time.Millisecond)
		}
		tier := s.tiers[it.RequestID]
		code := ""
		if it.Error != nil {
			code = it.Error.Code
		}
		rows = append(rows, tui.Row{
			Key: key, Status: it.Status, Tier: tier,
			Cells:  []string{key, it.Status, logsOptInt(it.HTTPStatus), logsDash(tier), dur, it.URL},
			Search: strings.Join([]string{it.URL, it.ExternalID, key, it.Status, code}, " "),
		})
	}
	return rows, truncated, nil
}

// learnTiers fills in the tier of items whose request the log has. The batch
// result line carries no tier, so it comes from the request log, one page for
// all of them; a miss leaves the cell as — and is not an error.
func (s *batchWatchSource) learnTiers(ctx context.Context, items []batchWatchItem) {
	s.mu.Lock()
	missing := false
	for _, it := range items {
		if _, ok := s.tiers[it.RequestID]; !ok && it.RequestID != "" {
			missing = true
		}
	}
	s.mu.Unlock()
	if !missing {
		return
	}
	resp, err := s.c.Do(ctx, api.Request{Path: "/v1/requests", Query: url.Values{"limit": {"200"}}})
	if err != nil {
		return
	}
	var page logsPage
	if resp.Decode(&page) != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, raw := range page.Data {
		var e logsEntry
		if json.Unmarshal(raw, &e) == nil {
			s.tiers[e.ID] = output.Tier(e.Engine)
		}
	}
}

// Detail is the item's record, then the request log entry behind it ("logs get").
func (s *batchWatchSource) Detail(ctx context.Context, key string) (string, error) {
	s.mu.Lock()
	it, ok := s.items[key]
	tier := s.tiers[it.RequestID]
	s.mu.Unlock()
	if !ok {
		return "", errors.New("this item is no longer in the table")
	}
	var b strings.Builder
	var u *ui.UI
	if tui.Colour() {
		u = ui.Force(&b)
	}
	kv := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s %s\n", viewKey(u, 14, k+":"), v)
		}
	}
	kv("item", key)
	kv("status", logsStatusCell(u, it.Status))
	kv("url", it.URL)
	kv("external id", it.ExternalID)
	kv("http status", logsOptInt(it.HTTPStatus))
	kv("tier", tier)
	if it.Attempts > 0 {
		kv("attempts", strconv.Itoa(it.Attempts))
	}
	if it.DurationMS > 0 {
		kv("duration", ui.Elapsed(time.Duration(it.DurationMS)*time.Millisecond))
	}
	kv("credits", logsCredits(it.CreditsMicro))
	if it.Bytes > 0 {
		kv("bytes", strconv.FormatInt(it.Bytes, 10))
	}
	if it.Error != nil {
		kv("error", it.Error.Code)
	}
	kv("warnings", strings.Join(it.Warnings, ", "))
	kv("finished", it.FinishedAt)
	kv("request id", it.RequestID)

	if it.RequestID != "" {
		fmt.Fprintln(&b, "\n"+u.Bold("request log entry:")+"\n")
		resp, err := s.c.Do(ctx, api.Request{Path: "/v1/requests/" + url.PathEscape(it.RequestID)})
		var e logsEntry
		if err == nil {
			err = resp.Decode(&e)
		}
		if err != nil {
			fmt.Fprintf(&b, "unavailable: %v\n", err)
		} else {
			logsWriteEntry(&b, u, &e)
		}
	}
	return b.String(), nil
}
