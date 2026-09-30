package cmd

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/Spicrawl/cli/internal/api"
	"github.com/Spicrawl/cli/internal/exitcode"
	"github.com/Spicrawl/cli/internal/output"
)

// batchTerminal are the job statuses at which polling stops.
var batchTerminal = map[string]bool{"completed": true, "failed": true, "cancelled": true}

// batchWaitJob polls GET /v1/batch/{id} every poll until the job is terminal.
// A 429 (or a retryable 503) is not fatal: the next poll waits for the
// server's Retry-After instead. When timeout (> 0) elapses first it returns
// the last job it saw together with an ExitError{Code: Pending}.
func batchWaitJob(ctx context.Context, c *api.Client, p *output.Printer, id string, poll, timeout time.Duration) ([]byte, error) {
	if poll <= 0 {
		poll = time.Second
	}
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	prog := batchNewProgress(p)
	defer prog.done()

	var last []byte
	var lastJob batchJob
	for {
		delay := poll
		resp, err := c.Do(ctx, api.Request{Path: "/v1/batch/" + url.PathEscape(id)})
		if err != nil {
			prob, ok := api.AsProblem(err)
			if !ok || !(prob.Status == http.StatusTooManyRequests || (prob.Status == http.StatusServiceUnavailable && prob.Retryable)) {
				return last, err
			}
			if prob.RetryAfterSeconds > 0 {
				delay = time.Duration(prob.RetryAfterSeconds) * time.Second
			}
			prog.note("rate limited; next poll in %s", delay)
		} else {
			var job batchJob
			if err := resp.Decode(&job); err != nil {
				return last, err
			}
			last, lastJob = resp.Body, job
			prog.update(job)
			if batchTerminal[job.Status] {
				return last, nil
			}
		}

		if !deadline.IsZero() {
			left := time.Until(deadline)
			if left <= 0 {
				status := lastJob.Status
				if status == "" {
					status = "unknown"
				}
				return last, &ExitError{Code: exitcode.Pending, Err: fmt.Errorf(
					"batch %s is still %s after %s; resume with: spicrawl batch wait %s", id, status, timeout, id)}
			}
			if delay > left {
				delay = left
			}
		}
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return last, ctx.Err()
		case <-t.C:
		}
	}
}

// batchProgress renders the wait progress line on stderr in human mode. On a
// TTY the line is rewritten in place with \r; otherwise a new line is written
// only when the counters change, so logs stay readable. JSON mode and --quiet
// print nothing.
type batchProgress struct {
	p       *output.Printer
	enabled bool
	tty     bool
	last    string
	dirty   bool
}

func batchNewProgress(p *output.Printer) *batchProgress {
	return &batchProgress{p: p, enabled: !p.JSON && !p.Quiet, tty: batchIsTerminal(p.Err)}
}

func (b *batchProgress) update(j batchJob) {
	if !b.enabled {
		return
	}
	line := fmt.Sprintf("batch %s: %s %d/%d (%.1f%%)  succeeded %d  failed %d",
		j.ID, j.Status, j.Progress.Completed, j.Progress.Total, j.Progress.PercentComplete,
		j.Progress.Succeeded, j.Progress.Failed)
	if line == b.last {
		return
	}
	b.last = line
	if b.tty {
		fmt.Fprintf(b.p.Err, "\r\033[K%s", line)
		b.dirty = true
		return
	}
	fmt.Fprintln(b.p.Err, line)
}

func (b *batchProgress) note(format string, a ...any) {
	if !b.enabled {
		return
	}
	b.done()
	fmt.Fprintf(b.p.Err, format+"\n", a...)
}

func (b *batchProgress) done() {
	if b.dirty {
		fmt.Fprintln(b.p.Err)
		b.dirty = false
	}
}

// batchIsTerminal reports whether w is a character device (a terminal).
func batchIsTerminal(w any) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
