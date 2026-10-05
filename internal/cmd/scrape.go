package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/OfficialSpicrawl/cli/internal/api"
	"github.com/OfficialSpicrawl/cli/internal/exitcode"
	"github.com/OfficialSpicrawl/cli/internal/output"
)

var (
	scrapeFlagSet       *scrapeFlags
	scrapeOutput        string
	scrapeScreenshotDir string
	scrapeJSONL         bool
	scrapeConcurrency   int
	scrapeRetry         int
	scrapeMeta          bool
)

var scrapeCmd = &cobra.Command{
	Use:   "scrape <url|->",
	Short: "Fetch one page (or many from stdin) as markdown, html, text, pdf or extracted data",
	Long: `Scrape a URL with POST /v1/scrape.

Output
  Human mode prints the document itself (or the extracted data when --extract,
  --ai, --ai-schema or --autoparse is set). --meta adds engine, credits, cache
  state, target status and request id on stderr.

  With --links or --network-capture, human mode prints that data on stdout
  after the document, each under a "--- links (N) ---" / "--- network (N) ---"
  separator line: links one per line, captured responses as
  "STATUS TYPE CONTENT-TYPE URL" (their bodies only in JSON). Pipe or use --json
  when you need the pieces separately.

  JSON mode (stdout not a terminal, or --json) prints one object. When the API
  answers with its JSON envelope (response_format=json, or any of --extract,
  --ai, --autoparse, --links, --network-capture, --screenshot) the envelope is
  printed as-is. When it answers with the raw document, the CLI wraps it:
    {"url","final_url","status","engine","proxy_source","credits_charged",
     "request_cost","cache_state","request_id","warnings","content_type","content"}
  where status is the target site's status (X-Target-Status).
  --jsonl prints that same object as one compact JSON line, in every mode
  (also on a terminal), so a single scrape can be appended to a .jsonl file.

Target status
  A site that answers with an error is still a successful API call: the API
  returns HTTP 200 with the site's status in X-Target-Status (envelope
  "status") and the site's body. The CLI treats any target status outside
  2xx as a failure unless it is listed in --allowed-status: the document is
  still printed (or written with -o), stderr gets "target returned 503" and
  the exit code is 6 (upstream). In JSON mode the object also carries
  "target_error": "target returned 503". --allowed-status 404 makes a 404 page
  exit 0. --original-status only moves the target's status onto the API's HTTP
  status line; the CLI reads the target status either way, so exit codes and
  output do not change.

  Binary documents (--format pdf) are never written to a terminal. In JSON mode
  without -o they are base64-encoded under "content_base64" instead of
  "content"; in human mode -o FILE is required.

Files
  -o FILE writes the document (raw bytes for pdf) to FILE instead of stdout. In
  JSON mode stdout then carries the metadata object without the written field,
  plus "output": FILE. Screenshots in the envelope are saved next to FILE as
  <FILE stem>.<label>.<format> (or into --screenshot-dir as <label>.<format>);
  their base64 "data" is replaced by "file" in the printed JSON.

Many URLs
  "spicrawl scrape -" reads URLs from stdin, one per line (blank lines and #
  comments skipped), runs --concurrency requests at once and prints one compact
  JSON line per URL in completion order, in every mode (--jsonl is implied).
  Each line is the result object above plus "index" (1-based position of the
  URL in the input), or {"index","url","error"} where error is the API's
  problem document. A target status outside 2xx/--allowed-status keeps the
  result and adds "target_error". Exit code is 0 when every URL succeeded,
  else the code of the first failure.`,
	Example: `  # A page as clean markdown, for an LLM context window
  spicrawl scrape https://example.com/blog/launch --format markdown

  # A JS-rendered page, waiting for the element that holds the data
  spicrawl scrape https://example.com/pricing --render --wait-for '.plans' --format markdown

  # Through a residential exit in Germany, with the metadata on stderr
  spicrawl scrape https://example.de/produkt/42 --premium-proxy --country de --meta

  # Selector extraction; prints only the extracted data
  spicrawl scrape https://example.com/product/1 --extract '{"title":"h1","price":".price"}'

  # AI extraction with a plain-language prompt, retrying transient failures
  spicrawl scrape https://news.example.com --ai 'the ten headlines with their links' --retry 3

  # Accept a 404 page as a result (exit 0) instead of a target failure (exit 6)
  spicrawl scrape https://example.com/old-page --allowed-status 404

  # One compact JSON line per scrape, appended to a log
  spicrawl scrape https://example.com --format markdown --jsonl >> pages.jsonl

  # Many URLs from a file, 8 at a time, one JSON line each; keep the failures
  spicrawl scrape - --format markdown --concurrency 8 < urls.txt | jq -c 'select(.error or .target_error)'`,
	Args: cobra.ExactArgs(1),
	RunE: scrapeRun,
}

