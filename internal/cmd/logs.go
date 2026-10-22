package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/OfficialSpicrawl/cli/internal/api"
	"github.com/OfficialSpicrawl/cli/internal/output"
	"github.com/OfficialSpicrawl/cli/internal/ui"
)

// logsEntry is the subset of RequestLogEntry the human views read. JSON mode
// prints the API's records unchanged.
type logsEntry struct {
	ID           string `json:"id"`
	CreatedAt    string `json:"created_at"`
	ProjectID    string `json:"project_id"`
	Status       string `json:"status"`
	HTTPStatus   *int   `json:"http_status"`
	Engine       string `json:"engine"`
	CreditsMicro int64  `json:"credits_micro"`
	URL          string `json:"url"`
	ErrorCode    string `json:"error_code"`
	ErrorDetail  string `json:"error_detail"`
	Attempts     int    `json:"attempts"`
	BytesIn      int64  `json:"bytes_in"`
	BytesOut     int64  `json:"bytes_out"`
	ProxyBytes   int64  `json:"proxy_bytes"`
	Spans        struct {
		Admission *int64 `json:"admission_ms"`
		Dispatch  *int64 `json:"dispatch_ms"`
		Worker    *int64 `json:"worker_ms"`
		Queue     *int64 `json:"queue_ms"`
		Engine    *int64 `json:"engine_ms"`
		Upstream  *int64 `json:"upstream_ms"`
		Extract   *int64 `json:"extract_ms"`
		Total     *int64 `json:"total_ms"`
	} `json:"spans"`
	Proxy struct {
		Source           string   `json:"source"`
		Reason           string   `json:"reason"`
		Provider         string   `json:"provider"`
		RequestedTier    string   `json:"requested_tier"`
		RequestedCountry string   `json:"requested_country"`
		Country          string   `json:"country"`
		EndpointID       string   `json:"endpoint_id"`
		Endpoints        []string `json:"endpoints"`
		Sticky           bool     `json:"sticky"`
	} `json:"proxy"`
	Blocked *struct {
		Vendor string `json:"vendor"`
		Rule   string `json:"rule"`
		Signal string `json:"signal"`
	} `json:"blocked"`
}

type logsPage struct {
	Data []json.RawMessage `json:"data"`
	Page struct {
		HasMore        bool   `json:"has_more"`
		NextBefore     string `json:"next_before,omitempty"`
		NextBeforeID   string `json:"next_before_id,omitempty"`
		RetainedSince  string `json:"retained_since"`
		RetentionHours int    `json:"retention_hours"`
	} `json:"page"`
}

var (
	logsErrors      bool
	logsStatus      string
	logsLimit       int
	logsAll         bool
	logsJSONL       bool
	logsAllProjects bool
)

