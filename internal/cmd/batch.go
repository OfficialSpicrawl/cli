package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/OfficialSpicrawl/cli/internal/api"
	"github.com/OfficialSpicrawl/cli/internal/output"
)

// batchSubmitScrapeFlags are the scrape flags "batch submit" registers: only
// those whose fields a batch job accepts, each mapped to its field exactly as
// on "spicrawl scrape" (scrapeFlags.build). A non-empty value replaces the flag's
// help text where batch narrows the values it takes.
//
// The API refuses every other ScrapeRequest field at submission with 400 and
// charges nothing, because the batch worker cannot run it: actions,
// screenshot*, network_capture, extract, extract_preset, ai_extract,
// autoparse, links, session_id, cache/cache_ttl, original_status,
// allowed_status_codes, parse_pdf=true, mode=auto and response_format=pdf.
// Offering flags for them would only produce that 400. Everything accepted is
// applied by the worker, except max_cost, which is enforced when the job is
// priced.
//
// "wait" (render delay, ms) is accepted but not copied: on batch submit
// --wait means "wait for the job", so the delay is --render-wait.
var batchSubmitScrapeFlags = map[string]string{
	"body":             "",
	"format":           "response_format: html|markdown|text|json (pdf is refused on batch)",
	"method":           "HTTP method sent to the target (default GET; non-GET only on non-rendered items)",
	"render":           "",
	"stealth":          "",
	"impersonate":      "",
	"engine":           "pin the engine: fetch|obscura|camoufox (chromium is refused on batch)",
	"premium-proxy":    "",
	"country":          "",
	"proxy":            "",
	"proxy-verify":     "",
	"sticky-key":       "",
	"wait-for":         "CSS selector to wait for (needs --render or --stealth)",
	"wait-for-timeout": "",
	"block":            "",
	"headless":         "rendered items: --headless=false runs with a real display",
	"header":           "",
	"main-content":     "",
	"no-main-content":  "",
	"include":          "",
	"exclude":          "",
	"max-cost":         "refuse the job if any one item could cost more credits than this",
}

type batchProgressCounts struct {
	Total           int64   `json:"total"`
	Completed       int64   `json:"completed"`
	Succeeded       int64   `json:"succeeded"`
	Failed          int64   `json:"failed"`
	Cancelled       int64   `json:"cancelled"`
	Skipped         int64   `json:"skipped"`
	Remaining       int64   `json:"remaining"`
	PercentComplete float64 `json:"percent_complete"`
	CreditsCharged  int64   `json:"credits_charged"`
	Bytes           int64   `json:"bytes"`
}

// batchJob is the subset of BatchJob the human renderer needs. JSON output
// always prints the API's body unchanged.
type batchJob struct {
	ID               string              `json:"id"`
	Name             string              `json:"name"`
	Status           string              `json:"status"`
	Open             bool                `json:"open"`
	TotalItems       int64               `json:"total_items"`
	Concurrency      int                 `json:"concurrency"`
	Priority         int                 `json:"priority"`
	MaxAttempts      int                 `json:"max_attempts"`
	EstimatedCredits int64               `json:"estimated_credits"`
	Progress         batchProgressCounts `json:"progress"`
	SubmittedAt      string              `json:"submitted_at"`
	StartedAt        string              `json:"started_at"`
	FinishedAt       string              `json:"finished_at"`
	ResultsExpireAt  string              `json:"results_expire_at"`
	ErrorCode        string              `json:"error_code"`
	ErrorMessage     string              `json:"error_message"`
	Warnings         []json.RawMessage   `json:"warnings"`
}

var batchCmd = &cobra.Command{
	Use:   "batch",
	Short: "Run many URLs as one asynchronous job (POST /v1/batch)",
	Long: `Submit a list of URLs as a batch job, follow its progress and download the
results as JSON Lines. Jobs run server-side; results stay readable until the
job's results_expire_at (72 h after submission by default).`,
}

// ---- submit ----------------------------------------------------------------

