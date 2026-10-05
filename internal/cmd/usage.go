package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/OfficialSpicrawl/cli/internal/api"
	"github.com/OfficialSpicrawl/cli/internal/output"
)

var (
	usageFrom    string
	usageTo      string
	usageGroupBy string
	usageProject string
	usageMetrics []string
	usageDay     string
)

var usageMetricNames = []string{"requests", "credits", "engine_ms", "bytes_egress", "bytes_ingress",
	"proxy_bytes_dc", "proxy_bytes_resi", "extractions", "ai_extractions", "batch_items",
	"requests_failed", "requests_timeout", "feature_actions", "feature_ai_extract", "feature_autoparse", "feature_extract", "feature_extract_preset", "feature_js_render", "feature_links", "feature_network_capture", "feature_pdf", "feature_proxy_country", "feature_proxy_custom", "feature_screenshot", "feature_session", "client_cli", "client_mcp", "client_sdk", "client_other"}

var usageCmd = &cobra.Command{
	Use:   "usage",
	Short: "Billed usage over a window (GET /v1/usage)",
	Long: `Billed usage for the key's organization, broken down by one dimension.

Days are UTC. --to is EXCLUSIVE: one day 2026-08-01 is
--from 2026-08-01 --to 2026-08-02. The default window is the last 30 days
including today; at most 400 days per call.

totals is the billed figure; trust it over the sum of groups. credits are in
micro-credits in JSON (1 credit = 1000000); the table shows whole credits.
Needs a key with the read scope.`,
	Example: `  spicrawl usage
  spicrawl usage --from 2026-08-01 --to 2026-09-01 --group-by engine
  spicrawl usage --group-by project --metrics credits,requests
  spicrawl usage --group-by key --metrics requests,client_cli
  spicrawl usage summary
  spicrawl usage reconciliation --day 2026-09-21`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		q := url.Values{}
		for _, d := range []struct{ flag, val, param string }{{"from", usageFrom, "from"}, {"to", usageTo, "to"}} {
			if d.val == "" {
				continue
			}
			if !usageValidDay(d.val) {
				return Usagef("--%s must be a date like 2026-08-01, not %q", d.flag, d.val)
			}
			q.Set(d.param, d.val)
		}
		if usageFrom != "" && usageTo != "" && usageTo <= usageFrom {
			return Usagef("--to is exclusive and must be after --from")
		}
		if usageGroupBy != "" {
			g := strings.ToLower(usageGroupBy)
			switch g {
			case "day", "project", "engine", "feature", "key":
			default:
				return Usagef("--group-by must be day, project, engine, feature or key, not %q", usageGroupBy)
			}
			q.Set("group_by", g)
		}
		if usageProject != "" {
			q.Set("project_id", usageProject)
		}
		if len(usageMetrics) > 0 {
			for _, m := range usageMetrics {
				if !usageKnownMetric(m) {
					return Usagef("unknown metric %q; known: %s", m, strings.Join(usageMetricNames, ","))
				}
			}
			q.Set("metrics", strings.Join(usageMetrics, ","))
		}
		body, err := usageGet(cmd.Context(), "/v1/usage", q)
		if err != nil {
			return err
		}
		p := Printer()
		if p.JSON {
			return p.RawJSON(body)
		}
		if q.Get("group_by") == "key" {
			return printUsageByKey(p, body)
		}
		var b struct {
			Period       usagePeriod      `json:"period"`
			GroupBy      string           `json:"group_by"`
			Groups       []usageGroup     `json:"groups"`
			Totals       map[string]int64 `json:"totals"`
			Unattributed map[string]int64 `json:"unattributed"`
			StaleSeconds *int64           `json:"stale_seconds"`
			Warnings     []string         `json:"warnings"`
		}
		if err := json.Unmarshal(body, &b); err != nil {
			return fmt.Errorf("decode usage: %w", err)
		}
		for _, w := range b.Warnings {
			p.Warn("%s", w)
		}
		fmt.Fprintf(p.Out, "period %s (%d days), by %s\n\n", b.Period.String(), b.Period.Days, b.GroupBy)
		cols := usageColumns(b.Totals)
		headers := append([]string{strings.ToUpper(b.GroupBy)}, usageHeaders(cols)...)
		rows := make([][]string, 0, len(b.Groups)+2)
		for _, g := range b.Groups {
			key := g.Key
			if g.Label != "" {
				key = g.Label
			}
			rows = append(rows, append([]string{key}, usageCells(cols, g.Metrics)...))
		}
		if usageAnyNonZero(b.Unattributed) {
			rows = append(rows, append([]string{"(unattributed)"}, usageCells(cols, b.Unattributed)...))
		}
		rows = append(rows, append([]string{"TOTAL"}, usageCells(cols, b.Totals)...))
		p.Table(headers, rows)
		if b.StaleSeconds != nil && *b.StaleSeconds > 3600 {
			p.Warn("usage rollup is %s behind", time.Duration(*b.StaleSeconds)*time.Second)
		}
		return nil
	},
}

