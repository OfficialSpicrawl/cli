package cmd

import (
	"net/http"
	"testing"
)

const gSavedKey = "spicrawl_live_0123456789abcdefghijklmnop"

func gSetupConfig(t *testing.T, e *goldenEnv) {
	goldenWrite(t, e.CfgDir, "config.json", `{"api_key":"`+gSavedKey+`","base_url":"https://api.example.test"}`)
}

func gSetupKeyOnlyConfig(t *testing.T, e *goldenEnv) {
	goldenWrite(t, e.CfgDir, "config.json", `{"api_key":"`+gSavedKey+`"}`)
}

// gAuthAPI answers the one call login and auth status make: GET /v1/requests?limit=1.
func gAuthAPI(status int) func() http.HandlerFunc {
	h := gJSON(200, `{"data":[],`+gLogPageEnd+`}`)
	if status == 401 {
		h = gProblem(401, "ERR::AUTH::INVALID_KEY", "the API key is not valid")
	}
	return goldenServe(map[string]http.HandlerFunc{"GET /v1/requests": h})
}

const gDocsHome = "# Spicrawl\n\n- [Quickstart](https://docs.spicrawl.com/quickstart.md)\n- [Anti-bot](https://docs.spicrawl.com/guides/anti-bot.md)\n"

func gDocsAPI() func() http.HandlerFunc {
	return goldenServe(map[string]http.HandlerFunc{
		"GET /llms.txt": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(gDocsHome))
		},
		"GET /guides/anti-bot.md": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/markdown")
			_, _ = w.Write([]byte("# Anti-bot\n\nUse --stealth only when a page needs it.\n"))
		},
		"GET /no-newline.md": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("# No newline")) },
	})
}