var logsStatuses = []string{"success", "failed", "timeout", "blocked", "rejected", "cancelled"}

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Recent requests in the key's project (GET /v1/requests)",
	Long: `List recent API requests, newest first. Records are kept for the
organization's retention window (24 hours by default); older ones are gone.

By default the list is the key's project. --all-projects lists every project
in the key's organization instead (all_projects=true) and adds a PROJECT
column; it needs a key with the read scope.

Status is one of: success, failed, timeout, blocked, rejected, cancelled.
--errors shows everything that is not success; it cannot be combined with --status.`,
	Example: `  spicrawl logs
  spicrawl logs --errors --limit 20
  spicrawl logs --all-projects --errors
  spicrawl logs --status blocked --all --jsonl | jq -r .url
  spicrawl logs get 01M0FK1EP2E7FBMWAR7W10SMXW`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		f := cmd.Flags()
		q := url.Values{}
		if f.Changed("limit") {
			if logsLimit < 1 || logsLimit > 200 {
				return Usagef("--limit must be between 1 and 200")
			}
			q.Set("limit", strconv.Itoa(logsLimit))
		}
		if logsStatus != "" {
			s := strings.ToLower(logsStatus)
			ok := false
			for _, v := range logsStatuses {
				ok = ok || v == s
			}
			if !ok {
				return Usagef("--status must be one of %s, not %q", strings.Join(logsStatuses, ", "), logsStatus)
			}
			if logsErrors {
				return Usagef("--errors and --status cannot be combined")
			}
			q.Set("status", s)
		}
		if logsErrors {
			q.Set("only_errors", "true") // the API accepts only the literal "true"
		}
		if logsAllProjects {
			// Kept on every page: a cursor pages the list it came from.
			q.Set("all_projects", "true")
		}

		c, err := Client()
		if err != nil {
			return err
		}
		p := Printer()
		sp := spin(p, "Fetching the request log")
		defer sp.Stop()
		var all []json.RawMessage
		var last logsPage
		read := 0
		for {
			resp, err := c.Do(cmd.Context(), api.Request{Path: "/v1/requests", Query: q})
			if err != nil {
				return err
			}
			var page logsPage
			if err := resp.Decode(&page); err != nil {
				return err
			}
			if logsJSONL {
				for _, r := range page.Data {
					var perr error
					sp.Around(func() { perr = p.Line(r) }) // stdout shares the terminal with the spinner
					if perr != nil {
						return perr
					}
				}
			} else {
				all = append(all, page.Data...)
			}
			read += len(page.Data)
			sp.Label("Fetching the request log · %d so far", read)
			last = page
			if !logsAll || !page.Page.HasMore || page.Page.NextBefore == "" || page.Page.NextBeforeID == "" {
				break
			}
			q.Set("before", page.Page.NextBefore)
			q.Set("before_id", page.Page.NextBeforeID)
		}
		sp.Stop()
		if logsJSONL {
			return nil
		}
		if all == nil {
			all = []json.RawMessage{}
		}
		out := map[string]any{"data": all, "page": last.Page}
		return p.Result(out, func(w io.Writer) {
			if len(all) == 0 {
				p.Info("no requests in the last %d hours", last.Page.RetentionHours)
				return
			}
			rows := make([][]string, 0, len(all))
			for _, raw := range all {
				var e logsEntry
				_ = json.Unmarshal(raw, &e)
				row := []string{e.CreatedAt}
				if logsAllProjects {
					row = append(row, logsDash(e.ProjectID))
				}
				rows = append(rows, append(row,
					e.Status, logsOptInt(e.HTTPStatus), logsDash(output.Tier(e.Engine)),
					logsCredits(e.CreditsMicro), logsSpan(e.Spans.Total), e.URL,
				))
			}
			header := []string{"TIME"}
			if logsAllProjects {
				header = append(header, "PROJECT")
			}
			u := outUI(p)
			statusCol := len(header) // TIME (and PROJECT) come first
			viewTable(p, u, append(header, "STATUS", "HTTP", "ENGINE", "CREDITS", "TOTAL_MS", "URL"), rows, func(col int, cell string) string {
				switch col {
				case 0:
					return u.Dim(cell)
				case statusCol:
					return logsStatusCell(u, cell)
				}
				return cell
			})
			if last.Page.HasMore && !logsAll {
				p.Info("more records exist; pass --all to fetch every page")
			}
		})
	},
}

var logsGetCmd = &cobra.Command{
	Use:   "get <request-id>",
	Short: "Show one request record (GET /v1/requests/{id})",
	Long: `Show one request: outcome, error, latency spans, proxy and bot-block
details. The id is the X-Request-Id header and a problem's request_id.

Spans are measured by different processes and do not sum to total_ms; a span
shown as — was not measured, which is not the same as 0.`,
	Example: `  spicrawl logs get 01M0FK1EP2E7FBMWAR7W10SMXW
  spicrawl logs get 01M0FK1EP2E7FBMWAR7W10SMXW --json | jq .spans`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := Client()
		if err != nil {
			return err
		}
		p := Printer()
		sp := spin(p, "Fetching request %s", args[0])
		resp, err := c.Do(cmd.Context(), api.Request{Path: "/v1/requests/" + url.PathEscape(args[0])})
		sp.Stop()
		if err != nil {
			return err
		}
		if p.JSON {
			return p.RawJSON(resp.Body)
		}
		var e logsEntry
		if err := resp.Decode(&e); err != nil {
			return err
		}
		logsWriteEntry(p.Out, outUI(p), &e)
		return nil
	},
}

func logsPrintEntry(w io.Writer, e *logsEntry) { logsWriteEntry(w, nil, e) }

// logsStatusCell colours a request status: success green, trouble red.
func logsStatusCell(u *ui.UI, s string) string {
	return viewStatus(u, s, []string{"success"}, []string{"failed", "timeout", "blocked", "rejected"})
}

