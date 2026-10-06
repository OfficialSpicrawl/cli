---
name: spicrawl
description: Fetch, render and extract data from web pages with Spicrawl, through its MCP tools, the `spicrawl` CLI or the HTTP API. Use when an agent needs a page's content as markdown, HTML, text or JSON, has to render JavaScript, get past anti-bot protection, extract structured fields, scrape many URLs as a batch job, keep a logged-in session, check its request history or usage, or look up Spicrawl's own documentation.
---

# Spicrawl

Spicrawl turns any URL into clean content or structured data. One request, one URL,
one response; the platform picks the engine, the proxy exit and the retries.

| | |
|---|---|
| API base | `https://api.spicrawl.com` (all routes under `/v1`) |
| MCP server (streamable HTTP) | `https://mcp.spicrawl.com/mcp` |
| Docs | `https://docs.spicrawl.com` (index for agents: `https://docs.spicrawl.com/llms.txt`) |
| OpenAPI spec | `https://docs.spicrawl.com/openapi.yaml` |
| Auth | `Authorization: Bearer $SPICRAWL_API_KEY` (API and MCP) |

## Pick an interface

Use the first one available to you; they reach the same API and bill the same way.

1. **MCP tools** (`spicrawl_*`), if the Spicrawl MCP server is connected. No HTTP code to write.
2. **The `spicrawl` CLI (Spicrawl)**, if it is installed (`spicrawl version`). JSON output when piped, stable exit codes.
3. **HTTP** with `curl`, `requests` or `fetch`, using the reference below.

Never hard-code, print or log the key. Read it from `SPICRAWL_API_KEY`. Never put it in a URL.

## Authentication and scopes

Keys look like `spicrawl_live_…` (production) or `spicrawl_test_…` (sandbox; never spends live
credits).

| Scope | Grants |
|---|---|
| `scrape` | `POST /v1/scrape` |
| `batch` | all `/v1/batch` routes |
| `sessions` | all `/v1/sessions` routes |
| `browser` | `GET /v1/browser` (CDP WebSocket, coming soon). Not granted by default. |
| `read` | `/v1/usage`, `/v1/usage/summary`, `/v1/usage/reconciliation`; `GET /v1/requests?all_projects=true` and another project's `GET /v1/requests/{id}`. Not granted by default. |

`GET /v1/requests` needs no scope beyond a valid key for the key's own project.
Every project of the organization (`all_projects=true`, `spicrawl logs --all-projects`)
needs `read`. A missing scope returns `403 ERR::AUTH::INSUFFICIENT_SCOPE`, naming the scope.

## MCP tools

Connect a client to `https://mcp.spicrawl.com/mcp` with the header
`Authorization: Bearer $SPICRAWL_API_KEY` (or run `spicrawl mcp install`, below).

| Tool | Use it to |
|---|---|
| `spicrawl_scrape` | Fetch one URL. Arguments: `url`, `format` (`markdown` default, `text`, `html`, `json`), `render` (JavaScript), `proxy`, `mode`, `engine`, `wait_for`, `wait`, `main_content_only`, `include_tags`, `exclude_tags`, `links`, `autoparse`, `extract`, `ai_extract` (coming soon), `screenshot`, `actions`, `session_id`, `cache`, `max_cost` and the other `/v1/scrape` fields. Note `render`/`format` instead of the API's `js_render`/`response_format`. |
| `spicrawl_batch_submit` | Start an async job over many URLs (`urls` or `items`, plus shared settings such as `render`, `format`, `wait_for`). |
| `spicrawl_batch_status` / `spicrawl_batch_list` | Poll one job / list jobs. |
| `spicrawl_batch_results` | Read finished items (paged with `cursor`). |
| `spicrawl_batch_task_content` | Get one item's full document by `job_id` and `seq`. |
| `spicrawl_batch_add_items` / `spicrawl_batch_close` | Append to an `open` job / mark it complete. |
| `spicrawl_batch_retry` / `spicrawl_batch_cancel` | Retry failed items / cancel a job (cannot be undone). |
| `spicrawl_session_create` / `_list` / `_get` / `_context` / `_release` / `_delete` | Manage sessions (cookies, storage, engine). |
| `spicrawl_browser_connect_url` | Coming soon: remote browser for Puppeteer/Playwright. Not available during the beta; do not use. |
| `spicrawl_requests_list` / `spicrawl_request_get` | Request history; look up a failure by request id. |
| `spicrawl_usage` / `spicrawl_usage_summary` / `spicrawl_usage_reconciliation` | Credits and volume. Need the `read` scope. |
| `spicrawl_docs_search` | Search Spicrawl's docs (`query`). Search an error code such as `ERR::PROXY::EXHAUSTED` to get its entry. |
| `spicrawl_docs_read` | Read a docs page as Markdown (`path`, e.g. `guides/anti-bot`). |
| `spicrawl_docs_index` | The docs index (`llms.txt`). |