var usageSummaryCmd = &cobra.Command{
	Use:   "summary",
	Short: "Current-period usage against the plan allowance (GET /v1/usage/summary)",
	Long: `Usage for the billing period covering now, with the plan's credit allowance.
When no subscription covers now, the period is the calendar month and there
is no allowance; that is not a bill preview.`,
	Example: `  spicrawl usage summary
  spicrawl usage summary --json | jq .credits.remaining_micro`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		body, err := usageGet(cmd.Context(), "/v1/usage/summary", nil)
		if err != nil {
			return err
		}
		p := Printer()
		if p.JSON {
			return p.RawJSON(body)
		}
		var s struct {
			Period  usagePeriod `json:"period"`
			Elapsed int         `json:"period_elapsed_days"`
			PlanRaw struct {
				Code               string `json:"code"`
				Name               string `json:"name"`
				SubscriptionStatus string `json:"subscription_status"`
				PeriodSource       string `json:"period_source"`
			} `json:"plan"`
			Credits *struct {
				Included  int64  `json:"included_micro"`
				Used      int64  `json:"used_micro"`
				Remaining int64  `json:"remaining_micro"`
				Overage   int64  `json:"overage_micro"`
				UsedBP    *int64 `json:"used_basis_points"`
			} `json:"credits"`
			Metrics  map[string]int64 `json:"metrics"`
			Warnings []string         `json:"warnings"`
		}
		if err := json.Unmarshal(body, &s); err != nil {
			return fmt.Errorf("decode usage summary: %w", err)
		}
		for _, w := range s.Warnings {
			p.Warn("%s", w)
		}
		w := p.Out
		plan := s.PlanRaw.Name
		if plan == "" {
			plan = "(no subscription)"
		} else if s.PlanRaw.SubscriptionStatus != "" {
			plan += " (" + s.PlanRaw.SubscriptionStatus + ")"
		}
		fmt.Fprintf(w, "%-12s %s\n", "plan:", plan)
		fmt.Fprintf(w, "%-12s %s, day %d of %d (%s)\n", "period:", s.Period.String(), s.Elapsed, s.Period.Days, s.PlanRaw.PeriodSource)
		if c := s.Credits; c != nil {
			pct := ""
			if c.UsedBP != nil {
				pct = fmt.Sprintf(" (%.1f%%)", float64(*c.UsedBP)/100)
			}
			fmt.Fprintf(w, "%-12s %s of %s used%s, %s remaining", "credits:",
				logsCredits(c.Used), logsCredits(c.Included), pct, logsCredits(c.Remaining))
			if c.Overage > 0 {
				fmt.Fprintf(w, ", %s overage", logsCredits(c.Overage))
			}
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w)
		cols := usageColumns(s.Metrics)
		rows := make([][]string, 0, len(cols))
		cells := usageCells(cols, s.Metrics)
		for i, c := range cols {
			rows = append(rows, []string{c, cells[i]})
		}
		p.Table([]string{"METRIC", "VALUE"}, rows)
		return nil
	},
}

var usageReconciliationCmd = &cobra.Command{
	Use:   "reconciliation",
	Short: "Drift between billed usage and the request log for one day (GET /v1/usage/reconciliation)",
	Long: `Compare billed usage for one UTC day (default: yesterday) with usage
re-derived from the request log. Report only; nothing is repaired.

Read "interpretation" before "drift": when the request log holds no rows for
the day, every billed row shows as drift, which is not evidence of
overbilling.`,
	Example: `  spicrawl usage reconciliation
  spicrawl usage reconciliation --day 2026-09-21 --json | jq .drift`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		var q url.Values
		if usageDay != "" {
			if !usageValidDay(usageDay) {
				return Usagef("--day must be a date like 2026-09-21, not %q", usageDay)
			}
			q = url.Values{"day": {usageDay}}
		}
		body, err := usageGet(cmd.Context(), "/v1/usage/reconciliation", q)
		if err != nil {
			return err
		}
		p := Printer()
		if !p.JSON {
			var r struct {
				Interpretation string            `json:"interpretation"`
				Drift          []json.RawMessage `json:"drift"`
			}
			if json.Unmarshal(body, &r) == nil && r.Interpretation != "" {
				p.Info("%d drift item(s). %s", len(r.Drift), r.Interpretation)
			}
		}
		return p.RawJSON(body)
	},
}

