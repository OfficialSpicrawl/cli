package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// gScrapeRaw answers POST /v1/scrape with the raw document and the metadata
// headers the CLI wraps into its JSON object.
func gScrapeRaw(contentType, body string, target int, warnings ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		h := w.Header()
		h.Set("Content-Type", contentType)
		h.Set("X-Request-Id", "01J9ZQ5N8S4T9WY3L6P7Q1C2DE")
		h.Set("X-Credits-Charged", "2")
		h.Set("X-Request-Cost", "2")
		h.Set("X-Engine", "fetch")
		h.Set("X-Target-Status", strconv.Itoa(target))
		h.Set("X-Proxy-Source", "pool")
		h.Set("X-Final-Url", "https://example.com/final")
		h.Set("Cache-State", "miss")
		for _, wn := range warnings {
			h.Add("X-Warning", wn)
		}
		_, _ = io.WriteString(w, body)
	}
}

// gScrapeEnv answers with the API's JSON envelope (response_format=json, or
// any of extract, ai, autoparse, links, network capture, screenshot).
func gScrapeEnv(extra string) http.HandlerFunc {
	body := `{"url":"https://example.com/product/1","final_url":"https://example.com/product/1","status":200,` +
		`"content":"<h1>Anvil</h1><p class=\"price\">$19.99 & free shipping</p>","headers":{"content-type":"text/html"},` +
		`"truncated":false,"credits":3,"engine":"fetch","proxy_source":"pool","warnings":[]` + extra + `}`
	return gJSON(http.StatusOK, body)
}

func gScrape(h http.HandlerFunc) func() http.HandlerFunc {
	return goldenServe(map[string]http.HandlerFunc{"POST /v1/scrape": h})
}

const gHTMLDoc = "<!doctype html>\n<html><head><title>Example Domain</title></head>\n<body><h1>Example Domain</h1><p>Prices from $19.99 & up.</p></body></html>\n"

