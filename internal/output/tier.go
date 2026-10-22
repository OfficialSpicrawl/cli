package output

import (
	"regexp"
	"strings"
)

// The rendering tiers a customer sees. Which engine serves a request is
// internal; the API reports a tier and so does the CLI.
const (
	TierFetch   = "fetch"
	TierJS      = "js"
	TierBrowser = "browser"
)

// Tier is the public tier of an engine value, for human output. A tier maps to
// itself; a name from before tiers maps as the API does (api/internal/router
// Engine.Tier): moli, obscura and headless are js, fetch is fetch, and anything
// else fails closed to browser instead of leaking its name. "" stays "".
func Tier(engine string) string {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "":
		return ""
	case TierFetch:
		return TierFetch
	case TierJS, "moli", "obscura", "headless":
		return TierJS
	}
	return TierBrowser
}

// internalNames are the engine names a person never reads, the same set the API
// rewrites (router.PublicText). "moli" is a whole word so it cannot hit the
// middle of another word. Not "chrome", "playwright" or "puppeteer": those are
// also the domains and package names a page can legitimately mention.
var internalNames = regexp.MustCompile(`(?i)plain[ _-]?chrome|browserless|camoufox|chromium|obscura|headful|headless|\bmoli\b`)

// PublicText rewrites internal names in server prose to their tier, as a net
// under the API's own rewriting. Human output only: JSON stays as sent.
func PublicText(s string) string {
	return internalNames.ReplaceAllStringFunc(s, func(w string) string {
		switch strings.ToLower(w) {
		case "moli", "obscura", "headless":
			return TierJS
		}
		return TierBrowser
	})
}