func init() {
	docsEnv := map[string]string{docsEnvURL: "{server}/"}

	goldenAdd(
		// config
		goldenCase{name: "config/get_all", args: []string{"config", "get"}, setup: gSetupConfig},
		goldenCase{name: "config/get_all_empty", args: []string{"config", "get"}},
		goldenCase{name: "config/get_base_url", args: []string{"config", "get", "base_url"}, setup: gSetupConfig},
		goldenCase{name: "config/get_api_key_masked", args: []string{"config", "get", "api_key"}, setup: gSetupConfig},
		goldenCase{name: "config/get_api_key_show_secret", args: []string{"config", "get", "api_key", "--show-secret"}, setup: gSetupConfig},
		goldenCase{name: "config/get_unset", args: []string{"config", "get", "base_url"}},
		goldenCase{name: "config/set_base_url", args: []string{"config", "set", "base_url", "http://localhost:8080/"}},
		goldenCase{name: "config/set_api_key", args: []string{"config", "set", "api_key", gSavedKey}},
		goldenCase{name: "config/unset_api_key", args: []string{"config", "unset", "api_key"}, setup: gSetupConfig},
		goldenCase{name: "config/path", args: []string{"config", "path"}},
		goldenCase{name: "config/err_unknown_key", args: []string{"config", "get", "nope"}, stderr: true},
		goldenCase{name: "config/err_bad_base_url", args: []string{"config", "set", "base_url", "ftp://x"}, stderr: true},
		goldenCase{name: "config_human/get_all", human: true, args: []string{"config", "get"}, setup: gSetupConfig},
		goldenCase{name: "config_human/get_all_empty", human: true, args: []string{"config", "get"}},
		goldenCase{name: "config_human/get_base_url", human: true, args: []string{"config", "get", "base_url"}, setup: gSetupConfig},
		goldenCase{name: "config_human/get_unset", human: true, args: []string{"config", "get", "base_url"}},
		goldenCase{name: "config_human/set_api_key", human: true, args: []string{"config", "set", "api_key", gSavedKey}},
		goldenCase{name: "config_human/unset_api_key", human: true, args: []string{"config", "unset", "api_key"}, setup: gSetupConfig},
		goldenCase{name: "config_human/path", human: true, args: []string{"config", "path"}},

		// auth
		goldenCase{name: "auth/status_ok", args: []string{"auth", "status"}, server: gAuthAPI(200)},
		goldenCase{name: "auth/status_offline", args: []string{"auth", "status", "--offline"}},
		goldenCase{name: "auth/status_key_from_file", args: []string{"auth", "status", "--offline"}, noKey: true, setup: gSetupKeyOnlyConfig},
		goldenCase{name: "auth/status_rejected_exit3", args: []string{"auth", "status"}, stderr: true, server: gAuthAPI(401)},
		goldenCase{name: "auth/status_no_key_exit3", args: []string{"auth", "status"}, noKey: true, stderr: true},
		goldenCase{name: "auth_human/status_ok", human: true, args: []string{"auth", "status"}, server: gAuthAPI(200)},
		goldenCase{name: "auth_human/status_rejected_exit3", human: true, args: []string{"auth", "status"}, server: gAuthAPI(401)},
		goldenCase{name: "auth_human/status_no_key_exit3", human: true, args: []string{"auth", "status"}, noKey: true},
		goldenCase{name: "auth/login_ok", args: []string{"login", "--api-key", gSavedKey}, server: gAuthAPI(200)},
		goldenCase{name: "auth/login_rejected_exit3", args: []string{"login", "--api-key", gSavedKey}, stderr: true, server: gAuthAPI(401)},
		goldenCase{name: "auth/login_needs_key_without_terminal", args: []string{"login"}, noKey: true, stderr: true, server: gAuthAPI(200)},
		goldenCase{name: "auth_human/login_ok", human: true, args: []string{"login", "--api-key", gSavedKey}, server: gAuthAPI(200)},
		goldenCase{name: "auth/logout_removed", args: []string{"logout"}, setup: gSetupConfig},
		goldenCase{name: "auth/logout_nothing_saved", args: []string{"logout"}},
		goldenCase{name: "auth_human/logout_removed", human: true, args: []string{"logout"}, setup: gSetupConfig},
		goldenCase{name: "auth_human/logout_nothing_saved", human: true, args: []string{"logout"}},

		// version, exit codes, root errors
		goldenCase{name: "meta/version", args: []string{"version"}},
		goldenCase{name: "meta/version_human", human: true, args: []string{"version"}},
		goldenCase{name: "meta/exit_codes", args: []string{"exit-codes"}},
		goldenCase{name: "meta/exit_codes_human", human: true, args: []string{"exit-codes"}},
		goldenCase{name: "meta/err_unknown_command", args: []string{"zzzzzz"}, stderr: true},
		goldenCase{name: "meta/err_unknown_subcommand_suggests", args: []string{"batch", "lst"}, stderr: true},
		goldenCase{name: "meta/err_unknown_flag", args: []string{"scrape", "https://example.com", "--nope"}, stderr: true},

		// schema: printed from the embedded files (no network)
		goldenCase{name: "schema/list", args: []string{"schema"}},
		goldenCase{name: "schema/list_flag", args: []string{"schema", "--list"}},
		goldenCase{name: "schema/session", args: []string{"schema", "session"}, digest: true},
		goldenCase{name: "schema/scrape", args: []string{"schema", "scrape"}, digest: true},
		goldenCase{name: "schema/batch", args: []string{"schema", "batch"}, digest: true},
		goldenCase{name: "schema/batch_item", args: []string{"schema", "batch-item"}, digest: true},
		goldenCase{name: "schema/err_unknown", args: []string{"schema", "nope"}, stderr: true},
		goldenCase{name: "schema_human/list", human: true, args: []string{"schema"}},
		goldenCase{name: "schema_human/session", human: true, args: []string{"schema", "session"}, digest: true},
		goldenCase{name: "schema_human/scrape", human: true, args: []string{"schema", "scrape"}, digest: true},
		goldenCase{name: "schema_human/batch", human: true, args: []string{"schema", "batch"}, digest: true},
		goldenCase{name: "schema_human/batch_item", human: true, args: []string{"schema", "batch-item"}, digest: true},

		// docs: the Markdown is the data, so it is printed as is whether or not stdout is a terminal
		goldenCase{name: "docs/topic_raw_markdown", args: []string{"docs", "guides/anti-bot"}, env: docsEnv, server: gDocsAPI()},
		goldenCase{name: "docs/list_raw_markdown", args: []string{"docs", "--list"}, env: docsEnv, server: gDocsAPI()},
		goldenCase{name: "docs/no_topic_is_the_index", args: []string{"docs"}, env: docsEnv, server: gDocsAPI()},
		goldenCase{name: "docs/adds_missing_newline", args: []string{"docs", "no-newline"}, env: docsEnv, server: gDocsAPI()},
		goldenCase{name: "docs/json_flag_wraps", args: []string{"docs", "guides/anti-bot"}, explicitJSON: true, env: docsEnv, server: gDocsAPI()},
		goldenCase{name: "docs/err_unknown_topic", args: []string{"docs", "missing"}, stderr: true, env: docsEnv, server: gDocsAPI()},
		goldenCase{name: "docs/err_bad_topic", args: []string{"docs", "../etc/passwd"}, stderr: true, env: docsEnv, server: gDocsAPI()},
		goldenCase{name: "docs_human/topic_raw_markdown", human: true, args: []string{"docs", "guides/anti-bot"}, env: docsEnv, server: gDocsAPI()},
	)
}