func init() {
	const u = "https://example.com/product/1"
	md := gScrapeRaw("text/markdown; charset=utf-8", "# Example Domain\n\nPrices from $19.99 & up.\n", 200, "FORMAT_COERCED: markdown requested, html served")
	pdf := gScrapeRaw("application/pdf", "%PDF-1.4\n1 0 obj\n<<>>\nendobj\n%%EOF\n", 200)
	shot := "UE5HREFUQQ==" // base64 "PNGDATA"
	shots := gScrapeEnv(`,"screenshots":[{"label":"page","format":"png","width":1280,"height":720,"data":"` + shot + `"},{"label":"page","format":"png","width":1280,"height":720,"data":"` + shot + `"}]`)

	goldenAdd(
		// ---- what a script gets: stdout is not a terminal, so one JSON object ----
		goldenCase{name: "scrape/html", args: []string{"scrape", u, "--format", "html"}, server: gScrape(gScrapeRaw("text/html; charset=utf-8", gHTMLDoc, 200))},
		goldenCase{name: "scrape/markdown", args: []string{"scrape", u, "--format", "markdown"}, server: gScrape(md)},
		goldenCase{name: "scrape/markdown_json_flag", args: []string{"scrape", u, "--format", "markdown"}, explicitJSON: true, server: gScrape(md)},
		goldenCase{name: "scrape/markdown_jsonl", args: []string{"scrape", u, "--format", "markdown", "--jsonl"}, server: gScrape(md)},
		goldenCase{name: "scrape/text", args: []string{"scrape", u, "--format", "text"}, server: gScrape(gScrapeRaw("text/plain; charset=utf-8", "Example Domain\nPrices from $19.99 & up.", 200))},
		goldenCase{name: "scrape/json_envelope", args: []string{"scrape", u, "--format", "json"}, server: gScrape(gScrapeEnv(`,"links":null`))},
		goldenCase{name: "scrape/json_envelope_jsonl", args: []string{"scrape", u, "--format", "json", "--jsonl"}, server: gScrape(gScrapeEnv(``))},
		goldenCase{name: "scrape/pdf_base64", args: []string{"scrape", u, "--format", "pdf"}, server: gScrape(pdf)},
		goldenCase{name: "scrape/extract", args: []string{"scrape", u, "--extract", `{"title":"h1","price":".price"}`}, server: gScrape(gScrapeEnv(`,"data":{"title":"Anvil","price":"$19.99 & free shipping"}`))},
		goldenCase{name: "scrape/ai", args: []string{"scrape", u, "--ai", "the product name and price"}, server: gScrape(gScrapeEnv(`,"data":{"name":"Anvil","price":19.99}`))},
		goldenCase{name: "scrape/autoparse", args: []string{"scrape", u, "--autoparse"}, server: gScrape(gScrapeEnv(`,"data":{"json_ld":[{"@type":"Product","name":"Anvil"}],"open_graph":{"og:title":"Anvil"}}`))},
		goldenCase{name: "scrape/links", args: []string{"scrape", u, "--links"}, server: gScrape(gScrapeEnv(`,"links":["https://example.com/a","https://example.com/b?x=1&y=2"]`))},
		goldenCase{name: "scrape/network_capture", args: []string{"scrape", u, "--render", "--network-capture", `{"url_patterns":["*/api/*"]}`},
			server: gScrape(gScrapeEnv(`,"network":[{"url":"https://example.com/api/price","status":200,"resource_type":"xhr","content_type":"application/json","body":"{\"price\":19.99}"}]`))},
		goldenCase{name: "scrape/screenshot_dir", args: []string{"scrape", u, "--render", "--screenshot", "--screenshot-dir", "{dir}/shots"}, server: gScrape(shots),
			files: []string{"shots/page.png", "shots/page-2.png"}},
		goldenCase{name: "scrape/output_file", args: []string{"scrape", u, "--format", "markdown", "-o", "{dir}/page.md"}, server: gScrape(md), files: []string{"page.md"}},
		goldenCase{name: "scrape/output_stdout_dash", args: []string{"scrape", u, "--format", "markdown", "-o", "-"}, server: gScrape(md)},

		// target answered non-2xx: the document is still printed, exit 6, target_error
		goldenCase{name: "scrape/target_503", args: []string{"scrape", u}, stderr: true, server: gScrape(gScrapeRaw("text/html", "<h1>Service Unavailable</h1>\n", 503))},
		goldenCase{name: "scrape/target_404_allowed", args: []string{"scrape", u, "--allowed-status", "404"}, server: gScrape(gScrapeRaw("text/html", "<h1>Not Found</h1>\n", 404))},
		goldenCase{name: "scrape/target_503_output_file_not_written", args: []string{"scrape", u, "-o", "{dir}/page.html"}, stderr: true, server: gScrape(gScrapeRaw("text/html", "<h1>Down</h1>\n", 503))},
		goldenCase{name: "scrape/target_403_envelope", args: []string{"scrape", u, "--extract", `{"t":"h1"}`}, stderr: true,
			server: gScrape(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Target-Status", "403")
				gScrapeEnv(`,"data":null`)(w, r)
			})},

		// many URLs on stdin: one compact JSON line each (completion order; --concurrency 1 makes it input order)
		goldenCase{name: "scrape/stdin_many", args: []string{"scrape", "-", "--format", "markdown", "--concurrency", "1"}, stderr: true,
			stdin:  "# targets\nhttps://example.com/ok\n\nhttps://example.com/down\nhttps://example.com/denied\n",
			server: gScrape(gScrapeStdinMany)},
		goldenCase{name: "scrape/stdin_many_all_ok", args: []string{"scrape", "-", "--format", "markdown", "--concurrency", "1"}, stdin: "https://example.com/ok\nhttps://example.com/ok2\n", server: gScrape(gScrapeStdinMany)},
		goldenCase{name: "scrape/stdin_many_empty", args: []string{"scrape", "-"}, stderr: true, stdin: "\n# nothing\n", server: gScrape(gScrapeStdinMany)},

		// failures: the problem document goes to stderr, stdout stays empty
		goldenCase{name: "scrape/err_auth", args: []string{"scrape", u}, stderr: true, server: gScrape(gProblem(401, "ERR::AUTH::INVALID_KEY", "the API key is not valid"))},
		goldenCase{name: "scrape/err_limit", args: []string{"scrape", u}, stderr: true, server: gScrape(gProblem(429, "ERR::LIMIT::RATE", "slow down"))},
		goldenCase{name: "scrape/err_request", args: []string{"scrape", u, "--render", "--engine", "nope"}, stderr: true, server: gScrape(gProblem(400, "ERR::REQUEST::INVALID_PARAMETER", "engine: unknown value"))},
		goldenCase{name: "scrape/err_upstream", args: []string{"scrape", u}, stderr: true, server: gScrape(gProblem(502, "ERR::UPSTREAM::BLOCKED", "the target served a bot challenge"))},
		goldenCase{name: "scrape/err_engine", args: []string{"scrape", u, "--ai", "x"}, stderr: true, server: gScrape(gProblem(501, "ERR::ENGINE::UNAVAILABLE", "ai_extract is not available"))},
		goldenCase{name: "scrape/err_usage_ai_exclusive", args: []string{"scrape", u, "--ai", "x", "--ai-schema", `{"type":"object"}`}, stderr: true, server: gScrape(md)},
		goldenCase{name: "scrape/err_usage_bad_extract", args: []string{"scrape", u, "--extract", "{nope"}, server: gScrape(md)},
		goldenCase{name: "scrape/err_usage_no_url", args: []string{"scrape"}, stderr: true, server: gScrape(md)},
		goldenCase{name: "scrape/err_no_key", args: []string{"scrape", u}, noKey: true, stderr: true, server: gScrape(md)},
		goldenCase{name: "scrape/err_unreachable", args: []string{"scrape", u}, baseURL: "http://127.0.0.1:1"}, // stderr names the OS's dial error

		// ---- what a person gets (human mode): the document itself ----
		goldenCase{name: "scrape_human/html", human: true, args: []string{"scrape", u, "--format", "html"}, server: gScrape(gScrapeRaw("text/html", gHTMLDoc, 200))},
		goldenCase{name: "scrape_human/markdown", human: true, args: []string{"scrape", u, "--format", "markdown"}, server: gScrape(md)},
		goldenCase{name: "scrape_human/markdown_meta", human: true, args: []string{"scrape", u, "--format", "markdown", "--meta"}, server: gScrape(md)},
		goldenCase{name: "scrape_human/text_no_trailing_newline", human: true, args: []string{"scrape", u, "--format", "text"}, server: gScrape(gScrapeRaw("text/plain", "Example Domain", 200))},
		goldenCase{name: "scrape_human/extract_data", human: true, args: []string{"scrape", u, "--extract", `{"title":"h1"}`}, server: gScrape(gScrapeEnv(`,"data":{"title":"Anvil","price":"$19.99 & free shipping"}`))},
		goldenCase{name: "scrape_human/links", human: true, args: []string{"scrape", u, "--links", "--format", "markdown"}, server: gScrape(gScrapeEnv(`,"links":["https://example.com/a","https://example.com/b?x=1&y=2"]`))},
		goldenCase{name: "scrape_human/network", human: true, args: []string{"scrape", u, "--render", "--network-capture", `{}`},
			server: gScrape(gScrapeEnv(`,"network":[{"url":"https://example.com/api/price","status":200,"resource_type":"xhr","content_type":"application/json"},{"url":"https://example.com/x.png","resource_type":"image"}]`))},
		goldenCase{name: "scrape_human/target_503", human: true, args: []string{"scrape", u}, server: gScrape(gScrapeRaw("text/html", "<h1>Service Unavailable</h1>\n", 503))},
		goldenCase{name: "scrape_human/output_file", human: true, args: []string{"scrape", u, "--format", "markdown", "-o", "{dir}/page.md"}, server: gScrape(md), files: []string{"page.md"}},
		goldenCase{name: "scrape_human/pdf_needs_file", human: true, args: []string{"scrape", u, "--format", "pdf"}, server: gScrape(pdf)},
		goldenCase{name: "scrape_human/pdf_output_file", human: true, args: []string{"scrape", u, "--format", "pdf", "-o", "{dir}/page.pdf"}, server: gScrape(pdf), files: []string{"page.pdf"}},
		goldenCase{name: "scrape_human/err_auth", human: true, args: []string{"scrape", u}, server: gScrape(gProblem(401, "ERR::AUTH::INVALID_KEY", "the API key is not valid"))},
	)
}

// gScrapeStdinMany answers per URL: /ok* is a page, /down a target 503, /denied an API problem.
func gScrapeStdinMany(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	b, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(b, &req)
	switch {
	case strings.HasSuffix(req.URL, "/down"):
		gScrapeRaw("text/markdown", "# Down\n", 503)(w, r)
	case strings.HasSuffix(req.URL, "/denied"):
		gProblem(403, "ERR::UPSTREAM::BLOCKED", "blocked by the target")(w, r)
	default:
		gScrapeRaw("text/markdown", "# "+req.URL+"\n", 200)(w, r)
	}
}
