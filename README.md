# Spicrawl CLI: Scrape Websites to Markdown & JSON from Your Terminal

Spicrawl CLI: scrape any URL to Markdown or JSON from your terminal, batch-scrape up to 10,000 URLs, and connect Claude Code, Cursor, Codex and VS Code to web data over MCP. JSON output and stable exit codes for scripts and AI agents.

[![npm version](https://img.shields.io/npm/v/@spicrawl/cli.svg)](https://www.npmjs.com/package/@spicrawl/cli)
[![license: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](https://github.com/Spicrawl/cli/blob/main/LICENSE)
[![CI](https://github.com/Spicrawl/cli/actions/workflows/release.yml/badge.svg)](https://github.com/Spicrawl/cli/actions/workflows/release.yml)
[![GitHub stars](https://img.shields.io/github/stars/Spicrawl/cli.svg)](https://github.com/Spicrawl/cli)

[Docs](https://docs.spicrawl.com/cli/overview) · [Get an API key](https://app.spicrawl.com/signup) · [GitHub](https://github.com/Spicrawl/cli) · [TypeScript SDK](https://github.com/Spicrawl/sdk) · [MCP server](https://docs.spicrawl.com/agents/mcp)

## What is the Spicrawl CLI?

The Spicrawl CLI is a single native binary, `spicrawl`, that calls the [Spicrawl](https://spicrawl.com) web scraping API from the command line. `spicrawl scrape` fetches one page as Markdown, HTML, text, JSON or extracted data; `spicrawl batch` runs a list of URLs as one server-side job; `spicrawl init` connects AI coding agents over MCP. Output is JSON whenever it is piped and exit codes are stable, so it works the same for a person at a terminal, a shell script, a CI job or an AI agent.

## Install

```sh
# Run it once without installing (Node.js 16 or later)
npx @spicrawl/cli scrape https://example.com --format markdown

# Install globally with npm; the command is `spicrawl`
npm install -g @spicrawl/cli

# Or build from source (Go 1.27+)
go install github.com/Spicrawl/cli/cmd/spicrawl@latest
```

Prebuilt archives for Linux, macOS and Windows (x64 and arm64), with `checksums.txt`, are attached to each [GitHub release](https://github.com/Spicrawl/cli/releases). A `curl | sh` installer and Homebrew are coming soon.

**How the npm package works:** `@spicrawl/cli` is one package that carries six prebuilt binaries (Linux, macOS and Windows on x64 and arm64) and a small Node.js launcher that runs the one for your machine with the same arguments, stdio and exit code. There are no platform sub-packages, no postinstall script and nothing is downloaded at install time. Node.js is needed only for the launcher. More in the [install guide](https://docs.spicrawl.com/cli/install).

Check the install with `spicrawl version`.

## Set up your AI agent in one command

```sh
spicrawl init
# or, without installing
npx @spicrawl/cli init
```

`spicrawl init` configures Claude Code, Cursor, VS Code (Copilot) and Codex to use the hosted Spicrawl MCP server (`https://mcp.spicrawl.com/mcp`) and installs the Spicrawl [agent skill](https://docs.spicrawl.com/agents/skill) (`SKILL.md`). It detects the clients you use in the current directory and your home directory, prints the files it will create or change, and asks before writing. It merges a `spicrawl` entry into existing MCP configs and keeps every other server; re-running it is safe.

| Client | `--client` | MCP config | Skill | With `--global` |
|---|---|---|---|---|
| Claude Code | `claude` | `.mcp.json` | `.claude/skills/spicrawl/SKILL.md` | `~/.claude.json`, `~/.claude/skills/spicrawl/SKILL.md` |
| Cursor | `cursor` | `.cursor/mcp.json` | `.cursor/skills/spicrawl/SKILL.md` | `~/.cursor/mcp.json`, `~/.cursor/skills/spicrawl/SKILL.md` |
| VS Code (Copilot) | `vscode` | `.vscode/mcp.json` | `.github/skills/spicrawl/SKILL.md` | `<user config dir>/Code/User/mcp.json`, `~/.copilot/skills/spicrawl/SKILL.md` |
| Codex | `codex` | `.codex/config.toml` | `.agents/skills/spicrawl/SKILL.md` | `~/.codex/config.toml` (or `$CODEX_HOME/config.toml`), `~/.agents/skills/spicrawl/SKILL.md` |

The MCP config and Skill columns are relative to the project directory (`--dir`, default: the current directory). The skill is written once per location: Cursor and VS Code also read `.claude/skills`, so with `--client all` it goes to `.claude/skills` and `.agents/skills` only.

```sh
spicrawl init --client claude,cursor --yes     # no prompt; required without a terminal
spicrawl init --client all --global --yes      # user-level config for every client
spicrawl init --client codex --agents-md --yes # also add a Spicrawl section to AGENTS.md
```

The API key is referenced, not copied: Claude Code, Cursor and Codex read `$SPICRAWL_API_KEY` from the environment (export it in the shell that starts the client), and VS Code asks for the key once and stores it. The exception is `--global --client claude`: `~/.claude.json` cannot expand variables, so the key is written into it, the file is set to mode 0600, and the CLI warns. `--use-env=false` embeds the key everywhere.

The two steps are also available on their own:

```sh
spicrawl mcp install --client claude          # MCP server entry only
spicrawl mcp install --client cursor --print  # print the config snippet instead of writing it
spicrawl skill install --client all           # the skill only
spicrawl skill install --print > SKILL.md     # print the skill
```

There is no uninstall command. To undo, remove the `spicrawl` entry from each MCP config (`mcpServers.spicrawl`; in VS Code `servers.spicrawl` and the `spicrawl-api-key` input; in Codex the `[mcp_servers.spicrawl]` table), delete the `spicrawl/` skill directory, and, if you used `--agents-md`, delete the block between `<!-- spicrawl:begin -->` and `<!-- spicrawl:end -->` in `AGENTS.md`. More in the [agent setup guide](https://docs.spicrawl.com/cli/agent-setup) and the [MCP guide](https://docs.spicrawl.com/agents/mcp).

## Quickstart

```sh
# 1. Authenticate: save a key (checked against the API first) ...
spicrawl login --api-key spicrawl_live_...
# ... or set it in the environment
export SPICRAWL_API_KEY=spicrawl_live_...

# 2. Scrape a web page to Markdown
spicrawl scrape https://example.com --format markdown

# 3. A JavaScript-heavy page: render it and wait for the element that holds the data
spicrawl scrape https://example.com/pricing --render --wait-for '.plans' --format markdown

# 4. Pipe the result: one compact JSON line per scrape
spicrawl scrape https://example.com --format markdown --jsonl | jq -r .content

# 5. Many URLs from a file, 8 at a time, one JSON line per URL
spicrawl scrape - --format markdown --concurrency 8 < urls.txt > pages.jsonl

# 6. A large list as one server-side batch job
spicrawl batch submit urls.txt --render --wait
spicrawl batch results <job-id> --all -o results.jsonl
```

Piped (or with `--json`), a scrape prints one object with the document under `content`:

```json
{
  "cache_state": "miss",
  "content": "# Example Domain\n\nThis domain is for use in documentation examples without needing permission.\n\n[Learn more](https://iana.org/domains/example)\n",
  "content_type": "text/markdown; charset=utf-8",
  "credits_charged": 1,
  "engine": "fetch",
  "final_url": "https://example.com/",
  "proxy_source": "direct",
  "request_cost": 1,
  "request_id": "01K6E8Z2Q4W9R3T7Y5N1M0B8VC",
  "status": 200,
  "url": "https://example.com",
  "warnings": []
}
```

`status` is the target site's HTTP status. `spicrawl --help` and `spicrawl <command> --help` list every flag.

## Commands

| Command | What it does |
|---|---|
| `spicrawl scrape <url\|->` | Fetch one page, or many from stdin, as Markdown, HTML, text, JSON, PDF or extracted data |
| `spicrawl batch` | Run up to 10,000 URLs as one server-side job: `submit`, `wait`, `get`, `list`, `results`, `content`, `retry`, `cancel`, `append`, `close` |
| `spicrawl sessions` | Keep one engine and cookie jar across scrapes: `create`, `list`, `get`, `context`, `release`, `delete` |
| `spicrawl browser` | Cloud browser over the Chrome DevTools Protocol (`browser url`). Coming soon |
| `spicrawl init` | Set up AI agent clients: MCP server config and the agent skill |
| `spicrawl mcp` | `mcp install`: add the hosted MCP server to a client's config |
| `spicrawl skill` | `skill install`: write the agent skill (`SKILL.md`) where each client looks for it |
| `spicrawl login` | Save an API key to the config file (validated first) |
| `spicrawl logout` | Remove the saved API key |
| `spicrawl auth` | `auth status`: which key and base URL are in use, where they come from, and whether the key works |
| `spicrawl config` | Read and write the config file: `get`, `set`, `unset`, `path` |
| `spicrawl logs` | Recent API requests, newest first; `logs get <request-id>` for one |
| `spicrawl usage` | Billed usage over a time window; `usage summary`, `usage reconciliation` |
| `spicrawl status` | Check the API is reachable, ready and accepts your key |
| `spicrawl docs` | Print a Spicrawl docs page as Markdown (`--list` for the index); no API key needed |
| `spicrawl schema` | Print the JSON Schema of a request body: `scrape`, `batch`, `batch-item`, `session` |
| `spicrawl exit-codes` | List the exit codes and what they mean |
| `spicrawl version` | Print the CLI version, Go version and platform |
| `spicrawl completion` | Generate shell completion for bash, zsh, fish or PowerShell |

### Scrape a web page to Markdown

```sh
spicrawl scrape https://example.com/blog/launch --format markdown -o launch.md
```

Markdown keeps only the main content by default. Use `--no-main-content` for the whole document, or narrow it with CSS selectors:

```sh
spicrawl scrape https://example.com/docs --format markdown \
  --include 'article' --exclude 'nav' --exclude '.cookie-banner'
```

Other formats: `--format html` (the API default), `text`, `json` or `pdf`. Add `--meta` to print the engine, credits, cache state, target status and request id to stderr. A target site that answers outside 2xx exits 6; accept a status with `--allowed-status 404`, and cap spend per request with `--max-cost`.

### Render JavaScript pages

```sh
spicrawl scrape https://example.com/pricing --render --wait-for '.plans' --format markdown
```

`--render` runs the page in a browser. `--wait-for` waits for a CSS selector (capped by `--wait-for-timeout`), `--wait` adds a fixed delay in milliseconds, and `--block images,fonts` skips resources you do not need.

### Extract structured data

```sh
# A CSS selector map; prints only the extracted data
spicrawl scrape https://example.com/product/1 --extract '{"title":"h1","price":".price"}'

# Rules from a file
spicrawl scrape https://example.com/product/1 --extract @rules.json

# The page's own JSON-LD, OpenGraph and embedded state
spicrawl scrape https://example.com/product/1 --autoparse

# Every link on the page
spicrawl scrape https://example.com --links --json | jq -r '.links[]'
```

AI extraction with a plain-language prompt or a JSON Schema (`--ai`, `--ai-schema`) is coming soon; see the [AI extraction guide](https://docs.spicrawl.com/guides/ai-extraction).

### Screenshots and PDF

Screenshots and PDF output need the Chromium engine:

```sh
spicrawl scrape https://example.com --engine chromium --screenshot --screenshot-full-page -o home.html
spicrawl scrape https://example.com --engine chromium --screenshot --screenshot-format png \
  --screenshot-selector '#hero' --screenshot-dir ./shots -o home.html
spicrawl scrape https://example.com/report --engine chromium --format pdf -o report.pdf
```

Screenshots are saved next to the `-o` file as `<name>.<label>.<format>`, or into `--screenshot-dir`. PDF is never written to a terminal: pass `-o FILE`, or `--json` to get it base64-encoded.

### Batch scrape a list of URLs

`spicrawl batch` submits a file of URLs (one per line, or `.json` / `.jsonl` items) as one server-side job of up to 10,000 URLs:

```sh
# Submit, render, and wait until the job finishes (up to 20 minutes)
spicrawl batch submit urls.txt --render --name nightly --wait --max-wait 20m

# Download every finished item as JSON Lines
spicrawl batch results <job-id> --all -o results.jsonl

# Capture the job id in a script
id=$(spicrawl batch submit urls.txt --json | jq -r .id)
spicrawl batch wait "$id" --max-wait 10m    # exits 11 if the job is still running
spicrawl batch results "$id" --status failed --all
spicrawl batch retry "$id"                  # re-run the failed items
```

Batch items currently return the raw page HTML: the batch worker applies `--render`, `--proxy` and (on rendered items) `--block`, and accepts but does not yet apply other scrape flags such as `--format markdown`. For Markdown or extraction per URL, use `spicrawl scrape -` with `--concurrency`. Results stay readable until the job's `results_expire_at` (72 hours after submission by default). More in the [batch guide](https://docs.spicrawl.com/cli/batch).

### Reuse cookies with sessions

A session keeps one engine and one cookie jar across many scrapes, so a login or a cart survives between requests:

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

### Use your own proxy

```sh
spicrawl scrape https://example.com --proxy "$MY_PROXY_URL" --proxy-verify
spicrawl batch submit urls.txt --proxy "$MY_PROXY_URL"
```

`--proxy` works on `spicrawl scrape` and `spicrawl batch submit`. On `scrape`, `--proxy-verify` checks the proxy before the request runs. A request through your own proxy is priced like a direct one, with no proxy surcharge.

## Built for scripts and AI agents

- **stdout is data only.** Progress, warnings and errors go to stderr; `-q` silences progress.
- **JSON when piped.** Output is JSON whenever stdout is not a terminal. `--json` forces it; `--jsonl` prints one compact object per line. `spicrawl docs` and `spicrawl schema` print their document as is.
- **Never prompts without a TTY.** `spicrawl login` without a key fails with exit 2 instead of waiting. `init`, `batch cancel` and `sessions delete` ask on a terminal and need `--yes` without one.
- **Errors are machine-readable.** In JSON mode a failed API call writes the API's RFC 7807 problem document (`code`, `retryable`, `retry_after_seconds`, `diagnostics.hint`) to stderr; other failures write `{"error", "exit_code"}`.
- **`-` reads stdin**, one URL per line.
- **A failed target is a failure.** A target status outside 2xx (and outside `--allowed-status`) exits 6; the document is still written, and JSON output carries `target_error`.
- **Request schemas offline.** `spicrawl schema scrape` prints the request body's JSON Schema from inside the binary, so an agent can validate a body before sending it.

### Exit codes

Stable: codes are never renumbered, only added. `spicrawl exit-codes --json` prints them as `[{code, name, meaning}]`.

| Code | Meaning | What an agent should do |
|---|---|---|
| 0 | success | Use stdout. |
| 1 | internal error: CLI bug, or a transient `ERR::INTERNAL::*` | Retry once; if it repeats, report it with the request id. |
| 2 | usage error: bad flags or arguments, or an unreadable/malformed config file; nothing was sent | Fix the command (see `--help`); do not retry it unchanged. |
| 3 | `ERR::AUTH::*`, or no API key configured | Set `SPICRAWL_API_KEY` or run `spicrawl login`; do not retry with the same key. |
| 4 | `ERR::REQUEST::*`, `ERR::SECURITY::*`, `ERR::EXTRACT::INVALID_RULES`: fix the request | Change the parameter named in `diagnostics.hint`; do not retry unchanged. |
| 5 | `ERR::LIMIT::*`: rate, concurrency, quota or `max_cost` | If `retryable`, wait `retry_after_seconds` and retry; otherwise stop and tell the user. |
| 6 | `ERR::UPSTREAM::*`: the target site failed or served a bot challenge, or answered outside `--allowed-status` | Retry later or try `--render`; accept an expected status with `--allowed-status`. |
| 7 | `ERR::PROXY::*` | Check the `--proxy` URL (`--proxy-verify`) or use another proxy. |
| 8 | `ERR::ENGINE::*`, `ERR::EXTRACT::FAILED`, or `ERR::INTERNAL::UNAVAILABLE` with `retryable: false` (capability not available on this deployment) | Retry only if `retryable` is true; otherwise drop that feature. |
| 9 | `ERR::SESSION::*` | Create a new session; if the session is busy, wait and retry. |
| 10 | network: the Spicrawl API could not be reached (DNS, refused, TLS); nothing was sent | Safe to retry after a pause; check the network and `--base-url`. |
| 11 | a `--wait` gave up before the job finished | The job is still running: resume with `spicrawl batch wait <id>`. |
| 12 | timeout: the request was sent but `--timeout` expired; it may have run and been billed | Check `spicrawl logs` before retrying. |

## Configuration

Settings resolve in this order: command-line flag, environment variable, config file, default.

| Setting | Flag | Environment variable | Config file key | Default |
|---|---|---|---|---|
| API key | `--api-key` | `SPICRAWL_API_KEY` | `api_key` | none |
| API base URL | `--base-url` | `SPICRAWL_BASE_URL` | `base_url` | `https://api.spicrawl.com` |
| Config file path | | `SPICRAWL_CONFIG` | | `~/.config/spicrawl/config.json` on Linux, `~/Library/Application Support/spicrawl/config.json` on macOS |
| Docs URL (`docs`, skill) | | `SPICRAWL_DOCS_URL` | | `https://docs.spicrawl.com`, or `<API origin>/docs` for a self-hosted API |
| MCP server URL | `--mcp-url` | `SPICRAWL_MCP_URL` | | `https://mcp.spicrawl.com/mcp`, or `<API origin>/mcp` for a self-hosted API |
| Agent skill URL | `--skill-url` | `SPICRAWL_SKILL_URL` | | `<docs URL>/skill.md` |
| Binary used by the npm launcher | | `SPICRAWL_BINARY` | | the binary bundled for your platform |
| HTTP timeout per API call | `--timeout` | | | `3m` |

```sh
spicrawl config path      # where the config file lives
spicrawl config get       # print saved values
spicrawl config set base_url https://api.spicrawl.com
spicrawl config unset api_key
spicrawl auth status      # the key and base URL in use, and where each came from
```

The config file is written with mode 0600. For a self-hosted deployment, set `--base-url` or `SPICRAWL_BASE_URL`; the docs, MCP and skill URLs are derived from it. Override the docs location with, for example, `SPICRAWL_DOCS_URL=https://spicrawl.example.com/docs` (the legacy `SPICRAWL_DOCS_HOST` is still read when it is unset).

**In CI**, store the key as a secret and export it as `SPICRAWL_API_KEY` instead of running `spicrawl login`; nothing is written to disk:

```sh
export SPICRAWL_API_KEY="$SPICRAWL_KEY"   # from your CI secret store
spicrawl scrape https://example.com/status --format text --retry 3 -o status.txt
```

## Beta limitations and coming soon

Spicrawl is in beta. Today:

- **AI extraction** (`--ai`, `--ai-schema`) is coming soon. Use `--extract` or `--autoparse` for structured data. See the [AI extraction guide](https://docs.spicrawl.com/guides/ai-extraction).
- **Cloud browser** (`spicrawl browser url`) is coming soon.
- **The managed proxy pool** (`--premium-proxy`, `--country`, `--sticky-key`) and stealth mode (`--stealth`) are coming soon. Bring your own proxy with `--proxy`.
- **Batch items return raw HTML.** For Markdown or extraction per URL, use `spicrawl scrape -`.
- **No whole-site crawl or sitemap command.** Collect the URLs (for example with `--links`) and scrape or batch the list.
- **The `curl | sh` installer and Homebrew** are coming soon. Use npm or `go install`.

## FAQ

### How do I scrape a website from the terminal?

Install the CLI with `npm install -g @spicrawl/cli` (or run it with `npx @spicrawl/cli`), set `SPICRAWL_API_KEY` or run `spicrawl login` once, then run `spicrawl scrape <url>`. Add `--format markdown` for Markdown or `-o file` to save the result.

### How do I convert a web page to Markdown from the command line?

Run `spicrawl scrape <url> --format markdown`. The output keeps the main content by default and is ready to paste into an LLM prompt; add `--render` for pages that load their content with JavaScript.

### How do I save a website as Markdown from the command line?

Run `spicrawl scrape <url> --format markdown -o page.md` for one page. For many pages, put the URLs in a file and run `spicrawl scrape - --format markdown --concurrency 8 < urls.txt > pages.jsonl`; collect a page's links first with `spicrawl scrape <url> --links --json | jq -r '.links[]'`. There is no whole-site crawl command yet.

### How do I scrape many URLs at once?

`spicrawl scrape - --concurrency 8 < urls.txt` scrapes a list in parallel from your machine, with every scrape flag available. For a large list that should keep running without your process, use `spicrawl batch submit urls.txt --wait` (up to 10,000 URLs per job), then `spicrawl batch results <id> --all -o results.jsonl`. Batch items currently return raw HTML.

### Can AI agents like Claude Code or Cursor use it?

Yes. Run `spicrawl init` to add the hosted MCP server and the agent skill to Claude Code, Cursor, VS Code or Codex. Agents can also call `spicrawl` directly from a shell: its output is JSON when piped and its exit codes are stable. See the [MCP guide](https://docs.spicrawl.com/agents/mcp).

### When should I use the CLI, the SDK or the MCP server?

In a terminal, a shell script or CI, use the CLI. In Node.js or TypeScript code, use [`@spicrawl/sdk`](https://github.com/Spicrawl/sdk). For an AI agent that talks MCP, use the [hosted MCP server](https://docs.spicrawl.com/agents/mcp); `spicrawl init` sets it up for you.

### Is the Spicrawl CLI open source?

Yes. It is licensed under Apache-2.0, and the source is at [github.com/Spicrawl/cli](https://github.com/Spicrawl/cli).

### Does it need Node.js?

Only to install it from npm. The npm package is a small Node.js launcher (Node 16 or later) that runs a prebuilt native binary; the scraping runs in that binary. `go install` and the release archives need no Node.js at all.

### Where is my API key stored?

In a config file written with mode 0600, at `~/.config/spicrawl/config.json` on Linux (run `spicrawl config path` to see yours). Set `SPICRAWL_API_KEY` to skip the file entirely, or `SPICRAWL_CONFIG` to move it.

### How do I use it in CI?

Set `SPICRAWL_API_KEY` as a secret environment variable and call `spicrawl` directly. Output is JSON when piped and exit codes are stable (`spicrawl exit-codes`), so a step can fail or branch on a specific error class.

### Can I use my own proxy?

Yes. Pass `--proxy <url>` to `spicrawl scrape` or `spicrawl batch submit`; on `scrape`, add `--proxy-verify` to check it before the request runs. The managed proxy pool is coming soon.

### Which platforms are supported?

Linux, macOS and Windows, each on x64 and arm64. The single `@spicrawl/cli` package bundles a binary for all six, and the launcher picks the right one at run time.

### Why does `spicrawl` say there is no prebuilt binary?

Your operating system or CPU is not one of the six bundled targets. Build it with `go install github.com/Spicrawl/cli/cmd/spicrawl@latest`, or point `SPICRAWL_BINARY` at a binary you already have.

### Is there a JavaScript or TypeScript SDK?

Yes: [`@spicrawl/sdk`](https://www.npmjs.com/package/@spicrawl/sdk) ([GitHub](https://github.com/Spicrawl/sdk)) calls the same API from Node.js code.

## Related

| Package | Latest version | Downloads |
|---|---|---|
| [`@spicrawl/cli`](https://www.npmjs.com/package/@spicrawl/cli) ([GitHub](https://github.com/Spicrawl/cli)) | [![npm](https://img.shields.io/npm/v/@spicrawl/cli.svg)](https://www.npmjs.com/package/@spicrawl/cli) | [![downloads](https://img.shields.io/npm/dm/@spicrawl/cli.svg)](https://www.npmjs.com/package/@spicrawl/cli) |
| [`@spicrawl/sdk`](https://www.npmjs.com/package/@spicrawl/sdk) ([GitHub](https://github.com/Spicrawl/sdk)) | [![npm](https://img.shields.io/npm/v/@spicrawl/sdk.svg)](https://www.npmjs.com/package/@spicrawl/sdk) | [![downloads](https://img.shields.io/npm/dm/@spicrawl/sdk.svg)](https://www.npmjs.com/package/@spicrawl/sdk) |

The versions and downloads are read live from the npm registry.

- TypeScript SDK: [@spicrawl/sdk on npm](https://www.npmjs.com/package/@spicrawl/sdk) and [on GitHub](https://github.com/Spicrawl/sdk)
- MCP server for AI agents: https://docs.spicrawl.com/agents/mcp
- Agent skill: https://docs.spicrawl.com/agents/skill
- CLI documentation: https://docs.spicrawl.com/cli/overview
- Full documentation: https://docs.spicrawl.com
- Docs index for LLMs: https://docs.spicrawl.com/llms.txt
- Issues and feature requests: https://github.com/Spicrawl/cli/issues

## Development

```sh
make build      # bin/spicrawl, version from git describe
make test lint  # go test, go vet and gofmt
make snapshot   # goreleaser build into dist/, uploads nothing
make npm        # lay out the npm package in dist/npm/cli/ from the snapshot
make spec       # refresh openapi.yaml from the docs site
```

The design and the contract agents rely on are in [DESIGN.md](https://github.com/Spicrawl/cli/blob/main/DESIGN.md); the API wire contract is [openapi.yaml](https://github.com/Spicrawl/cli/blob/main/openapi.yaml).

**Releasing:** bump `"version"` in [`npm/package.json`](https://github.com/Spicrawl/cli/blob/main/npm/package.json) and merge to main. When npm does not have that version yet, the [release workflow](https://github.com/Spicrawl/cli/blob/main/.github/workflows/release.yml) runs the tests, tags the commit `vX.Y.Z`, builds the six binaries with GoReleaser, creates the GitHub release with the archives and `checksums.txt`, and publishes `@spicrawl/cli` to npm. No manual tag is needed.

## License

Apache-2.0. See [LICENSE](https://github.com/Spicrawl/cli/blob/main/LICENSE).
