# Changelog

## Unreleased

Bug fixes from a review of 0.1.3. No new commands; two behaviour changes are marked.

- **Retries are safe.** A connection that drops after the request was sent now exits 12 ("may have run and been billed"), not 10 ("nothing was sent, safe to retry"). A gateway 5xx without an API problem document is retried only for GET/HEAD, and is no longer mistaken for the target's status under `--original-status`. A 301/302/303 on a POST is an error naming the Location instead of a silent GET. `Retry-After` waits are capped at 60 s and announced on stderr.
- **Ctrl-C exits 130** with "interrupted", including while `scrape -` waits on stdin. A mistyped subcommand (`spicrawl batch wiat`) is a usage error with a suggestion, not help and exit 0.
- **Keys:** a trailing CR/LF or space on the key (flag, env or file) is trimmed. The config file is written via a private temp file, short keys are fully masked, and commands that get the key and base URL from flags work without `$HOME`.
- **scrape:** `--body null` is a usage error, not a panic (also `batch submit`). Big integers survive `--json` (**key order now follows the API**, as `--jsonl` already did). Stdout write errors stop the run instead of exiting 0. `-o` writes atomically and **a target error page no longer overwrites the file**. `-o -` means stdout. Screenshot labels never overwrite each other. `--body` merges deeply. A BOM on stdin is stripped. Human output no longer HTML-escapes `<>&`. The multi-URL summary counts correctly. `--impersonate` help says it is on by default.
- **batch:** `wait` and `submit --wait` exit 4 when the job failed or was cancelled. Polling survives transient 5xx and network errors. `results -o` no longer empties an existing file on failure.
- **browser:** a server without the cloud browser says so (exit 8) instead of `NOT_FOUND`. `$(spicrawl browser url -q)` yields the bare URL.
- **usage summary** shows the enforced monthly allowance. **status** fails when the key check cannot reach the API.
- **mcp install / init** back up the client config to `.bak`, write through symlinks, refuse to replace a customised `spicrawl` entry without `--force`, and accept VS Code JSONC/BOM `mcp.json`.
- **Docs:** the embedded skill, `openapi.yaml` and schemas are synced with the API (cache hits are billed, `impersonate` defaults to true, batch honours `--format`).
- **install.sh** accepts `SPICRAWL_VERSION=0.1.3` as well as `v0.1.3` and explains a failed download. The npm launcher forwards Ctrl-C to the binary.

## 0.1.3 — 2026-10-05

- The repository and Go module moved to `github.com/OfficialSpicrawl/cli`. Install from source with `go install github.com/OfficialSpicrawl/cli/cmd/spicrawl@latest`.
- The install script and release downloads now come from this repository.
- No command changes.