Before guessing a parameter, search the docs with `spicrawl_docs_search`.

## CLI

Set up once: `spicrawl login` (or `export SPICRAWL_API_KEY=…`). For a self-hosted deployment,
also pass `--base-url` or set `SPICRAWL_BASE_URL`; the MCP, docs and skill URLs are derived from it.

~~~bash
spicrawl scrape https://example.com --format markdown                   # one page
spicrawl scrape https://example.com/app --render --wait-for '.content'  # JavaScript page
spicrawl scrape https://example.de --proxy "$MY_PROXY_URL"               # through your own proxy
spicrawl scrape https://example.com/p/1 --extract '{"title":"h1","price":".price"}'
spicrawl scrape - --format markdown --concurrency 8 < urls.txt          # many URLs, one JSON line each
spicrawl batch submit urls.txt --format markdown --wait                 # async job, wait for it
spicrawl batch results <id> --all -o results.jsonl
spicrawl sessions create --ttl 1800 && spicrawl scrape URL --session <id>
spicrawl logs --errors                                                  # recent failures
spicrawl logs --all-projects                                            # every project in the org (read scope)
spicrawl usage                                                          # needs the read scope
spicrawl docs guides/anti-bot                                           # a docs page as Markdown
spicrawl schema scrape                                                  # JSON Schema of a request body
spicrawl init                                                           # wire MCP + this skill into Claude Code, Cursor, VS Code, Codex
~~~

The contract agents rely on:

- **stdout is data only.** Progress, warnings and errors go to stderr.
- **JSON when stdout is not a terminal** (or with `--json`). `spicrawl docs` prints Markdown unless `--json`.
- **Never prompts without a terminal.** Destructive commands take `--yes` (`batch cancel`, `sessions delete`).
- **A failed target site is a failure.** If the site answers non-2xx, the CLI still prints the page but exits 6 and adds `"target_error"` to the JSON. `--allowed-status 403` accepts a status.
- **Errors are the API's problem document**, written to stderr in JSON mode.

| Exit code | Meaning |
|---|---|
| 0 | Success |
| 1 | CLI bug, or a transient `ERR::INTERNAL::*` (retryable) |
| 2 | Bad flags, arguments or config file; nothing was sent |
| 3 | `ERR::AUTH::*`, or no API key configured |
| 4 | `ERR::REQUEST::*`, `ERR::SECURITY::*`: fix the request |
| 5 | `ERR::LIMIT::*`: rate, concurrency, quota or `max_cost` |
| 6 | The target site failed, served a bot challenge, or answered non-2xx |
| 7 | `ERR::PROXY::*` |
| 8 | `ERR::ENGINE::*`, `ERR::EXTRACT::FAILED`, or a feature not available on this deployment |
| 9 | `ERR::SESSION::*` |
| 10 | The API could not be reached; nothing was sent |
| 11 | A `--wait` gave up before the job finished |
| 12 | Timed out after sending: the request may have run and been billed. Check `spicrawl logs` before retrying |

## Scrape one page: `POST /v1/scrape`

~~~bash
curl -sS -X POST https://api.spicrawl.com/v1/scrape \
  -H "Authorization: Bearer $SPICRAWL_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"url": "https://example.com", "response_format": "markdown"}'
~~~

~~~python
import os, requests
r = requests.post(
    "https://api.spicrawl.com/v1/scrape",
    headers={"Authorization": f"Bearer {os.environ['SPICRAWL_API_KEY']}"},
    json={"url": "https://example.com", "response_format": "markdown"},
    timeout=120,
)
r.raise_for_status()
print(r.headers.get("X-Target-Status"), r.headers.get("X-Credits-Charged"), r.text[:500])
~~~

Unknown fields are rejected with `400`: send only the fields below.

