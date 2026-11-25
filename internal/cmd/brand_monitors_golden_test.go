package cmd

import "net/http"

// Golden cases for brand, webhooks and monitors. Generate the files with
//
//	GOLDEN_UPDATE=1 go test ./internal/cmd/ -run 'TestGolden/(brand|webhooks|monitors)'
//
// then review the diff. The request each command sends is asserted in
// brand_monitors_test.go.

const (
	bmBrand = `{"url":"https://example.com","identity":{"name":"Example","tagline":"Examples, made easy","subtitle":null,"ctas":[]},` +
		`"logos":[{"url":"https://example.com/logo-small.png","score":0.4},{"url":"https://example.com/logo.svg","score":0.9}],` +
		`"colors":[{"hex":"#0055ff","role":"primary"},{"hex":"#ffffff","role":"background"},{"hex":"#ff8800","role":"primary"}],` +
		`"fonts":[{"family":"Inter","role":"body"},{"family":"Fraunces","role":"heading"}],` +
		`"socials":[{"network":"github","url":"https://github.com/example"},{"network":"x","url":"https://x.com/example"}],"warnings":["no icons found"]}`

	bmHook = `{"id":"0190f3a2-7c1e-7d4b-9a2f-3b8e5c6d7e80","name":"prod","url":"https://hooks.example.com/spicrawl","events":["batch.completed","monitor.changed"],` +
		`"format":"json","enabled":true,"disabled_reason":null,"consecutive_failures":0,"secret_version":1,"last_success_at":"2026-11-24T10:00:00Z","last_failure_at":null}`
	bmHookSecret = `{"id":"0190f3a2-7c1e-7d4b-9a2f-3b8e5c6d7e80","name":"prod","url":"https://hooks.example.com/spicrawl","events":["batch.completed","monitor.changed"],` +
		`"format":"json","enabled":true,"disabled_reason":null,"consecutive_failures":0,"secret_version":1,"last_success_at":null,"last_failure_at":null,"secret":"whsec_0123456789abcdef"}`
	bmHookOff = `{"id":"0190f3a2-7c1e-7d4b-9a2f-3b8e5c6d7e80","name":null,"url":"https://hooks.example.com/spicrawl","events":["batch.completed"],` +
		`"format":"json","enabled":false,"disabled_reason":"disabled by you","consecutive_failures":3,"secret_version":2,"last_success_at":null,"last_failure_at":"2026-11-24T11:00:00Z"}`
	bmDeliveries = `{"data":[{"id":"0190f400-0000-7000-8000-000000000001","event_type":"monitor.changed","status":"delivered","attempt":1,"max_attempts":6,"last_error":null,"created_at":"2026-11-24T10:00:00Z"},` +
		`{"id":"0190f400-0000-7000-8000-000000000002","event_type":"batch.failed","status":"dead","attempt":6,"max_attempts":6,"last_error":"HTTP 500","created_at":"2026-11-23T09:00:00Z"}]}`

	bmMon = `{"id":"0190f5c4-2b7a-7e10-8d3f-6a1c9e4b2d70","name":"example.com","url":"https://example.com/pricing","scope":"page","selector":".plan-price","js_render":false,` +
		`"interval_seconds":3600,"current_interval_seconds":3600,"confirm_checks":1,"webhook_endpoint_id":"0190f3a2-7c1e-7d4b-9a2f-3b8e5c6d7e80","enabled":true,` +
		`"disabled_reason":null,"last_error":null,"last_checked_at":"2026-11-25T08:00:00Z","last_changed_at":null,"next_check_at":"2026-11-25T09:00:00Z","max_pages":null,"max_credits_per_run":null,"pages_tracked":0}`
	bmMonSite = `{"id":"0190f5c4-2b7a-7e10-8d3f-6a1c9e4b2d71","name":"example.com","url":"https://example.com","scope":"site","selector":null,"js_render":true,` +
		`"interval_seconds":86400,"current_interval_seconds":86400,"confirm_checks":2,"webhook_endpoint_id":null,"enabled":true,` +
		`"disabled_reason":null,"last_error":null,"last_checked_at":null,"last_changed_at":null,"next_check_at":"2026-11-26T08:00:00Z","max_pages":200,"max_credits_per_run":500,"pages_tracked":42}`
	bmChanges = `{"data":[{"id":"0190f600-0000-7000-8000-000000000001","detected_at":"2026-11-25T08:00:00Z","diff":"--- a\n+++ b\n-$9\n+$12\n+new\n","summary":null,"notified_at":"2026-11-25T08:00:05Z"},` +
		`{"id":"0190f600-0000-7000-8000-000000000002","detected_at":"2026-11-24T08:00:00Z","diff":null,"summary":{"counts":{"checked":40,"skipped":0,"changed":2,"added":1,"removed":0,"credits":40,"truncated":false}},"notified_at":null}]}`
	bmPages = `{"data":[{"url":"https://example.com/a","first_seen_at":"2026-11-20T00:00:00Z","last_seen_at":"2026-11-25T00:00:00Z","last_changed_at":"2026-11-24T00:00:00Z","removed_at":null},` +
		`{"url":"https://example.com/b","first_seen_at":"2026-11-20T00:00:00Z","last_seen_at":"2026-11-22T00:00:00Z","last_changed_at":null,"removed_at":"2026-11-23T00:00:00Z"}],"next_cursor":"cur_2"}`
)

