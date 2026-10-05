package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/OfficialSpicrawl/cli/internal/api"
	"github.com/OfficialSpicrawl/cli/internal/config"
)

const (
	// docsPublicURL is the hosted docs site. Its pages are at the root of the
	// host (https://docs.spicrawl.com/quickstart.md), not under /docs.
	docsPublicURL = "https://docs.spicrawl.com"
	// docsEnvURL is the docs base URL, pages appended directly:
	// http(s)://host[:port][/path], e.g. https://spicrawl.example.com/docs.
	docsEnvURL = "SPICRAWL_DOCS_URL"
	// docsEnvHost is the legacy setting: an origin whose /docs holds the docs.
	// Read only when $SPICRAWL_DOCS_URL is unset, and it keeps that meaning.
	docsEnvHost = "SPICRAWL_DOCS_HOST"
	// docsHostPath is where a self-hosted API (and a legacy docs host) serves
	// the docs.
	docsHostPath = "/docs"
	docsMaxBytes = 8 << 20
)

var docsList bool

var docsCmd = &cobra.Command{
	Use:   "docs [topic]",
	Short: "Print Spicrawl documentation as Markdown",
	Long: `Fetch a documentation page as Markdown from the docs site and print it.
The docs URL is $SPICRAWL_DOCS_URL if set (e.g. https://spicrawl.example.com/docs),
else the legacy $SPICRAWL_DOCS_HOST + /docs, else derived from the API base URL
(--base-url, $SPICRAWL_BASE_URL or the config file): https://docs.spicrawl.com
for the hosted API, <API origin>/docs for a self-hosted one.

A topic is the page's path on the docs site without the extension, e.g.
"quickstart" or "guides/anti-bot". --list (or no topic) prints the site's
llms.txt index, which names every page. No API key is needed.

The Markdown is the data, so it is printed as is on a terminal AND when stdout
is redirected or piped: "spicrawl docs quickstart > quickstart.md" writes a
Markdown file. Unlike other commands, docs does not switch to JSON when stdout
is not a terminal; only --json wraps the page as {"topic", "url", "markdown"}.
An unknown topic exits 2.`,
	Example: `  spicrawl docs --list
  spicrawl docs quickstart
  spicrawl docs guides/anti-bot > anti-bot.md
  spicrawl docs errors --json | jq -r .markdown`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		base, err := docsResolveURL()
		if err != nil {
			return err
		}
		topic := "llms.txt"
		u := base + "/llms.txt"
		if !docsList && len(args) == 1 {
			topic = strings.Trim(strings.TrimSuffix(strings.TrimSpace(args[0]), ".md"), "/")
			if topic == "" || strings.Contains(topic, "..") || strings.ContainsAny(topic, "?#\\ ") {
				return Usagef("invalid docs topic %q: use a page path like guides/anti-bot (see spicrawl docs --list)", args[0])
			}
			u = base + "/" + topic + ".md"
		}
		md, err := docsFetch(rootCmd.Context(), u)
		if err != nil {
			return err
		}
		p := Printer()
		// Only an explicit --json wraps the page: the Markdown is the data.
		if flagJSON {
			return p.Value(map[string]any{"topic": topic, "url": u, "markdown": md})
		}
		if _, err := io.WriteString(p.Out, md); err != nil {
			return err
		}
		if !strings.HasSuffix(md, "\n") {
			_, err = io.WriteString(p.Out, "\n")
		}
		return err
	},
}

func init() {
	docsCmd.Flags().BoolVar(&docsList, "list", false, "print the index of all pages (llms.txt)")
	rootCmd.AddCommand(docsCmd)
}

func docsFetch(ctx context.Context, u string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", Usagef("bad docs URL %q: %v", u, err)
	}
	req.Header.Set("Accept", "text/markdown, text/plain;q=0.9, */*;q=0.1")
	req.Header.Set("User-Agent", "spicrawl-cli/"+api.Version)
	resp, err := (&http.Client{Timeout: flagTimeout}).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", &api.NetworkError{Err: err}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, docsMaxBytes))
	if err != nil {
		return "", &api.NetworkError{Err: fmt.Errorf("read %s: %w", u, err)}
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return "", Usagef("no docs page at %s (see spicrawl docs --list)", u)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return "", fmt.Errorf("docs site answered %s for %s", resp.Status, u)
	}
	return string(b), nil
}

// docsResolveURL picks the docs base URL: an override from the environment,
// else the one derived from the resolved API base URL, else the hosted docs.
func docsResolveURL() (string, error) {
	if u, err := docsOverride(); u != "" || err != nil {
		return u, err
	}
	r, err := config.Resolve(flagAPIKey, flagBaseURL)
	if err != nil {
		return "", err
	}
	if origin, err := agentOrigin(r.BaseURL); err == nil {
		return docsDerived(origin), nil
	}
	return docsPublicURL, nil
}

// docsOverride returns the docs base URL the environment sets, or "":
// $SPICRAWL_DOCS_URL, else the legacy $SPICRAWL_DOCS_HOST + /docs.
func docsOverride() (string, error) {
	if v := strings.TrimSpace(os.Getenv(docsEnvURL)); v != "" {
		u, err := url.Parse(v)
		switch {
		case err != nil:
			return "", Usagef("invalid $%s %q: %v", docsEnvURL, v, err)
		case (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
			return "", Usagef("invalid $%s %q: want an absolute http:// or https:// URL such as %s", docsEnvURL, v, docsPublicURL)
		case u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(v, "#"):
			return "", Usagef("invalid $%s %q: want a base URL, scheme://host[:port][/path], with no credentials, query or fragment", docsEnvURL, v)
		}
		return strings.TrimRight(v, "/"), nil
	}
	if host := strings.TrimRight(strings.TrimSpace(os.Getenv(docsEnvHost)), "/"); host != "" {
		return host + docsHostPath, nil
	}
	return "", nil
}

// docsDerived is the docs base URL for an API origin when the environment
// sets none: the hosted docs for the hosted API (config.DefaultBase), else
// <origin>/docs, where a self-hosted API serves its docs build.
func docsDerived(origin string) string {
	if origin == config.DefaultBase {
		return docsPublicURL
	}
	return origin + docsHostPath
}
