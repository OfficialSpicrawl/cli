package cmd

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/OfficialSpicrawl/cli/internal/exitcode"
	"github.com/OfficialSpicrawl/cli/internal/output"
	"github.com/OfficialSpicrawl/cli/internal/ui"
)

var monitorsCmd = &cobra.Command{
	Use:   "monitors",
	Short: "Watch pages (or a whole site) for changes, and get events when they change",
	Long: `A monitor checks a page on a schedule and records a change when its content differs from
the last check. A change is confirmed after --confirm-checks checks in a row, and then
sent to the monitor's webhook endpoint (see "spicrawl webhooks").

--selector watches the matching elements instead of the main content. --js renders
the page in a browser before reading it, which costs more credits. --site watches
every page under the URL's host, up to --max-pages; its changes list the pages that
changed, were added or were removed.

"check" runs one check now and shows its outcome. A monitor disables itself after
repeated failures or when it runs out of credits; "update --enable" turns it back on.`,
	Example: `  spicrawl monitors create https://example.com/pricing --selector '.plan-price' --every 1h --webhook <id>
  spicrawl monitors create https://example.com --site --max-pages 200 --include '/blog/*' --every 1d
  spicrawl monitors changes <id> --limit 5`,
}

var (
	monitorChangesLimit int
	monitorPagesLimit   int
	monitorPagesCursor  string
	monitorYes          bool
	monitorEnable       bool
	monitorDisable      bool
)

// monitorOpts are the flags create and update share. Each is read only when
// the command's own flag set says it changed, so an unset flag is never sent.
type monitorOpts struct {
	name          string
	selector      string
	every         string
	webhook       string
	confirmChecks int
	js            bool
	site          bool
	maxPages      int
	include       []string
	exclude       []string
	maxCredits    int
}

var monitorFlags monitorOpts

// monitorAddFlags registers the shared monitor flags. --site is create-only:
// a monitor's scope cannot change.
func monitorAddFlags(c *cobra.Command, o *monitorOpts, create bool) {
	f := c.Flags()
	f.StringVar(&o.name, "name", "", "a label for the monitor (default: the host of the URL)")
	f.StringVar(&o.selector, "selector", "", "CSS selector whose matching elements are watched (default: the main content)")
	f.StringVar(&o.every, "every", "", "how often to check: seconds, or 30m, 1h, 1d (default: the plan's minimum)")
	f.StringVar(&o.webhook, "webhook", "", "id of the webhook endpoint that receives its events")
	f.IntVar(&o.confirmChecks, "confirm-checks", 0, "checks in a row that must see a change before it counts, 1 to 10 (default 1)")
	f.BoolVar(&o.js, "js", false, "render the page in a browser before reading it (costs more credits)")
	if create {
		f.BoolVar(&o.site, "site", false, "watch every page under the URL's host, not just the one page")
	}
	f.IntVar(&o.maxPages, "max-pages", 0, "site only: the most pages a run considers")
	f.StringArrayVar(&o.include, "include", nil, "site only: a path pattern a page must match (repeatable)")
	f.StringArrayVar(&o.exclude, "exclude", nil, "site only: a path pattern that drops a page (repeatable)")
	f.IntVar(&o.maxCredits, "max-credits-per-run", 0, "site only: the most credits one run may spend")
}

