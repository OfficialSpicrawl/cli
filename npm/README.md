# Spicrawl CLI: Scrape Websites from Your Terminal

The `spicrawl` command-line tool scrapes any URL to clean Markdown, HTML or JSON and batch-crawls lists of URLs from your terminal, using the Spicrawl web scraping API. Pipe its output into scripts, LLM prompts and AI coding agents like Claude Code and Cursor.

[![npm version](https://img.shields.io/npm/v/@spicrawl/cli.svg)](https://www.npmjs.com/package/@spicrawl/cli)
[![platforms](https://img.shields.io/badge/platforms-linux%20%7C%20macOS%20%7C%20windows-blue.svg)](https://docs.spicrawl.com/cli/install)
[![arch](https://img.shields.io/badge/arch-x64%20%7C%20arm64-blue.svg)](https://docs.spicrawl.com/cli/install)
[![npm downloads](https://img.shields.io/npm/dm/@spicrawl/cli.svg)](https://www.npmjs.com/package/@spicrawl/cli)
[![docs](https://img.shields.io/badge/docs-docs.spicrawl.com-blue.svg)](https://docs.spicrawl.com)
[![node](https://img.shields.io/node/v/@spicrawl/cli.svg)](https://www.npmjs.com/package/@spicrawl/cli)

Documentation: [docs.spicrawl.com/cli/overview](https://docs.spicrawl.com/cli/overview)

---

## What is the Spicrawl CLI?

The Spicrawl CLI is a single native binary, `spicrawl`, that calls the Spicrawl web scraping API from the command line. `spicrawl scrape` fetches one page as Markdown, HTML, text, JSON or extracted data; `spicrawl batch` runs a list of URLs as one server-side job; `spicrawl init` connects AI agents over MCP. Output is JSON whenever it is piped, and exit codes are stable, so it works the same for a person at a terminal, a shell script, a CI job or an AI agent.

## Install

```sh
# Install globally; the command is `spicrawl`
npm install -g @spicrawl/cli

# Or run it once without installing
npx @spicrawl/cli scrape https://example.com --format markdown
```

- Requires Node.js 16 or later, only for the small launcher script.
- One self-contained, cross-platform package: it bundles prebuilt native binaries for
  Linux, macOS and Windows (x64 and arm64), and the launcher runs the one for your machine.
  No platform-specific sub-packages, no optional dependencies, no postinstall script,
  no download at install time and no Go toolchain needed.
- Other install options (a shell installer, release archives, `go install`) are in the
  [install guide](https://docs.spicrawl.com/cli/install).

Check the install:

```sh
spicrawl version
```

## Quickstart

```sh
# 1. Save your API key (validated before it is saved)
spicrawl login

# 2. Scrape a web page to Markdown
spicrawl scrape https://example.com --format markdown

# 3. Save the result to a file
spicrawl scrape https://example.com --format markdown -o example.md
```

Confirm which key is in use and that the API accepts it:

```sh
spicrawl auth status
```

## What you can do with Spicrawl CLI

- **Convert any web page to Markdown** for LLM context windows, RAG pipelines and notes (`--format markdown`).
- **Scrape HTML, plain text, JSON or PDF output** from any URL with one command.
- **Render JavaScript-heavy pages** in a headless browser and wait for a CSS selector (`--render`, `--wait-for`).
- **Extract structured data** with CSS selectors, a JSON Schema, or a plain-language AI prompt (`--extract`, `--ai`, `--ai-schema`).
- **Read a page's own metadata**: JSON-LD, OpenGraph and embedded state (`--autoparse`), plus all links (`--links`).
- **Take screenshots** of a full page or a single element (`--screenshot`).
- **Batch scrape a list of URLs** as one asynchronous job, wait for it, and download results as JSON Lines (`spicrawl batch`).
- **Scrape many URLs in parallel from stdin** with `spicrawl scrape -`.
- **Reuse cookies and logins** across requests with persisted sessions (`spicrawl sessions`).
- **Use your own proxy** for any request (`--proxy`).
- **Inspect request logs and usage** from the terminal (`spicrawl logs`, `spicrawl usage`).
- **Connect AI coding agents** over MCP and install an agent skill (`spicrawl init`).
- **Script it safely**: JSON output when piped, stable exit codes, env-var configuration.

## Scrape a web page to Markdown

```sh
spicrawl scrape https://example.com/blog/launch --format markdown
```

Markdown keeps only the main content by default. Use `--no-main-content` for the
whole document, or narrow it with CSS selectors:

```sh
spicrawl scrape https://example.com/docs --format markdown \
  --include 'article' --exclude 'nav' --exclude '.cookie-banner'
```

Other formats: `--format html`, `text`, `json` or `pdf` (PDF needs `-o FILE`).

For pages that build their content with JavaScript, render them first:

```sh
spicrawl scrape https://example.com/pricing --render --wait-for '.plans' --format markdown
```

Add `--meta` to print the engine, credits, cache state, target status and request id to stderr.

## Output formats and piping

Output switches to JSON automatically whenever stdout is not a terminal, so
piping just works. Force it on a terminal with `--json`, or write one compact
line per result with `--jsonl`:

```sh
spicrawl scrape https://example.com --json | jq -r .content
spicrawl scrape https://example.com --format markdown --jsonl >> pages.jsonl
```

Pick the document format with `--format html|markdown|text|json|pdf`. In JSON mode the
document is under `content`, next to `url`, `final_url`, `status` and `request_id`:

```sh
spicrawl scrape https://example.com --format markdown --json | jq -r .content > example.md
spicrawl scrape https://example.com --links --json | jq '.links'
```

Scrape many URLs from a file, several at a time, with one JSON line per URL:

```sh
spicrawl scrape - --format markdown --concurrency 8 < urls.txt > pages.jsonl
```

Data goes to stdout; progress and errors go to stderr (silence progress with `-q`).

## Extract structured data from a web page

With a CSS selector map:

```sh
spicrawl scrape https://example.com/product/1 --extract '{"title":"h1","price":".price"}'
```

With a plain-language prompt (AI extraction):

```sh
spicrawl scrape https://news.example.com --ai 'the ten headlines with their links'
```

With a JSON Schema from a file:

```sh
spicrawl scrape https://example.com/product/1 --ai-schema @product.schema.json
```

Or read the structured data the page already publishes (JSON-LD, OpenGraph, embedded state):

```sh
spicrawl scrape https://example.com/product/1 --autoparse
```

When extraction is set, only the extracted data is printed.

## Take a screenshot of a web page

```sh
spicrawl scrape https://example.com --screenshot --screenshot-full-page -o example.html
```

Screenshots are saved next to the `-o` file, or into a directory:

```sh
spicrawl scrape https://example.com --screenshot --screenshot-format png \
  --screenshot-selector '#hero' --screenshot-dir ./shots -o example.html
```

## Batch scrape a list of URLs and wait for the results

`spicrawl batch` submits a file of URLs (one per line, or `.json` / `.jsonl` items)
as one server-side job:

```sh
# Submit, render, and wait until the job finishes (up to 20 minutes)
spicrawl batch submit urls.txt --render --format markdown --name nightly --wait --max-wait 20m

# Download every finished item as JSON Lines
spicrawl batch results <job-id> --all -o results.jsonl
```

Useful follow-ups:

```sh
spicrawl batch list                        # jobs, newest first
spicrawl batch get <job-id>                # status and progress
spicrawl batch wait <job-id> --max-wait 10m
spicrawl batch results <job-id> --status failed --all
spicrawl batch retry <job-id>              # re-run failed items
spicrawl batch cancel <job-id>
```

Capture the job id in a script:

```sh
id=$(spicrawl batch submit urls.txt --json | jq -r .id)
```

Results stay readable until the job's `results_expire_at` (72 hours after submission by default).

## Reuse cookies with sessions

A session keeps one engine and one cookie jar across many scrapes, so a login
or a cart survives between requests:

```sh
SID=$(spicrawl sessions create --engine chromium --ttl 7200 --json | jq -r .id)
spicrawl scrape https://example.com/account --session "$SID" --format markdown
spicrawl sessions release "$SID"
```

Save a session's cookies and storage, and seed a new session from them:

```sh
spicrawl sessions context "$SID" --json > login.json
spicrawl sessions create --engine chromium --session-context login.json
```

The context output is a credential: treat it like a password.

## Inspect request logs and usage

```sh
spicrawl logs                            # recent requests, newest first
spicrawl logs --errors --limit 20        # only the ones that did not succeed
spicrawl logs get <request-id>           # one request in full

spicrawl usage                           # last 30 days, by day
spicrawl usage --group-by feature
spicrawl usage summary                   # current period against the plan allowance
```

`spicrawl status` checks the whole path to a working API: reachability, readiness and your key.

## Connect AI agents over MCP

One command sets up Claude Code, Cursor, VS Code and Codex in the current project:

```sh
spicrawl init
```

It detects the clients you use, shows the files it would change, and after
confirmation adds the hosted MCP server (named `spicrawl`) and installs the agent
skill. In a non-interactive shell pass `--yes`:

```sh
spicrawl init --client claude,cursor --yes
spicrawl init --client all --global --yes
```

The two steps are also available separately:

```sh
spicrawl mcp install --client claude          # MCP server entry only
spicrawl mcp install --client cursor --print  # print the snippet instead of writing it
spicrawl skill install --client all           # SKILL.md only
```

By default the API key is referenced from `$SPICRAWL_API_KEY` rather than copied into
client config files, where the client supports it. More in the
[MCP guide](https://docs.spicrawl.com/agents/mcp).

## Use Spicrawl CLI in CI and scripts

- **Non-interactive auth**: set `SPICRAWL_API_KEY` in the environment; no login step needed.
  `spicrawl login` never waits for input without a terminal.
- **Machine-readable output**: JSON whenever stdout is not a terminal, or `--json` / `--jsonl`.
- **Stable exit codes** you can branch on. List them with `spicrawl exit-codes`:

| Code | Meaning |
|---|---|
| 0 | success |
| 2 | bad flags or arguments; nothing was sent |
| 3 | authentication failed or no API key configured |
| 4 | invalid request |
| 5 | rate, concurrency, quota or cost limit |
| 6 | the target site failed (non-2xx target status) |
| 7 | proxy error |
| 10 | the API could not be reached |
| 11 | a `--wait` gave up before the batch job finished |
| 12 | timed out waiting for the API (check `spicrawl logs` before retrying) |

```sh
# Example CI step
export SPICRAWL_API_KEY="$SPICRAWL_KEY"
spicrawl scrape https://example.com/status --format text --retry 3 -o status.txt \
  || echo "scrape failed with exit $?"
```

Accept a specific target status as success with `--allowed-status 404`, and cap
spend per request with `--max-cost`.

## Beta limitations

Spicrawl is in beta. Today you bring your own proxy with `--proxy <url>` (on `spicrawl scrape`
and `spicrawl batch submit`). Managed proxy pools and a remote browser are coming soon.

## Configuration

Settings are resolved in this order: command-line flag, environment variable, config file, default.

| Setting | Flag | Environment variable | Default |
|---|---|---|---|
| API key | `--api-key` | `SPICRAWL_API_KEY` | config file |
| API base URL | `--base-url` | `SPICRAWL_BASE_URL` | `https://api.spicrawl.com` |
| Config file path | | `SPICRAWL_CONFIG` | `~/.config/spicrawl/config.json` on Linux |
| Binary override | | `SPICRAWL_BINARY` (or `SPICRAWL_BINARY`) | binary bundled for your platform |
| HTTP timeout | `--timeout` | | `3m` |

```sh
spicrawl config path             # where the config file lives
spicrawl config get              # print saved values
spicrawl config set base_url https://api.spicrawl.com
spicrawl config unset api_key
spicrawl logout                  # remove the saved key
```

The config file is written with mode 0600. On macOS it lives under
`~/Library/Application Support/spicrawl/config.json`.

## Command reference

| Command | What it does |
|---|---|
| `spicrawl scrape <url\|->` | Fetch one page (or many from stdin) as Markdown, HTML, text, JSON, PDF or extracted data |
| `spicrawl batch submit\|wait\|get\|list\|results\|content\|retry\|cancel\|append\|close` | Run many URLs as one asynchronous job |
| `spicrawl sessions create\|list\|get\|context\|release\|delete` | Persisted sessions: cookies and storage reused across scrapes |
| `spicrawl logs [get <id>]` | Recent API requests |
| `spicrawl usage [summary\|reconciliation]` | Usage over a time window |
| `spicrawl init` | Set up AI agent clients: MCP server and agent skill |
| `spicrawl mcp install` | Add the hosted MCP server to a client's config |
| `spicrawl skill install` | Install the agent skill (SKILL.md) |
| `spicrawl login` / `spicrawl logout` | Save or remove the API key |
| `spicrawl auth status` | Show which key and base URL are in use and whether the key works |
| `spicrawl config get\|set\|unset\|path` | Read and write the config file |
| `spicrawl status` | Check the API is reachable, ready and accepts your key |
| `spicrawl docs [topic]` | Print documentation as Markdown |
| `spicrawl schema [scrape\|batch\|batch-item\|session]` | Print the JSON Schema of a request body |
| `spicrawl exit-codes` | List exit codes and their meaning |
| `spicrawl version` | Print the CLI version and platform |
| `spicrawl completion <shell>` | Generate shell completion (bash, zsh, fish, powershell) |

Run `spicrawl <command> --help` for every flag and more examples.

## FAQ

### How do I scrape a website from the terminal?

Install the CLI with `npm install -g @spicrawl/cli`, run `spicrawl login` once, then run `spicrawl scrape <url>`. Add `--format markdown` for Markdown or `-o file` to save the result.

### How do I convert a web page to Markdown from the command line?

Run `spicrawl scrape <url> --format markdown`. The output keeps the main content by default and is ready to paste into an LLM prompt; add `--render` for pages that load content with JavaScript.

### How do I save a website as Markdown from the command line?

Run `spicrawl scrape <url> --format markdown -o page.md`. For a list of pages, put the URLs in a file and run `spicrawl batch submit urls.txt --format markdown --wait`, then `spicrawl batch results <id> --all -o results.jsonl`; collect a page's links first with `spicrawl scrape <url> --links`.

### How do I scrape many URLs at once?

Use `spicrawl batch submit urls.txt --wait` for a server-side job, then `spicrawl batch results <id> --all -o results.jsonl`. For smaller lists, `spicrawl scrape - --concurrency 8 < urls.txt` scrapes in parallel from stdin.

### Can AI agents like Claude Code or Cursor use it?

Yes. Run `spicrawl init` to add the MCP server and agent skill to Claude Code, Cursor, VS Code or Codex, and agents can also call `spicrawl` directly from a shell because its output is JSON when piped. See the [MCP guide](https://docs.spicrawl.com/agents/mcp).

### Does it need Node.js?

Only to install it from npm. The npm package is a small Node.js launcher (Node 16 or later) that runs a prebuilt native binary; the scraping itself runs in that binary, and other [install options](https://docs.spicrawl.com/cli/install) need no Node.js at all.

### Where is my API key stored?

In a config file written with mode 0600, at `~/.config/spicrawl/config.json` on Linux (run `spicrawl config path` to see yours). You can skip the file entirely by setting `SPICRAWL_API_KEY`, and set `SPICRAWL_CONFIG` to move it.

### How do I use it in CI?

Set `SPICRAWL_API_KEY` as a secret environment variable and call `spicrawl` directly. Output is JSON when piped, and exit codes are stable (`spicrawl exit-codes`), so a step fails or branches on a specific error class.

### Can I use my own proxy?

Yes. Pass `--proxy <url>` to `spicrawl scrape` or `spicrawl batch submit`, and add `--proxy-verify` to check it before the request runs. See [Beta limitations](#beta-limitations).

### Which platforms are supported?

Linux, macOS and Windows, each on x64 and arm64. The single `@spicrawl/cli` package bundles a binary for all six combinations, and the launcher picks the right one at run time.

### Why does `spicrawl` say there is no prebuilt binary?

Your operating system or CPU is not one of the six bundled targets (Linux, macOS or Windows on x64 or arm64). Build from source or use another option from the [install guide](https://docs.spicrawl.com/cli/install), or point `SPICRAWL_BINARY` at a binary you already have.

### Is there a JavaScript or TypeScript SDK?

Yes: [`@spicrawl/sdk`](https://www.npmjs.com/package/@spicrawl/sdk) calls the same API from Node.js code.

## Links

- CLI documentation: https://docs.spicrawl.com/cli/overview
- Install guide: https://docs.spicrawl.com/cli/install
- MCP for AI agents: https://docs.spicrawl.com/agents/mcp
- Full documentation: https://docs.spicrawl.com
- SDK: https://www.npmjs.com/package/@spicrawl/sdk