func init() {
	scrapeFlagSet = addScrapeFlags(scrapeCmd)
	f := scrapeCmd.Flags()
	f.StringVarP(&scrapeOutput, "output", "o", "", "write the document to FILE instead of stdout (screenshots are saved next to it)")
	f.StringVar(&scrapeScreenshotDir, "screenshot-dir", "", "directory to save screenshots in (default: next to -o FILE)")
	f.BoolVar(&scrapeJSONL, "jsonl", false, "print the result as one compact JSON line, even on a terminal (always on for '-')")
	f.IntVar(&scrapeConcurrency, "concurrency", 4, "with '-': parallel requests")
	f.IntVar(&scrapeRetry, "retry", 1, "attempts for retryable errors (1 = no retry)")
	f.BoolVar(&scrapeMeta, "meta", false, "human mode: print engine, credits, cache, target status and request id to stderr")
	rootCmd.AddCommand(scrapeCmd)
}

// scrapePrinter is Printer; tests swap it to exercise human mode.
var scrapePrinter = Printer

func scrapeRun(cmd *cobra.Command, args []string) error {
	body, err := scrapeFlagSet.build()
	if err != nil {
		return err
	}
	if scrapeRetry < 1 {
		return Usagef("--retry must be at least 1")
	}
	p := scrapePrinter()
	if args[0] == "-" {
		return scrapeRunMany(cmd.Context(), p, body)
	}
	machine := p.JSON || scrapeJSONL
	if format, _ := body["response_format"].(string); format == "pdf" && !machine && scrapeOutput == "" {
		return Usagef("--format pdf is binary: pass -o FILE (or --json to get it base64-encoded)")
	}
	client, err := Client()
	if err != nil {
		return err
	}
	body["url"] = args[0]
	resp, err := scrapeDo(cmd.Context(), client, body)
	if err != nil {
		return err
	}
	res, err := scrapeResultOf(args[0], resp)
	if err != nil {
		return err
	}
	targetErr := res.checkTarget(body)

	// Screenshots: save when there is somewhere to put them.
	if shots := scrapeScreenshots(res.obj); len(shots) > 0 {
		dir, stem := scrapeScreenshotDir, ""
		if dir == "" && scrapeOutput != "" {
			dir = filepath.Dir(scrapeOutput)
			stem = strings.TrimSuffix(filepath.Base(scrapeOutput), filepath.Ext(scrapeOutput)) + "."
		}
		if dir != "" {
			paths, err := scrapeSaveScreenshots(shots, dir, stem)
			if err != nil {
				return err
			}
			res.modified = true
			for _, path := range paths {
				p.Info("saved screenshot %s", path)
			}
		} else if !machine {
			p.Info("%d screenshot(s) not saved: pass -o FILE or --screenshot-dir DIR", len(shots))
		}
	}

	doc, docField := res.document(body)
	if scrapeOutput != "" {
		if err := os.WriteFile(scrapeOutput, doc, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", scrapeOutput, err)
		}
		p.Info("wrote %d bytes to %s", len(doc), scrapeOutput)
	}

	if machine {
		if scrapeOutput != "" {
			delete(res.obj, docField)
			res.obj["output"] = scrapeOutput
			res.modified = true
		}
		var err error
		switch {
		case res.envelope && !res.modified && scrapeJSONL:
			var buf bytes.Buffer
			if err = json.Compact(&buf, resp.Body); err == nil {
				buf.WriteByte('\n')
				_, err = p.Out.Write(buf.Bytes())
			}
		case res.envelope && !res.modified:
			err = p.RawJSON(resp.Body)
		case scrapeJSONL:
			err = p.Line(res.obj)
		default:
			err = p.Value(res.obj)
		}
		if err != nil {
			return err
		}
		return targetErr
	}

	if scrapeOutput == "" {
		if !res.text {
			return Usagef("the response is binary (%s): pass -o FILE", res.mediaType)
		}
		if _, err := p.Out.Write(doc); err != nil {
			return err
		}
		if len(doc) > 0 && doc[len(doc)-1] != '\n' {
			fmt.Fprintln(p.Out)
		}
	}
	res.printExtras(p.Out)
	for _, w := range res.warnings() {
		p.Warn("%s", w)
	}
	if scrapeMeta {
		res.printMeta(p)
	}
	return targetErr
}

