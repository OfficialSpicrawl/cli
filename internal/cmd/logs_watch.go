package cmd

// "spicrawl logs --watch": a live request log. On a terminal it is the
// full-screen view of internal/tui; anywhere else it streams new rows as
// plain lines (JSONL in JSON mode), oldest first, like tail -f.
//
// The flags and the branch are added from here (init wraps logsCmd.RunE), so
// logs.go is untouched.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/OfficialSpicrawl/cli/internal/api"
	"github.com/OfficialSpicrawl/cli/internal/exitcode"
	"github.com/OfficialSpicrawl/cli/internal/output"
	"github.com/OfficialSpicrawl/cli/internal/tui"
	"github.com/OfficialSpicrawl/cli/internal/ui"
)

var (
	logsWatch bool
	logsPoll  time.Duration
)

func init() {
	f := logsCmd.Flags()
	f.BoolVar(&logsWatch, "watch", false, "live view: new requests appear at the top (full-screen on a terminal; plain lines, or JSONL with --json, otherwise)")
	f.DurationVar(&logsPoll, "poll", 2*time.Second, "with --watch: poll interval")
	logsCmd.Example += `
  spicrawl logs --watch
  spicrawl logs --watch --errors --all-projects
  spicrawl logs --watch --json | jq -r 'select(.status != "success") | .url'`
	logsCmd.Long += `

--watch polls the log and shows new requests as they arrive. On a terminal it
is full-screen: arrows move, Enter opens a request (what "logs get" prints),
/ searches, s, t and e filter by status, tier and errors, p pauses, q or Esc
quits. Filters apply to the rows loaded so far (the newest --limit, up to 200,
and everything since); --errors and --status narrow the query itself. When
stdin or stdout is not a terminal, or with --json, --quiet, CI or TERM=dumb, it
prints new requests one per line instead (JSONL with --json). NO_COLOR is
honoured. A failed poll is retried with backoff.`

	run := logsCmd.RunE
	logsCmd.RunE = func(cmd *cobra.Command, args []string) error {
		switch {
		case logsWatch:
			return logsRunWatch(cmd)
		case cmd.Flags().Changed("poll"):
			return Usagef("--poll needs --watch")
		}
		return run(cmd, args)
	}
}