| Field | Type | Use it when |
|---|---|---|
| `url` | string | **Required.** The page to fetch. |
| `response_format` | string | `markdown` (best for LLMs), `html`, `text`, `json` (envelope with metadata), `pdf`. |
| `js_render` | bool | The content is built by JavaScript (SPA, empty HTML shell). |
| `impersonate` | bool | Fetch tier only: present a current Chrome TLS/HTTP2 fingerprint. On by default and free; send `false` only to turn it off. |
| `mode` | string | `auto` escalates fetch → obscura until one succeeds, billing only that rung. Not with `js_render` or `engine`. |
| `engine` | string | Pin `fetch` / `obscura` / `chromium`. Not with `mode: auto`; `js_render` cannot pin `fetch`. |
| `wait_for` / `wait_for_timeout` | string / int ms | CSS selector to wait for before capturing, and its cap. |
| `wait` | int ms | Flat delay after load. |
| `main_content_only` | bool | Strip nav, footer and aside (default on for markdown). |
| `include_tags` / `exclude_tags` | string[] | CSS selectors to keep / drop. |
| `links` | bool | Also return the page's absolute, de-duplicated links (JSON envelope). |
| `autoparse` | bool | Return the page's JSON-LD, OpenGraph, microdata and embedded app state. No selectors needed. |
| `extract` | object | Selector map, or a JSON Schema whose properties carry `selector`. |
| `ai_extract` | object | `{"prompt": …}` **or** `{"schema": …}` (both is a 400). Coming soon: model-driven, +4 credits; refused with 503 `ERR::INTERNAL::UNAVAILABLE` until it launches. |
| `network_capture` | object | Record the JSON the page itself fetched (XHR/fetch). Needs a render. |
| `screenshot` | bool | Capture a screenshot (`screenshot_fullpage`, `screenshot_selector`, `screenshot_format`). Screenshots and `pdf` need a browser engine (`chromium`), not `mode: auto`. |
| `actions` | object[] | Browser workflow, at most 50 steps: `click`, `fill`, `wait_for`, `scroll`, `select`, `evaluate`, `screenshot`. |
| `block_resources` | string[] | `images` `fonts` `media` `stylesheets` `scripts`, or `none`. |
| `session_id` | string | Reuse a session's cookies and storage (see Sessions). Combine with `js_render` when you also send `actions` or `wait_for`. |
| `cache` / `cache_ttl` | bool / int s | Cache is **on** (48h max). A hit is billed like the fetch that stored it: the cache saves time, not credits. Send `cache: false` for prices, stock or anything time-sensitive. |
| `max_cost` | int | Refuse up front if the request would cost more credits than this. |
| `proxy` / `proxy_verify` | string / bool | Your own proxy URL (+0 credits), optionally checked first. Use it when a site blocks our IPs or you need a specific country. |
| `custom_headers` / `method` | object / string | Headers sent to the target (the field is `custom_headers`; `headers` is rejected). Default method `GET`. |
| `allowed_status_codes` | int[] | Treat these target statuses as billable successes. |

Coming soon, so do not send these yet: `ai_extract`, stealth mode (`stealth`, `engine: "camoufox"`), extraction presets (`extract_preset`), Spicrawl's managed proxy pool (`premium_proxy`, `proxy_country`, `sticky_key`, session `rotate_ip`/`region_pool`) and the remote browser (`/v1/browser`).

### Structured extraction

~~~json
{
  "url": "https://example.com/product/1",
  "response_format": "json",
  "autoparse": true,
  "extract": {
    "type": "object",
    "properties": {
      "title": {"type": "string", "selector": "h1"},
      "price": {"type": "number", "selector": ".price"}
    },
    "required": ["title"]
  }
}
~~~

The schema form coerces (`"$1,234.56"` → `1234.56`) and validates. Every property needs a
`selector`. Results land in `data` in the JSON envelope; fields that matched nothing are listed
in `empty_fields`. Extraction costs no extra credits, except `ai_extract`.

### Browser actions

~~~json
{
  "url": "https://example.com/login",
  "engine": "chromium",
  "actions": [
    {"fill": {"selector": "#user", "value": "alice"}},
    {"fill": {"selector": "#pass", "value": "…", "secret": true}},
    {"click": {"selector": "button[type=submit]"}},
    {"wait_for": {"selector": ".dashboard"}},
    {"scroll": {"to_bottom": true}}
  ]
}
~~~

Every step accepts `label`, `timeout_ms` and `on_error` (`fail` or `skip`). `secret: true` on
`fill` redacts the value from logs.

### Response

- `response_format: "json"`, or any extraction, links or screenshot, returns an envelope:
  `{url, final_url, status, content, headers, truncated, credits, engine, proxy_source, warnings, data?, links?, empty_fields?}`.
- Any other format returns **the document itself** as the body; metadata is in headers.
- **Two statuses.** The HTTP status is the platform's. The site's status is `X-Target-Status`
  (envelope `status`). A 403 or 503 from the site arrives inside a `200`: always check it.