// checkTarget returns an upstream ExitError when the target's status is
// outside 2xx and not accepted by allowed_status_codes, and records it in
// the printed object as "target_error". The API reports such a status on a
// successful call (HTTP 200 + X-Target-Status), so without this a failed
// target would look like success.
func (r *scrapeResult) checkTarget(body map[string]any) error {
	st := r.targetStatus(body)
	if st == 0 || (st >= 200 && st <= 299) || scrapeStatusAllowed(st, body["allowed_status_codes"]) {
		return nil
	}
	msg := fmt.Sprintf("target returned %d", st)
	r.obj["target_error"] = msg
	r.modified = true
	return &ExitError{Code: exitcode.Upstream, Err: fmt.Errorf("%s (pass --allowed-status %d to accept it)", msg, st)}
}

// targetStatus is the target site's status: X-Target-Status, else the
// envelope's status, else (with original_status) the HTTP status. 0 = unknown.
func (r *scrapeResult) targetStatus(body map[string]any) int {
	if n, err := strconv.Atoi(strings.TrimSpace(r.resp.Header.Get("X-Target-Status"))); err == nil {
		return n
	}
	switch v := r.obj["status"].(type) {
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	case int:
		return v
	case float64:
		return int(v)
	}
	if body["original_status"] == true {
		return r.resp.Status
	}
	return 0
}

// scrapeStatusAllowed reports whether st is in allowed_status_codes, which
// is []int from --allowed-status or []any of numbers from --body.
func scrapeStatusAllowed(st int, allowed any) bool {
	switch list := allowed.(type) {
	case []int:
		for _, a := range list {
			if a == st {
				return true
			}
		}
	case []any:
		for _, a := range list {
			if n, ok := a.(float64); ok && int(n) == st {
				return true
			}
		}
	}
	return false
}

// printExtras writes, in human mode, the envelope's links and captured
// network responses after the document, each under a separator line, so
// data the caller asked for is not silently dropped.
func (r *scrapeResult) printExtras(w io.Writer) {
	if !r.envelope {
		return
	}
	if list, ok := r.obj["links"].([]any); ok {
		fmt.Fprintf(w, "--- links (%d) ---\n", len(list))
		for _, l := range list {
			fmt.Fprintln(w, fmt.Sprint(l))
		}
	}
	if list, ok := r.obj["network"].([]any); ok {
		fmt.Fprintf(w, "--- network (%d) ---\n", len(list))
		str := func(m map[string]any, k string) string {
			if v, ok := m[k]; ok && v != nil && fmt.Sprint(v) != "" {
				return fmt.Sprint(v)
			}
			return "-"
		}
		for _, e := range list {
			m, ok := e.(map[string]any)
			if !ok {
				continue
			}
			fmt.Fprintf(w, "%s %s %s %s\n", str(m, "status"), str(m, "resource_type"), str(m, "content_type"), str(m, "url"))
		}
	}
}

// scrapeDo sends one scrape, retrying retryable failures when --retry > 1.
func scrapeDo(ctx context.Context, client *api.Client, body map[string]any) (*api.Response, error) {
	return client.DoWithRetry(ctx, api.Request{
		Method: http.MethodPost,
		Path:   "/v1/scrape",
		Body:   body,
		Accept: "*/*",
		// original_status relays the target's status on a successful scrape.
		AllowNonProblem: body["original_status"] == true,
	}, scrapeRetry)
}

// scrapeResult is one successful scrape turned into the object the CLI prints.
type scrapeResult struct {
	resp      *api.Response
	obj       map[string]any // the envelope, or the wrapper built from headers
	envelope  bool           // the API returned its JSON envelope
	text      bool           // the raw document is text (not pdf/image)
	mediaType string
	modified  bool // obj differs from the envelope the API sent
}

