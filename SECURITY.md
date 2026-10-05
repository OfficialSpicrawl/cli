# Security policy

Please don't report security problems in public issues.

## Reporting a vulnerability

- **In this repository (the `spicrawl` CLI and `@spicrawl/cli`):** report it privately with [GitHub private vulnerability reporting](https://github.com/OfficialSpicrawl/cli/security/advisories/new).
- **In the Spicrawl service** (spicrawl.com, app.spicrawl.com, api.spicrawl.com, docs.spicrawl.com or mcp.spicrawl.com): email support@spicrawl.com with "Security" in the subject.

Include what you found, steps to reproduce it, the affected version (`spicrawl version`) and the impact you expect. We'll acknowledge the report, keep you updated while we fix it, and credit you in the release notes if you'd like.

## Supported versions

Fixes ship in the latest release on npm, so upgrade with `npm install -g @spicrawl/cli@latest`.

## Leaked API keys

If a Spicrawl API key was exposed (for example, committed to a public repository), revoke it in the dashboard at https://app.spicrawl.com and create a new one.