// logsWriteEntry writes one record as key/value lines. With u on, a verdict
// line leads, keys are dimmed and the section titles are bold.
func logsWriteEntry(w io.Writer, u *ui.UI, e *logsEntry) {
	if u.On() {
		m := markBad
		switch e.Status {
		case "success":
			m = markOK
		case "cancelled":
			m = markNeutral
		}
		fmt.Fprintf(w, "%s %s  %s\n\n", viewMark(u, m), u.Bold(logsDash(e.Status)), e.URL)
	}
	kv := func(k, v string) {
		if v != "" {
			if k == "status" {
				v = logsStatusCell(u, v)
			}
			fmt.Fprintf(w, "%s %s\n", viewKey(u, 18, k+":"), v)
		}
	}
	kv("id", e.ID)
	kv("time", e.CreatedAt)
	kv("project", e.ProjectID)
	kv("status", e.Status)
	kv("url", e.URL)
	kv("http status", logsOptInt(e.HTTPStatus))
	kv("engine", logsDash(output.Tier(e.Engine)))
	kv("credits", logsCredits(e.CreditsMicro))
	kv("error", e.ErrorCode)
	kv("error detail", output.PublicText(e.ErrorDetail))
	if e.Attempts > 0 {
		kv("attempts", strconv.Itoa(e.Attempts))
	}
	if e.BytesIn > 0 || e.BytesOut > 0 || e.ProxyBytes > 0 {
		kv("bytes", fmt.Sprintf("in %d, out %d, via proxy %d", e.BytesIn, e.BytesOut, e.ProxyBytes))
	}

	fmt.Fprintln(w, "\n"+u.Bold("spans (ms):"))
	s := e.Spans
	for _, sp := range []struct {
		name string
		v    *int64
	}{
		{"admission", s.Admission}, {"dispatch", s.Dispatch}, {"worker", s.Worker}, {"queue", s.Queue},
		{"engine", s.Engine}, {"upstream", s.Upstream}, {"extract", s.Extract}, {"total", s.Total},
	} {
		fmt.Fprintf(w, "  %s %s\n", viewKey(u, 16, sp.name), logsSpan(sp.v))
	}

	pr := e.Proxy
	fmt.Fprintln(w, "\n"+u.Bold("proxy:"))
	pkv := func(k, v string) {
		if v != "" {
			fmt.Fprintf(w, "  %s %s\n", viewKey(u, 16, k+":"), v)
		}
	}
	pkv("source", logsDash(pr.Source))
	pkv("reason", pr.Reason)
	pkv("provider", pr.Provider)
	req := strings.TrimSpace(pr.RequestedTier + " " + pr.RequestedCountry)
	pkv("requested", req)
	pkv("served country", pr.Country)
	pkv("endpoint", pr.EndpointID)
	if len(pr.Endpoints) > 1 {
		pkv("endpoints", strings.Join(pr.Endpoints, " -> "))
	}
	if pr.Sticky {
		pkv("sticky", "yes")
	}

	if e.Blocked != nil {
		fmt.Fprintln(w, "\n"+u.Bold("blocked:"))
		pkv("vendor", logsDash(output.PublicText(e.Blocked.Vendor)))
		pkv("rule", output.PublicText(e.Blocked.Rule))
		pkv("signal", output.PublicText(e.Blocked.Signal))
	}
}

// logsSpan renders a nullable span: null means "not measured", shown as —.
func logsSpan(v *int64) string {
	if v == nil {
		return "—"
	}
	return strconv.FormatInt(*v, 10)
}

func logsOptInt(v *int) string {
	if v == nil {
		return "—"
	}
	return strconv.Itoa(*v)
}

func logsDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// logsCredits formats micro-credits as credits.
func logsCredits(micro int64) string {
	s := strconv.FormatFloat(float64(micro)/1e6, 'f', 6, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	return s
}

func init() {
	f := logsCmd.Flags()
	f.BoolVar(&logsErrors, "errors", false, "only requests that did not succeed (only_errors=true)")
	f.StringVar(&logsStatus, "status", "", "only this status: "+strings.Join(logsStatuses, "|"))
	f.IntVar(&logsLimit, "limit", 50, "page size, 1-200")
	f.BoolVar(&logsAll, "all", false, "follow the cursor until every retained record is read")
	f.BoolVar(&logsJSONL, "jsonl", false, "one compact JSON record per line, streamed page by page")
	f.BoolVar(&logsAllProjects, "all-projects", false,
		"every project in the key's organization, not just its own (all_projects=true; needs the read scope)")

	logsCmd.AddCommand(logsGetCmd)
	rootCmd.AddCommand(logsCmd)
}