func scrapeResultOf(url string, resp *api.Response) (*scrapeResult, error) {
	ct := resp.Header.Get("Content-Type")
	mt, _, _ := mime.ParseMediaType(ct)
	r := &scrapeResult{resp: resp, mediaType: mt}
	if mt == "application/json" {
		dec := json.NewDecoder(bytes.NewReader(resp.Body))
		dec.UseNumber()
		if err := dec.Decode(&r.obj); err != nil {
			return nil, fmt.Errorf("decode scrape envelope: %w", err)
		}
		r.envelope, r.text = true, true
		return r, nil
	}
	r.text = scrapeIsText(mt, resp.Body)
	h := resp.Header
	warnings := h.Values("X-Warning")
	if warnings == nil {
		warnings = []string{}
	}
	r.obj = map[string]any{
		"url":             url,
		"final_url":       h.Get("X-Final-Url"),
		"status":          scrapeHeaderInt(h, "X-Target-Status"),
		"engine":          h.Get("X-Engine"),
		"proxy_source":    h.Get("X-Proxy-Source"),
		"credits_charged": scrapeHeaderInt(h, "X-Credits-Charged"),
		"request_cost":    scrapeHeaderInt(h, "X-Request-Cost"),
		"cache_state":     h.Get("Cache-State"),
		"request_id":      h.Get("X-Request-Id"),
		"warnings":        warnings,
		"content_type":    ct,
	}
	if r.text {
		r.obj["content"] = string(resp.Body)
	} else {
		r.obj["content_base64"] = base64.StdEncoding.EncodeToString(resp.Body)
	}
	return r, nil
}

// document returns what human mode prints and -o writes, and the obj field
// it came from: the raw body, the envelope's data (when extraction was asked
// for), or the envelope's content.
func (r *scrapeResult) document(body map[string]any) ([]byte, string) {
	if !r.envelope {
		if r.text {
			return r.resp.Body, "content"
		}
		return r.resp.Body, "content_base64"
	}
	if scrapeWantsData(body) {
		if data, ok := r.obj["data"]; ok && data != nil {
			b, err := json.MarshalIndent(data, "", "  ")
			if err == nil {
				return append(b, '\n'), "data"
			}
		}
	}
	s, _ := r.obj["content"].(string)
	return []byte(s), "content"
}

func scrapeWantsData(body map[string]any) bool {
	for _, k := range []string{"extract", "ai_extract", "autoparse"} {
		if v, ok := body[k]; ok && v != nil && v != false {
			return true
		}
	}
	return false
}

func (r *scrapeResult) warnings() []string {
	if w := r.resp.Header.Values("X-Warning"); len(w) > 0 {
		return w
	}
	var out []string
	if list, ok := r.obj["warnings"].([]any); ok {
		for _, w := range list {
			if s, ok := w.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func (r *scrapeResult) printMeta(p *output.Printer) {
	h := r.resp.Header
	get := func(header, field string) string {
		if v := h.Get(header); v != "" {
			return v
		}
		if v, ok := r.obj[field]; ok && v != nil {
			return fmt.Sprint(v)
		}
		return "-"
	}
	fmt.Fprintf(p.Err, "engine: %s\n", get("X-Engine", "engine"))
	fmt.Fprintf(p.Err, "credits: %s (request cost %s)\n", get("X-Credits-Charged", "credits"), get("X-Request-Cost", "request_cost"))
	fmt.Fprintf(p.Err, "cache: %s\n", get("Cache-State", "cache_state"))
	fmt.Fprintf(p.Err, "target status: %s\n", get("X-Target-Status", "status"))
	fmt.Fprintf(p.Err, "proxy: %s\n", get("X-Proxy-Source", "proxy_source"))
	if fu := get("X-Final-Url", "final_url"); fu != "-" && fu != "" {
		fmt.Fprintf(p.Err, "final url: %s\n", fu)
	}
	fmt.Fprintf(p.Err, "request id: %s\n", get("X-Request-Id", "request_id"))
}

func scrapeHeaderInt(h http.Header, name string) any {
	n, err := strconv.Atoi(strings.TrimSpace(h.Get(name)))
	if err != nil {
		return nil
	}
	return n
}

func scrapeIsText(mt string, b []byte) bool {
	switch {
	case strings.HasPrefix(mt, "text/"),
		mt == "application/json", strings.HasSuffix(mt, "+json"),
		mt == "application/xml", strings.HasSuffix(mt, "+xml"),
		mt == "application/javascript":
		return true
	case mt == "":
		return utf8.Valid(b)
	}
	return false
}

// scrapeScreenshots returns the envelope's screenshot objects.
func scrapeScreenshots(obj map[string]any) []map[string]any {
	list, _ := obj["screenshots"].([]any)
	var out []map[string]any
	for _, s := range list {
		if m, ok := s.(map[string]any); ok {
			if _, has := m["data"].(string); has {
				out = append(out, m)
			}
		}
	}
	return out
}

// scrapeSaveScreenshots decodes each screenshot into dir as
// <prefix><label>.<format> and replaces its "data" with "file".
func scrapeSaveScreenshots(shots []map[string]any, dir, prefix string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("screenshot dir: %w", err)
	}
	seen := map[string]int{}
	var paths []string
	for i, s := range shots {
		raw, err := base64.StdEncoding.DecodeString(s["data"].(string))
		if err != nil {
			return paths, fmt.Errorf("screenshot %d: bad base64: %w", i, err)
		}
		label := scrapeSafeName(fmt.Sprint(s["label"]))
		if label == "" || label == "<nil>" {
			label = "screenshot"
		}
		if n := seen[label]; n > 0 {
			label = fmt.Sprintf("%s-%d", label, n+1)
		}
		seen[label]++
		ext, _ := s["format"].(string)
		if ext == "" {
			ext = "png"
		}
		path := filepath.Join(dir, prefix+label+"."+scrapeSafeName(ext))
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			return paths, fmt.Errorf("write %s: %w", path, err)
		}
		delete(s, "data")
		s["file"] = path
		paths = append(paths, path)
	}
	return paths, nil
}

func scrapeSafeName(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return '_'
	}, s)
}

