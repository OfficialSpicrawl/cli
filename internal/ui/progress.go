package ui

import (
	"fmt"
	"strings"
	"time"
)

// Bar draws a determinate progress bar exactly width cells wide:
//
//	━━━━━━━━╸───────────
//
// The filled part is the accent, the rest muted, and a ╸ marks a half cell so
// the bar moves twice as smoothly as its width. Where Unicode is not safe it
// is ####------. done is clamped to 0..total.
func (u *UI) Bar(width, done, total int) string {
	if width < 1 {
		return ""
	}
	g := u.Glyphs()
	done = max(0, min(done, total))
	half := 0 // filled, in half cells
	if total > 0 {
		half = done * width * 2 / total
	}
	full, part := half/2, half%2
	if g.BarHalf == "" { // ASCII: no half cells
		part = 0
	}
	rest := width - full - part
	return u.Accent(strings.Repeat(g.BarFull, full)+strings.Repeat(g.BarHalf, part)) +
		u.Dim(strings.Repeat(g.BarEmpty, rest))
}

// ETA estimates the time left from the rate so far: linear, so it is wrong
// when items vary a lot in cost, and not offered until something finished.
// ponytail: a moving average would steady it on long runs of uneven items.
func ETA(elapsed time.Duration, done, total int) (time.Duration, bool) {
	if done <= 0 || done >= total || elapsed <= 0 {
		return 0, false
	}
	return elapsed * time.Duration(total-done) / time.Duration(done), true
}

// Span formats a duration to whole seconds for an estimate: 22s, 1m04s, 1h05m.
func Span(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d/time.Minute), int(d%time.Minute/time.Second))
	}
	return fmt.Sprintf("%dh%02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
}

// progressLine lays out one spinner line with a bar inside cols cells:
//
//	⠹ Batch job  ━━━━━━━━╸────────────  7/20 · 12.1s · ETA 22s
//
// It gives up the least important parts first: the ETA, then the bar (down to
// 10 cells), then the label's tail. The result is plain text styled by u and
// never wider than cols.
func (u *UI) progressLine(frame, label string, done, total int, elapsed time.Duration, cols int) string {
	g := u.Glyphs()
	counts := fmt.Sprintf("%d/%d", done, total)
	if total <= 0 {
		counts = fmt.Sprintf("%d", done)
	}
	facts := []string{counts, Elapsed(elapsed)}
	eta := ""
	if d, ok := ETA(elapsed, done, total); ok && elapsed >= 2*time.Second { // sooner it is a guess from one item
		eta = "ETA " + Span(d)
	}
	stats := func(withETA bool) string {
		f := facts
		if withETA && eta != "" {
			f = append(append([]string{}, f...), eta)
		}
		return strings.Join(f, " "+g.Dot+" ")
	}
	const minLabel, minBar, maxBar = 12, 10, 28
	lead := Width(frame) + 1
	withETA := true
	barW := 0
	if total > 0 {
		// Room after the glyph, the shortest label and the two-cell gaps.
		room := cols - 1 - lead - minLabel - 2*2 - Width(stats(true))
		if room < minBar {
			withETA = false
			room = cols - 1 - lead - minLabel - 2*2 - Width(stats(false))
		}
		if room >= minBar {
			barW = min(room, maxBar)
		}
	}
	tail := stats(withETA)
	if barW > 0 {
		tail = u.Bar(barW, done, total) + "  " + u.Dim(tail)
	} else {
		tail = u.Dim(tail)
	}
	labelW := cols - 1 - lead - Width(tail) - 2
	if labelW < 4 { // not even a short label fits beside the numbers: the label wins
		return u.Accent(frame) + " " + Truncate(label, max(cols-1-lead, 1), g.Ellipsis)
	}
	return u.Accent(frame) + " " + Truncate(label, labelW, g.Ellipsis) + "  " + tail
}