var (
	batchSubmitScrape       *scrapeFlags
	batchSubmitRenderWait   int
	batchSubmitName         string
	batchSubmitConcurrency  int
	batchSubmitPriority     int
	batchSubmitMaxAttempts  int
	batchSubmitFailThresh   int64
	batchSubmitCreditBudget int64
	batchSubmitOpen         bool
	batchSubmitWait         bool
	batchSubmitPoll         time.Duration
	batchSubmitTimeout      time.Duration
)

var batchSubmitCmd = &cobra.Command{
	Use:   "submit <file|->",
	Short: "Submit a batch job from a URL list or a .json/.jsonl item file",
	Long: `Submit a batch job. The file is one URL per line, a .json array of items, or
a .jsonl file with one item per line. An item is {"url": ..., "external_id": ...}
plus any per-item scrape overrides. "-" reads stdin (a leading '[' or '{' is
read as JSON items, anything else as URLs).

Scrape flags set job-wide defaults; items override them. Prints the job; with
--wait, polls until the job finishes and prints the final job (exit 11 if
--max-wait elapses first).

Each item is one fetch or render, returned as html (default), markdown, text
or json (--format). Only the scrape flags batch accepts are offered here; the
API refuses screenshots, extraction (extract, ai_extract, autoparse, links),
actions, sessions, network capture, cache controls, mode=auto and pdf output
on batch with a 400 before anything is charged. Use "spicrawl scrape" for those.
--render-wait is the render delay in ms (the scrape "wait" field); --wait waits
for the job.`,
	Example: `  # One URL per line, rendered, wait for completion, then fetch results
  spicrawl batch submit urls.txt --render --name nightly --wait --max-wait 20m
  spicrawl batch results 01J9Z6V0Q8M4K2T7R3N5B1C9XA --all -o results.jsonl

  # Items with correlation ids and per-item overrides
  printf '%s\n' '{"url":"https://example.com/a","external_id":"sku-1"}' \
    '{"url":"https://example.com/b","external_id":"sku-2","js_render":false}' \
    | spicrawl batch submit - --render --premium-proxy --country us

  # Markdown of the main content, rendered, 1.5 s after load
  spicrawl batch submit urls.txt --render --render-wait 1500 --format markdown --main-content

  # Capture the job id for later polling
  id=$(spicrawl batch submit urls.txt --json | jq -r .id)`,
	Args: cobra.ExactArgs(1),
	RunE: batchRunSubmit,
}

func batchRunSubmit(cmd *cobra.Command, args []string) error {
	body, err := batchSubmitScrape.build()
	if err != nil {
		return err
	}
	if cmd.Flags().Changed("render-wait") {
		body["wait"] = batchSubmitRenderWait
	}
	if _, ok := body["url"]; ok {
		return Usagef("a top-level \"url\" is not allowed in a batch: list targets in the file")
	}
	if batchSubmitWait && batchSubmitOpen {
		return Usagef("--wait with --open would never finish: an open job completes only after \"spicrawl batch close\"")
	}
	urls, items, err := batchReadTargets(args[0])
	if err != nil {
		return err
	}
	if len(urls) > 0 {
		body["urls"] = urls
	} else {
		body["items"] = items
	}
	f := cmd.Flags()
	if f.Changed("name") {
		body["name"] = batchSubmitName
	}
	if f.Changed("concurrency") {
		body["concurrency"] = batchSubmitConcurrency
	}
	if f.Changed("priority") {
		body["priority"] = batchSubmitPriority
	}
	if f.Changed("max-attempts") {
		body["max_attempts"] = batchSubmitMaxAttempts
	}
	if f.Changed("failure-threshold") {
		body["failure_threshold"] = batchSubmitFailThresh
	}
	if f.Changed("credit-budget") {
		body["credit_budget"] = batchSubmitCreditBudget
	}
	if f.Changed("open") {
		body["open"] = batchSubmitOpen
	}

	p := Printer()
	c, err := Client()
	if err != nil {
		return err
	}
	resp, err := c.Do(cmd.Context(), api.Request{Method: "POST", Path: "/v1/batch", Body: body})
	if err != nil {
		return err
	}
	var job batchJob
	if err := resp.Decode(&job); err != nil {
		return err
	}
	batchPrintWarnings(p, job)
	if !batchSubmitWait {
		if err := batchPrintJob(p, resp.Body, job); err != nil {
			return err
		}
		if !p.JSON {
			p.Info("\nnext: spicrawl batch wait %s", job.ID)
		}
		return nil
	}
	p.Info("submitted batch %s (%d items)", job.ID, job.TotalItems)
	return batchWaitAndPrint(cmd, c, p, job.ID, batchSubmitPoll, batchSubmitTimeout)
}