// scrapeRunMany scrapes every URL on stdin and prints one JSON line each.
func scrapeRunMany(ctx context.Context, p *output.Printer, base map[string]any) error {
	if scrapeOutput != "" {
		return Usagef("-o does not apply to 'scrape -': results go to stdout as JSON lines (use --screenshot-dir for screenshots)")
	}
	if scrapeConcurrency < 1 {
		return Usagef("--concurrency must be at least 1")
	}
	urls, err := readLines("-")
	if err != nil {
		return Usagef("read stdin: %v", err)
	}
	if len(urls) == 0 {
		return Usagef("no URLs on stdin")
	}
	client, err := Client()
	if err != nil {
		return err
	}

	type job struct {
		index int
		url   string
	}
	jobs := make(chan job)
	var (
		mu        sync.Mutex
		failed    int
		firstCode int
	)
	emit := func(line map[string]any, code int) {
		mu.Lock()
		defer mu.Unlock()
		if code != exitcode.OK {
			failed++
			if firstCode == exitcode.OK {
				firstCode = code
			}
		}
		_ = p.Line(line)
	}
	one := func(j job) {
		body := make(map[string]any, len(base)+1)
		for k, v := range base {
			body[k] = v
		}
		body["url"] = j.url
		resp, err := scrapeDo(ctx, client, body)
		var res *scrapeResult
		if err == nil {
			res, err = scrapeResultOf(j.url, resp)
		}
		if err == nil && scrapeScreenshotDir != "" {
			if shots := scrapeScreenshots(res.obj); len(shots) > 0 {
				_, err = scrapeSaveScreenshots(shots, scrapeScreenshotDir, strconv.Itoa(j.index)+".")
			}
		}
		if err != nil {
			errObj, code := scrapeErrorObject(err)
			emit(map[string]any{"index": j.index, "url": j.url, "error": errObj}, code)
			return
		}
		code := exitcode.OK
		if terr := res.checkTarget(body); terr != nil {
			code = exitcode.Upstream
		}
		res.obj["index"] = j.index
		if _, ok := res.obj["url"]; !ok {
			res.obj["url"] = j.url
		}
		emit(res.obj, code)
	}

	var wg sync.WaitGroup
	workers := min(scrapeConcurrency, len(urls))
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				one(j)
			}
		}()
	}
feed:
	for i, u := range urls {
		select {
		case jobs <- job{index: i + 1, url: u}:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()

	if !p.JSON {
		p.Info("%d ok, %d failed", len(urls)-failed, failed)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if failed > 0 {
		return &ExitError{Code: firstCode, Err: fmt.Errorf("%d of %d URLs failed", failed, len(urls))}
	}
	return nil
}

// scrapeErrorObject is the "error" value of a failed line and its exit code.
func scrapeErrorObject(err error) (any, int) {
	// ExitCode keeps per-line codes in step with the process exit code
	// (timeouts, capability-unavailable problems, config errors).
	code := ExitCode(err)
	if prob, ok := api.AsProblem(err); ok {
		return prob, code
	}
	return map[string]any{"detail": err.Error(), "exit_code": code}, code
}
