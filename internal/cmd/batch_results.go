package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Spicrawl/cli/internal/api"
)

var (
	batchResultsStatus string
	batchResultsLimit  int
	batchResultsCursor string
	batchResultsAll    bool
	batchResultsOut    string
)

var batchResultsCmd = &cobra.Command{
	Use:   "results <id>",
	Short: "Download finished items as JSON Lines",
	Long: `Fetch the job's finished items (GET /v1/batch/{id}/results), one BatchResultLine
JSON object per line, in seq order. In JSON mode (or with -o) the lines are
written unchanged; in human mode a seq/status/http_status/url/bytes table is
printed. --all follows X-Next-Cursor to the last finished item.

result.content is the stored payload as raw JSON text: a JSON string holding the
document in the job's response_format (html by default, or markdown/text), so
decode it once. Bodies are only inlined once the job is terminal; for one item's
payload use "spicrawl batch content <id> <seq>".
Results expire at the job's results_expire_at; after that the API answers 410
ERR::REQUEST::BEYOND_RETENTION.`,
	Example: `  # Everything, to a file, for later processing
  spicrawl batch results 01J9Z6V0Q8M4K2T7R3N5B1C9XA --all -o results.jsonl

  # Only failures, with their error codes
  spicrawl batch results "$id" --status failed --all | jq -r '[.seq, .url, .error.code] | @tsv'

  # Decode the document of every succeeded item
  spicrawl batch results "$id" --status succeeded --all | jq -r '.result.content | fromjson'`,
	Args: cobra.ExactArgs(1),
	RunE: batchRunResults,
}

func batchAddResultsFlags() {
	f := batchResultsCmd.Flags()
	f.StringVar(&batchResultsStatus, "status", "", "only items with this status: succeeded|failed|cancelled|skipped")
	f.IntVar(&batchResultsLimit, "limit", 0, "result lines per page, 1-5000 (server default 500)")
	f.StringVar(&batchResultsCursor, "cursor", "", "resume from a previous page's X-Next-Cursor")
	f.BoolVar(&batchResultsAll, "all", false, "follow X-Next-Cursor until the last finished item")
	f.StringVarP(&batchResultsOut, "output", "o", "", "write the JSONL to FILE instead of stdout")

	batchContentCmd.Flags().StringVarP(&batchContentOut, "output", "o", "", "write the document to FILE instead of stdout")
}

type batchResultLine struct {
	Seq        int64  `json:"seq"`
	ExternalID string `json:"external_id"`
	URL        string `json:"url"`
	Status     string `json:"status"`
	HTTPStatus *int   `json:"http_status"`
	Bytes      int64  `json:"bytes"`
	Error      *struct {
		Code string `json:"code"`
	} `json:"error"`
}

func batchRunResults(cmd *cobra.Command, args []string) error {
	c, err := Client()
	if err != nil {
		return err
	}
	p := Printer()
	path := "/v1/batch/" + url.PathEscape(args[0]) + "/results"
	q := url.Values{}
	if batchResultsStatus != "" {
		q.Set("status", batchResultsStatus)
	}
	if cmd.Flags().Changed("limit") {
		q.Set("limit", strconv.Itoa(batchResultsLimit))
	}

	raw := p.JSON || batchResultsOut != ""
	var w io.Writer
	closeOut := func() error { return nil }
	if raw {
		if w, closeOut, err = batchOpenOut(batchResultsOut); err != nil {
			return err
		}
	}

	cursor := batchResultsCursor
	lines := 0
	var rows [][]string
	var next string
	for {
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		resp, err := c.DoWithRetry(cmd.Context(), api.Request{Path: path, Query: q, Accept: "application/x-ndjson"}, 3)
		if err != nil {
			_ = closeOut()
			return err
		}
		body := resp.Body
		if raw {
			if len(body) > 0 && body[len(body)-1] != '\n' {
				body = append(body, '\n')
			}
			if _, err := w.Write(body); err != nil {
				_ = closeOut()
				return err
			}
			lines += bytes.Count(body, []byte{'\n'})
		} else {
			r, err := batchResultRows(body)
			if err != nil {
				return err
			}
			rows = append(rows, r...)
		}
		next = batchNextCursor(resp)
		if !batchResultsAll || next == "" {
			break
		}
		cursor = next
	}
	if err := closeOut(); err != nil {
		return err
	}

	if !raw {
		if len(rows) == 0 {
			fmt.Fprintln(p.Out, "no finished items yet")
		} else {
			p.Table([]string{"SEQ", "STATUS", "HTTP", "URL", "BYTES"}, rows)
		}
	} else if batchResultsOut != "" && batchResultsOut != "-" {
		p.Info("wrote %d results to %s", lines, batchResultsOut)
	}
	if next != "" && !batchResultsAll {
		p.Info("more results: --cursor %s, or --all", next)
	}
	return nil
}

