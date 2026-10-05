# Spicrawl CLI (`spicrawl`) — design

The command-line client for the Spicrawl web data API, installed as `spicrawl`. It has two readers: a person at a
terminal and an AI agent driving a shell. Where they conflict, the agent wins,
because a person can read JSON and an agent cannot read a spinner.

The wire contract is `openapi.yaml` (at the repo root). Every flag maps to a field there;
never invent a field the API does not accept (the API rejects unknown fields).

## Contract agents rely on

1. **stdout is data only.** Progress, warnings and errors go to stderr.
2. **JSON when not a terminal.** `--json` forces it. Human mode prints tables
   or the document itself.
3. **Never prompts without a TTY.** Anything interactive has a flag form.
   Destructive commands take `--yes`.
4. **Stable exit codes** (`spicrawl exit-codes`, `internal/exitcode`; with
   `--json` it prints `[{code, name, meaning}]`). Never renumber, only add.

   | Code | Name | Meaning |
   | ---- | ---- | ------- |
   | 0 | ok | success |
   | 1 | internal | CLI bug, or a transient `ERR::INTERNAL::*` (retryable) |
   | 2 | usage | bad flags or arguments, or an unreadable/malformed config file (the message names it); nothing was sent |
   | 3 | auth | `ERR::AUTH::*`, or no API key configured |
   | 4 | request | `ERR::REQUEST::*`, `ERR::SECURITY::*`, `ERR::EXTRACT::INVALID_RULES` |
   | 5 | limit | `ERR::LIMIT::*` |
   | 6 | upstream | `ERR::UPSTREAM::*`, or a target status outside `--allowed-status` |
   | 7 | proxy | `ERR::PROXY::*` |
   | 8 | engine | `ERR::ENGINE::*`, `ERR::EXTRACT::FAILED`, and `ERR::INTERNAL::UNAVAILABLE` with `retryable: false` (a capability such as `ai_extract` is not available on this deployment) |
   | 9 | session | `ERR::SESSION::*` |
   | 10 | network | the API could not be reached (DNS, refused, TLS, or a timeout before the request was written); nothing was sent, safe to retry |
   | 11 | pending | a `--wait` gave up before the job finished |
   | 12 | timeout | the request was sent but `--timeout` expired (awaiting headers or reading the body); it may have run and been billed, so check `spicrawl logs` before retrying |

   `internal` is reserved for things only a CLI fix or a transient server
   blip explains. A permanent server-side refusal is not one of them. The
   client (`api.Client.Do`) tells 10 from 12 with an `httptrace` hook: a
   timeout after the request headers were written is `*api.TimeoutError`,
   anything earlier is `*api.NetworkError`. `DoWithRetry` retries only
   problems the server marked `retryable` and never a timeout, because a
   retried POST could be billed twice. Commands that report per-item errors
   use `cmd.ExitCode(err)` to stay in step with the process exit code.
5. **Errors are the API's problem document.** In JSON mode a failure writes
   the RFC 7807 body to stderr unchanged; in human mode `error:`, `hint:`,
   `target status:`, `retryable:` and `request id:` lines.
6. **`-` reads stdin.** Commands that take URLs accept `-` for one per line.
7. **Streams are JSONL.** Anything that yields many records supports
   `--jsonl` (one compact JSON object per line) so output can be piped.
8. **Target status is a failure.** A scrape whose target answered non-2xx
   (and not in `--allowed-status`) exits 6; the document is still written to
   stdout, JSON output carries `target_error`, and stderr gets one line.
9. **Secrets are printed once.** `browser url` writes the URL (which carries
   the key) once on stdout; its connection hints on stderr use
   `$SPICRAWL_BROWSER_URL` and are suppressed by `-q`. A `ws://` URL warns on
   stderr, always, that the key travels unencrypted.

## Configuration

Key and base URL resolve flag > env (`SPICRAWL_API_KEY`, `SPICRAWL_BASE_URL`) >
config file (`spicrawl config path`: `~/.config/spicrawl/config.json` on Linux, `~/Library/Application Support/spicrawl/config.json` on macOS; mode 0600; override with
`SPICRAWL_CONFIG`) > default `https://api.spicrawl.com`.
Every other URL derives from the resolved base URL, so a self-hosted
deployment (e.g. `--base-url https://spicrawl.example.com`) needs no further settings:

| URL | Precedence |
|---|---|
| Docs (`spicrawl docs`) | `$SPICRAWL_DOCS_URL` > `$SPICRAWL_DOCS_HOST/docs` (legacy) > `<base URL origin>/docs` (`https://docs.spicrawl.com` when the base URL is the default `https://api.spicrawl.com`) |
| MCP server (`init`, `mcp install`) | `--mcp-url` > `$SPICRAWL_MCP_URL` > `<base URL origin>/mcp` (`https://mcp.spicrawl.com/mcp` when the base URL is the default `https://api.spicrawl.com`) |
| Agent skill (`init`, `skill install`) | `--skill-url` > `$SPICRAWL_SKILL_URL` > `<docs URL>/skill.md` |