// monitorBody builds the request body from the flags that were set. create
// also sets the URL and the site scope; an update clears a field when it is
// given an empty value (selector, webhook, max-credits-per-run) and create
// leaves it out.
func monitorBody(cmd *cobra.Command, o *monitorOpts, create bool) (map[string]any, error) {
	f := cmd.Flags()
	set := func(name string) bool { return f.Changed(name) }
	body := map[string]any{}

	if set("name") {
		body["name"] = o.name
	}
	if set("selector") {
		switch {
		case o.selector != "":
			body["selector"] = o.selector
		case !create:
			body["selector"] = nil
		}
	}
	if set("every") {
		sec, err := monitorEvery(o.every)
		if err != nil {
			return nil, err
		}
		body["interval_seconds"] = sec
	}
	if set("webhook") {
		switch {
		case o.webhook != "":
			body["webhook_endpoint_id"] = o.webhook
		case !create:
			body["webhook_endpoint_id"] = nil
		}
	}
	if set("confirm-checks") {
		if o.confirmChecks < 1 || o.confirmChecks > 10 {
			return nil, Usagef("--confirm-checks must be between 1 and 10")
		}
		body["confirm_checks"] = o.confirmChecks
	}
	if set("js") {
		body["js_render"] = o.js
	}

	siteFlag := set("max-pages") || set("include") || set("exclude") || set("max-credits-per-run")
	if create {
		if o.site {
			if !set("max-pages") {
				return nil, Usagef("a site monitor needs --max-pages: the most pages a run considers")
			}
			body["scope"] = "site"
		} else if siteFlag {
			return nil, Usagef("--max-pages, --include, --exclude and --max-credits-per-run apply to --site monitors only")
		}
	}
	if set("max-pages") {
		body["max_pages"] = o.maxPages
	}
	if set("include") {
		body["include_paths"] = monitorPatterns(o.include)
	}
	if set("exclude") {
		body["exclude_paths"] = monitorPatterns(o.exclude)
	}
	if set("max-credits-per-run") {
		switch {
		case o.maxCredits > 0:
			body["max_credits_per_run"] = o.maxCredits
		case !create:
			body["max_credits_per_run"] = nil // 0 removes the cap
		default:
			return nil, Usagef("--max-credits-per-run must be 1 or more")
		}
	}
	return body, nil
}

// monitorPatterns drops empty patterns, so --include "" clears the list.
func monitorPatterns(in []string) []string {
	out := []string{}
	for _, p := range in {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}

// monitorEvery reads --every: a number of seconds, or a duration such as 30m,
// 1h or 1d (a day is 24 hours).
func monitorEvery(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, nil
	}
	if strings.HasSuffix(s, "d") {
		if n, err := strconv.ParseInt(strings.TrimSuffix(s, "d"), 10, 64); err == nil {
			return n * 86400, nil
		}
	} else if d, err := time.ParseDuration(s); err == nil {
		return int64(d / time.Second), nil
	}
	return 0, Usagef("--every: use seconds, or a duration such as 30m, 1h or 1d, not %q", s)
}

// monitorEveryText shows an interval the way --every reads it.
func monitorEveryText(sec int64) string {
	switch {
	case sec%86400 == 0:
		return strconv.FormatInt(sec/86400, 10) + "d"
	case sec%3600 == 0:
		return strconv.FormatInt(sec/3600, 10) + "h"
	case sec%60 == 0:
		return strconv.FormatInt(sec/60, 10) + "m"
	}
	return strconv.FormatInt(sec, 10) + "s"
}

// monitorOut is the part of a monitor the human views read.
type monitorOut struct {
	ID                     string  `json:"id"`
	Name                   string  `json:"name"`
	URL                    string  `json:"url"`
	Scope                  string  `json:"scope"`
	Selector               *string `json:"selector"`
	JSRender               bool    `json:"js_render"`
	IntervalSeconds        int64   `json:"interval_seconds"`
	CurrentIntervalSeconds int64   `json:"current_interval_seconds"`
	ConfirmChecks          int     `json:"confirm_checks"`
	WebhookEndpointID      *string `json:"webhook_endpoint_id"`
	Enabled                bool    `json:"enabled"`
	DisabledReason         *string `json:"disabled_reason"`
	LastError              *string `json:"last_error"`
	LastCheckedAt          *string `json:"last_checked_at"`
	LastChangedAt          *string `json:"last_changed_at"`
	NextCheckAt            *string `json:"next_check_at"`
	MaxPages               *int    `json:"max_pages"`
	MaxCreditsPerRun       *int64  `json:"max_credits_per_run"`
	PagesTracked           int     `json:"pages_tracked"`
}

func monitorStatus(m monitorOut) string {
	if m.Enabled {
		return "enabled"
	}
	return "disabled"
}

