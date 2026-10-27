package cmd

import (
	"net/http"
	"testing"
)

// ---- logs ----

const (
	gLogOK = `{"id":"01M0FK1EP2E7FBMWAR7W10SMXW","created_at":"2026-09-22T10:00:00Z","project_id":"proj_01J9Z","status":"success","http_status":200,"engine":"fetch",` +
		`"credits_micro":1500000,"url":"https://example.com/a","attempts":2,"bytes_in":2048,"bytes_out":512,"proxy_bytes":4096,` +
		`"spans":{"admission_ms":1,"dispatch_ms":2,"worker_ms":3,"queue_ms":0,"engine_ms":80,"upstream_ms":120,"extract_ms":null,"total_ms":210},` +
		`"proxy":{"source":"pool","reason":"default","provider":"dc-1","requested_tier":"datacenter","requested_country":"de","country":"de","endpoint_id":"ep-1","endpoints":["ep-1","ep-2"],"sticky":true}}`

	gLogBlocked = `{"id":"01M0FK1EP2E7FBMWAR7W10SMXX","created_at":"2026-09-22T10:00:01Z","project_id":"proj_01J9Z","status":"blocked","http_status":403,"engine":"",` +
		`"credits_micro":0,"url":"https://example.com/b?x=1&y=2","error_code":"ERR::UPSTREAM::BLOCKED","error_detail":"bot challenge",` +
		`"spans":{"admission_ms":1,"dispatch_ms":null,"worker_ms":null,"queue_ms":0,"engine_ms":null,"upstream_ms":120,"extract_ms":null,"total_ms":130},` +
		`"proxy":{"source":"pool"},"blocked":{"vendor":"cloudflare","rule":"r-17","signal":"cf-mitigated"}}`

	gLogPageEnd  = `"page":{"has_more":false,"retention_hours":24,"retained_since":"2026-09-21T10:00:00Z"}`
	gLogPageMore = `"page":{"has_more":true,"next_before":"2026-09-22T10:00:00.5Z","next_before_id":"01M0FK1EP2E7FBMWAR7W10SMXW","retention_hours":24,"retained_since":"2026-09-21T10:00:00Z"}`
)

// ---- status ----

func gStatusAPI(ready bool, keyStatus int, workers string) func() http.HandlerFunc {
	return goldenServe(map[string]http.HandlerFunc{
		"GET /readyz": func(w http.ResponseWriter, r *http.Request) {
			if ready {
				gJSON(200, `{"status":"ready","checks":{"postgres":"ok","redis":"ok"},"cdp":"enabled"}`)(w, r)
				return
			}
			gJSON(503, `{"status":"not_ready","checks":{"postgres":"ok","redis":"unreachable"},"cdp":"disabled"}`)(w, r)
		},
		"GET /v1/requests": func(w http.ResponseWriter, r *http.Request) {
			switch keyStatus {
			case 401:
				gProblem(401, "ERR::AUTH::INVALID_KEY", "the API key is not valid")(w, r)
			case 403:
				gProblem(403, "ERR::AUTH::INSUFFICIENT_SCOPE", "the key lacks the read scope")(w, r)
			default:
				gJSON(200, `{"data":[],`+gLogPageEnd+`}`)(w, r)
			}
		},
		"GET /v1/workers": gJSON(200, workers),
	})
}

// gStatusClient makes Claude Code look in use (~/.claude), with or without a
// Spicrawl MCP server in ~/.claude.json.
func gStatusClient(connected bool) func(t *testing.T, e *goldenEnv) {
	return func(t *testing.T, e *goldenEnv) {
		goldenWrite(t, e.Home, ".claude/settings.json", `{}`)
		if connected {
			goldenWrite(t, e.Home, ".claude.json", `{"mcpServers": {"spicrawl": {"type": "http", "url": "https://mcp.spicrawl.com/mcp"}}}`)
		}
	}
}

// gStatusAPIWorkersDown is a healthy API whose fleet endpoint fails.
func gStatusAPIWorkersDown() func() http.HandlerFunc {
	ok := gStatusAPI(true, 200, "")()
	return func() http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/workers" {
				gProblem(503, "ERR::INTERNAL::UNAVAILABLE", "fleet registry is down")(w, r)
				return
			}
			ok(w, r)
		}
	}
}

