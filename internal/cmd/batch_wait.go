package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/OfficialSpicrawl/cli/internal/api"
	"github.com/OfficialSpicrawl/cli/internal/exitcode"
	"github.com/OfficialSpicrawl/cli/internal/output"
)

// batchTerminal are the job statuses at which polling stops.
var batchTerminal = map[string]bool{"completed": true, "failed": true, "cancelled": true}

// batchWaitMaxFailures is how many polls in a row may fail transiently before
// batchWaitJob gives up with the last error.
const batchWaitMaxFailures = 5

// batchPollTransient reports whether a failed poll is worth repeating. A poll
// is a GET, so a repeat is always safe, even after a timeout.
func batchPollTransient(err error) bool {
	if prob, ok := api.AsProblem(err); ok {
		return prob.Status == http.StatusBadGateway || prob.Status == http.StatusGatewayTimeout ||
			(prob.Status >= 500 && prob.Retryable)
	}
	var (
		ne *api.NetworkError
		te *api.TimeoutError
		ce *api.ConnectionLostError
	)
	return errors.As(err, &ne) || errors.As(err, &te) || errors.As(err, &ce)
}

// batchWaitJob polls GET /v1/batch/{id} every poll until the job is terminal.
// A 429 is not fatal: the next poll waits for the server's Retry-After
// instead. Transient failures (502/504, retryable 5xx, network errors and
// timeouts) are retried with exponential backoff; batchWaitMaxFailures in a
// row return the last error. When timeout (> 0) elapses first it returns the
// last job it saw together with an ExitError{Code: Pending}.
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
	failures := 0
	for {
		delay := poll
		resp, err := c.Do(ctx, api.Request{Path: "/v1/batch/" + url.PathEscape(id)})
		if err != nil {
			prob, isProb := api.AsProblem(err)
			limited := isProb && prob.Status == http.StatusTooManyRequests
			if !limited {
				if failures++; !batchPollTransient(err) || failures >= batchWaitMaxFailures {
					return last, err
				}
				delay = min(poll<<failures, max(poll, 30*time.Second))
			}
			if isProb && prob.RetryAfterSeconds > 0 {
				delay = min(time.Duration(prob.RetryAfterSeconds)*time.Second, time.Minute)
			}
			prog.note("poll failed (%v); next poll in %s", err, delay)
		} else {
			failures = 0
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