func bmServe(route string, status int, body string) func() http.HandlerFunc {
	return goldenServe(map[string]http.HandlerFunc{route: gJSON(status, body)})
}

func init() {
	hook := "0190f3a2-7c1e-7d4b-9a2f-3b8e5c6d7e80"
	mon := "0190f5c4-2b7a-7e10-8d3f-6a1c9e4b2d70"
	goldenAdd(
		// brand
		goldenCase{name: "brand/json", args: []string{"brand", "https://example.com"}, server: bmServe("POST /v1/brand", 200, bmBrand)},
		goldenCase{name: "brand/include", args: []string{"brand", "https://example.com", "--include", "logos,colors", "--screenshot"}, server: bmServe("POST /v1/brand", 200, bmBrand)},
		goldenCase{name: "brand_human/summary", human: true, args: []string{"brand", "https://example.com"}, server: bmServe("POST /v1/brand", 200, bmBrand)},
		goldenCase{name: "brand_human/include", human: true, args: []string{"brand", "https://example.com", "--include", "colors"},
			server: bmServe("POST /v1/brand", 200, `{"url":"https://example.com","colors":[{"hex":"#0055ff","role":"primary"}],"warnings":[]}`)},

		// webhooks, JSON
		goldenCase{name: "webhooks/create", args: []string{"webhooks", "create", "https://hooks.example.com/spicrawl", "--events", "batch.completed,monitor.changed"}, server: bmServe("POST /v1/webhooks", 201, bmHookSecret)},
		goldenCase{name: "webhooks/list", args: []string{"webhooks", "list"}, server: bmServe("GET /v1/webhooks", 200, `{"data":[`+bmHook+`]}`)},
		goldenCase{name: "webhooks/deliveries", args: []string{"webhooks", "deliveries", hook, "--limit", "2"}, server: bmServe("GET /v1/webhooks/{id}/deliveries", 200, bmDeliveries)},
		goldenCase{name: "webhooks/delete_needs_yes", args: []string{"webhooks", "delete", hook}, stderr: true, server: pgNoCalls()},
		goldenCase{name: "webhooks/delete_yes", args: []string{"webhooks", "delete", hook, "--yes"}, server: bmServe("DELETE /v1/webhooks/{id}", 204, "")},

		// webhooks, human
		goldenCase{name: "webhooks_human/create", human: true, args: []string{"webhooks", "create", "https://hooks.example.com/spicrawl", "--name", "prod", "--events", "batch.completed,monitor.changed"}, server: bmServe("POST /v1/webhooks", 201, bmHookSecret)},
		goldenCase{name: "webhooks_human/list", human: true, args: []string{"webhooks", "list"}, server: bmServe("GET /v1/webhooks", 200, `{"data":[`+bmHook+`,`+bmHookOff+`]}`)},
		goldenCase{name: "webhooks_human/list_empty", human: true, args: []string{"webhooks", "list"}, server: bmServe("GET /v1/webhooks", 200, `{"data":[]}`)},
		goldenCase{name: "webhooks_human/get", human: true, args: []string{"webhooks", "get", hook}, server: bmServe("GET /v1/webhooks/{id}", 200, bmHook)},
		goldenCase{name: "webhooks_human/update_disable", human: true, args: []string{"webhooks", "update", hook, "--disable"}, server: bmServe("PATCH /v1/webhooks/{id}", 200, bmHookOff)},
		goldenCase{name: "webhooks_human/delete_yes", human: true, args: []string{"webhooks", "delete", hook, "--yes"}, server: bmServe("DELETE /v1/webhooks/{id}", 204, "")},
		goldenCase{name: "webhooks_human/test", human: true, args: []string{"webhooks", "test", hook}, server: bmServe("POST /v1/webhooks/{id}/test", 202, `{"delivery_id":"0190f400-0000-7000-8000-000000000009"}`)},
		goldenCase{name: "webhooks_human/rotate", human: true, args: []string{"webhooks", "rotate-secret", hook}, server: bmServe("POST /v1/webhooks/{id}/rotate-secret", 200, bmHookSecret)},
		goldenCase{name: "webhooks_human/deliveries", human: true, args: []string{"webhooks", "deliveries", hook, "--limit", "2"}, server: bmServe("GET /v1/webhooks/{id}/deliveries", 200, bmDeliveries)},

		// monitors, JSON
		goldenCase{name: "monitors/create_page", args: []string{"monitors", "create", "https://example.com/pricing", "--every", "1h", "--selector", ".plan-price", "--webhook", hook}, server: bmServe("POST /v1/monitors", 201, bmMon)},
		goldenCase{name: "monitors/create_bad_every", args: []string{"monitors", "create", "https://example.com", "--every", "soon"}, stderr: true, server: pgNoCalls()},
		goldenCase{name: "monitors/create_site_needs_max_pages", args: []string{"monitors", "create", "https://example.com", "--site"}, stderr: true, server: pgNoCalls()},
		goldenCase{name: "monitors/check_failed", args: []string{"monitors", "check", mon}, stderr: true, server: bmServe("POST /v1/monitors/{id}/check", 200, `{"outcome":"failed","event_id":null,"credits":0,"run":null}`)},
		goldenCase{name: "monitors/delete_needs_yes", args: []string{"monitors", "delete", mon}, stderr: true, server: pgNoCalls()},

		// monitors, human
		goldenCase{name: "monitors_human/create_page", human: true, args: []string{"monitors", "create", "https://example.com/pricing", "--every", "1h", "--selector", ".plan-price", "--webhook", hook}, server: bmServe("POST /v1/monitors", 201, bmMon)},
		goldenCase{name: "monitors_human/create_site", human: true, args: []string{"monitors", "create", "https://example.com", "--site", "--max-pages", "200", "--include", "/blog/*", "--exclude", "/tag/*", "--max-credits-per-run", "500", "--every", "1d", "--js", "--confirm-checks", "2"}, server: bmServe("POST /v1/monitors", 201, bmMonSite)},
		goldenCase{name: "monitors_human/list", human: true, args: []string{"monitors", "list"}, server: bmServe("GET /v1/monitors", 200, `{"data":[`+bmMon+`,`+bmMonSite+`]}`)},
		goldenCase{name: "monitors_human/get", human: true, args: []string{"monitors", "get", mon}, server: bmServe("GET /v1/monitors/{id}", 200, bmMon)},
		goldenCase{name: "monitors_human/update_clear", human: true, args: []string{"monitors", "update", mon, "--selector", "", "--webhook", ""}, server: bmServe("PATCH /v1/monitors/{id}", 200, bmMon)},
		goldenCase{name: "monitors_human/delete_yes", human: true, args: []string{"monitors", "delete", mon, "--yes"}, server: bmServe("DELETE /v1/monitors/{id}", 204, "")},
		goldenCase{name: "monitors_human/check_changed", human: true, args: []string{"monitors", "check", mon}, server: bmServe("POST /v1/monitors/{id}/check", 200, `{"outcome":"changed","event_id":"0190f600-0000-7000-8000-000000000001","credits":1,"run":null}`)},
		goldenCase{name: "monitors_human/check_unchanged", human: true, args: []string{"monitors", "check", mon}, server: bmServe("POST /v1/monitors/{id}/check", 200, `{"outcome":"unchanged","event_id":null,"credits":1,"run":null}`)},
		goldenCase{name: "monitors_human/check_site", human: true, args: []string{"monitors", "check", mon}, server: bmServe("POST /v1/monitors/{id}/check", 200, `{"outcome":"changed","event_id":"0190f600-0000-7000-8000-000000000002","credits":40,"run":{"checked":40,"skipped":2,"changed":2,"added":1,"removed":0,"credits":40,"truncated":true}}`)},
		goldenCase{name: "monitors_human/check_failed", human: true, args: []string{"monitors", "check", mon}, server: bmServe("POST /v1/monitors/{id}/check", 200, `{"outcome":"failed","event_id":null,"credits":0,"run":null}`)},
		goldenCase{name: "monitors_human/changes", human: true, args: []string{"monitors", "changes", mon, "--limit", "2"}, server: bmServe("GET /v1/monitors/{id}/changes", 200, bmChanges)},
		goldenCase{name: "monitors_human/pages", human: true, args: []string{"monitors", "pages", mon, "--cursor", "cur_1"}, server: bmServe("GET /v1/monitors/{id}/pages", 200, bmPages)},
	)
}