// monitorRows is one monitor as key/value lines.
func monitorRows(m monitorOut) []ui.Row {
	selector := "main content"
	if m.Selector != nil {
		selector = *m.Selector
	}
	js := "no"
	if m.JSRender {
		js = "yes"
	}
	webhook := "none"
	if m.WebhookEndpointID != nil {
		webhook = *m.WebhookEndpointID
	}
	rows := []ui.Row{
		{Key: "id", Val: m.ID},
		{Key: "name", Val: m.Name},
		{Key: "url", Val: m.URL},
		{Key: "scope", Val: m.Scope},
		{Key: "selector", Val: selector},
		{Key: "renders JavaScript", Val: js},
		{Key: "every", Val: monitorEveryText(m.IntervalSeconds)},
		{Key: "running every", Val: monitorEveryText(m.CurrentIntervalSeconds)},
		{Key: "confirm checks", Val: strconv.Itoa(m.ConfirmChecks)},
		{Key: "webhook", Val: webhook},
		{Key: "status", Val: monitorStatus(m)},
		{Key: "disabled because", Val: derefText(m.DisabledReason)},
		{Key: "last error", Val: derefText(m.LastError)},
		{Key: "last checked", Val: derefText(m.LastCheckedAt)},
		{Key: "last changed", Val: derefText(m.LastChangedAt)},
		{Key: "next check", Val: derefText(m.NextCheckAt)},
	}
	if m.Scope == "site" {
		maxPages := ""
		if m.MaxPages != nil {
			maxPages = strconv.Itoa(*m.MaxPages)
		}
		credits := "no cap"
		if m.MaxCreditsPerRun != nil {
			credits = strconv.FormatInt(*m.MaxCreditsPerRun, 10)
		}
		rows = append(rows,
			ui.Row{Key: "max pages", Val: maxPages},
			ui.Row{Key: "credits per run", Val: credits},
			ui.Row{Key: "pages tracked", Val: strconv.Itoa(m.PagesTracked)},
		)
	}
	return rows
}

// monitorShow prints one monitor: the body unchanged in JSON, otherwise a
// heading and its rows.
func monitorShow(p *output.Printer, body []byte, heading string) error {
	if p.JSON {
		return p.RawJSON(body)
	}
	var m monitorOut
	if err := partnerDecode(body, &m); err != nil {
		return err
	}
	u := outUI(p)
	fmt.Fprintf(p.Out, "%s%s\n", viewLead(u, markOK), heading)
	fmt.Fprint(p.Out, viewRows(u, "", monitorRows(m)))
	return nil
}

var monitorsCreateCmd = &cobra.Command{
	Use:   "create <url>",
	Short: "Start watching a page, or a site with --site (POST /v1/monitors)",
	Long: `Create a monitor. The first check records a baseline and sends no event; later changes
are sent to --webhook, once --confirm-checks checks in a row have seen them.

A site monitor (--site) needs --max-pages. --include and --exclude keep or drop pages
by path pattern; --max-credits-per-run caps what one run may spend.`,
	Example: `  spicrawl monitors create https://example.com/status --every 30m --webhook 0190f3a2-7c1e-7d4b-9a2f-3b8e5c6d7e80
  spicrawl monitors create https://example.com --site --max-pages 100 --exclude '/tag/*' --max-credits-per-run 500 --every 1d`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		body, err := monitorBody(cmd, &monitorFlags, true)
		if err != nil {
			return err
		}
		body["url"] = args[0]
		resp, err := apiCall(cmd, http.MethodPost, "/v1/monitors", nil, body)
		if err != nil {
			return err
		}
		return monitorShow(Printer(), resp.Body, "Created monitor")
	},
}

var monitorsListCmd = &cobra.Command{
	Use:     "list",
	Short:   "Your monitors (GET /v1/monitors)",
	Example: `  spicrawl monitors list --json | jq -r '.data[] | select(.enabled | not) | .id'`,
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		resp, err := apiCall(cmd, http.MethodGet, "/v1/monitors", nil, nil)
		if err != nil {
			return err
		}
		p := Printer()
		if p.JSON {
			return p.RawJSON(resp.Body)
		}
		var list struct {
			Data []monitorOut `json:"data"`
		}
		if err := partnerDecode(resp.Body, &list); err != nil {
			return err
		}
		if len(list.Data) == 0 {
			p.Info("no monitors: create one with spicrawl monitors create")
			return nil
		}
		rows := make([][]string, 0, len(list.Data))
		for _, m := range list.Data {
			rows = append(rows, []string{
				m.ID, m.Name, m.Scope, m.URL, monitorEveryText(m.IntervalSeconds), monitorStatus(m), partnerDash(derefText(m.LastChangedAt)),
			})
		}
		u := outUI(p)
		viewTable(p, u, []string{"ID", "NAME", "SCOPE", "URL", "EVERY", "STATUS", "LAST CHANGED"}, rows, func(col int, cell string) string {
			if col == 5 {
				return viewStatus(u, cell, []string{"enabled"}, []string{"disabled"})
			}
			return cell
		})
		return nil
	},
}

