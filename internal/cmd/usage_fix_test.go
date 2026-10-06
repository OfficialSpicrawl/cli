package cmd

import (
	"net/http"
	"strings"
	"testing"
)

// The summary must show the enforced monthly allowance and what is left of it,
// not only the plan's included credits.
func TestUsageSummaryShowsEnforcedAllowance(t *testing.T) {
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"period":{"start":"2026-10-01","end":"2026-11-01","days":31},"period_elapsed_days":5,
"plan":{"code":"free","name":"Free","subscription_status":"none","period_source":"allowance_month"},
"credits":{"included_micro":1000000000,"used_micro":10000000,"remaining_micro":990000000,"overage_micro":0},
"allowance":{"limit_micro":1000000000,"limit_credits":1000,"used_micro":250500000,"remaining_micro":749500000,"unlimited":false,"resets_at":"2026-11-01T00:00:00Z"},
"metrics":{"credits":10000000}}`))
	})
	out, errb, code := runCLIHuman(t, srv.URL, "usage", "summary")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if !strings.Contains(out, "allowance:") || !strings.Contains(out, "250.5 of 1000 used") ||
		!strings.Contains(out, "749.5 remaining") || !strings.Contains(out, "resets 2026-11-01T00:00:00Z") {
		t.Errorf("allowance line missing or wrong:\n%s", out)
	}
}