| Header | Meaning |
|---|---|
| `X-Request-Id` | ULID of the call. Log it; look it up with `GET /v1/requests/{id}`. |
| `X-Target-Status` | The site's HTTP status. |
| `X-Credits-Charged` | Credits actually billed (`0` on any failure). |
| `X-Request-Cost` | What it would have cost. |
| `X-Credits-Remaining` | Monthly allowance left after this call (absent if the org has no limit). |
| `X-Engine` | Engine that served it. |
| `X-Final-Url` | URL after redirects. |
| `Cache-State` | `hit` (billed like the fetch that stored it), `miss` or `bypass`. |
| `X-Warning` | Non-fatal decisions (a clamped `cache_ttl`, an ignored flag). |
| `X-RateLimit-Limit` / `-Remaining` / `-Reset` | Rate limit state. |

## Many URLs: `POST /v1/batch`

Asynchronous; returns `202` with a job. Job-wide settings at the top level, per-item overrides
on each item. Give `urls` (a list) **or** `items` (objects with `url`, optional `external_id`
and overrides), up to 10,000 per job.

~~~bash
curl -sS -X POST https://api.spicrawl.com/v1/batch \
  -H "Authorization: Bearer $SPICRAWL_API_KEY" -H 'Content-Type: application/json' \
  -d '{"items": [{"url": "https://example.com/1"}, {"url": "https://example.com/2", "external_id": "sku-42"}],
       "response_format": "markdown", "js_render": true}'
~~~

Each item is one fetch or render, returned as `html`, `markdown` or `text`. Batch accepts
the retrieval and cleaning fields (`response_format`, `js_render`, `impersonate`,
`engine` of `fetch`/`obscura`, `proxy`,
`proxy_verify`, `wait`, `wait_for`, `wait_for_timeout`, `block_resources`,
`headless`, `custom_headers`, `main_content_only`, `include_tags`, `exclude_tags`, `max_cost`).
It refuses with `400` (nothing queued, nothing charged): `extract`, `ai_extract`, `autoparse`,
`actions`, `screenshot`, `network_capture`, `session_id`, `cache`, `links`, `mode: auto`,
`response_format: pdf`, and `engine: chromium` (use `js_render: true`). Use `/v1/scrape` for those.

1. Poll `GET /v1/batch/{id}` while `status` is `queued`, `running`, `paused` or `cancelling`
   (terminal: `completed`, `failed`, `cancelled`). Back off between polls.
2. Read `GET /v1/batch/{id}/results?limit=100&cursor=…`. It is **JSONL** (one object per line);
   the next cursor is in the `X-Next-Cursor` header. Each line has `seq`, `url`, `status`,
   `http_status` and, once the item is done, `result` (`content`, `bytes`, `truncated`) or
   `error` (`code`, `retryable`). `result.content` is the stored value as JSON text: decode it
   once. For one item's full document, use `GET /v1/batch/{id}/tasks/{seq}/content`.
3. Results expire after **72 hours** (`410 Gone`).
4. Other routes: `POST /v1/batch/{id}/cancel`, `/retry` (failed items), `/items` (append to an
   `open: true` job), `/close` (finish an open job).

## Sessions: `/v1/sessions`

Keep cookies and storage across requests (log in once, then scrape).

| Route | Purpose |
|---|---|
| `POST /v1/sessions` | Create (`engine`, `ttl_seconds`, `session_context`). |
| `GET /v1/sessions` / `GET /v1/sessions/{id}` | List / fetch. |
| `GET /v1/sessions/{id}/context` | Captured cookies and storage. |
| `POST /v1/sessions/{id}/release` | End it: permanently purges its cookies and storage (`409 ERR::SESSION::BUSY` while in use; `?force=true` overrides). |
| `DELETE /v1/sessions/{id}` | Destroy. |

Pass `session_id` on `/v1/scrape`. Sessions are single-writer: run requests on one session one at
a time. A concurrent request gets `ERR::SESSION::BUSY` (retryable).

## Live browser: `GET /v1/browser` (CDP)

Coming soon: connect Puppeteer or Playwright to a Spicrawl-hosted browser. Not available during the beta.
When it is: 8 credits per started minute, and the session's whole `session_ttl` is held against the
allowance while it is open (24 credits for the default 3 minutes); unused minutes come back at close.

## Spicrawl's docs, for agents

