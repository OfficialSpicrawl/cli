package cmd

// Shared pieces of the live views: "spicrawl logs --watch" and
// "spicrawl batch watch" (logs_watch.go, batch_watch.go). The full-screen part
// is internal/tui; this file is what both commands need to start it.

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/OfficialSpicrawl/cli/internal/api"
	"github.com/OfficialSpicrawl/cli/internal/tui"
)

// watchInteractive reports whether a full-screen view may run: stdin and
// stdout are terminals, and none of --json, --quiet, CI or TERM=dumb is set.
// Tests replace it.
var watchInteractive = func() bool { return tui.Interactive(flagJSON || flagQuiet) }

// tuiOptions are extra Bubble Tea options for the view. Tests use them to give
// it a fake terminal.
var tuiOptions []tea.ProgramOption

// watchClient is c without the stderr spinner and retry notes, which would be
// drawn across the alternate screen.
func watchClient(c *api.Client) *api.Client {
	q := *c
	q.Wait, q.Notify = nil, nil
	return &q
}

// rateLimited marks a 429 so the view waits for Retry-After, not for its own
// backoff.
type rateLimited struct {
	error
	after time.Duration
}

func (e *rateLimited) Unwrap() error             { return e.error }
func (e *rateLimited) RetryAfter() time.Duration { return e.after }

func watchErr(err error) error {
	if p, ok := api.AsProblem(err); ok && p.Status == 429 && p.RetryAfterSeconds > 0 {
		return &rateLimited{err, time.Duration(p.RetryAfterSeconds) * time.Second}
	}
	return err
}

// watchRetryable reports whether a failed first poll is worth opening the view
// for. A wrong key or id (a 4xx) is not: it fails at once with its exit code,
// like any other command. After the first poll every failure is retried.
func watchRetryable(err error) bool {
	if p, ok := api.AsProblem(err); ok && p.Status == 429 {
		return true
	}
	return batchPollTransient(err)
}

// sleepCtx waits d, or returns ctx's error as soon as ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