`$SPICRAWL_DOCS_URL` is the docs base URL, `http(s)://host[:port][/path]`, and
pages are appended to it directly: the hosted docs are at the root of
`https://docs.spicrawl.com`, a self-hosted API serves its docs build at its own
`/docs`. `$SPICRAWL_DOCS_HOST`, an origin, keeps its old meaning (the docs at its
`/docs`) and is read only when `$SPICRAWL_DOCS_URL` is unset. The installed
skill's `api.spicrawl.com` / `mcp.spicrawl.com` / `docs.spicrawl.com` links
(and the older `/docs` forms of the docs) are rewritten to the resolved URLs.

## Code layout

```
cmd/spicrawl/main.go  entry point (go install github.com/OfficialSpicrawl/cli/cmd/spicrawl@latest)
internal/api        HTTP client, Problem, DoWithRetry
internal/config     key/base resolution, config file
internal/exitcode   exit codes
internal/output     Printer (JSON/human/JSONL, stderr info)
internal/cmd        one file per command group; each registers in init()
```

Commands use `cmd.Client()`, `cmd.Printer()`, `cmd.Usagef()` and return
errors; `root.go` turns errors into output and exit codes. Never call
`os.Exit` from a command. Use `cmd.Context()` for cancellation.

## Command surface

```
spicrawl login [--api-key KEY] [--base-url URL]   save a key (validates it first)
spicrawl logout                                   remove the saved key
spicrawl auth status                              which key/base URL is in use and from where
spicrawl config get|set|path [key] [value]        base_url, api_key

spicrawl scrape <url|-> [flags]                   POST /v1/scrape
  --format html|markdown|text|json|pdf          response_format
  --render                                      js_render
  --stealth  --impersonate
  --engine fetch|obscura|chromium|camoufox  --mode auto
  --premium-proxy  --country CC  --proxy URL  --proxy-verify  --sticky-key K
  --session ID
  --wait MS  --wait-for SEL  --wait-for-timeout MS
  --block images,fonts,...  --headless=false
  --header 'Name: value' (repeatable)            custom_headers
  --extract JSON|@file  --ai PROMPT  --ai-schema JSON|@file  --autoparse  --links
  --main-content/--no-main-content  --include SEL  --exclude SEL  --no-parse-pdf
  --actions JSON|@file  --network-capture JSON|@file
  --screenshot  --screenshot-full-page  --screenshot-selector SEL  --screenshot-format F
  --no-cache  --cache-ttl S  --max-cost N  --allowed-status 403  --original-status
  --body JSON|@file     raw request body; flags override its fields
  -o, --output FILE     write the document (or screenshot) to a file
  --jsonl               one compact JSON line per result (single URL or `-`)
  --concurrency N       with `-`: parallel requests (default 4)
  --retry N             retry retryable errors (default 1 = no retry)
  --meta                human mode: print engine/credits/cache/target status to stderr

spicrawl batch submit <file|-> [batch-accepted scrape flags] [--render-wait MS] [--name N]
                     [--wait] [--poll 5s] [--max-wait 30m]
                                                --render-wait maps to the item's `wait`
spicrawl batch list [--status S] [--limit N]
spicrawl batch get <id>
spicrawl batch results <id> [--status S] [--all] [-o FILE]   JSONL
spicrawl batch content <id> <seq> [-o FILE]                   one item's document
spicrawl batch wait <id> [--max-wait 30m]                     exit 11 when it gives up
spicrawl batch cancel <id> [--yes]                           --yes required without a TTY
spicrawl batch retry|close <id>
spicrawl batch append <id> <file|->

spicrawl sessions create [--engine E] [--ttl S] [--country CC] [--rotate-ip] [--sticky-key K]
spicrawl sessions list | get <id> | context <id> | release <id> | delete <id> [--yes]

spicrawl browser url [--engine chromium|obscura] [--country CC] [--ttl S] [--headless=false]
                                                prints the wss:// CDP URL once (stdout)

spicrawl logs [--errors] [--status S] [--limit N] [--all] [--all-projects]
                                                GET /v1/requests (--all-projects: all_projects=true, read scope)
spicrawl logs get <request-id>
spicrawl usage [--from D] [--to D] [--group-by day]           GET /v1/usage
spicrawl usage summary | reconciliation
spicrawl status                                    /readyz + workers + key check

spicrawl init [--client claude|cursor|codex|vscode|all] [--yes]
                                                detect agent tooling, write MCP config + skill
spicrawl mcp install [--client ...] [--print]      add the hosted MCP server to a client config
spicrawl skill install [--client ...] [--print]    write the Spicrawl agent skill (SKILL.md)
spicrawl docs [topic] [--list]                     print raw Markdown (from docs.spicrawl.com); JSON only with --json
spicrawl schema [scrape|batch|session]             print the JSON Schema of a request body
spicrawl exit-codes
spicrawl version
```