- `https://docs.spicrawl.com/llms.txt`: every page with a one-line description and its Markdown URL.
- `https://docs.spicrawl.com/llms-full.txt`: all pages in one file.
- Any page as Markdown: append `.md` (for example `https://docs.spicrawl.com/guides/anti-bot.md`),
  or send `Accept: text/markdown`.
- Error codes: `https://docs.spicrawl.com/errors#<FAMILY>_<NAME>` (the code without `ERR::`, `::` replaced by `_`, e.g. `#AUTH_MISSING_KEY`).
- MCP: `spicrawl_docs_search`, `spicrawl_docs_read`, `spicrawl_docs_index`. CLI: `spicrawl docs <page>`.

## Credits

| Engine | Credits |
|---|---|
| `fetch` | 1 |
| `obscura` | 3 |
| `chromium` | 8 |

You pay for the engine that actually served (`X-Engine`). `ai_extract` adds +4. **Failures cost
0.** A cache hit is billed at the same price as the fetch that stored it. The cache saves the
fetch (latency and load on the target), not credits. A deployment may discount these prices:
`X-Credits-Charged` and `X-Request-Cost` are authoritative.

Each organization gets a monthly allowance (1,000 credits by default). The month starts on the
day the organization was created and resets at 00:00 UTC on that day. What is left, and when it
resets: `allowance.remaining_micro` and `allowance.resets_at` in `GET /v1/usage/summary`
(`spicrawl_usage_summary`, `spicrawl usage summary`); 1 credit = 1,000,000 micro. It counts
credits held for work still running (a batch job's unfinished items), so it can dip and recover.

## Errors and retries

Errors are RFC 7807 JSON (`application/problem+json`):
`{type, title, status, code, detail, retryable, doc_url, request_id, target_status, retry_after_seconds?, diagnostics?}`.
`target_status` is `null` when the failure did not come from the site.

- **Switch on `code`, and trust `retryable`** rather than the HTTP status. Honour
  `retry_after_seconds` / `Retry-After`.
- **Read `diagnostics.hint`** when present: it names the parameter to change.
- Retryable, with backoff: `ERR::UPSTREAM::CHALLENGE`, `ERR::UPSTREAM::TIMEOUT`,
  `ERR::PROXY::RATE_LIMITED`, `ERR::LIMIT::RATE_LIMITED`,
  `ERR::LIMIT::CONCURRENCY_EXCEEDED`, `ERR::SESSION::BUSY`, `ERR::ENGINE::UNAVAILABLE`
  (except a `501`, which is permanent).
- Not retryable: `ERR::REQUEST::*`, `ERR::AUTH::*`, `ERR::SECURITY::*` (private or blocked
  target), and a failing custom `proxy` (`ERR::PROXY::UNREACHABLE`, `ERR::PROXY::AUTH_FAILED`).
- `ERR::UPSTREAM::CHALLENGE` means an anti-bot page (Cloudflare, DataDome, Akamai…). Retry
  once, then `js_render: true`, then the user's own `proxy` if they have one.
- `ERR::LIMIT::QUOTA_EXCEEDED` (HTTP 402) means the organization's monthly allowance cannot cover
  this request. Nothing was charged. Waiting does not help until the reset: stop and tell the user,
  including the reset date (in the `detail`, or `allowance.resets_at` in the usage summary). A
  cheaper request (a lower engine, fewer batch URLs) may still fit.
- `ERR::INTERNAL::UNAVAILABLE` with `retryable: false` means the feature (for example
  `ai_extract`) is not configured on this deployment: don't retry.
- The API is **not idempotent**: a retried scrape is billed again. Only retry a failure, and
  after a timeout check `GET /v1/requests` first.

## Playbook

1. Start cheap: `{"url": …, "response_format": "markdown"}` (1 credit).
2. Check the site's status (`X-Target-Status`), not just the HTTP status.
3. Empty or skeleton content → `js_render: true` (or `mode: "auto"`).
4. Blocked or challenged → retry, then the user's own `proxy`; still blocked → stop and report.
5. Need fields, not prose → `autoparse: true` first, then an `extract` schema (`ai_extract` is coming soon).
6. More than about 20 URLs → a batch job (or `spicrawl scrape -` with `--concurrency`), not a loop.
7. Cap spend with `max_cost`; track it with `X-Credits-Charged`.
8. Log `X-Request-Id`. To debug a failure, read `GET /v1/requests/{id}` (or `spicrawl logs get <id>`).
9. Unsure about a parameter or error → search the docs (`spicrawl_docs_search`, `spicrawl docs`, or `llms.txt`).