type usagePeriod struct {
	Start string `json:"start"`
	End   string `json:"end"`
	Days  int    `json:"days"`
}

func (u usagePeriod) String() string { return "[" + u.Start + ", " + u.End + ")" }

type usageGroup struct {
	Key     string           `json:"key"`
	Label   string           `json:"label"`
	Metrics map[string]int64 `json:"metrics"`
}

// usageGet runs one GET and adds a scope hint on INSUFFICIENT_SCOPE.
func usageGet(ctx context.Context, path string, q url.Values) ([]byte, error) {
	c, err := Client()
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(ctx, api.Request{Path: path, Query: q})
	if err != nil {
		if prob, ok := api.AsProblem(err); ok && prob.Code == "ERR::AUTH::INSUFFICIENT_SCOPE" && !Printer().JSON {
			// Human mode only: in JSON mode stderr must stay one problem document.
			fmt.Fprintln(stderr, "hint: usage needs a key with the read scope; create one in the dashboard or pass --api-key")
		}
		return nil, err
	}
	return resp.Body, nil
}

func usageValidDay(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

func usageKnownMetric(m string) bool {
	for _, k := range usageMetricNames {
		if k == m {
			return true
		}
	}
	return false
}

// usageColumns orders metrics: known ones in spec order, then any new ones.
func usageColumns(m map[string]int64) []string {
	var cols []string
	seen := map[string]bool{}
	for _, k := range usageMetricNames {
		if _, ok := m[k]; ok {
			cols = append(cols, k)
			seen[k] = true
		}
	}
	var extra []string
	for k := range m {
		if !seen[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	return append(cols, extra...)
}

func usageHeaders(cols []string) []string {
	h := make([]string, len(cols))
	for i, c := range cols {
		h[i] = strings.ToUpper(c)
	}
	return h
}

func usageCells(cols []string, m map[string]int64) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		if c == "credits" {
			out[i] = logsCredits(m[c])
		} else {
			out[i] = strconv.FormatInt(m[c], 10)
		}
	}
	return out
}

func usageAnyNonZero(m map[string]int64) bool {
	for _, v := range m {
		if v != 0 {
			return true
		}
	}
	return false
}

func init() {
	f := usageCmd.Flags()
	f.StringVar(&usageFrom, "from", "", "first UTC day, inclusive (YYYY-MM-DD; default 29 days ago)")
	f.StringVar(&usageTo, "to", "", "end UTC day, EXCLUSIVE (YYYY-MM-DD; default tomorrow)")
	f.StringVar(&usageGroupBy, "group-by", "", "breakdown: day|project|engine|feature|key (default day)")
	f.StringVar(&usageProject, "project", "", "only this project id (project_id)")
	f.StringSliceVar(&usageMetrics, "metrics", nil, "only these metrics, comma-separated: "+strings.Join(usageMetricNames, ","))

	usageReconciliationCmd.Flags().StringVar(&usageDay, "day", "", "UTC day to reconcile (YYYY-MM-DD; default yesterday)")

	usageCmd.AddCommand(usageSummaryCmd, usageReconciliationCmd)
	rootCmd.AddCommand(usageCmd)
}

// printUsageByKey renders group_by=key: one row per API key, window totals.
func printUsageByKey(p *output.Printer, body []byte) error {
	var b struct {
		Period usagePeriod `json:"period"`
		Keys   []struct {
			KeyID  string           `json:"key_id"`
			Name   string           `json:"name"`
			Prefix string           `json:"prefix"`
			Totals map[string]int64 `json:"totals"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(body, &b); err != nil {
		return fmt.Errorf("decode usage: %w", err)
	}
	fmt.Fprintf(p.Out, "period %s (%d days), by key\n\n", b.Period.String(), b.Period.Days)
	all := map[string]int64{}
	for _, k := range b.Keys {
		for m, v := range k.Totals {
			all[m] += v
		}
	}
	cols := usageColumns(all)
	headers := append([]string{"KEY", "NAME", "PREFIX"}, usageHeaders(cols)...)
	rows := make([][]string, 0, len(b.Keys))
	for _, k := range b.Keys {
		rows = append(rows, append([]string{k.KeyID, k.Name, k.Prefix}, usageCells(cols, k.Totals)...))
	}
	p.Table(headers, rows)
	return nil
}
