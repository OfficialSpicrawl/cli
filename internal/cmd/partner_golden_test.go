package cmd

import (
	"net/http"
)

// Golden cases for the partner commands. Their files under testdata/golden are
// not written by hand: generate them with
//
//	GOLDEN_UPDATE=1 go test ./internal/cmd/ -run 'TestGolden/(partner|partner_human|auth.status_partner_key)'
//
// then review the diff. TestGolden runs each case in three environments and
// TestJSONStderrIsOneProblemDocument covers every JSON case, so nothing here
// needs registering elsewhere.

const (
	pgKey = "spicrawl_live_WSKEY0123456789abcdefghijk" // the workspace key the fake API returns

	pgWorkspace = `{"workspace_id":"01JDMZ4Q8V0W6Z1F2N4T5R7X9A","external_user_id":"user-42","display_name":"Jane","status":"active",` +
		`"created_at":"2026-11-18T09:00:00Z","free_credits":250,"wallet_cap":500,"wallet_cap_source":"tier","window_resets_at":"2026-12-18T00:00:00Z",` +
		`"wallet_used":120,"wallet_used_source":"live","free_used":250,"own_credits":50,` +
		`"tier":{"id":"01JDN2K7M3Q9R8T6V4W1X0Y5Z2","name":"Standard"},"requests_per_minute":60,"concurrency":2}`

	pgWorkspaceNoTier = `{"workspace_id":"01JDM0B9Q8V0W6Z1F2N4T5R7X8","external_user_id":"user-43","display_name":null,"status":"suspended",` +
		`"created_at":"2026-11-02T08:15:00Z","free_credits":250,"wallet_cap":null,"wallet_cap_source":"none","window_resets_at":"2026-12-02T00:00:00Z",` +
		`"wallet_used":0,"wallet_used_source":"rollup","free_used":0,"own_credits":0,"tier":null,"requests_per_minute":null,"concurrency":null}`

	pgCreated = `{"workspace_id":"01JDMZ4Q8V0W6Z1F2N4T5R7X9A","external_user_id":"user-42","display_name":null,"status":"active",` +
		`"created_at":"2026-11-18T09:00:00Z","free_credits":250,"wallet_cap":500,"wallet_cap_source":"tier","window_resets_at":"2026-12-18T00:00:00Z",` +
		`"wallet_used":null,"wallet_used_source":null,"free_used":null,"own_credits":null,` +
		`"tier":{"id":"01JDN2K7M3Q9R8T6V4W1X0Y5Z2","name":"Standard"},"requests_per_minute":60,"concurrency":2,"created":true,"api_key":"` + pgKey + `"}`

	pgExisting = `{"workspace_id":"01JDMZ4Q8V0W6Z1F2N4T5R7X9A","external_user_id":"acct:7.eu_west","display_name":null,"status":"active",` +
		`"created_at":"2026-11-18T09:00:00Z","free_credits":250,"wallet_cap":null,"wallet_cap_source":"none","window_resets_at":"2026-12-18T00:00:00Z",` +
		`"wallet_used":null,"wallet_used_source":null,"free_used":null,"own_credits":null,"tier":null,"requests_per_minute":null,"concurrency":null,` +
		`"created":false,"api_key":"` + pgKey + `"}`

	pgTier = `{"id":"01JDN2K7M3Q9R8T6V4W1X0Y5Z2","name":"Standard","requests_per_minute":60,"concurrency":2,"free_credits":250,` +
		`"wallet_cap":500,"default":true,"workspaces":3,"created_at":"2026-11-19T10:00:00Z","updated_at":"2026-11-19T10:00:00Z"}`

	pgTierList = `{"tiers":[` + pgTier + `]}`

	pgSummary = `{"partner":true,"org_id":"0b6f3c1e-2d4a-4e8b-9f10-6a7c8d9e0f12","free_credits":250,"workspace_daily_limit":50,"workspace_count":3,` +
		`"limits":{"max_requests_per_minute":600,"max_concurrency":10,"max_free_credits":1000},` +
		`"users":{"total":3,"active":2,"suspended":1},"caps":{"committed":1000,"uncapped_users":0},` +
		`"balance":{"funded_credits":1000,"credit_line_credits":0,"spent_credits":120,"balance_credits":880,"available_credits":880}}`

	pgWallet = `{"funded_credits":1000,"credit_line_credits":0,"spent_credits":120,"balance_credits":880,"available_credits":880,` +
		`"funded_micro":1000000000,"credit_line_micro":0,"spent_micro":120000000,"balance_micro":880000000,"available_micro":880000000}`

	pgUsageDay = `{"period":{"start":"2026-11-01","end":"2026-11-03","bounds":"[start, end)","days":2},"group_by":"day",` +
		`"totals":{"requests":14,"requests_failed":1,"credits_micro":14000000},` +
		`"rows":[{"day":"2026-11-01","requests":5,"requests_failed":0,"credits_micro":5000000},{"day":"2026-11-02","requests":9,"requests_failed":1,"credits_micro":9000000}]}`

	pgUsageUser = `{"period":{"start":"2026-11-01","end":"2026-12-01","bounds":"[start, end)","days":30},"group_by":"workspace",` +
		`"totals":{"requests":14,"requests_failed":1,"credits_micro":14000000,"credits_wallet_micro":4000000,"credits_own_micro":5500000},` +
		`"rows":[{"external_user_id":"user-42","requests":9,"requests_failed":1,"credits_micro":9000000,"credits_wallet_micro":2500000,"credits_own_micro":5000000},` +
		`{"external_user_id":"user-41","requests":5,"requests_failed":0,"credits_micro":5000000,"credits_wallet_micro":1500000,"credits_own_micro":500000}],` +
		`"page":{"total":2,"limit":2,"offset":0,"next_offset":null}}`

	pgUserDay = `{"external_user_id":"user-42","period":{"start":"2026-11-01","end":"2026-11-03","bounds":"[start, end)","days":2},` +
		`"totals":{"requests":5,"requests_failed":0,"credits_micro":5000000},` +
		`"rows":[{"day":"2026-11-01","requests":5,"requests_failed":0,"credits_micro":5000000},{"day":"2026-11-02","requests":0,"requests_failed":0,"credits_micro":0}]}`

	pgGiven = `{"external_user_id":"user-42","credits":50,"applied":true,"user_credits":50,"balance_available":830}`

	pgGivenReplay = `{"external_user_id":"user-42","credits":50,"applied":false,"user_credits":50,"balance_available":830}`

	pgKeyPartner = `{"key_id":"01JDQ0000000000000000000AB","name":"Default","prefix":"spicrawl_live_WSKEY","scopes":["partner","read"],` +
		`"environment":"live","organization":{"id":"01JDN0000000000000000000CD","name":"partner"},"partner":true,"workspace_of_partner":false,` +
		`"created_at":"2026-11-18T09:00:00Z","expires_at":null}`
)

