# Spicrawl CLI: scrape websites to Markdown and JSON from the terminal

The Spicrawl CLI is a command-line web scraper that turns any URL into Markdown, HTML, text or JSON, batch-scrapes up to 10,000 URLs per job, and connects Claude Code, Cursor, Codex and VS Code to Spicrawl over MCP, with JSON output and 14 stable exit codes for shell scripts, CI and AI agents.

[![npm version](https://img.shields.io/npm/v/@spicrawl/cli.svg)](https://www.npmjs.com/package/@spicrawl/cli)
[![npm downloads](https://img.shields.io/npm/dm/@spicrawl/cli.svg)](https://www.npmjs.com/package/@spicrawl/cli)
[![license: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](https://github.com/OfficialSpicrawl/cli/blob/main/LICENSE)
[![release](https://github.com/OfficialSpicrawl/cli/actions/workflows/release.yml/badge.svg)](https://github.com/OfficialSpicrawl/cli/actions/workflows/release.yml)

[Docs](https://docs.spicrawl.com/cli/overview) · [Get an API key](https://app.spicrawl.com/signup) · [TypeScript SDK](https://github.com/OfficialSpicrawl/sdk) · [MCP server](https://github.com/OfficialSpicrawl/mcp) · [Agent plugins](https://github.com/OfficialSpicrawl/agent-plugins)

## Quickstart: scrape a web page to Markdown

1. Create an API key at [app.spicrawl.com](https://app.spicrawl.com/signup). Keys start with `spicrawl_live_` or `spicrawl_test_`.
2. Run the CLI with `npx` (Node.js 16 or later), no install needed:

```sh
export SPICRAWL_API_KEY=spicrawl_live_...
npx @spicrawl/cli scrape https://example.com --format markdown --json
```

The result is one JSON object with the page under `content`:

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

`status` is the target site's HTTP status. Without `--json`, on a terminal, the CLI prints just the Markdown.

## What the Spicrawl CLI does

`spicrawl` is a single native binary that calls the [Spicrawl](https://spicrawl.com) web scraping API:

- **Five output formats.** `--format` takes `html` (the API default), `markdown`, `text`, `json` or `pdf`; Markdown keeps only the main content unless you pass `--no-main-content`.
- **JavaScript rendering.** `--render` runs the page's JavaScript and `--wait-for` waits for a CSS selector. The API picks the rendering tier (`fetch`, `js` or `browser`) from what the request needs; there is no engine to choose.
- **Batch jobs of up to 10,000 URLs.** `spicrawl batch submit` sends up to 10,000 URLs and 1 MiB per request as one server-side job, runs up to 50 items at once (server default 10) and tries each item up to 10 times (default 3).
- **Parallel scrapes from stdin.** `spicrawl scrape -` reads one URL per line and runs 4 requests at once by default (`--concurrency`), with every scrape flag available.
- **Structured data without a model.** `--extract` (CSS selectors), `--autoparse` (JSON-LD, OpenGraph) and `--links`.
- **One-command agent setup.** `spicrawl init` configures 4 clients (Claude Code, Cursor, VS Code and Codex) for the hosted MCP server and installs the agent skill.
- **14 stable exit codes (0 to 12, and 130).** Each maps to an error class an agent can branch on; codes are never renumbered, only added.
- **Offline request schemas.** `spicrawl schema` prints 4 JSON Schemas (`scrape`, `batch`, `batch-item`, `session`) from inside the binary.
- **6 platforms.** Linux, macOS and Windows on x64 and arm64, built with GoReleaser.

## How to install the Spicrawl CLI

### npm (Linux, macOS, Windows)

```sh
npm install -g @spicrawl/cli   # the command is `spicrawl`
npx @spicrawl/cli --help       # or run it without installing
```

`@spicrawl/cli` is one package that carries all 6 prebuilt binaries and a small Node.js launcher (Node 16 or later) that runs the one for your machine with the same arguments, stdio and exit code. No postinstall script, nothing downloaded at install time.

### Install script (Linux, macOS)

```sh
curl -fsSL https://spicrawl.com/install.sh | sh
```

It downloads the archive for your OS and CPU from the latest GitHub release, checks its SHA-256 against `checksums.txt`, and installs `spicrawl` into `~/.local/bin` (`SPICRAWL_INSTALL_DIR` changes it; `SPICRAWL_VERSION=v0.1.3` or `0.1.3` pins a release). It never prompts and never uses `sudo`.

### GitHub releases (prebuilt archives)

Every [GitHub release](https://github.com/OfficialSpicrawl/cli/releases) has `spicrawl_<os>_<arch>.tar.gz` (`.zip` on Windows) for `linux`, `darwin` and `windows` on `amd64` and `arm64`, plus `checksums.txt`. The names carry no version, so the latest is always at the same URL:

```sh
curl -fsSL https://github.com/OfficialSpicrawl/cli/releases/latest/download/spicrawl_linux_amd64.tar.gz | tar -xz spicrawl
./spicrawl version
```

### go install (Go 1.27.1 or later)

```sh
go install github.com/OfficialSpicrawl/cli/cmd/spicrawl@latest
```

A binary built this way reports its version as `dev`.

### Not available yet

A Homebrew tap is planned but not live. Use one of the channels above.

### Check the install

```sh
spicrawl version   # spicrawl 0.1.4 (go1.27.1, linux/amd64)
```

`spicrawl version --json` prints the same as `{version, go, os, arch}`. If the npm launcher says there is no prebuilt binary, your OS or CPU is not one of the 6 targets: use `go install`, or point `SPICRAWL_BINARY` at a binary you already have.

## Set up Claude Code, Cursor, Codex or VS Code with one command

```sh
spicrawl init
# or, without installing
npx @spicrawl/cli init
```

`spicrawl init` configures Claude Code, Cursor, VS Code (Copilot) and Codex to use the hosted Spicrawl MCP server (`https://mcp.spicrawl.com/mcp`) and installs the Spicrawl [agent skill](https://docs.spicrawl.com/agents/skill) (`SKILL.md`). It detects the clients you use in the current directory and your home directory, prints the files it will create or change, and asks before writing. It merges a `spicrawl` entry into existing MCP configs and keeps every other server; re-running it is safe.

| Client | `--client` | Project MCP config | With `--global` |
|---|---|---|---|
| Claude Code | `claude` | `.mcp.json` | `~/.claude.json` |
| Cursor | `cursor` | `.cursor/mcp.json` | `~/.cursor/mcp.json` |
| VS Code (Copilot) | `vscode` | `.vscode/mcp.json` | `<user config dir>/Code/User/mcp.json` |
| Codex | `codex` | `.codex/config.toml` | `~/.codex/config.toml` (or `$CODEX_HOME`) |

The skill goes to `spicrawl/SKILL.md` under `.claude/skills` (Claude Code), `.cursor/skills` (Cursor), `.github/skills` (VS Code) or `.agents/skills` (Codex); with `--global`, under `~/.claude/skills`, `~/.cursor/skills`, `~/.copilot/skills` or `~/.agents/skills`. Project paths are relative to `--dir` (default: the current directory). With `--client all` the skill is written only to `.claude/skills` and `.agents/skills`, since Cursor and VS Code also read `.claude/skills`.

```sh
spicrawl init --all --yes --api-key <key>      # every detected client, no prompt, key checked, saved and written into your user-level configs
spicrawl init --client claude,cursor --yes     # no prompt; required without a terminal
spicrawl init --client all --global --yes      # user-level config for every client
spicrawl init --client codex --agents-md --yes # also add a Spicrawl section to AGENTS.md
```

The API key is referenced, not copied: Claude Code, Cursor and Codex read `$SPICRAWL_API_KEY` from the environment of the shell that starts them, and VS Code asks for it once. There are two exceptions. `--global --client claude`: `~/.claude.json` cannot expand variables, so the key is written into it with mode 0600 and a warning. And `--api-key <key>` (see "Set up with one prompt" below) writes the key it checked into every client's config, so nothing needs exporting. `--use-env=false` embeds the key everywhere.

The two steps also run on their own: `spicrawl mcp install --client cursor` (add `--print` for the snippet only) and `spicrawl skill install --client all` (`--print > SKILL.md` to print it).

<details>
<summary>How to undo <code>spicrawl init</code></summary>

There is no uninstall command. Remove the `spicrawl` entry from each MCP config (`mcpServers.spicrawl`; in VS Code `servers.spicrawl` and the `spicrawl-api-key` input; in Codex the `[mcp_servers.spicrawl]` table), delete the `spicrawl/` skill directory, and, if you used `--agents-md`, delete the block between `<!-- spicrawl:begin -->` and `<!-- spicrawl:end -->` in `AGENTS.md`.

</details>

For other clients (Gemini CLI, OpenCode, Factory Droid, Devin and more) use the plugins in [OfficialSpicrawl/agent-plugins](https://github.com/OfficialSpicrawl/agent-plugins). More in the [agent setup guide](https://docs.spicrawl.com/cli/agent-setup).

## Set up with one prompt

Paste this into a coding agent that runs shell commands on your computer (Claude Code, Cursor, Codex, VS Code with Copilot agent mode), and put your key in place of `<YOUR_API_KEY>`. Create a key at [app.spicrawl.com/dashboard/keys](https://app.spicrawl.com/dashboard/keys).

```text
Set up Spicrawl for yourself. My API key is <YOUR_API_KEY>. Only if your shell runs on my computer, run: npx -y @spicrawl/cli@latest init --all --yes --api-key <YOUR_API_KEY> — it finds every coding agent installed here, adds the Spicrawl MCP server and installs the Spicrawl skill. Its JSON output ends with an element whose "kind" is "report", with "connected", "skipped", "api_key" and "next": tell me which agents connected, which were skipped and why, and each step in "next", such as restarting my agents. Check it: npx -y @spicrawl/cli@latest status --json. If a check failed, run the repairs it lists in order; repairs with unattended_safe: false are mine, so show me the command instead of running it. Prove it works: run npx -y @spicrawl/cli@latest scrape https://news.ycombinator.com and tell me today's top story. Report anything you could not finish. Never repeat my API key back to me. If you have no shell on my computer (a chat app or cloud sandbox), stop and send me to https://docs.spicrawl.com/agents/quickstart instead. New MCP tools appear only in a session started after the install.
```

What the two commands do, with no questions asked:

- `init --all --yes --api-key <key>` connects every detected client (`--all`; it conflicts with `--client`) without confirming (`--yes`). The key is checked with the API and saved to the config file as `spicrawl login` does, before any client file is written; a rejected key exits 3 and writes nothing. Then the key is written into each client's MCP config as is, in your **user-level** files (`~/.claude.json`, `~/.cursor/mcp.json`, ... with mode 0600; `--global` is implied), so the agents authenticate right after a restart with nothing to export. A key already saved in the config file (by `login` or an earlier `--api-key`) is embedded the same way when you run `init` without `--api-key`; a key that is only in `$SPICRAWL_API_KEY` stays a reference. Pass `--use-env=true` to keep the `${SPICRAWL_API_KEY}` reference instead, or `--global=false` to write the project files: those get the same key, a warning, and a line in the project's `.gitignore` when it has one that does not ignore them yet. The key is never printed, only masked (`spicrawl_live_ab12…cd34`), and API errors are masked too. The result list is on stdout (JSON when it is not a terminal). It ends with a summary of the run. In JSON mode, which is what an agent gets, that is one more element of the array, `{"kind": "report", "connected": ["claude", "cursor"], "skipped": [{"client": "vscode", "reason": "not detected"}], "api_key": "spicrawl_live_ab12…cd34", "next": ["restart Claude Code, Cursor to load the MCP server and the skill", ...]}`: `connected` and `skipped` are client ids, `api_key` is masked (empty when none is configured), and `next` is what the user still has to do. Every other element is what it was (`mcp`, `skill`, `agents_md` or `gitignore` in `kind`), so a consumer that filters by `kind` is unaffected, and stderr stays empty. In human mode (a terminal, or `NO_COLOR`/`CI`) the same summary follows on stderr:

  ```text
  connected: Claude Code, Cursor
  skipped: VS Code (Copilot) (not detected), Codex (not detected)
  api key: spicrawl_live_ab12…cd34 (checked, saved to /home/you/.config/spicrawl/config.json; written into the client configs)
  next: restart Claude Code, Cursor to load the MCP server and the skill
  next: run npx -y @spicrawl/cli@latest status, then ask your agent to scrape a page (or try: npx -y @spicrawl/cli@latest scrape https://example.com)
  ```

  Commands in `next` and in the repairs below say `npx -y @spicrawl/cli@latest` when the CLI was started by npx, and `spicrawl` otherwise. With no key given or saved, and with a key only in `$SPICRAWL_API_KEY`, `init` is unchanged: project files that reference `$SPICRAWL_API_KEY`, and a `next: export SPICRAWL_API_KEY=...` step.
- `status --json` adds a `repairs` array with one entry per failed check, in the order to run them, and exits non-zero when any check fails:

  ```json
  "repairs": [
    {"check": "key", "command": "spicrawl login --api-key <YOUR_API_KEY>", "unattended_safe": false, "why": "no API key is configured: create one at https://app.spicrawl.com/dashboard/keys and supply it"},
    {"check": "mcp", "command": "spicrawl init --all --yes", "unattended_safe": true, "why": "no Spicrawl MCP server is configured for Claude Code; this writes the MCP entry and the skill for every client found here"}
  ]
  ```

  `unattended_safe: false` means the user must act (supply a key) or decide; the command holds a placeholder, never a key. The checks are `reachable`, `ready`, `key`, `workers.<kind>` and `mcp`: each agent client found in this directory or your home directory needs a Spicrawl MCP server that can authenticate (skipped when no client is found). A server whose key is `${SPICRAWL_API_KEY}` does not count while that variable is unset where `status` runs (`mcp.needs_env`), and neither does one with another key written into it than the one in use (`mcp.stale`, compared by fingerprint; neither key is printed). The repair, `init --client <ids> --yes`, rewrites the entry with the saved key, or asks for a key if none is saved. A project-level entry shadows a user-level one. On a terminal, `status` draws a checklist with the repairs under it; with `NO_COLOR`, `TERM=dumb` or `CI` set it prints them as `check:`, `command:`, `unattended_safe:` and `why:` lines. Piped (as an agent runs it), it prints the JSON above.

The key is passed as an argument, so other users on the machine can see it in the process list while the command runs, and it ends up in plain text in the client configs. To avoid both, export `SPICRAWL_API_KEY` and drop `--api-key`: as long as no key is saved in the config file, `init` then writes `$SPICRAWL_API_KEY` references and does not check or save the key (run `spicrawl login` to save it, after which `init` embeds it). Claude Code rewrites `~/.claude.json` itself, so if a running session overwrites the entry, `status` reports the `mcp` check as failed and its repair writes it again (with the saved key, as a literal).

## Commands

| Command | What it does |
|---|---|
| `spicrawl scrape <url\|->` | Fetch one page, or many from stdin, as Markdown, HTML, text, JSON, PDF or extracted data |
| `spicrawl batch` | Run up to 10,000 URLs as one server-side job: `submit`, `wait`, `watch`, `get`, `list`, `results`, `content`, `retry`, `cancel`, `append`, `close` |
| `spicrawl sessions` | Keep one rendering tier and cookie jar across scrapes: `create`, `list`, `get`, `context`, `release`, `delete` |
| `spicrawl browser` | Cloud browser over a DevTools Protocol (CDP) WebSocket (`browser url`). Coming soon |
| `spicrawl init` | Set up AI agent clients: MCP server config and the agent skill |
| `spicrawl mcp` | `mcp install`: add the hosted MCP server to a client's config |
| `spicrawl skill` | `skill install`: write the agent skill (`SKILL.md`) where each client looks for it |
| `spicrawl login` | Save an API key to the config file (validated first) |
| `spicrawl logout` | Remove the saved API key |
| `spicrawl auth` | `auth status`: which key and base URL are in use, where they come from, and whether the key works |
| `spicrawl config` | Read and write the config file: `get`, `set`, `unset`, `path` |
| `spicrawl logs` | Recent API requests, newest first; `logs get <request-id>` for one; `logs --watch` for a live view |
| `spicrawl usage` | Billed usage over a time window; `usage summary`, `usage reconciliation` |
| `spicrawl status` | Check the API is reachable, ready and accepts your key, and that detected agent clients have the MCP server; prints a repair command for each failed check |
| `spicrawl docs` | Print a Spicrawl docs page as Markdown (`--list` for the index); no API key needed |
| `spicrawl schema` | Print the JSON Schema of a request body: `scrape`, `batch`, `batch-item`, `session` |
| `spicrawl exit-codes` | List the exit codes and what they mean |
| `spicrawl version` | Print the CLI version, Go version and platform |
| `spicrawl completion` | Generate shell completion for bash, zsh, fish or PowerShell |

`spicrawl --help` and `spicrawl <command> --help` list every flag. Global flags: `--api-key`, `--base-url`, `--json`, `-q/--quiet` and `--timeout` (default `3m`).

### Progress on a terminal

When stderr is a terminal, a wait longer than about 300 ms shows a spinner with what the CLI is doing and how long it has taken (`⠹ Scraping example.com/pricing (rendering JavaScript) · 4.2s`, `⠸ Batch job 3/10 done · 12.1s`). It is cleared before the result prints, and `scrape`, `scrape -` and `batch wait` end with a `✓` or `✗` line on stderr: page, target status, credits charged, duration. API errors keep their `ERR::` code and hint in a short block.

On a terminal `spicrawl --help` lists the commands grouped by purpose, with examples and the docs link; piped, the help is the plain text it always was. Colour follows your terminal: your theme's own palette by default, richer where the terminal advertises it, ASCII symbols outside a UTF-8 locale.

All of it is on stderr and stdout is byte-for-byte unchanged. It switches itself off, and the CLI prints exactly what it printed before, when stderr is not a terminal (a pipe, a file, an agent or CI log), with `--json` or `-q/--quiet`, with `NO_COLOR` set (or `CLICOLOR=0`), with `TERM=dumb`, or when `CI` is set.

### Live views

`spicrawl logs --watch` and `spicrawl batch watch <id>` open a full-screen view that updates as the API answers. They need a terminal for stdin and stdout; the colours follow your terminal's theme and `NO_COLOR` turns them off (the view stays usable: the selected row is marked with `>`).

- `logs --watch` polls the request log (`--poll`, default `2s`). New requests appear at the top and the selection stays on its row. Arrows, `PgUp`/`PgDn`, `g`/`G` move; `Enter` opens a request (what `logs get` prints); `/` searches; `s`, `t` and `e` filter by status, tier and errors only; `c` clears the filters; `p` pauses; `r` refreshes; `q` or `Esc` quits. `--errors`, `--status`, `--limit` and `--all-projects` work as in `logs`. The filters apply to the requests loaded so far (the newest `--limit`, 200 by default, and everything since), so start with `--errors` or `--status` to narrow the query itself.
- `batch watch <id>` shows a progress bar with done, failed and total, the ETA and credits, and a table of the finished items (status, HTTP status, tier, duration, URL), newest first. `Enter` opens an item, `f` shows only failed ones, and `s`, `t`, `/` filter as above. When the job finishes the view closes, the final job is printed and the exit code is `batch wait`'s: `0` completed, `4` failed or cancelled, `11` if `--max-wait` elapsed first. `q` leaves the job running (exit `0`); `Ctrl-C` exits `130`.

A failed poll never closes a view: the error shows on the status bar and the poll is retried with backoff (a `429` waits for its `Retry-After`). A wrong key or an unknown job id fails at once with its usual exit code, before the screen opens. Resizing is followed, and the views fit 80x24; below 40x10 they ask for a bigger terminal.

When stdin or stdout is not a terminal, or with `--json`, `-q/--quiet`, `CI` or `TERM=dumb`, there is no full-screen view: `logs --watch` streams new requests as plain lines, oldest first (JSONL with `--json`, which is the default when stdout is piped), and `batch watch` is exactly `batch wait`.

### How to convert a web page to Markdown from the command line

```sh
spicrawl scrape https://example.com/blog/launch --format markdown -o launch.md
```

Markdown keeps only the main content by default. Use `--no-main-content` for the whole document, or narrow it with CSS selectors:

```sh
spicrawl scrape https://example.com/docs --format markdown \
  --include 'article' --exclude 'nav' --exclude '.cookie-banner'
```

Add `--meta` to print the engine, credits, cache state, target status and request id to stderr. A target site that answers outside 2xx exits 6; accept a status with `--allowed-status 404`, and cap spend per request with `--max-cost`.

### How to scrape a JavaScript-rendered page

```sh
spicrawl scrape https://example.com/pricing --render --wait-for '.plans' --format markdown
```

`--render` runs the page in a browser. `--wait-for` waits for a CSS selector (capped by `--wait-for-timeout`), `--wait` adds a fixed delay in milliseconds, and `--block images,fonts` skips resources you do not need.

### How to extract structured data from a web page

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

### How to take a screenshot or save a page as PDF

Screenshots and PDF output are rendered in a full browser, which the API picks for you:

```sh
spicrawl scrape https://example.com --screenshot --screenshot-full-page -o home.html
spicrawl scrape https://example.com/report --format pdf -o report.pdf
```

Screenshots are saved next to the `-o` file as `<name>.<label>.<format>`, or into `--screenshot-dir`; `--screenshot-selector` and `--screenshot-format png|jpeg|webp` narrow them. PDF is never written to a terminal: pass `-o FILE`, or `--json` to get it base64-encoded.

### How to batch scrape thousands of URLs

`spicrawl batch` submits a file of URLs (one per line, or `.json` / `.jsonl` items) as one server-side job, up to 10,000 URLs and 1 MiB per submit:

```sh
id=$(spicrawl batch submit urls.txt --render --name nightly --json | jq -r .id)
spicrawl batch wait "$id" --max-wait 10m    # exits 11 if still running, 4 if it failed or was cancelled
spicrawl batch results "$id" --all -o results.jsonl
spicrawl batch retry "$id"                  # re-run the failed items
```

Each batch item is one fetch or render, returned as `html` (the default), `markdown`, `text` or `json` with `--format`. Screenshots, extraction (`--extract`, `--links`), actions, sessions, cache controls and PDF output are refused with a 400 at submit, before anything is queued or charged; use `spicrawl scrape - --concurrency 8 < urls.txt` for those. `batch wait` and `batch submit --wait` print the final job either way, and exit 4 when it ended `failed` or `cancelled`, so a script can tell it from `completed`; partial results stay readable with `batch results`. Results stay readable until the job's `results_expire_at` (72 hours after submission by default). More in the [batch guide](https://docs.spicrawl.com/cli/batch).

### How to keep cookies and logins across scrapes

A session keeps one rendering tier and one cookie jar across many scrapes, so a login or a cart survives between requests:

```sh
SID=$(spicrawl sessions create --engine browser --ttl 7200 --json | jq -r .id)
spicrawl scrape https://example.com/account --session "$SID" --format markdown
spicrawl sessions release "$SID"
```

`spicrawl sessions context <id> --json > login.json` saves the cookies and storage, and `sessions create --session-context login.json` seeds a new session from them. That file is a credential: treat it like a password.

### How to scrape through your own proxy

```sh
spicrawl scrape https://example.com --proxy "$MY_PROXY_URL" --proxy-verify
spicrawl batch submit urls.txt --proxy "$MY_PROXY_URL"
```

On `scrape`, `--proxy-verify` checks the proxy before the request runs. Your own proxy adds no proxy surcharge.

## CLI vs SDK vs MCP server vs REST API: which should I use?

All four call the same Spicrawl API with the same API key and the same credits.

| You are... | Use | Get it |
|---|---|---|
| In a terminal, a shell script or a CI job | The Spicrawl CLI | `npm install -g @spicrawl/cli` |
| Writing Node.js or TypeScript code | The Spicrawl TypeScript SDK | [`@spicrawl/sdk`](https://www.npmjs.com/package/@spicrawl/sdk) ([GitHub](https://github.com/OfficialSpicrawl/sdk)) |
| An AI agent or MCP client that should call scraping as tools | The Spicrawl MCP server (25 `spicrawl_*` tools) | Hosted at `https://mcp.spicrawl.com/mcp`, or local with `npx -y @spicrawl/mcp` ([`@spicrawl/mcp`](https://www.npmjs.com/package/@spicrawl/mcp), [GitHub](https://github.com/OfficialSpicrawl/mcp)) |
| Installing Spicrawl as a plugin in Claude Code, Codex, Cursor, Gemini CLI or another agent | The Spicrawl agent plugins | [OfficialSpicrawl/agent-plugins](https://github.com/OfficialSpicrawl/agent-plugins) |
| Any other language, or full control over the HTTP request | The REST API | `https://api.spicrawl.com`, documented in the [quickstart](https://docs.spicrawl.com/quickstart) |

- If you want an agent to use Spicrawl with the least setup, run `spicrawl init`: it writes the MCP config and the skill for you.
- If an agent already has a shell, it can call `spicrawl` directly instead of MCP: output is JSON when piped and the exit code says what went wrong.
- If you need Markdown for a list of URLs, use `spicrawl batch submit urls.txt --format markdown` for a server-side job, or `spicrawl scrape -` for one JSON line per URL as it finishes.

## Built for scripts and AI agents

- **stdout is data only.** Progress, warnings and errors go to stderr; `-q` silences progress.
- **JSON when piped.** Output is JSON whenever stdout is not a terminal; `--json` forces it and `--jsonl` prints one compact object per line (`docs` and `schema` print their document as is).
- **Never prompts without a TTY.** `login` without a key exits 2; `init`, `batch cancel` and `sessions delete` need `--yes` without a terminal.
- **Errors are machine-readable.** In JSON mode a failed API call writes the API's RFC 7807 problem document (`code`, `retryable`, `retry_after_seconds`, `diagnostics.hint`) to stderr; other failures write `{"error", "exit_code"}`.
- **A failed target is a failure.** A target status outside 2xx (and outside `--allowed-status`) exits 6; the document is still written, and JSON output carries `target_error`.

### Exit codes

There are 14 exit codes: 0 to 12, and 130. They are stable: never renumbered, only added. `spicrawl exit-codes --json` prints them as `[{code, name, meaning}]`.

| Code | Name | Meaning | What an agent should do |
|---|---|---|---|
| 0 | `ok` | success | Use stdout. |
| 1 | `internal` | CLI bug, or a transient `ERR::INTERNAL::*` | Retry once; if it repeats, report it with the request id. |
| 2 | `usage` | bad flags or arguments, or an unreadable/malformed config file; nothing was sent | Fix the command (see `--help`); do not retry it unchanged. |
| 3 | `auth` | `ERR::AUTH::*`, or no API key configured | Set `SPICRAWL_API_KEY` or run `spicrawl login`; do not retry with the same key. |
| 4 | `request` | `ERR::REQUEST::*`, `ERR::SECURITY::*`, `ERR::EXTRACT::INVALID_RULES`: fix the request | Change the parameter named in `diagnostics.hint`; do not retry unchanged. |
| 5 | `limit` | `ERR::LIMIT::*`: rate, concurrency, quota or `max_cost` | If `retryable`, wait `retry_after_seconds` and retry; otherwise stop and tell the user. |
| 6 | `upstream` | `ERR::UPSTREAM::*`: the target site failed or served a bot challenge, or answered outside `--allowed-status` | Retry later or try `--render`; accept an expected status with `--allowed-status`. |
| 7 | `proxy` | `ERR::PROXY::*` | Check the `--proxy` URL (`--proxy-verify`) or use another proxy. |
| 8 | `engine` | `ERR::ENGINE::*`, `ERR::EXTRACT::FAILED`, or `ERR::INTERNAL::UNAVAILABLE` with `retryable: false` (capability not available on this deployment) | Retry only if `retryable` is true; otherwise drop that feature. |
| 9 | `session` | `ERR::SESSION::*` | Create a new session; if the session is busy, wait and retry. |
| 10 | `network` | the Spicrawl API could not be reached (DNS, refused, TLS); nothing was sent | Safe to retry after a pause; check the network and `--base-url`. |
| 11 | `pending` | a `--wait` gave up before the job finished | The job is still running: resume with `spicrawl batch wait <id>`. |
| 12 | `timeout` | the request was sent but `--timeout` expired or the connection dropped; it may have run and been billed | Check `spicrawl logs` before retrying. |
| 130 | `interrupted` | Ctrl-C | Nothing to do; a request already sent may still have run. |

`spicrawl status` also exits 1 when a check other than the key or the network fails (the API reports not ready, a worker kind has none ready, an agent client has no Spicrawl MCP server); its `repairs` say what to run.

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

`spicrawl config path|get|set|unset` manages the file, which is written with mode 0600, and `spicrawl auth status` shows the key and base URL in use and where each came from. For a self-hosted deployment, set `--base-url` or `SPICRAWL_BASE_URL`; the docs, MCP and skill URLs are derived from it. Override the docs location with, for example, `SPICRAWL_DOCS_URL=https://spicrawl.example.com/docs` (the legacy `SPICRAWL_DOCS_HOST` is still read when it is unset).

## Beta limitations and coming soon

Spicrawl is in beta. Coming soon: AI extraction (`--ai`, `--ai-schema`), the cloud browser (`spicrawl browser url`), the managed proxy pool (`--premium-proxy`, `--country`, `--sticky-key`), stealth mode (`--stealth`), the `spicrawl.com/install.sh` installer and Homebrew. Today there is no whole-site crawl or sitemap command: collect URLs with `--links` and scrape or batch the list.

## FAQ

### How do I scrape a website from the terminal?

Run `npx @spicrawl/cli scrape <url> --format markdown -o page.md` with `SPICRAWL_API_KEY` set.

### How do I save many web pages as Markdown?

Put the URLs in a file and run `spicrawl scrape - --format markdown --concurrency 8 < urls.txt > pages.jsonl`. To collect a page's links first, run `spicrawl scrape <url> --links --json | jq -r '.links[]'`; there is no whole-site crawl command yet.

### How many URLs can one batch job take?

Up to 10,000 URLs, and 1 MiB of request body, per `spicrawl batch submit`. A job created with `--open` accepts more items through `spicrawl batch append` until `spicrawl batch close`.

### Does it render JavaScript?

Yes: add `--render` to run the page in a browser, and `--wait-for '<css selector>'` to wait for the content. Screenshots and PDF are rendered in a full browser automatically; you do not pick one.

### Is Spicrawl free?

Spicrawl is priced in credits: each successful request draws on your organization's monthly credit allowance. The [credits page](https://docs.spicrawl.com/credits) lists 1 credit for a plain fetch, 3 with `--render` and 8 for a screenshot or PDF (full-browser rendering); failed requests cost 0, and a cache hit is billed at the price of the fetch that stored it. `spicrawl usage summary` shows the current period against your allowance.

### Does it work with Claude Code, Cursor, Codex and VS Code?

Yes: `spicrawl init` adds the hosted MCP server and the agent skill to all four. Agents can also call `spicrawl` from a shell, since its output is JSON when piped and its exit codes are stable.

### Is there a hosted MCP server?

Yes: `https://mcp.spicrawl.com/mcp`, authenticated with an `Authorization: Bearer <API key>` header, serving 25 `spicrawl_*` tools. The same tools run locally with `npx -y @spicrawl/mcp`; see [OfficialSpicrawl/mcp](https://github.com/OfficialSpicrawl/mcp) and the [MCP guide](https://docs.spicrawl.com/agents/mcp).

### Is the Spicrawl CLI open source?

Yes. It is licensed under Apache-2.0, and the source is at [github.com/OfficialSpicrawl/cli](https://github.com/OfficialSpicrawl/cli).

### How do I use it in CI?

Export the key from your CI secret store as `SPICRAWL_API_KEY` (nothing is written to disk) and call `spicrawl` directly. Output is JSON when piped and exit codes are stable (`spicrawl exit-codes`), so a step can fail or branch on a specific error class.

## Related

| Package | Latest version | Downloads |
|---|---|---|
| [`@spicrawl/cli`](https://www.npmjs.com/package/@spicrawl/cli) ([GitHub](https://github.com/OfficialSpicrawl/cli)) | [![npm](https://img.shields.io/npm/v/@spicrawl/cli.svg)](https://www.npmjs.com/package/@spicrawl/cli) | [![downloads](https://img.shields.io/npm/dm/@spicrawl/cli.svg)](https://www.npmjs.com/package/@spicrawl/cli) |
| [`@spicrawl/sdk`](https://www.npmjs.com/package/@spicrawl/sdk) ([GitHub](https://github.com/OfficialSpicrawl/sdk)) | [![npm](https://img.shields.io/npm/v/@spicrawl/sdk.svg)](https://www.npmjs.com/package/@spicrawl/sdk) | [![downloads](https://img.shields.io/npm/dm/@spicrawl/sdk.svg)](https://www.npmjs.com/package/@spicrawl/sdk) |
| [`@spicrawl/mcp`](https://www.npmjs.com/package/@spicrawl/mcp) ([GitHub](https://github.com/OfficialSpicrawl/mcp)) | [![npm](https://img.shields.io/npm/v/@spicrawl/mcp.svg)](https://www.npmjs.com/package/@spicrawl/mcp) | [![downloads](https://img.shields.io/npm/dm/@spicrawl/mcp.svg)](https://www.npmjs.com/package/@spicrawl/mcp) |

The versions and downloads are read live from the npm registry.

- TypeScript SDK: [@spicrawl/sdk on npm](https://www.npmjs.com/package/@spicrawl/sdk) and [on GitHub](https://github.com/OfficialSpicrawl/sdk)
- MCP server for AI agents: [@spicrawl/mcp on npm](https://www.npmjs.com/package/@spicrawl/mcp) and [on GitHub](https://github.com/OfficialSpicrawl/mcp); guide at https://docs.spicrawl.com/agents/mcp
- Agent plugins for Claude Code, Codex, Cursor, Gemini CLI and more: https://github.com/OfficialSpicrawl/agent-plugins
- Agent skill: https://docs.spicrawl.com/agents/skill
- CLI documentation: https://docs.spicrawl.com/cli/overview
- Full documentation: https://docs.spicrawl.com
- Docs index for LLMs: https://docs.spicrawl.com/llms.txt
- Issues and feature requests: https://github.com/OfficialSpicrawl/cli/issues

## Development

```sh
make build      # bin/spicrawl, version from git describe
make test lint  # go test, go vet and gofmt
make snapshot   # goreleaser build into dist/, uploads nothing
make npm        # lay out the npm package in dist/npm/cli/ from the snapshot
make spec       # refresh openapi.yaml from the docs site
```

The design and the contract agents rely on are in [DESIGN.md](https://github.com/OfficialSpicrawl/cli/blob/main/DESIGN.md); the API wire contract is [openapi.yaml](https://github.com/OfficialSpicrawl/cli/blob/main/openapi.yaml).

**Releasing:** bump `"version"` in [`npm/package.json`](https://github.com/OfficialSpicrawl/cli/blob/main/npm/package.json) and merge to main. When npm does not have that version yet, the [release workflow](https://github.com/OfficialSpicrawl/cli/blob/main/.github/workflows/release.yml) runs the tests, tags the commit `vX.Y.Z`, builds the six binaries with GoReleaser, creates the GitHub release with the archives and `checksums.txt`, and publishes `@spicrawl/cli` to npm. No manual tag is needed.

## License

Apache-2.0. See [LICENSE](https://github.com/OfficialSpicrawl/cli/blob/main/LICENSE).
