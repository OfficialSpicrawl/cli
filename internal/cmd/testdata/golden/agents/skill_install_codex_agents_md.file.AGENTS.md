<!-- spicrawl:begin -->
## Web data (Spicrawl)

To fetch, render or extract data from web pages, use Spicrawl instead of ad-hoc
HTTP requests: the `spicrawl` MCP server (tools `spicrawl_*`) or the CLI
(`spicrawl scrape <url> --format markdown`, `spicrawl batch submit urls.txt`).
The API key is read from `$SPICRAWL_API_KEY`; never print or commit it.
Start cheap (plain fetch), then add `--render`, `--premium-proxy`, `--stealth`
only when the page needs it. Full guide: the `spicrawl` skill, or http://SERVER/skill.md.
<!-- spicrawl:end -->