// ---- wait / get / list -----------------------------------------------------

var (
	batchWaitPoll    time.Duration
	batchWaitTimeout time.Duration
)

var batchWaitCmd = &cobra.Command{
	Use:   "wait <id>",
	Short: "Poll a job until it finishes, then print it (exit 11 on --max-wait)",
	Long: `Poll GET /v1/batch/{id} until the job is completed, failed or cancelled, then
print the job. If --max-wait elapses first, prints the last job seen and exits
11, so a script can resume with the same command. --max-wait 0 waits forever.
Rate limits (429) are honoured: the next poll waits for Retry-After.`,
	Example: `  spicrawl batch wait 01J9Z6V0Q8M4K2T7R3N5B1C9XA --max-wait 10m
  spicrawl batch wait "$id" --poll 10s --json | jq '.progress'

  # Branch on the outcome in a script
  spicrawl batch wait "$id" --max-wait 5m; [ $? -eq 11 ] && echo "still running"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := Client()
		if err != nil {
			return err
		}
		return batchWaitAndPrint(cmd, c, Printer(), args[0], batchWaitPoll, batchWaitTimeout)
	},
}

func batchWaitAndPrint(cmd *cobra.Command, c *api.Client, p *output.Printer, id string, poll, timeout time.Duration) error {
	raw, werr := batchWaitJob(cmd.Context(), c, p, id, poll, timeout)
	if raw != nil {
		var job batchJob
		if err := json.Unmarshal(raw, &job); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
		if err := batchPrintJob(p, raw, job); err != nil {
			return err
		}
		if werr == nil && !p.JSON && job.Status != "" {
			p.Info("\nnext: spicrawl batch results %s --all -o %s.jsonl", job.ID, job.ID)
		}
	}
	return werr
}

var batchGetCmd = &cobra.Command{
	Use:     "get <id>",
	Short:   "Show a batch job and its progress",
	Example: `  spicrawl batch get 01J9Z6V0Q8M4K2T7R3N5B1C9XA --json | jq '{status, progress}'`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return batchJobCall(cmd, "GET", "/v1/batch/"+url.PathEscape(args[0]))
	},
}

var (
	batchListStatus string
	batchListLimit  int
	batchListCursor string
	batchListAll    bool
)

var batchListCmd = &cobra.Command{
	Use:   "list",
	Short: "List batch jobs, newest first",
	Example: `  spicrawl batch list --status running
  spicrawl batch list --all --json | jq -r '.batches[] | select(.status=="failed") | .id'`,
	Args: cobra.NoArgs,
	RunE: batchRunList,
}

func batchRunList(cmd *cobra.Command, _ []string) error {
	c, err := Client()
	if err != nil {
		return err
	}
	p := Printer()
	q := url.Values{}
	if batchListStatus != "" {
		q.Set("status", batchListStatus)
	}
	if cmd.Flags().Changed("limit") {
		q.Set("limit", strconv.Itoa(batchListLimit))
	}
	cursor := batchListCursor
	var all []json.RawMessage
	var page struct {
		Batches    []json.RawMessage `json:"batches"`
		NextCursor string            `json:"next_cursor"`
	}
	for {
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		resp, err := c.DoWithRetry(cmd.Context(), api.Request{Path: "/v1/batch", Query: q}, 3)
		if err != nil {
			return err
		}
		page.Batches, page.NextCursor = nil, ""
		if err := resp.Decode(&page); err != nil {
			return err
		}
		all = append(all, page.Batches...)
		if !batchListAll || page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if all == nil {
		all = []json.RawMessage{}
	}
	out := map[string]any{"batches": all}
	if page.NextCursor != "" {
		out["next_cursor"] = page.NextCursor
	}
	return p.Result(out, func(w io.Writer) {
		rows := make([][]string, 0, len(all))
		for _, r := range all {
			var j batchJob
			_ = json.Unmarshal(r, &j)
			rows = append(rows, []string{j.ID, j.Status, batchDash(j.Name),
				fmt.Sprintf("%d/%d", j.Progress.Completed, j.Progress.Total), j.SubmittedAt})
		}
		if len(rows) == 0 {
			fmt.Fprintln(w, "no batch jobs")
		} else {
			p.Table([]string{"ID", "STATUS", "NAME", "DONE", "SUBMITTED"}, rows)
		}
		if page.NextCursor != "" {
			p.Info("more jobs: --cursor %s, or --all", page.NextCursor)
		}
	})
}

// ---- cancel / retry / close / append ----------------------------------------

var (
	batchCancelYes bool
	// Seams for tests: whether stdin is a terminal, and where answers come from.
	batchStdinIsTerminal           = output.IsStdinTerminal
	batchStdin           io.Reader = os.Stdin
)

var batchCancelCmd = &cobra.Command{
	Use:   "cancel <id>",
	Short: "Cancel a job; in-flight items drain, the rest are cancelled",
	Long: `Cancel a job. Items already running finish; every queued item is cancelled
and will not run. This cannot be undone ("batch retry" re-runs failed items,
not cancelled ones). Asks for confirmation on a terminal; without a terminal
you must pass --yes.`,
	Example: `  spicrawl batch cancel 01J9Z6V0Q8M4K2T7R3N5B1C9XA
  spicrawl batch cancel "$id" --yes && spicrawl batch wait "$id" --max-wait 2m`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		if !batchCancelYes {
			if !batchStdinIsTerminal() {
				return Usagef("refusing to cancel batch %s without confirmation: cancelling cannot be undone; pass --yes", id)
			}
			fmt.Fprintf(stderr, "Cancel batch %s? Queued items will not run; this cannot be undone. [y/N] ", id)
			line, _ := bufio.NewReader(batchStdin).ReadString('\n')
			if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
				return Usagef("aborted: batch %s was not cancelled", id)
			}
		}
		return batchJobCall(cmd, "POST", "/v1/batch/"+url.PathEscape(id)+"/cancel")
	},
}

var batchCloseCmd = &cobra.Command{
	Use:     "close <id>",
	Short:   "Stop an --open job accepting items so it can complete",
	Example: `  spicrawl batch close "$id" && spicrawl batch wait "$id"`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return batchJobCall(cmd, "POST", "/v1/batch/"+url.PathEscape(args[0])+"/close")
	},
}

var batchRetryCmd = &cobra.Command{
	Use:   "retry <id>",
	Short: "Re-run the job's failed items",
	Long: `Reset every failed item to queued and dispatch it again. Items not dispatched
immediately are picked up by the worker's recovery sweep: do not call retry
again for them.`,
	Example: `  spicrawl batch retry "$id" && spicrawl batch wait "$id" --max-wait 15m
  spicrawl batch results "$id" --status failed --all   # what failed and why`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := Client()
		if err != nil {
			return err
		}
		resp, err := c.Do(cmd.Context(), api.Request{Method: "POST", Path: "/v1/batch/" + url.PathEscape(args[0]) + "/retry"})
		if err != nil {
			return err
		}
		p := Printer()
		if p.JSON {
			return p.RawJSON(resp.Body)
		}
		var r struct {
			Job             batchJob `json:"job"`
			ItemsReset      int      `json:"items_reset"`
			ItemsDispatched int      `json:"items_dispatched"`
		}
		if err := resp.Decode(&r); err != nil {
			return err
		}
		fmt.Fprintf(p.Out, "reset %d failed items (%d dispatched now, the rest by the recovery sweep)\n\n", r.ItemsReset, r.ItemsDispatched)
		batchHumanJob(p.Out, r.Job)
		return nil
	},
}