var monitorsGetCmd = &cobra.Command{
	Use:     "get <id>",
	Short:   "One monitor (GET /v1/monitors/{monitorID})",
	Example: `  spicrawl monitors get 0190f5c4-2b7a-7e10-8d3f-6a1c9e4b2d70`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := apiCall(cmd, http.MethodGet, "/v1/monitors/"+url.PathEscape(args[0]), nil, nil)
		if err != nil {
			return err
		}
		return monitorShow(Printer(), resp.Body, "Monitor "+args[0])
	},
}

var monitorsUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Change a monitor's schedule, selector, webhook, limits or state (PATCH /v1/monitors/{monitorID})",
	Long: `Change the fields you pass; the rest stay as they are. A monitor's scope and URL cannot be
changed: create another one. Empty values clear a field: --selector "" watches the main
content again, --webhook "" detaches the endpoint, --max-credits-per-run 0 removes the cap.
--include and --exclude replace the whole list. --enable turns a disabled monitor back
on, clears its last error and checks it next.`,
	Example: `  spicrawl monitors update 0190f5c4-2b7a-7e10-8d3f-6a1c9e4b2d70 --every 2h --confirm-checks 2
  spicrawl monitors update 0190f5c4-2b7a-7e10-8d3f-6a1c9e4b2d70 --enable`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		body, err := monitorBody(cmd, &monitorFlags, false)
		if err != nil {
			return err
		}
		f := cmd.Flags()
		if f.Changed("enable") && f.Changed("disable") {
			return Usagef("--enable and --disable are opposites: pass one")
		}
		if f.Changed("enable") {
			body["enabled"] = true
		}
		if f.Changed("disable") {
			body["enabled"] = false
		}
		if len(body) == 0 {
			return Usagef("nothing to change: pass a flag such as --every, --selector, --webhook or --enable")
		}
		resp, err := apiCall(cmd, http.MethodPatch, "/v1/monitors/"+url.PathEscape(args[0]), nil, body)
		if err != nil {
			return err
		}
		return monitorShow(Printer(), resp.Body, "Updated monitor "+args[0])
	},
}

var monitorsDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Stop and delete a monitor and its change history (DELETE /v1/monitors/{monitorID})",
	Long: `Stop the checks and delete the monitor with its change history. Asks for confirmation on a
terminal; without one you must pass --yes.`,
	Example: `  spicrawl monitors delete 0190f5c4-2b7a-7e10-8d3f-6a1c9e4b2d70 --yes`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !monitorYes && !output.IsStdinTerminal() {
			return Usagef("refusing to delete monitor %s without confirmation: pass --yes", args[0])
		}
		if !monitorYes && !partnerConfirm(fmt.Sprintf("Delete monitor %s? Its change history is deleted with it.", args[0])) {
			return Usagef("aborted")
		}
		if _, err := apiCall(cmd, http.MethodDelete, "/v1/monitors/"+url.PathEscape(args[0]), nil, nil); err != nil {
			return err
		}
		p := Printer()
		return p.Result(map[string]any{"id": args[0], "deleted": true}, func(w io.Writer) {
			fmt.Fprintf(w, "%sDeleted monitor %s\n", viewLead(outUI(p), markOK), args[0])
		})
	},
}

// monitorRunOut is the counts of one site run.
type monitorRunOut struct {
	Checked   int  `json:"checked"`
	Skipped   int  `json:"skipped"`
	Changed   int  `json:"changed"`
	Added     int  `json:"added"`
	Removed   int  `json:"removed"`
	Credits   int  `json:"credits"`
	Truncated bool `json:"truncated"`
}

