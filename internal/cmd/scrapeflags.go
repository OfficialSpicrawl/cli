package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// scrapeFlags are the request-shaping flags shared by `scrape` and
// `batch submit`. Each maps to one ScrapeRequest field in openapi.yaml.
// Only flags the user actually set are sent, so the API's defaults (and a
// project's defaults) apply to everything else.
type scrapeFlags struct {
	fs *pflag.FlagSet

	format, engine, mode, country, proxy, stickyKey, session        string
	waitFor, screenshotSelector, screenshotFormat, method, aiPrompt string
	extract, aiSchema, actions, networkCapture, body                string
	render, stealth, impersonate, premium, proxyVerify, headless    bool
	autoparse, links, mainContent, noMainContent, noParsePDF        bool
	screenshot, screenshotFull, noCache, originalStatus             bool
	wait, waitForTimeout, cacheTTL, maxCost, screenshotQuality      int
	block, include, exclude, headers, allowedStatus                 []string
}

// addScrapeFlags registers the shared flags on cmd.
func addScrapeFlags(cmd *cobra.Command) *scrapeFlags {
	s := &scrapeFlags{fs: cmd.Flags()}
	f := cmd.Flags()
	f.StringVar(&s.format, "format", "", "response_format: html|markdown|text|json|pdf")
	f.StringVar(&s.method, "method", "", "HTTP method sent to the target (default GET)")
	f.BoolVar(&s.render, "render", false, "render JavaScript in a browser (js_render)")
	f.BoolVar(&s.stealth, "stealth", false, "hardened browser fingerprint (camoufox; dearest tier)")
	f.BoolVar(&s.impersonate, "impersonate", false, "fetch tier: present a real browser TLS/HTTP2 fingerprint (on by default; --impersonate=false turns it off)")
	f.StringVar(&s.engine, "engine", "", "pin the engine: fetch|obscura|chromium|camoufox")
	f.StringVar(&s.mode, "mode", "", "auto: escalate through tiers until one succeeds")
	f.BoolVar(&s.premium, "premium-proxy", false, "residential exits (priced higher; fails rather than falling back)")
	f.StringVar(&s.country, "country", "", "proxy_country, ISO 3166-1 alpha-2 lowercase (needs --premium-proxy)")
	f.StringVar(&s.proxy, "proxy", "", "your own proxy URL (takes precedence over the pool)")
	f.BoolVar(&s.proxyVerify, "proxy-verify", false, "check --proxy before spending engine time")
	f.StringVar(&s.stickyKey, "sticky-key", "", "pin the same exit across requests")
	f.StringVar(&s.session, "session", "", "session_id: reuse cookies and storage")
	f.IntVar(&s.wait, "wait", 0, "fixed delay after load, ms")
	f.StringVar(&s.waitFor, "wait-for", "", "CSS selector to wait for")
	f.IntVar(&s.waitForTimeout, "wait-for-timeout", 0, "cap on --wait-for, ms")
	f.StringSliceVar(&s.block, "block", nil, "block_resources: images,fonts,media,stylesheets,scripts or none")
	f.BoolVar(&s.headless, "headless", true, "chromium only: --headless=false runs with a real display")
	f.StringArrayVar(&s.headers, "header", nil, "custom header sent to the target, 'Name: value' (repeatable)")
	f.StringVar(&s.extract, "extract", "", "selector map or JSON Schema, as JSON or @file")
	f.StringVar(&s.aiPrompt, "ai", "", "ai_extract prompt: describe the data in plain language")
	f.StringVar(&s.aiSchema, "ai-schema", "", "ai_extract JSON Schema, as JSON or @file (instead of --ai)")
	f.BoolVar(&s.autoparse, "autoparse", false, "return the page's own JSON-LD, OpenGraph and embedded state")
	f.BoolVar(&s.links, "links", false, "return the page's links")
	f.BoolVar(&s.mainContent, "main-content", false, "keep only the main article")
	f.BoolVar(&s.noMainContent, "no-main-content", false, "keep the whole document (markdown defaults to main content)")
	f.StringArrayVar(&s.include, "include", nil, "include_tags CSS selector (repeatable)")
	f.StringArrayVar(&s.exclude, "exclude", nil, "exclude_tags CSS selector (repeatable)")
	f.BoolVar(&s.noParsePDF, "no-parse-pdf", false, "refuse PDFs instead of parsing them to text")
	f.StringVar(&s.actions, "actions", "", "browser actions array, as JSON or @file")
	f.StringVar(&s.networkCapture, "network-capture", "", "network_capture object, as JSON or @file")
	f.BoolVar(&s.screenshot, "screenshot", false, "capture a screenshot")
	f.BoolVar(&s.screenshotFull, "screenshot-full-page", false, "screenshot the full page")
	f.StringVar(&s.screenshotSelector, "screenshot-selector", "", "screenshot one element")
	f.StringVar(&s.screenshotFormat, "screenshot-format", "", "png|jpeg|webp")
	f.IntVar(&s.screenshotQuality, "screenshot-quality", 0, "jpeg/webp quality 1-100")
	f.BoolVar(&s.noCache, "no-cache", false, "force a fresh fetch (cache=false)")
	f.IntVar(&s.cacheTTL, "cache-ttl", 0, "accept a cached copy no older than this many seconds")
	f.IntVar(&s.maxCost, "max-cost", 0, "refuse if the request would cost more credits than this")
	f.StringSliceVar(&s.allowedStatus, "allowed-status", nil, "extra target statuses to accept, e.g. 403: billed as a success (200/404/410 always are), ends --mode auto escalation, and scrape exits 0 instead of 6 on them")
	f.BoolVar(&s.originalStatus, "original-status", false, "API answers with the target's status on its HTTP status line (the CLI reads the target status either way)")
	f.StringVar(&s.body, "body", "", "raw request body as JSON or @file; flags override its fields (nested objects are merged key by key)")
	return s
}