var batchAppendCmd = &cobra.Command{
	Use:   "append <id> <file|->",
	Short: "Add items to an --open job",
	Long: `Append items to a job submitted with --open. The file has the same forms as
"batch submit": one URL per line, a .json array or .jsonl items. Items not
dispatched immediately are recovered by the worker's sweep: never re-append
them, they would run twice.`,
	Example: `  spicrawl batch submit seeds.txt --open --name crawl
  echo https://example.com/discovered/17 | spicrawl batch append "$id" -
  spicrawl batch append "$id" more.jsonl && spicrawl batch close "$id"`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		urls, items, err := batchReadTargets(args[1])
		if err != nil {
			return err
		}
		body := map[string]any{}
		if len(urls) > 0 {
			body["urls"] = urls
		} else {
			body["items"] = items
		}
		c, err := Client()
		if err != nil {
			return err
		}
		resp, err := c.Do(cmd.Context(), api.Request{Method: "POST", Path: "/v1/batch/" + url.PathEscape(args[0]) + "/items", Body: body})
		if err != nil {
			return err
		}
		p := Printer()
		if p.JSON {
			return p.RawJSON(resp.Body)
		}
		var r struct {
			Job             batchJob `json:"job"`
			ItemsAdded      int      `json:"items_added"`
			ItemsDispatched int      `json:"items_dispatched"`
		}
		if err := resp.Decode(&r); err != nil {
			return err
		}
		fmt.Fprintf(p.Out, "added %d items (%d dispatched now, the rest by the recovery sweep; do not re-append them)\n\n", r.ItemsAdded, r.ItemsDispatched)
		batchHumanJob(p.Out, r.Job)
		return nil
	},
}