var monitorsCheckCmd = &cobra.Command{
	Use:   "check <id>",
	Short: "Check a monitor now and show what it found (POST /v1/monitors/{monitorID}/check)",
	Long: `Run one check now. It always fetches the page fresh and returns when the check is done.
A change found here is recorded and sent like one from the schedule. An outcome of
"failed" means the page could not be read; it counts toward the monitor's auto-disable,
and the command exits 6 (upstream) after printing the outcome.`,
	Example: `  spicrawl monitors check 0190f5c4-2b7a-7e10-8d3f-6a1c9e4b2d70`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := apiCall(cmd, http.MethodPost, "/v1/monitors/"+url.PathEscape(args[0])+"/check", nil, nil)
		if err != nil {
			return err
		}
		p := Printer()
		var res struct {
			Outcome string         `json:"outcome"`
			EventID *string        `json:"event_id"`
			Credits int            `json:"credits"`
			Run     *monitorRunOut `json:"run"`
		}
		if err := partnerDecode(resp.Body, &res); err != nil {
			return err
		}
		// A check that could not read the page is a failed operation: the
		// answer is still printed, then the exit code says so (like scrape).
		failed := error(nil)
		if res.Outcome == "failed" {
			failed = &ExitError{Code: exitcode.Upstream, Err: fmt.Errorf("monitor %s: the check failed: the page could not be read", args[0])}
		}
		if p.JSON {
			if err := p.RawJSON(resp.Body); err != nil {
				return err
			}
			return failed
		}
		rows := []ui.Row{
			{Key: "outcome", Val: res.Outcome},
			{Key: "change event", Val: derefText(res.EventID)},
			{Key: "credits", Val: strconv.Itoa(res.Credits)},
		}
		if r := res.Run; r != nil {
			pages := fmt.Sprintf("%d checked, %d skipped, %d changed, %d added, %d removed", r.Checked, r.Skipped, r.Changed, r.Added, r.Removed)
			if r.Truncated {
				pages += " (stopped at the credit cap)"
			}
			rows = append(rows, ui.Row{Key: "pages", Val: pages})
		}
		u := outUI(p)
		fmt.Fprintf(p.Out, "%sChecked monitor %s\n", viewLead(u, markOK), args[0])
		fmt.Fprint(p.Out, viewRows(u, "", rows))
		return failed
	},
}

// monitorChangeOut is one confirmed change. Diff is null for a site change,
// whose Summary holds the run's digest instead.
type monitorChangeOut struct {
	ID         string  `json:"id"`
	DetectedAt string  `json:"detected_at"`
	Diff       *string `json:"diff"`
	Summary    *struct {
		Counts monitorRunOut `json:"counts"`
	} `json:"summary"`
	NotifiedAt *string `json:"notified_at"`
}

// monitorChangeText is what changed: the page's diff size, or a site run's counts.
func monitorChangeText(c monitorChangeOut) string {
	if c.Summary != nil {
		n := c.Summary.Counts
		return fmt.Sprintf("%d changed, %d added, %d removed pages", n.Changed, n.Added, n.Removed)
	}
	if c.Diff == nil {
		return ""
	}
	added, removed := 0, 0
	for _, l := range strings.Split(*c.Diff, "\n") {
		switch {
		case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"):
		case strings.HasPrefix(l, "+"):
			added++
		case strings.HasPrefix(l, "-"):
			removed++
		}
	}
	return fmt.Sprintf("+%d -%d lines", added, removed)
}

var monitorsChangesCmd = &cobra.Command{
	Use:   "changes <id>",
	Short: "A monitor's confirmed changes, newest first (GET /v1/monitors/{monitorID}/changes)",
	Long: `The confirmed changes of one monitor. The baseline is not a change. The JSON carries each
change's diff (or, for a site monitor, its summary); the table shows the size of each.`,
	Example: `  spicrawl monitors changes 0190f5c4-2b7a-7e10-8d3f-6a1c9e4b2d70 --limit 20 --json`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if monitorChangesLimit < 1 || monitorChangesLimit > 100 {
			return Usagef("--limit must be between 1 and 100")
		}
		q := url.Values{"limit": {strconv.Itoa(monitorChangesLimit)}}
		resp, err := apiCall(cmd, http.MethodGet, "/v1/monitors/"+url.PathEscape(args[0])+"/changes", q, nil)
		if err != nil {
			return err
		}
		p := Printer()
		if p.JSON {
			return p.RawJSON(resp.Body)
		}
		var list struct {
			Data []monitorChangeOut `json:"data"`
		}
		if err := partnerDecode(resp.Body, &list); err != nil {
			return err
		}
		if len(list.Data) == 0 {
			p.Info("no changes yet")
			return nil
		}
		rows := make([][]string, 0, len(list.Data))
		for _, c := range list.Data {
			notified := "no"
			if c.NotifiedAt != nil {
				notified = "yes"
			}
			rows = append(rows, []string{c.DetectedAt, c.ID, monitorChangeText(c), notified})
		}
		viewTable(p, outUI(p), []string{"DETECTED", "ID", "CHANGE", "NOTIFIED"}, rows, nil)
		return nil
	},
}