func logsRunWatch(cmd *cobra.Command) error {
	if logsAll || logsJSONL {
		return Usagef("--watch cannot be combined with --all or --jsonl")
	}
	if logsPoll <= 0 {
		return Usagef("--poll must be positive")
	}
	q, err := logsWatchQuery(cmd.Flags())
	if err != nil {
		return err
	}
	c, err := Client()
	if err != nil {
		return err
	}
	p, ctx := Printer(), cmd.Context()
	src := &logsWatchSource{c: watchClient(c), q: q, project: logsAllProjects}
	if !watchInteractive() {
		return logsWatchPlain(ctx, p, src, logsPoll)
	}

	sp := spin(p, "Fetching the request log")
	first, err := src.Poll(ctx, nil)
	sp.Stop()
	cfg := tui.Config{
		Title: "spicrawl logs", Noun: "requests", Empty: "no requests yet: new ones appear here as they arrive",
		Columns: src.columns(), Statuses: logsStatuses, BadKey: "e", BadLabel: "errors",
		BadStatus: func(s string) bool { return s != "success" },
		Source:    src, Initial: first, Poll: logsPoll,
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
	if reason == tui.Interrupt {
		return &ExitError{Code: exitcode.Interrupted, Err: errors.New("interrupted")}
	}
	return nil
}

// logsWatchQuery is the query of "logs" for the flags --watch shares with it
// (the validation is the same as logs.go's). A watch fetches the largest page
// unless --limit says otherwise, so the filters have rows to work on.
func logsWatchQuery(f *pflag.FlagSet) (url.Values, error) {
	q := url.Values{}
	limit := 200
	if f.Changed("limit") {
		if logsLimit < 1 || logsLimit > 200 {
			return nil, Usagef("--limit must be between 1 and 200")
		}
		limit = logsLimit
	}
	q.Set("limit", strconv.Itoa(limit))
	if logsStatus != "" {
		s := strings.ToLower(logsStatus)
		found := false
		for _, v := range logsStatuses {
			found = found || v == s
		}
		if !found {
			return nil, Usagef("--status must be one of %s, not %q", strings.Join(logsStatuses, ", "), logsStatus)
		}
		if logsErrors {
			return nil, Usagef("--errors and --status cannot be combined")
		}
		q.Set("status", s)
	}
	if logsErrors {
		q.Set("only_errors", "true")
	}
	if logsAllProjects {
		q.Set("all_projects", "true")
	}
	return q, nil
}

// logsWatchSource reads the request log. It is the tui.Source of the view and
// the poller of the plain stream.
type logsWatchSource struct {
	c       *api.Client
	q       url.Values
	project bool
}

type logsRec struct {
	raw json.RawMessage
	e   logsEntry
}

// maxGapPages bounds how far back one poll pages to reach rows it already has.
const maxGapPages = 5

// fetch returns the requests newer than known, newest first. The first poll
// (nothing known) reads one page; later ones page back until they reach a
// request they have, so a burst longer than one page leaves no hole.
func (s *logsWatchSource) fetch(ctx context.Context, known map[string]struct{}) ([]logsRec, error) {
	q := url.Values{}
	for k, v := range s.q {
		q[k] = v
	}
	pages := 1
	if len(known) > 0 {
		pages = maxGapPages
	}
	var out []logsRec
	for i := 0; i < pages; i++ {
		resp, err := s.c.Do(ctx, api.Request{Path: "/v1/requests", Query: q})
		if err != nil {
			return nil, watchErr(err)
		}
		var page logsPage
		if err := resp.Decode(&page); err != nil {
			return nil, err
		}
		reached := false
		for _, raw := range page.Data {
			var r logsRec
			r.raw = raw
			_ = json.Unmarshal(raw, &r.e)
			if _, ok := known[r.e.ID]; ok {
				reached = true
				continue
			}
			out = append(out, r)
		}
		if reached || !page.Page.HasMore || page.Page.NextBefore == "" || page.Page.NextBeforeID == "" {
			break
		}
		q.Set("before", page.Page.NextBefore)
		q.Set("before_id", page.Page.NextBeforeID)
	}
	return out, nil
}

func (s *logsWatchSource) columns() []tui.Column {
	cols := []tui.Column{{Title: "TIME", Width: 8}}
	if s.project {
		cols = append(cols, tui.Column{Title: "PROJECT", Width: 8})
	}
	return append(cols,
		tui.Column{Title: "STATUS", Width: 9, Status: true},
		tui.Column{Title: "HTTP", Width: 4, Right: true},
		tui.Column{Title: "TIER", Width: 7, MinTerm: 60},
		tui.Column{Title: "CREDITS", Width: 8, Right: true, MinTerm: 100},
		tui.Column{Title: "MS", Width: 6, Right: true, MinTerm: 72},
		tui.Column{Title: "URL"},
	)
}

func (s *logsWatchSource) row(e logsEntry) tui.Row {
	tier := output.Tier(e.Engine)
	cells := []string{watchClock(e.CreatedAt)}
	if s.project {
		cells = append(cells, strings.TrimSpace(tail(e.ProjectID, 8)))
	}
	cells = append(cells, e.Status, logsOptInt(e.HTTPStatus), logsDash(tier), logsCredits(e.CreditsMicro), logsSpan(e.Spans.Total), e.URL)
	return tui.Row{
		Key: e.ID, Cells: cells, Status: e.Status, Tier: tier,
		Search: strings.Join([]string{e.URL, e.ID, e.Status, e.ErrorCode, e.ProjectID}, " "),
	}
}

func (s *logsWatchSource) Poll(ctx context.Context, known map[string]struct{}) (tui.Update, error) {
	recs, err := s.fetch(ctx, known)
	if err != nil {
		return tui.Update{}, err
	}
	rows := make([]tui.Row, len(recs))
	for i, r := range recs {
		rows[i] = s.row(r.e)
	}
	return tui.Update{Rows: rows}, nil
}

// Detail is the page of "logs get", in colour unless NO_COLOR is set.
func (s *logsWatchSource) Detail(ctx context.Context, id string) (string, error) {
	resp, err := s.c.Do(ctx, api.Request{Path: "/v1/requests/" + url.PathEscape(id)})
	if err != nil {
		return "", err
	}
	var e logsEntry
	if err := resp.Decode(&e); err != nil {
		return "", err
	}
	var b strings.Builder
	var u *ui.UI
	if tui.Colour() {
		u = ui.Force(&b)
	}
	logsWriteEntry(&b, u, &e)
	return b.String(), nil
}

// logsWatchPlain streams requests as they arrive, oldest first within a poll:
// the newest page at the start, then only new ones. Failed polls are retried
// with backoff and reported on stderr; Ctrl-C ends it.
func logsWatchPlain(ctx context.Context, p *output.Printer, src *logsWatchSource, every time.Duration) error {
	var newest []string // the newest request ids seen, newest first
	known := map[string]struct{}{}
	failures := 0
	for first := true; ; first = false {
		recs, err := src.fetch(ctx, known)
		delay := every
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if first && !watchRetryable(err) {
				return err
			}
			failures++
			delay = tui.Backoff(every, failures, err)
			p.Warn("request log: %v; retrying in %s", err, delay.Round(time.Second))
		} else {
			failures = 0
			ids := make([]string, 0, len(recs))
			for i := len(recs) - 1; i >= 0; i-- {
				if err := logsStreamLine(p, recs[i]); err != nil {
					return err
				}
			}
			for _, r := range recs {
				ids = append(ids, r.e.ID)
			}
			newest = append(ids, newest...)
			newest = newest[:min(len(newest), 400)]
			known = make(map[string]struct{}, len(newest))
			for _, id := range newest {
				known[id] = struct{}{}
			}
		}
		if err := sleepCtx(ctx, delay); err != nil {
			return err
		}
	}
}

func logsStreamLine(p *output.Printer, r logsRec) error {
	if p.JSON {
		return p.Line(r.raw)
	}
	e := r.e
	_, err := fmt.Fprintf(p.Out, "%s  %-9s  %4s  %-7s  %8s  %6s  %s\n",
		e.CreatedAt, e.Status, logsOptInt(e.HTTPStatus), logsDash(output.Tier(e.Engine)),
		logsCredits(e.CreditsMicro), logsSpan(e.Spans.Total), e.URL)
	return err
}

// watchClock is a timestamp as HH:MM:SS in the local zone; anything that does
// not parse is shown as sent.
func watchClock(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ts
	}
	return t.Local().Format("15:04:05")
}

// tail is the last n bytes of s.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