// batchJobCall sends a bodiless request that answers with a BatchJob.
func batchJobCall(cmd *cobra.Command, method, path string) error {
	c, err := Client()
	if err != nil {
		return err
	}
	var resp *api.Response
	if method == "GET" {
		resp, err = c.DoWithRetry(cmd.Context(), api.Request{Path: path}, 3)
	} else {
		resp, err = c.Do(cmd.Context(), api.Request{Method: method, Path: path})
	}
	if err != nil {
		return err
	}
	var job batchJob
	if err := resp.Decode(&job); err != nil {
		return err
	}
	return batchPrintJob(Printer(), resp.Body, job)
}

// ---- rendering --------------------------------------------------------------

func batchPrintJob(p *output.Printer, raw []byte, job batchJob) error {
	if p.JSON {
		return p.RawJSON(raw)
	}
	batchHumanJob(p.Out, job)
	return nil
}

func batchHumanJob(w io.Writer, j batchJob) {
	kv := func(k, v string) {
		if v != "" {
			fmt.Fprintf(w, "%-12s %s\n", k, v)
		}
	}
	kv("id", j.ID)
	kv("name", j.Name)
	status := j.Status
	if j.Open {
		status += " (open: accepting items until closed)"
	}
	kv("status", status)
	pr := j.Progress
	kv("progress", fmt.Sprintf("%d/%d (%.1f%%)  succeeded %d  failed %d  cancelled %d  skipped %d",
		pr.Completed, pr.Total, pr.PercentComplete, pr.Succeeded, pr.Failed, pr.Cancelled, pr.Skipped))
	kv("run", fmt.Sprintf("concurrency %d  priority %d  max attempts %d", j.Concurrency, j.Priority, j.MaxAttempts))
	kv("credits", fmt.Sprintf("%d charged, %d reserved", pr.CreditsCharged, j.EstimatedCredits))
	kv("submitted", j.SubmittedAt)
	kv("started", j.StartedAt)
	kv("finished", j.FinishedAt)
	kv("results", batchIf(j.ResultsExpireAt != "", "readable until "+j.ResultsExpireAt))
	if j.ErrorCode != "" || j.ErrorMessage != "" {
		kv("error", strings.TrimSpace(j.ErrorCode+" "+j.ErrorMessage))
	}
}