// gWorkersOK has every kind ready; gWorkers has a batch kind with none ready,
// which is a failed check.
const gWorkersOK = `{"kinds":[{"kind":"browser","present":3,"ready":3,"workers":[]},{"kind":"egress","present":1,"ready":1,"workers":[]},{"kind":"batch","present":2,"ready":2,"workers":[]}]}`

const gWorkers = `{"kinds":[{"kind":"browser","present":3,"ready":2,"workers":[]},{"kind":"egress","present":1,"ready":1,"workers":[]},{"kind":"batch","present":2,"ready":0,"workers":[]}]}`

// ---- usage ----

const (
	gUsageDay = `{"period":{"start":"2026-08-01","end":"2026-08-04","days":3,"bounds":"[start, end)"},"group_by":"day","groups":[` +
		`{"key":"2026-08-01","label":"","metrics":{"requests":10,"credits":12500000,"engine_ms":4200}},` +
		`{"key":"2026-08-02","label":"","metrics":{"requests":15,"credits":18500000,"engine_ms":4800}}],` +
		`"totals":{"requests":25,"credits":31000000,"engine_ms":9000},"unattributed":{"requests":0,"credits":0,"engine_ms":0},` +
		`"usage_as_of":"2026-09-22T06:00:00Z","stale_seconds":120,"warnings":["rollup is partial for 2026-08-03"]}`

	gUsageEngine = `{"period":{"start":"2026-08-01","end":"2026-09-01","days":31,"bounds":"[start, end)"},"group_by":"engine","groups":[` +
		`{"key":"fetch","label":"Fetch","metrics":{"requests":40,"credits":40000000}},{"key":"browser","label":"Browser rendering","metrics":{"requests":5,"credits":25000000}}],` +
		`"totals":{"requests":47,"credits":66000000},"unattributed":{"requests":2,"credits":1000000},"usage_as_of":null,"stale_seconds":7200}`

	gUsageKey = `{"period":{"start":"2026-08-01","end":"2026-09-01","days":31,"bounds":"[start, end)"},"group_by":"key","keys":[` +
		`{"key_id":"K1","name":"ci-key","prefix":"spicrawl_live_ab12","project_id":"proj_01J9Z","revoked":false,"days":[],"totals":{"requests":7,"client_cli":3}},` +
		`{"key_id":"K2","name":"old","prefix":"spicrawl_live_cd34","project_id":"proj_01J9Z","revoked":true,"days":[],"totals":{"requests":1}}]}`

	gUsageSummary = `{"period":{"start":"2026-09-01","end":"2026-10-01","days":30,"bounds":"[start, end)"},"period_elapsed_days":22,` +
		`"plan":{"code":"starter","name":"Starter","subscription_status":"active","period_source":"subscription"},` +
		`"credits":{"included_micro":50000000000,"used_micro":12345670000,"remaining_micro":37654330000,"overage_micro":0,"used_basis_points":2469},` +
		`"allowance":{"limit_micro":50000000000,"used_micro":12400000000,"remaining_micro":37600000000,"unlimited":false,"resets_at":"2026-10-01T00:00:00Z"},` +
		`"metrics":{"requests":1234,"credits":12345670000,"extractions":55},"warnings":["allowance counter is 40s behind"]}`

	gUsageRecon = `{"day":"2026-09-21","interpretation":"The request log holds no rows for the day; drift is not evidence of overbilling.",` +
		`"drift":[{"metric":"requests","billed":10,"derived":0}]}`
)

