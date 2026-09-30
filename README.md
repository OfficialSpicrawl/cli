# Spicrawl CLI

`spicrawl` is the command-line client for the Spicrawl web data API. Built for
people at a terminal and for AI agents driving a shell.

Docs: https://docs.spicrawl.com/cli/overview. Design and contract: [DESIGN.md](DESIGN.md).

## Install

```sh
# Linux / macOS: installs to ~/.local/bin, no sudo, verifies the SHA-256
curl -fsSL https://spicrawl.com/install.sh | sh

# pick the directory or version
curl -fsSL https://spicrawl.com/install.sh | SPICRAWL_INSTALL_DIR=/usr/local/bin SPICRAWL_VERSION=v1.2.3 sh

# npm (no Go needed; also works on Windows)
npx @spicrawl/cli scrape https://example.com
npm install -g @spicrawl/cli   # the command is `spicrawl`

# from source (Go 1.27+)
go install github.com/Spicrawl/cli/cmd/spicrawl@latest
```

Release archives for linux, macOS and Windows (amd64, arm64) and
`checksums.txt` are on the GitHub releases page.

## Quickstart

```sh
spicrawl login --api-key "$KEY"           # or set SPICRAWL_API_KEY
spicrawl scrape https://example.com --format markdown
spicrawl scrape https://example.com --render --json > page.json

# many URLs, one JSON object per line
cat urls.txt | spicrawl scrape - --jsonl --concurrency 8 > results.jsonl

# batch jobs
spicrawl batch submit urls.txt --render --format markdown --wait
spicrawl batch results <id> --all -o results.jsonl
spicrawl batch content <id> 0 -o page.md          # one item's document
spicrawl batch cancel <id> --yes                  # --yes required without a terminal

# set up an AI agent (MCP config + skill) for Claude Code, Cursor, Codex, VS Code
spicrawl init --client all --yes
```

`spicrawl --help` and `spicrawl <command> --help` list every flag.
`spicrawl schema scrape` prints the request body's JSON Schema.

## Agent contract

- stdout carries data only. Progress, warnings and errors go to stderr.
- Output is JSON when stdout is not a terminal. `--json` forces it; `--jsonl`
  streams one object per line.
- Never prompts without a TTY. Destructive commands take `--yes`.
- In JSON mode a failure writes the API's RFC 7807 problem document to stderr.
- `-` reads one URL per line from stdin.

Configuration resolves flag > env (`SPICRAWL_API_KEY`, `SPICRAWL_BASE_URL`) >
config file (`spicrawl config path`) > `https://api.spicrawl.com`.
`spicrawl docs` reads from `$SPICRAWL_DOCS_URL` (e.g. `http://192.0.2.10:8080/docs`), else
the legacy `$SPICRAWL_DOCS_HOST/docs`, else `https://docs.spicrawl.com` for the hosted API
and `<base URL origin>/docs` for a self-hosted one.

### Exit codes

Stable; scripts and agents may branch on them (`spicrawl exit-codes`).

| Code | Meaning |
| ---- | ------- |
| 0 | success |
| 1 | internal error: CLI bug, or a transient `ERR::INTERNAL::*` |
| 2 | usage error: bad flags or arguments, or an unreadable/malformed config file; nothing was sent |
| 3 | `ERR::AUTH::*`, or no API key configured |
| 4 | `ERR::REQUEST::*`, `ERR::SECURITY::*`, `ERR::EXTRACT::INVALID_RULES`: fix the request |
| 5 | `ERR::LIMIT::*`: rate, concurrency, quota or max_cost |
| 6 | `ERR::UPSTREAM::*`: the target site failed or served a bot challenge |
| 7 | `ERR::PROXY::*` |
| 8 | `ERR::ENGINE::*`, `ERR::EXTRACT::FAILED`, or `ERR::INTERNAL::UNAVAILABLE` with `retryable: false` (capability not available on this deployment) |
| 9 | `ERR::SESSION::*` |
| 10 | network: the Spicrawl API could not be reached (DNS, refused, TLS); nothing was sent |
| 11 | a `--wait` gave up before the job finished |
| 12 | timeout: the request was sent but `--timeout` expired; it may have run and been billed, so check `spicrawl logs` before retrying |

`spicrawl exit-codes --json` prints the same table as `[{code, name, meaning}]`.

## Development

```sh
make build      # bin/spicrawl, version from git describe
make test lint
make snapshot   # goreleaser build into dist/, uploads nothing
make npm        # the single npm package into dist/npm/cli/ from the snapshot
```
