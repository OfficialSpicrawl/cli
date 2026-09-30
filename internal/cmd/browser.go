package cmd

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Spicrawl/cli/internal/api"
	"github.com/Spicrawl/cli/internal/config"
)

var (
	browserEngine    string
	browserCountry   string
	browserRegion    string
	browserTTL       int
	browserHeadless  bool
	browserStickyKey string
)

var browserCmd = &cobra.Command{
	Use:   "browser",
	Short: "Cloud browsers over the Chrome DevTools Protocol",
	Example: `  spicrawl browser url --engine chromium --country de
  puppeteer_ws=$(spicrawl browser url --json | jq -r .url)`,
}

var browserURLCmd = &cobra.Command{
	Use:   "url",
	Short: "Mint a single-use wss:// CDP URL for a cloud browser (no API key in it)",
	Long: `Mint a connect URL for GET /v1/browser. Connect Puppeteer, Playwright or
any CDP client to it; the browser starts when the socket opens and is billed
from then.

The command calls POST /v1/browser/token with your API key in the
Authorization header and prints a URL that carries a short-lived token
instead of the key: it works ONCE, within 60 seconds, and only with the
options given here (changing them in the URL is refused). Mint a new URL for
every connection. Clients that can set headers on the upgrade can skip the
token and send "Authorization: Bearer <key>" to /v1/browser directly.

Unlike sessions, the exit rotates unless you pass --sticky-key.`,
	Example: `  spicrawl browser url
  spicrawl browser url --engine obscura --region eu --ttl 600
  spicrawl browser url --country us --headless=false --sticky-key crawl-1
  export SPICRAWL_BROWSER_URL="$(spicrawl browser url -q)"
  WS=$(spicrawl browser url --json | jq -r .url)`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		f := cmd.Flags()
		body := map[string]any{}
		engine := strings.ToLower(browserEngine)
		if f.Changed("engine") {
			if engine != "chromium" && engine != "obscura" {
				return Usagef("--engine must be chromium or obscura (fetch and camoufox do not speak CDP)")
			}
			body["engine"] = engine
		}
		if browserCountry != "" && browserRegion != "" {
			return Usagef("--country and --region cannot be combined")
		}
		if browserCountry != "" {
			if len(browserCountry) != 2 {
				return Usagef("--country must be a two-letter ISO 3166-1 code such as de")
			}
			body["proxy_country"] = strings.ToLower(browserCountry)
		}
		if browserRegion != "" {
			body["proxy_region"] = strings.ToLower(browserRegion)
		}
		if f.Changed("ttl") {
			if browserTTL < 60 || browserTTL > 900 {
				return Usagef("--ttl must be between 60 and 900 seconds")
			}
			body["session_ttl"] = browserTTL
		}
		if f.Changed("headless") {
			if !browserHeadless && engine == "obscura" {
				return Usagef("--headless=false is not available with --engine obscura")
			}
			body["headless"] = browserHeadless
		}
		if f.Changed("sticky-key") {
			if !browserStickyKeyOK(browserStickyKey) {
				return Usagef("--sticky-key must be 2-64 letters, digits or hyphens")
			}
			body["sticky_key"] = browserStickyKey
		}

		r, err := config.Resolve(flagAPIKey, flagBaseURL)
		if err != nil {
			return err
		}
		if r.APIKey == "" {
			return ErrNoKey
		}
		c, err := Client()
		if err != nil {
			return err
		}
		resp, err := c.Do(cmd.Context(), api.Request{Method: http.MethodPost, Path: "/v1/browser/token", Body: body})
		if err != nil {
			return err
		}
		var tok struct {
			Path      string `json:"path"`
			ExpiresAt string `json:"expires_at"`
		}
		if err := resp.Decode(&tok); err != nil {
			return err
		}
		ws, err := browserWSURL(r.BaseURL, tok.Path)
		if err != nil {
			return err
		}

		p := Printer()
		if strings.HasPrefix(ws, "ws://") {
			p.Warn("ws:// is unencrypted: the browser token in this URL travels in cleartext (use an https base URL)")
		}
		err = p.Result(map[string]any{"url": ws, "expires_at": tok.ExpiresAt, "single_use": true},
			func(w io.Writer) { fmt.Fprintln(w, ws) })
		if err != nil {
			return err
		}
		p.Info("the URL works once, within 60 s (until %s), with these options only", tok.ExpiresAt)
		if !p.JSON {
			p.Info("\n%s", browserHints)
		}
		return nil
	},
}

// browserHints shows how to connect. The URL is single-use, so mint it right
// before connecting.
const browserHints = `export SPICRAWL_BROWSER_URL="$(spicrawl browser url -q)"
Puppeteer:  const browser = await puppeteer.connect({ browserWSEndpoint: process.env.SPICRAWL_BROWSER_URL });
Playwright: const browser = await chromium.connectOverCDP(process.env.SPICRAWL_BROWSER_URL);`

// browserWSURL joins the API base URL and the token path the API returned,
// switching the scheme to ws/wss. The base URL is used rather than the API's
// own url field because the CLI may reach the API through a different host
// than the one the API sees.
func browserWSURL(base, path string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return "", Usagef("base URL %q is not a valid URL", base)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	case "ws", "wss":
	default:
		return "", Usagef("base URL %q must be http or https", base)
	}
	p, err := url.Parse(path)
	if err != nil || p.Path != "/v1/browser" || p.Query().Get("token") == "" {
		return "", fmt.Errorf("the API returned an unexpected browser token path")
	}
	u.Path = strings.TrimRight(u.Path, "/") + p.Path
	u.RawQuery = p.RawQuery
	return u.String(), nil
}

func browserStickyKeyOK(k string) bool {
	if len(k) < 2 || len(k) > 64 {
		return false
	}
	for _, r := range k {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

func init() {
	f := browserURLCmd.Flags()
	f.StringVar(&browserEngine, "engine", "chromium", "browser: chromium|obscura")
	f.StringVar(&browserCountry, "country", "", "exit country, ISO 3166-1 alpha-2 (proxy_country)")
	f.StringVar(&browserRegion, "region", "", "broad exit region such as eu (proxy_region)")
	f.IntVar(&browserTTL, "ttl", 180, "hard session lifetime in seconds, 60-900 (session_ttl)")
	f.BoolVar(&browserHeadless, "headless", true, "--headless=false attaches a display (chromium only)")
	f.StringVar(&browserStickyKey, "sticky-key", "", "pin the exit for the session (default: rotates)")

	browserCmd.AddCommand(browserURLCmd)
	rootCmd.AddCommand(browserCmd)
}
