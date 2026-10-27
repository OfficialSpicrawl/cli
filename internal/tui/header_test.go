package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"
)

var batchNow = time.Date(2026, 10, 27, 12, 10, 0, 0, time.UTC)

func batchSnap() BatchSnapshot {
	return BatchSnapshot{
		ID: "01J9Z6V0Q8M4K2T7R3N5B1C9XA", Name: "nightly-crawl", Status: "running",
		Total: 1000, Completed: 380, Succeeded: 372, Failed: 8, Credits: 1140, Reserved: 3000,
		Started: batchNow.Add(-90 * time.Second),
	}
}

func batchCfg(src Source, rows ...Row) Config {
	return Config{
		Title: "spicrawl batch watch", Noun: "items", Empty: "no item has finished yet",
		Columns: []Column{
			{Title: "SEQ", Width: 6, Right: true}, {Title: "STATUS", Width: 9, Status: true},
			{Title: "HTTP", Width: 4, Right: true}, {Title: "TIER", Width: 7, MinTerm: 60},
			{Title: "TOOK", Width: 6, Right: true}, {Title: "URL"},
		},
		Statuses: []string{"succeeded", "failed", "cancelled", "skipped"},
		BadKey:   "f", BadLabel: "failed", BadStatus: func(s string) bool { return s == "failed" },
		Source: src, Poll: time.Second, Now: func() time.Time { return batchNow },
		Initial: Update{Rows: rows, Header: BatchHeader(batchSnap())},
	}
}

func item(seq, status, tier, took, url string) Row {
	return Row{Key: seq, Status: status, Tier: tier, Search: url + " " + seq, Cells: []string{seq, status, "200", tier, took, url}}
}

func batchRows() []Row {
	return []Row{
		item("379", "succeeded", "js", "1.8s", "https://docs.example.org/page/379"),
		item("378", "failed", "browser", "12.4s", "https://docs.example.org/page/378"),
		item("377", "succeeded", "fetch", "0.4s", "https://docs.example.org/page/377"),
		item("376", "cancelled", "—", "—", "https://docs.example.org/page/376"),
	}
}

func newBatch() *Model {
	m := New(context.Background(), batchCfg(&fakeSource{}, batchRows()...))
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m
}

func TestSnapshotBatch80x24(t *testing.T) {
	golden.RequireEqual(t, text(newBatch()))
}

func TestSnapshotBatchFailedOnly(t *testing.T) {
	m := newBatch()
	press(m, "f")
	if keys(m) != "378" {
		t.Errorf("failed only: %s", keys(m))
	}
	golden.RequireEqual(t, text(m))
}

func TestBatchHeaderNumbers(t *testing.T) {
	out := strings.Join(BatchHeader(batchSnap())(80, batchNow), "\n")
	out = ansi.Strip(out)
	for _, want := range []string{
		"batch 01J9Z6V0Q8M4K2T7R3N5B1C9XA · nightly-crawl", "running",
		" 38%  380/1000 done · 8 failed",
		"ETA 2m27s", "4.2 items/s", "elapsed 1m30s", "credits 1140 of ~3000 reserved",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("header lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "█") || !strings.Contains(out, "░") {
		t.Errorf("no progress bar:\n%s", out)
	}
}

func TestBatchHeaderBeforeAndAfterTheRun(t *testing.T) {
	queued := batchSnap()
	queued.Status, queued.Started, queued.Completed, queued.Failed = "queued", time.Time{}, 0, 0
	out := ansi.Strip(strings.Join(BatchHeader(queued)(80, batchNow), "\n"))
	if !strings.Contains(out, "ETA — · — · elapsed —") || !strings.Contains(out, "0%  0/1000 done") {
		t.Errorf("queued job:\n%s", out)
	}

	done := batchSnap()
	done.Status, done.Completed, done.Finished = "completed", 1000, batchNow.Add(-30*time.Second)
	out = ansi.Strip(strings.Join(BatchHeader(done)(80, batchNow), "\n"))
	if !strings.Contains(out, "ETA — ") || !strings.Contains(out, "elapsed 1m00s") || !strings.Contains(out, "100%") {
		t.Errorf("finished job keeps counting:\n%s", out)
	}

	failed := batchSnap()
	failed.Error = "ERR::LIMIT::FAILURE_THRESHOLD reached in moli"
	out = ansi.Strip(strings.Join(BatchHeader(failed)(80, batchNow), "\n"))
	if strings.Contains(strings.ToLower(out), "moli") || !strings.Contains(out, "ERR::LIMIT::FAILURE_THRESHOLD") {
		t.Errorf("job error:\n%s", out)
	}
}

func TestBatchHeaderFitsNarrowTerminals(t *testing.T) {
	for _, w := range []int{40, 50, 80, 140} {
		for i, l := range BatchHeader(batchSnap())(w, batchNow) {
			if got := ansi.StringWidth(l); got > w {
				t.Errorf("width %d: header line %d is %d wide: %q", w, i, got, ansi.Strip(l))
			}
		}
	}
}

func TestBatchTableAndHeaderTogether24Rows(t *testing.T) {
	// The header is three lines; the table gets what is left of 24.
	m := newBatch()
	if got := m.listH(); got != 24-5-3 {
		t.Errorf("listH = %d", got)
	}
	if lines := strings.Split(text(m), "\n"); len(lines) != 24 {
		t.Errorf("%d lines", len(lines))
	}
}