func init() {
	get := func(path, body string) func() http.HandlerFunc {
		return goldenServe(map[string]http.HandlerFunc{"GET " + path: gJSON(200, body)})
	}
	logs := func(body string) func() http.HandlerFunc { return get("/v1/requests", body) }
	both := `{"data":[` + gLogOK + `,` + gLogBlocked + `],` + gLogPageEnd + `}`
	logsPaged := goldenServe(map[string]http.HandlerFunc{"GET /v1/requests": func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("before") == "" {
			gJSON(200, `{"data":[`+gLogOK+`],`+gLogPageMore+`}`)(w, r)
			return
		}
		gJSON(200, `{"data":[`+gLogBlocked+`],`+gLogPageEnd+`}`)(w, r)
	}})
	logGet := func(body string) func() http.HandlerFunc { return get("/v1/requests/{id}", body) }

	goldenAdd(
		goldenCase{name: "logs/list", args: []string{"logs"}, server: logs(both)},
		goldenCase{name: "logs/errors", args: []string{"logs", "--errors", "--limit", "20"}, server: logs(`{"data":[` + gLogBlocked + `],` + gLogPageEnd + `}`)},
		goldenCase{name: "logs/jsonl", args: []string{"logs", "--jsonl"}, server: logs(both)},
		goldenCase{name: "logs/all_pages", args: []string{"logs", "--all"}, server: logsPaged},
		goldenCase{name: "logs/all_pages_jsonl", args: []string{"logs", "--all", "--jsonl"}, server: logsPaged},
		goldenCase{name: "logs/has_more", args: []string{"logs"}, server: logs(`{"data":[` + gLogOK + `],` + gLogPageMore + `}`)},
		goldenCase{name: "logs/empty", args: []string{"logs"}, server: logs(`{"data":[],` + gLogPageEnd + `}`)},
		goldenCase{name: "logs/get", args: []string{"logs", "get", "01M0FK1EP2E7FBMWAR7W10SMXW"}, server: logGet(gLogOK)},
		goldenCase{name: "logs/get_not_found", args: []string{"logs", "get", "nope"}, stderr: true,
			server: goldenServe(map[string]http.HandlerFunc{"GET /v1/requests/{id}": gProblem(404, "ERR::REQUEST::NOT_FOUND", "no such request")})},
		goldenCase{name: "logs/err_usage_status", args: []string{"logs", "--status", "weird"}, stderr: true},
		goldenCase{name: "logs/err_usage_errors_and_status", args: []string{"logs", "--errors", "--status", "failed"}, stderr: true},
		goldenCase{name: "logs_human/list", human: true, args: []string{"logs"}, server: logs(both)},
		goldenCase{name: "logs_human/all_projects", human: true, args: []string{"logs", "--all-projects", "--errors"}, server: logs(both)},
		goldenCase{name: "logs_human/has_more", human: true, args: []string{"logs"}, server: logs(`{"data":[` + gLogOK + `],` + gLogPageMore + `}`)},
		goldenCase{name: "logs_human/empty", human: true, args: []string{"logs"}, server: logs(`{"data":[],` + gLogPageEnd + `}`)},
		goldenCase{name: "logs_human/get_full", human: true, args: []string{"logs", "get", "x"}, server: logGet(gLogOK)},
		goldenCase{name: "logs_human/get_blocked", human: true, args: []string{"logs", "get", "x"}, server: logGet(gLogBlocked)},

		// status: the report is on stdout in every outcome, exit 0 / 3 / 10
		goldenCase{name: "status/ok", args: []string{"status"}, server: gStatusAPI(true, 200, gWorkersOK)},
		goldenCase{name: "status/not_ready", args: []string{"status"}, server: gStatusAPI(false, 200, gWorkers)},
		goldenCase{name: "status/worker_kind_down_exit1", args: []string{"status"}, server: gStatusAPI(true, 200, gWorkers)},
		goldenCase{name: "status/mcp_not_configured_exit1", args: []string{"status"}, server: gStatusAPI(true, 200, gWorkersOK), setup: gStatusClient(false)},
		goldenCase{name: "status/mcp_connected", args: []string{"status"}, server: gStatusAPI(true, 200, gWorkersOK), setup: gStatusClient(true)},
		goldenCase{name: "status/key_invalid_exit3", args: []string{"status"}, stderr: true, server: gStatusAPI(true, 401, gWorkers)},
		goldenCase{name: "status/key_without_read_scope", args: []string{"status"}, server: gStatusAPI(true, 403, gWorkersOK)},
		goldenCase{name: "status/no_key_exit3", args: []string{"status"}, noKey: true, stderr: true, server: gStatusAPI(true, 200, gWorkers)},
		goldenCase{name: "status/workers_error", args: []string{"status"}, server: gStatusAPIWorkersDown()},
		goldenCase{name: "status/unreachable_exit10", args: []string{"status"}, baseURL: "http://127.0.0.1:1"},
		goldenCase{name: "status_human/ok", human: true, args: []string{"status"}, server: gStatusAPI(true, 200, gWorkersOK)},
		goldenCase{name: "status_human/not_ready", human: true, args: []string{"status"}, server: gStatusAPI(false, 200, gWorkers)},
		goldenCase{name: "status_human/mcp_not_configured_exit1", human: true, args: []string{"status"}, server: gStatusAPI(true, 200, gWorkersOK), setup: gStatusClient(false)},
		goldenCase{name: "status_human/key_invalid_exit3", human: true, args: []string{"status"}, server: gStatusAPI(true, 401, gWorkers)},
		goldenCase{name: "status_human/no_key_exit3", human: true, args: []string{"status"}, noKey: true, server: gStatusAPI(true, 200, gWorkers)},
		goldenCase{name: "status_human/unreachable_exit10", human: true, args: []string{"status"}, baseURL: "http://127.0.0.1:1"},

		// usage
		goldenCase{name: "usage/by_day", args: []string{"usage", "--from", "2026-08-01", "--to", "2026-08-04"}, server: get("/v1/usage", gUsageDay)},
		goldenCase{name: "usage/by_key", args: []string{"usage", "--group-by", "key"}, server: get("/v1/usage", gUsageKey)},
		goldenCase{name: "usage/summary", args: []string{"usage", "summary"}, server: get("/v1/usage/summary", gUsageSummary)},
		goldenCase{name: "usage/reconciliation", args: []string{"usage", "reconciliation", "--day", "2026-09-21"}, server: get("/v1/usage/reconciliation", gUsageRecon)},
		goldenCase{name: "usage/err_scope", args: []string{"usage"}, stderr: true,
			server: goldenServe(map[string]http.HandlerFunc{"GET /v1/usage": gProblem(403, "ERR::AUTH::INSUFFICIENT_SCOPE", "the key lacks the read scope")})},
		goldenCase{name: "usage/err_usage_to_before_from", args: []string{"usage", "--from", "2026-09-01", "--to", "2026-09-01"}, stderr: true},
		goldenCase{name: "usage_human/by_day", human: true, args: []string{"usage"}, server: get("/v1/usage", gUsageDay)},
		goldenCase{name: "usage_human/by_engine_unattributed", human: true, args: []string{"usage", "--group-by", "engine"}, server: get("/v1/usage", gUsageEngine)},
		goldenCase{name: "usage_human/by_key", human: true, args: []string{"usage", "--group-by", "key"}, server: get("/v1/usage", gUsageKey)},
		goldenCase{name: "usage_human/summary", human: true, args: []string{"usage", "summary"}, server: get("/v1/usage/summary", gUsageSummary)},
		goldenCase{name: "usage_human/reconciliation", human: true, args: []string{"usage", "reconciliation"}, server: get("/v1/usage/reconciliation", gUsageRecon)},
		goldenCase{name: "usage_human/err_scope", human: true, args: []string{"usage"},
			server: goldenServe(map[string]http.HandlerFunc{"GET /v1/usage": gProblem(403, "ERR::AUTH::INSUFFICIENT_SCOPE", "the key lacks the read scope")})},

		// browser url
		goldenCase{name: "browser/url", args: []string{"browser", "url", "--country", "de"}, server: gBrowser(200)},
		goldenCase{name: "browser/url_quiet_prints_bare_url", args: []string{"-q", "browser", "url"}, server: gBrowser(200)},
		goldenCase{name: "browser/url_quiet_json", args: []string{"-q", "--json", "browser", "url"}, server: gBrowser(200)},
		goldenCase{name: "browser/not_enabled_exit8", args: []string{"browser", "url"}, stderr: true, server: gBrowser(501)},
		goldenCase{name: "browser/err_usage_ttl", args: []string{"browser", "url", "--ttl", "5"}, stderr: true},
		goldenCase{name: "browser/err_no_key", args: []string{"browser", "url"}, noKey: true, stderr: true, server: gBrowser(200)},
		goldenCase{name: "browser_human/url", human: true, args: []string{"browser", "url"}, server: gBrowser(200)},
	)
}

func gBrowser(status int) func() http.HandlerFunc {
	h := gJSON(200, `{"path":"/v1/browser?token=wbt_testtoken&engine=browser&proxy_country=de","expires_at":"2026-09-22T06:01:00Z"}`)
	if status == 501 {
		h = gProblem(501, "ERR::ENGINE::UNAVAILABLE", "cloud browser is not enabled")
	}
	return goldenServe(map[string]http.HandlerFunc{"POST /v1/browser/token": h})
}