// build returns the request body: --body first, then every flag that was set.
func (s *scrapeFlags) build() (map[string]any, error) {
	body := map[string]any{}
	if s.body != "" {
		if err := decodeJSONArg("--body", s.body, &body); err != nil {
			return nil, err
		}
		if body == nil { // --body null
			return nil, Usagef("--body: want a JSON object, got null")
		}
	}
	set := func(flag, field string, v any) {
		if s.fs.Changed(flag) {
			body[field] = v
		}
	}
	set("format", "response_format", s.format)
	set("method", "method", strings.ToUpper(s.method))
	set("render", "js_render", s.render)
	set("stealth", "stealth", s.stealth)
	set("impersonate", "impersonate", s.impersonate)
	set("engine", "engine", s.engine)
	set("mode", "mode", s.mode)
	set("premium-proxy", "premium_proxy", s.premium)
	set("country", "proxy_country", strings.ToLower(s.country))
	set("proxy", "proxy", s.proxy)
	set("proxy-verify", "proxy_verify", s.proxyVerify)
	set("sticky-key", "sticky_key", s.stickyKey)
	set("session", "session_id", s.session)
	set("wait", "wait", s.wait)
	set("wait-for", "wait_for", s.waitFor)
	set("wait-for-timeout", "wait_for_timeout", s.waitForTimeout)
	set("block", "block_resources", s.block)
	set("headless", "headless", s.headless)
	set("autoparse", "autoparse", s.autoparse)
	set("links", "links", s.links)
	set("include", "include_tags", s.include)
	set("exclude", "exclude_tags", s.exclude)
	set("screenshot", "screenshot", s.screenshot)
	set("screenshot-full-page", "screenshot_fullpage", s.screenshotFull)
	set("screenshot-selector", "screenshot_selector", s.screenshotSelector)
	set("screenshot-format", "screenshot_format", s.screenshotFormat)
	set("screenshot-quality", "screenshot_quality", s.screenshotQuality)
	set("cache-ttl", "cache_ttl", s.cacheTTL)
	set("max-cost", "max_cost", s.maxCost)
	set("original-status", "original_status", s.originalStatus)

	if s.mainContent && s.noMainContent {
		return nil, Usagef("--main-content and --no-main-content are mutually exclusive")
	}
	if s.mainContent {
		body["main_content_only"] = true
	}
	if s.noMainContent {
		body["main_content_only"] = false
	}
	if s.noParsePDF {
		body["parse_pdf"] = false
	}
	if s.noCache {
		body["cache"] = false
	}
	if len(s.headers) > 0 {
		h := map[string]any{}
		for _, raw := range s.headers {
			name, val, ok := strings.Cut(raw, ":")
			if !ok || strings.TrimSpace(name) == "" {
				return nil, Usagef("--header %q: want 'Name: value'", raw)
			}
			h[strings.TrimSpace(name)] = strings.TrimSpace(val)
		}
		mergeField(body, "custom_headers", h)
	}
	if len(s.allowedStatus) > 0 {
		codes := make([]int, 0, len(s.allowedStatus))
		for _, c := range s.allowedStatus {
			n, err := strconv.Atoi(strings.TrimSpace(c))
			if err != nil {
				return nil, Usagef("--allowed-status %q: not a status code", c)
			}
			codes = append(codes, n)
		}
		body["allowed_status_codes"] = codes
	}
	for _, j := range []struct{ flag, field, val string }{
		{"extract", "extract", s.extract},
		{"actions", "actions", s.actions},
		{"network-capture", "network_capture", s.networkCapture},
	} {
		if j.val == "" {
			continue
		}
		var v any
		if err := decodeJSONArg("--"+j.flag, j.val, &v); err != nil {
			return nil, err
		}
		mergeField(body, j.field, v)
	}
	// ai_extract below replaces --body's: prompt and schema are alternatives.
	if s.aiPrompt != "" && s.aiSchema != "" {
		return nil, Usagef("--ai and --ai-schema are mutually exclusive: the API takes a prompt or a schema, not both")
	}
	if s.aiPrompt != "" {
		body["ai_extract"] = map[string]any{"prompt": s.aiPrompt}
	}
	if s.aiSchema != "" {
		var schema map[string]any
		if err := decodeJSONArg("--ai-schema", s.aiSchema, &schema); err != nil {
			return nil, err
		}
		body["ai_extract"] = map[string]any{"schema": schema}
	}
	return body, nil
}

// mergeField sets dst[k] = v. When both are JSON objects they are merged
// recursively, so a flag overrides leaf fields and keeps --body's siblings.
func mergeField(dst map[string]any, k string, v any) {
	d, dok := dst[k].(map[string]any)
	s, sok := v.(map[string]any)
	if !dok || !sok {
		dst[k] = v
		return
	}
	for k2, v2 := range s {
		mergeField(d, k2, v2)
	}
}

// decodeJSONArg parses a flag value that is inline JSON or @path (@- = stdin).
func decodeJSONArg(flag, val string, v any) error {
	b, err := readArg(val)
	if err != nil {
		return Usagef("%s: %v", flag, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return Usagef("%s: invalid JSON: %v", flag, err)
	}
	return nil
}

// readArg returns val's bytes, reading the file when val starts with @.
func readArg(val string) ([]byte, error) {
	if !strings.HasPrefix(val, "@") {
		return []byte(val), nil
	}
	path := val[1:]
	if path == "-" {
		return readAllStdin()
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return b, nil
}

func readAllStdin() ([]byte, error) {
	var sb strings.Builder
	buf := make([]byte, 64*1024)
	for {
		n, err := os.Stdin.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return []byte(sb.String()), nil
}