func batchPrintWarnings(p *output.Printer, j batchJob) {
	for _, w := range j.Warnings {
		var s string
		if json.Unmarshal(w, &s) != nil {
			s = string(w)
		}
		p.Warn("%s", s)
	}
}

func batchDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func batchIf(ok bool, s string) string {
	if ok {
		return s
	}
	return ""
}

func init() {
	// Scrape flags go through a scratch command so only the ones batch accepts
	// (batchSubmitScrapeFlags) are registered. The copied *pflag.Flag values
	// are shared, so scrapeFlags.build() still sees Changed on them; flags
	// never copied can never be Changed, so build() never sends their fields.
	scratch := &cobra.Command{Use: "scratch"}
	batchSubmitScrape = addScrapeFlags(scratch)
	scratch.Flags().VisitAll(func(fl *pflag.Flag) {
		usage, ok := batchSubmitScrapeFlags[fl.Name]
		if !ok {
			return
		}
		if usage != "" {
			fl.Usage = usage
		}
		batchSubmitCmd.Flags().AddFlag(fl)
	})
	sf := batchSubmitCmd.Flags()
	sf.IntVar(&batchSubmitRenderWait, "render-wait", 0, "fixed delay after load before the page is read, ms (the scrape \"wait\" field; needs --render or --stealth)")
	sf.StringVar(&batchSubmitName, "name", "", "label shown on the job and in the dashboard")
	sf.IntVar(&batchSubmitConcurrency, "concurrency", 0, "max items in flight at once (server default 10, max 50)")
	sf.IntVar(&batchSubmitPriority, "priority", 0, "scheduling priority 0-1000 relative to your other jobs (default 100)")
	sf.IntVar(&batchSubmitMaxAttempts, "max-attempts", 0, "attempts per item, 1-10 (default 3)")
	sf.Int64Var(&batchSubmitFailThresh, "failure-threshold", 0, "abort the job once this many items have failed")
	sf.Int64Var(&batchSubmitCreditBudget, "credit-budget", 0, "ceiling on the whole run, in credits")
	sf.BoolVar(&batchSubmitOpen, "open", false, "keep accepting items (batch append) until \"batch close\"")
	sf.BoolVar(&batchSubmitWait, "wait", false, "poll until the job finishes, then print it")
	sf.DurationVar(&batchSubmitPoll, "poll", 5*time.Second, "with --wait: poll interval")
	sf.DurationVar(&batchSubmitTimeout, "max-wait", 30*time.Minute, "with --wait: give up after this long (exit 11); 0 = never")

	wf := batchWaitCmd.Flags()
	wf.DurationVar(&batchWaitPoll, "poll", 5*time.Second, "poll interval")
	wf.DurationVar(&batchWaitTimeout, "max-wait", 30*time.Minute, "give up after this long (exit 11); 0 = never")

	lf := batchListCmd.Flags()
	lf.StringVar(&batchListStatus, "status", "", "only jobs in this status: queued|running|paused|cancelling|completed|failed|cancelled")
	lf.IntVar(&batchListLimit, "limit", 50, "jobs per page, 1-200")
	lf.StringVar(&batchListCursor, "cursor", "", "resume from a previous page's next_cursor")
	lf.BoolVar(&batchListAll, "all", false, "follow next_cursor until the oldest job")

	batchCancelCmd.Flags().BoolVarP(&batchCancelYes, "yes", "y", false, "do not ask for confirmation (required without a terminal)")

	batchAddResultsFlags()

	batchCmd.AddCommand(batchSubmitCmd, batchListCmd, batchGetCmd, batchWaitCmd, batchResultsCmd,
		batchContentCmd, batchCancelCmd, batchRetryCmd, batchCloseCmd, batchAppendCmd)
	rootCmd.AddCommand(batchCmd)
}

// batchOpenOut returns the writer for -o (or stdout) and a closer.
func batchOpenOut(path string) (io.Writer, func() error, error) {
	if path == "" || path == "-" {
		return stdout, func() error { return nil }, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, Usagef("-o: %v", err)
	}
	return f, f.Close, nil
}
