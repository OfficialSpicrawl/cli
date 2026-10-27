package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/OfficialSpicrawl/cli/internal/output"
	"github.com/OfficialSpicrawl/cli/internal/ui"
)

// BatchSnapshot is the job facts the dashboard header shows.
type BatchSnapshot struct {
	ID, Name, Status                    string
	Total, Completed, Succeeded, Failed int64
	Cancelled, Skipped                  int64
	Credits, Reserved                   int64
	Started, Finished                   time.Time // zero when unknown
	Error                               string
}

// BatchHeader is the three-line block above the item table:
//
//	batch 01J9Z… · my-job                                   running
//	████████████░░░░░░░░░░░░░░░░░░░  38%  380/1000 done · 8 failed
//	ETA 2m10s · 4.3 items/s · elapsed 1m28s · credits 1140 of ~3000 reserved
func BatchHeader(s BatchSnapshot) func(width int, now time.Time) []string {
	return func(w int, now time.Time) []string {
		pct := 0.0
		if s.Total > 0 {
			pct = float64(s.Completed) / float64(s.Total)
		}
		title := "batch " + clean(s.ID)
		if s.Name != "" {
			title += " · " + clean(s.Name)
		}
		status := clean(s.Status)
		pad := max(1, w-ansi.StringWidth(title)-ansi.StringWidth(status)-2)
		l1 := sBold.Render(fit(" "+title+strings.Repeat(" ", pad)+status, w, false))

		counts := fmt.Sprintf("  %3.0f%%  %d/%d done", pct*100, s.Completed, s.Total)
		failed := ""
		for _, c := range []struct {
			n    int64
			name string
		}{{s.Failed, "failed"}, {s.Cancelled, "cancelled"}, {s.Skipped, "skipped"}} {
			if c.n > 0 {
				failed += fmt.Sprintf(" · %d %s", c.n, c.name)
			}
		}
		bar := progress.New(progress.WithColors(lipgloss.Color("2")), progress.WithoutPercentage(), progress.WithFillCharacters('█', '░'),
			progress.WithWidth(max(10, w-ansi.StringWidth(counts+failed)-2)))
		l2 := " " + bar.ViewAs(pct) + counts
		if failed != "" {
			l2 += lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(failed)
		}

		eta, rate, elapsed := "—", "—", "—"
		if !s.Started.IsZero() {
			end := now
			if !s.Finished.IsZero() {
				end = s.Finished
			}
			d := max(0, end.Sub(s.Started))
			elapsed = ui.Elapsed(d)
			if s.Completed > 0 && d > 0 {
				per := float64(s.Completed) / d.Seconds()
				rate = fmt.Sprintf("%.1f items/s", per)
				if left := s.Total - s.Completed; left > 0 && s.Finished.IsZero() {
					eta = ui.Elapsed(time.Duration(float64(left) / per * float64(time.Second)))
				}
			}
		}
		l3 := fmt.Sprintf(" ETA %s · %s · elapsed %s · credits %d of ~%d reserved", eta, rate, elapsed, s.Credits, s.Reserved)
		if s.Error != "" {
			l3 = " " + sRed.Render(output.PublicText(clean(s.Error)))
		}
		return []string{l1, ansi.Truncate(l2, w, "…"), sDim.Render(fit(l3, w, false))}
	}
}