// pgQuery answers only when every parameter in want is set to its value (a
// stand-in for "the CLI sent the right query"); otherwise a 400.
func pgQuery(want map[string]string, status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.URL.Query()
		for k, v := range want {
			if got.Get(k) != v {
				writeProblem(w, http.StatusBadRequest, "ERR::REQUEST::INVALID_PARAMETER", false)
				return
			}
		}
		gJSON(status, body)(w, r)
	}
}

// pgPath answers only when the request's escaped path is want.
func pgPath(want string, status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != want {
			writeProblem(w, http.StatusBadRequest, "ERR::REQUEST::INVALID_PARAMETER", false)
			return
		}
		gJSON(status, body)(w, r)
	}
}

// pgUsers is the list route, with the first page holding a next_after.
var pgUsers = goldenServe(map[string]http.HandlerFunc{
	"GET /v1/partner/workspaces": func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("after") == "" {
			gJSON(200, `{"workspaces":[`+pgWorkspace+`],"next_after":"user-42"}`)(w, r)
			return
		}
		gJSON(200, `{"workspaces":[`+pgWorkspaceNoTier+`]}`)(w, r)
	},
})

func init() {
	usageDay := goldenServe(map[string]http.HandlerFunc{"GET /v1/partner/usage": pgQuery(map[string]string{"group_by": "day", "from": "2026-11-01", "to": "2026-11-03"}, 200, pgUsageDay)})
	usageUser := goldenServe(map[string]http.HandlerFunc{"GET /v1/partner/usage": pgQuery(map[string]string{"group_by": "workspace", "sort": "credits", "limit": "2"}, 200, pgUsageUser)})

	goldenAdd(
		// summary, balance, usage
		goldenCase{name: "partner/summary", args: []string{"partner", "summary"}, server: goldenServe(map[string]http.HandlerFunc{"GET /v1/partner/summary": gJSON(200, pgSummary)})},
		goldenCase{name: "partner/summary_ordinary_key", args: []string{"partner", "summary"}, stderr: true,
			server: goldenServe(map[string]http.HandlerFunc{"GET /v1/partner/summary": gProblem(403, "ERR::AUTH::INSUFFICIENT_SCOPE", "this key cannot call the partner routes")})},
		goldenCase{name: "partner/balance", args: []string{"partner", "balance"}, server: goldenServe(map[string]http.HandlerFunc{"GET /v1/partner/wallet": gJSON(200, pgWallet)})},
		goldenCase{name: "partner/usage_day", args: []string{"partner", "usage", "--from", "2026-11-01", "--to", "2026-11-03"}, server: usageDay},
		goldenCase{name: "partner/usage_by_user", args: []string{"partner", "usage", "--by", "user", "--sort", "credits", "--limit", "2"}, server: usageUser},
		goldenCase{name: "partner/usage_bad_window", args: []string{"partner", "usage", "--from", "2026-11-03", "--to", "2026-11-01"}, stderr: true, server: pgNoCalls()},
		goldenCase{name: "partner/usage_limit_needs_by_user", args: []string{"partner", "usage", "--limit", "5"}, stderr: true, server: pgNoCalls()},

		// users: list, create, get
		goldenCase{name: "partner/users_list", args: []string{"partner", "users", "list"}, server: pgUsers},
		goldenCase{name: "partner/users_list_all", args: []string{"partner", "users", "list", "--all"}, server: pgUsers},
		goldenCase{name: "partner/users_list_empty", args: []string{"partner", "users", "list", "--query", "zz"},
			server: goldenServe(map[string]http.HandlerFunc{"GET /v1/partner/workspaces": gJSON(200, `{"workspaces":[]}`)})},
		goldenCase{name: "partner/users_list_used_sort_offset", args: []string{"partner", "users", "list", "--sort", "used", "--offset", "2", "--query", "user-4", "--limit", "5"},
			server: goldenServe(map[string]http.HandlerFunc{"GET /v1/partner/workspaces": pgQuery(map[string]string{"sort": "wallet_used", "offset": "2", "q": "user-4", "limit": "5"}, 200, `{"workspaces":[`+pgWorkspace+`],"next_offset":3}`)})},
		goldenCase{name: "partner/users_list_after_with_used_sort", args: []string{"partner", "users", "list", "--sort", "used", "--after", "user-1"}, stderr: true, server: pgNoCalls()},
		goldenCase{name: "partner/users_create", args: []string{"partner", "users", "create", "user-42", "--tier", "Standard"},
			server: goldenServe(map[string]http.HandlerFunc{"PUT /v1/partner/workspaces/{id}": pgPath("/v1/partner/workspaces/user-42", 201, pgCreated)})},
		goldenCase{name: "partner/users_create_existing_escaped_id", args: []string{"partner", "users", "create", "acct:7.eu_west"},
			server: goldenServe(map[string]http.HandlerFunc{"PUT /v1/partner/workspaces/{id}": pgPath("/v1/partner/workspaces/acct%3A7.eu_west", 200, pgExisting)})},
		goldenCase{name: "partner/users_create_daily_limit", args: []string{"partner", "users", "create", "user-42"}, stderr: true,
			server: goldenServe(map[string]http.HandlerFunc{"PUT /v1/partner/workspaces/{id}": gProblem(429, "ERR::LIMIT::PARTNER_WORKSPACE_LIMIT", "This partner has used its daily allowance of new workspaces. It resets at 00:00 UTC.")})},
		goldenCase{name: "partner/users_create_bad_id", args: []string{"partner", "users", "create", "jane@example.com"}, stderr: true, server: pgNoCalls()},
		goldenCase{name: "partner/users_get", args: []string{"partner", "users", "get", "user-42"}, server: goldenServe(map[string]http.HandlerFunc{"GET /v1/partner/workspaces/{id}": gJSON(200, pgWorkspace)})},

		// users: rename, suspend, reactivate, tier, usage
		goldenCase{name: "partner/users_rename", args: []string{"partner", "users", "rename", "user-42", "Jane"}, server: goldenServe(map[string]http.HandlerFunc{"PATCH /v1/partner/workspaces/{id}": gJSON(200, pgWorkspace)})},
		goldenCase{name: "partner/users_rename_clear", args: []string{"partner", "users", "rename", "user-42", "--clear"}, server: goldenServe(map[string]http.HandlerFunc{"PATCH /v1/partner/workspaces/{id}": gJSON(200, pgWorkspace)})},
		goldenCase{name: "partner/users_rename_name_and_clear", args: []string{"partner", "users", "rename", "user-42", "Jane", "--clear"}, stderr: true, server: pgNoCalls()},
		goldenCase{name: "partner/users_suspend_yes", args: []string{"partner", "users", "suspend", "user-42", "--yes"}, server: goldenServe(map[string]http.HandlerFunc{"POST /v1/partner/workspaces/{id}/suspend": gJSON(200, pgWorkspace)})},
		goldenCase{name: "partner/users_suspend_needs_yes", args: []string{"partner", "users", "suspend", "user-42"}, stderr: true, server: pgNoCalls()},
		goldenCase{name: "partner/users_reactivate", args: []string{"partner", "users", "reactivate", "user-42"}, server: goldenServe(map[string]http.HandlerFunc{"POST /v1/partner/workspaces/{id}/reactivate": gJSON(200, pgWorkspace)})},
		goldenCase{name: "partner/users_tier", args: []string{"partner", "users", "tier", "user-42", "Standard"}, server: goldenServe(map[string]http.HandlerFunc{"PUT /v1/partner/workspaces/{id}/tier": gJSON(200, pgWorkspace)})},
		goldenCase{name: "partner/users_usage", args: []string{"partner", "users", "usage", "user-42", "--from", "2026-11-01", "--to", "2026-11-03"},
			server: goldenServe(map[string]http.HandlerFunc{"GET /v1/partner/workspaces/{id}/usage": gJSON(200, pgUserDay)})},

		// users: give
		goldenCase{name: "partner/users_give_provided_key", args: []string{"partner", "users", "give", "user-42", "50", "--idempotency-key", "grant-2026-11-20-42", "--yes"},
			server: goldenServe(map[string]http.HandlerFunc{"POST /v1/partner/workspaces/{id}/credits": gJSON(200, pgGiven)})},
		goldenCase{name: "partner/users_give_not_applied", args: []string{"partner", "users", "give", "user-42", "50", "--idempotency-key", "grant-2026-11-20-42", "--yes"},
			server: goldenServe(map[string]http.HandlerFunc{"POST /v1/partner/workspaces/{id}/credits": gJSON(200, pgGivenReplay)})},
		goldenCase{name: "partner/users_give_402", args: []string{"partner", "users", "give", "user-42", "5000", "--yes"}, stderr: true,
			server: goldenServe(map[string]http.HandlerFunc{"POST /v1/partner/workspaces/{id}/credits": gProblem(402, "ERR::LIMIT::QUOTA_EXCEEDED", "The most you can give is 880 credits.")})},
		goldenCase{name: "partner/users_give_409", args: []string{"partner", "users", "give", "user-42", "60", "--idempotency-key", "grant-2026-11-20-42", "--yes"}, stderr: true,
			server: goldenServe(map[string]http.HandlerFunc{"POST /v1/partner/workspaces/{id}/credits": gProblem(409, "ERR::REQUEST::CONFLICT", "This idempotency_key was already used for a different assignment (another amount or user). Use a new key.")})},
		goldenCase{name: "partner/users_give_needs_yes", args: []string{"partner", "users", "give", "user-42", "50"}, stderr: true, server: pgNoCalls()},
		goldenCase{name: "partner/users_give_short_key", args: []string{"partner", "users", "give", "user-42", "50", "--idempotency-key", "short", "--yes"}, stderr: true, server: pgNoCalls()},

		// users: create output modes (JSON is the default for a script)
		goldenCase{name: "partner/users_create_quiet_bare_key", args: []string{"partner", "users", "create", "user-42", "-q"},
			server: goldenServe(map[string]http.HandlerFunc{"PUT /v1/partner/workspaces/{id}": gJSON(201, pgCreated)})},

		// tiers
		goldenCase{name: "partner/tiers_list", args: []string{"partner", "tiers", "list"}, server: goldenServe(map[string]http.HandlerFunc{"GET /v1/partner/tiers": gJSON(200, pgTierList)})},
		goldenCase{name: "partner/tiers_create", args: []string{"partner", "tiers", "create", "Standard", "--requests-per-minute", "60", "--concurrency", "2", "--free-credits", "250", "--cap", "500", "--default"},
			server: goldenServe(map[string]http.HandlerFunc{"POST /v1/partner/tiers": gJSON(201, pgTier)})},
		goldenCase{name: "partner/tiers_create_needs_cap_or_no_cap", args: []string{"partner", "tiers", "create", "Standard", "--requests-per-minute", "60", "--concurrency", "2", "--free-credits", "250"},
			stderr: true, server: pgNoCalls()},
		goldenCase{name: "partner/tiers_create_missing_flag", args: []string{"partner", "tiers", "create", "Standard", "--requests-per-minute", "60", "--free-credits", "250", "--no-cap"},
			stderr: true, server: pgNoCalls()},
		goldenCase{name: "partner/tiers_update_rename", args: []string{"partner", "tiers", "update", "standard", "--rename", "Pro"}, server: goldenServe(map[string]http.HandlerFunc{
			"GET /v1/partner/tiers":             gJSON(200, pgTierList),
			"PATCH /v1/partner/tiers/{tier_id}": gJSON(200, pgTier),
		})},
		goldenCase{name: "partner/tiers_update_nothing", args: []string{"partner", "tiers", "update", "Standard"}, stderr: true, server: pgNoCalls()},
		goldenCase{name: "partner/tiers_default", args: []string{"partner", "tiers", "default", "Standard"}, server: goldenServe(map[string]http.HandlerFunc{
			"GET /v1/partner/tiers":             gJSON(200, pgTierList),
			"PATCH /v1/partner/tiers/{tier_id}": gJSON(200, pgTier),
		})},
		goldenCase{name: "partner/tiers_delete_yes", args: []string{"partner", "tiers", "delete", "Standard", "--yes"}, server: goldenServe(map[string]http.HandlerFunc{
			"GET /v1/partner/tiers":              gJSON(200, pgTierList),
			"DELETE /v1/partner/tiers/{tier_id}": gJSON(204, ""),
		})},
		goldenCase{name: "partner/tiers_delete_refused", args: []string{"partner", "tiers", "delete", "Standard", "--yes"}, stderr: true, server: goldenServe(map[string]http.HandlerFunc{
			"GET /v1/partner/tiers":              gJSON(200, pgTierList),
			"DELETE /v1/partner/tiers/{tier_id}": gProblem(409, "ERR::REQUEST::CONFLICT", "3 workspaces are on this tier. Move them to another tier first."),
		})},
		goldenCase{name: "partner/tiers_delete_needs_yes", args: []string{"partner", "tiers", "delete", "Standard"}, stderr: true, server: pgNoCalls()},

		// auth status: the key type, when GET /v1/key answers
		goldenCase{name: "auth/status_partner_key", args: []string{"auth", "status"}, server: goldenServe(map[string]http.HandlerFunc{
			"GET /v1/requests": gJSON(200, `{"data":[],`+gLogPageEnd+`}`),
			"GET /v1/key":      gJSON(200, pgKeyPartner),
		})},

		// ---- human (terminal) variants ----
		goldenCase{name: "partner_human/summary", human: true, args: []string{"partner", "summary"}, server: goldenServe(map[string]http.HandlerFunc{"GET /v1/partner/summary": gJSON(200, pgSummary)})},
		goldenCase{name: "partner_human/balance", human: true, args: []string{"partner", "balance"}, server: goldenServe(map[string]http.HandlerFunc{"GET /v1/partner/wallet": gJSON(200, pgWallet)})},
		goldenCase{name: "partner_human/usage_day", human: true, args: []string{"partner", "usage", "--from", "2026-11-01", "--to", "2026-11-03"}, server: usageDay},
		goldenCase{name: "partner_human/usage_by_user", human: true, args: []string{"partner", "usage", "--by", "user", "--sort", "credits", "--limit", "2"}, server: usageUser},
		goldenCase{name: "partner_human/users_list", human: true, args: []string{"partner", "users", "list"}, server: pgUsers},
		goldenCase{name: "partner_human/users_list_empty", human: true, args: []string{"partner", "users", "list", "--query", "zz"},
			server: goldenServe(map[string]http.HandlerFunc{"GET /v1/partner/workspaces": gJSON(200, `{"workspaces":[]}`)})},
		goldenCase{name: "partner_human/users_create", human: true, args: []string{"partner", "users", "create", "user-42", "--tier", "Standard"},
			server: goldenServe(map[string]http.HandlerFunc{"PUT /v1/partner/workspaces/{id}": gJSON(201, pgCreated)})},
		goldenCase{name: "partner_human/users_create_no_key", human: true, args: []string{"partner", "users", "create", "user-42", "--no-key"},
			server: goldenServe(map[string]http.HandlerFunc{"PUT /v1/partner/workspaces/{id}": gJSON(200, pgExisting)})},
		goldenCase{name: "partner_human/users_create_quiet", human: true, args: []string{"partner", "users", "create", "user-42", "-q"},
			server: goldenServe(map[string]http.HandlerFunc{"PUT /v1/partner/workspaces/{id}": gJSON(201, pgCreated)})},
		goldenCase{name: "partner_human/users_get", human: true, args: []string{"partner", "users", "get", "user-42"}, server: goldenServe(map[string]http.HandlerFunc{"GET /v1/partner/workspaces/{id}": gJSON(200, pgWorkspace)})},
		goldenCase{name: "partner_human/users_suspend_yes", human: true, args: []string{"partner", "users", "suspend", "user-42", "--yes"}, server: goldenServe(map[string]http.HandlerFunc{"POST /v1/partner/workspaces/{id}/suspend": gJSON(200, pgWorkspace)})},
		goldenCase{name: "partner_human/users_give_provided_key", human: true, args: []string{"partner", "users", "give", "user-42", "50", "--idempotency-key", "grant-2026-11-20-42", "--yes"},
			server: goldenServe(map[string]http.HandlerFunc{"POST /v1/partner/workspaces/{id}/credits": gJSON(200, pgGiven)})},
		goldenCase{name: "partner_human/users_give_not_applied", human: true, args: []string{"partner", "users", "give", "user-42", "50", "--idempotency-key", "grant-2026-11-20-42", "--yes"},
			server: goldenServe(map[string]http.HandlerFunc{"POST /v1/partner/workspaces/{id}/credits": gJSON(200, pgGivenReplay)})},
		goldenCase{name: "partner_human/tiers_list", human: true, args: []string{"partner", "tiers", "list"}, server: goldenServe(map[string]http.HandlerFunc{"GET /v1/partner/tiers": gJSON(200, pgTierList)})},
		goldenCase{name: "partner_human/tiers_create", human: true, args: []string{"partner", "tiers", "create", "Standard", "--requests-per-minute", "60", "--concurrency", "2", "--free-credits", "250", "--no-cap"},
			server: goldenServe(map[string]http.HandlerFunc{"POST /v1/partner/tiers": gJSON(201, pgTier)})},
	)
}

// pgNoCalls is a server for a case that must send nothing: any call to it
// answers 404, which would show in the golden.
func pgNoCalls() func() http.HandlerFunc {
	return goldenServe(map[string]http.HandlerFunc{})
}