var monitorsPagesCmd = &cobra.Command{
	Use:   "pages <id>",
	Short: "The pages a site monitor tracks (GET /v1/monitors/{monitorID}/pages)",
	Long: `The pages a site monitor tracks, by URL. A page it stopped finding stays listed with a
REMOVED date. A page monitor has no pages (the API answers 404). Pass the next cursor
printed on stderr as --cursor to read on.`,
	Example: `  spicrawl monitors pages 0190f5c4-2b7a-7e10-8d3f-6a1c9e4b2d70 --limit 100`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if monitorPagesLimit < 1 || monitorPagesLimit > 500 {
			return Usagef("--limit must be between 1 and 500")
		}
		q := url.Values{"limit": {strconv.Itoa(monitorPagesLimit)}}
		if monitorPagesCursor != "" {
			q.Set("cursor", monitorPagesCursor)
		}
		resp, err := apiCall(cmd, http.MethodGet, "/v1/monitors/"+url.PathEscape(args[0])+"/pages", q, nil)
		if err != nil {
			return err
		}
		p := Printer()
		if p.JSON {
			return p.RawJSON(resp.Body)
		}
		var list struct {
			Data []struct {
				URL           string  `json:"url"`
				FirstSeenAt   string  `json:"first_seen_at"`
				LastSeenAt    string  `json:"last_seen_at"`
				LastChangedAt *string `json:"last_changed_at"`
				RemovedAt     *string `json:"removed_at"`
			} `json:"data"`
			NextCursor *string `json:"next_cursor"`
		}
		if err := partnerDecode(resp.Body, &list); err != nil {
			return err
		}
		if len(list.Data) == 0 {
			p.Info("no pages tracked yet")
			return nil
		}
		rows := make([][]string, 0, len(list.Data))
		for _, g := range list.Data {
			state := "tracked"
			if g.RemovedAt != nil {
				state = "removed " + *g.RemovedAt
			}
			rows = append(rows, []string{g.URL, g.FirstSeenAt, g.LastSeenAt, partnerDash(derefText(g.LastChangedAt)), state})
		}
		viewTable(p, outUI(p), []string{"URL", "FIRST SEEN", "LAST SEEN", "LAST CHANGED", "STATE"}, rows, nil)
		if list.NextCursor != nil && *list.NextCursor != "" {
			p.Info("more pages: --cursor %s", *list.NextCursor)
		}
		return nil
	},
}

func init() {
	monitorAddFlags(monitorsCreateCmd, &monitorFlags, true)
	monitorAddFlags(monitorsUpdateCmd, &monitorFlags, false)
	uf := monitorsUpdateCmd.Flags()
	uf.BoolVar(&monitorEnable, "enable", false, "turn a disabled monitor back on")
	uf.BoolVar(&monitorDisable, "disable", false, "stop checking the monitor")
	monitorsDeleteCmd.Flags().BoolVarP(&monitorYes, "yes", "y", false, "do not ask for confirmation")
	monitorsChangesCmd.Flags().IntVar(&monitorChangesLimit, "limit", 20, "changes to show, 1 to 100")
	pf := monitorsPagesCmd.Flags()
	pf.IntVar(&monitorPagesLimit, "limit", 100, "pages to show, 1 to 500")
	pf.StringVar(&monitorPagesCursor, "cursor", "", "the cursor printed for the previous page")

	monitorsCmd.AddCommand(monitorsCreateCmd, monitorsListCmd, monitorsGetCmd, monitorsUpdateCmd,
		monitorsDeleteCmd, monitorsCheckCmd, monitorsChangesCmd, monitorsPagesCmd)
	rootCmd.AddCommand(monitorsCmd)
}
