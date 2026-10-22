package output

import "testing"

func TestTier(t *testing.T) {
	for in, want := range map[string]string{
		"": "", "fetch": "fetch", "FETCH": "fetch",
		"js": "js", "moli": "js", "Obscura": "js", "headless": "js",
		"browser": "browser", "chromium": "browser", "chrome": "browser", "camoufox": "browser",
		"browserless": "browser", "headful": "browser", "moli-chromium": "browser", "something-new": "browser",
	} {
		if got := Tier(in); got != want {
			t.Errorf("Tier(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPublicText(t *testing.T) {
	for in, want := range map[string]string{
		"served by Moli":                          "served by js",
		"Chromium crashed; retried on Obscura":    "browser crashed; retried on js",
		"plain Chrome and Browserless are slow":   "browser and browser are slow",
		"headful camoufox, headless obscura":      "browser browser, js js",
		"a molinillo and the evolution":           "a molinillo and the evolution",
		"see https://playwright.dev/docs":         "see https://playwright.dev/docs",
		"nothing to rewrite: 3 credits, fetch/js": "nothing to rewrite: 3 credits, fetch/js",
	} {
		if got := PublicText(in); got != want {
			t.Errorf("PublicText(%q) = %q, want %q", in, got, want)
		}
	}
}