func batchResultRows(body []byte) ([][]string, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	var rows [][]string
	for {
		var l batchResultLine
		err := dec.Decode(&l)
		if errors.Is(err, io.EOF) {
			return rows, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decode results: %w", err)
		}
		status := l.Status
		if l.Error != nil && l.Error.Code != "" {
			status += " " + l.Error.Code
		}
		hs := "-"
		if l.HTTPStatus != nil {
			hs = strconv.Itoa(*l.HTTPStatus)
		}
		u := l.URL
		if l.ExternalID != "" {
			u += " [" + l.ExternalID + "]"
		}
		rows = append(rows, []string{strconv.FormatInt(l.Seq, 10), status, hs, u, strconv.FormatInt(l.Bytes, 10)})
	}
}

// batchNextCursor reads X-Next-Cursor, falling back to the cursor parameter of
// the rel="next" Link header.
func batchNextCursor(resp *api.Response) string {
	if c := resp.Header.Get("X-Next-Cursor"); c != "" {
		return c
	}
	for _, link := range strings.Split(resp.Header.Get("Link"), ",") {
		target, params, ok := strings.Cut(link, ";")
		if !ok || !strings.Contains(params, `rel="next"`) {
			continue
		}
		target = strings.Trim(strings.TrimSpace(target), "<>")
		if u, err := url.Parse(target); err == nil {
			return u.Query().Get("cursor")
		}
	}
	return ""
}

// ---- content ------------------------------------------------------------------

var batchContentOut string

var batchContentCmd = &cobra.Command{
	Use:   "content <id> <seq>",
	Short: "Print one finished item's document (GET /v1/batch/{id}/tasks/{seq}/content)",
	Long: `Fetch a single item's payload by its seq (0-based submission order, "seq" on a
result line) without paging the whole result set. The API caps it at 512 KiB;
a cut payload is reported on stderr.

In JSON mode the payload is printed as the API returned it (a JSON string holding
the document). In human mode, and always with -o, the document itself is written.

409 means the item has not finished yet (poll the job and ask again) or finished
as failed/cancelled/skipped and never will; 404 means seq is beyond the job;
503 means the deployment cannot serve per-item payloads (use "batch results").`,
	Example: `  spicrawl batch content 01J9Z6V0Q8M4K2T7R3N5B1C9XA 0 -o page.html
  spicrawl batch content "$id" 17 --json | jq -r .`,
	Args: cobra.ExactArgs(2),
	RunE: batchRunContent,
}

func batchRunContent(cmd *cobra.Command, args []string) error {
	seq, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil || seq < 0 {
		return Usagef("seq %q: want the item's 0-based position (\"seq\" on a result line)", args[1])
	}
	c, err := Client()
	if err != nil {
		return err
	}
	p := Printer()
	resp, err := c.DoWithRetry(cmd.Context(), api.Request{
		Path: "/v1/batch/" + url.PathEscape(args[0]) + "/tasks/" + strconv.FormatInt(seq, 10) + "/content",
	}, 3)
	if err != nil {
		return err
	}
	if resp.Header.Get("X-Content-Truncated") == "true" {
		p.Warn("item %d's payload was cut at 512 KiB; the full document is in \"spicrawl batch results %s\"", seq, args[0])
	}
	if p.JSON && (batchContentOut == "" || batchContentOut == "-") {
		return p.RawJSON(resp.Body)
	}
	doc := resp.Body
	var s string
	if json.Unmarshal(resp.Body, &s) == nil {
		doc = []byte(s)
	}
	w, closeOut, err := batchOpenOut(batchContentOut)
	if err != nil {
		return err
	}
	if _, err := w.Write(doc); err != nil {
		_ = closeOut()
		return err
	}
	if err := closeOut(); err != nil {
		return err
	}
	if batchContentOut != "" && batchContentOut != "-" {
		p.Info("wrote %d bytes to %s", len(doc), batchContentOut)
	}
	return nil
}
