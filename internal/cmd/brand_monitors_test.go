package cmd

import (
	"strings"
	"testing"

	"github.com/OfficialSpicrawl/cli/internal/exitcode"
)

// bmRun runs the CLI against a fake API that answers every route with reply
// and returns stdout, the exit code and the one recorded request.
// bmCompact strips the whitespace of indented JSON so a test can match a field.
func bmCompact(s string) string { return strings.Join(strings.Fields(s), "") }

func bmRun(t *testing.T, route string, reply pgReply, args ...string) (string, int, recordedRequest) {
	t.Helper()
	partnerStdin(t)
	srv, reqs := pgFake(t, map[string]pgReply{route: reply})
	out, _, code := runCLI(t, srv.URL, args...)
	got := recordedSoFar(reqs)
	if len(got) != 1 {
		t.Fatalf("%v: %d requests, want 1", args, len(got))
	}
	return out, code, got[0]
}

// bmWant checks the request's method, path and JSON body (nil: no body). A
// value of nil in want means the key is sent as JSON null.
func bmWant(t *testing.T, r recordedRequest, method, path string, want map[string]any) {
	t.Helper()
	if r.Method != method || r.Path != path {
		t.Fatalf("request %s %s, want %s %s", r.Method, r.Path, method, path)
	}
	got := pgJSONBody(t, r)
	if len(got) != len(want) {
		t.Fatalf("%s %s body %v, want %v", method, path, got, want)
	}
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			t.Fatalf("%s %s body %v lacks %q", method, path, got, k)
		}
		if gs, isSlice := g.([]any); isSlice {
			if len(gs) != len(w.([]string)) {
				t.Fatalf("body %q = %v, want %v", k, g, w)
			}
			for i, s := range w.([]string) {
				if gs[i] != s {
					t.Fatalf("body %q = %v, want %v", k, g, w)
				}
			}
			continue
		}
		if g != w {
			t.Fatalf("body %q = %#v, want %#v", k, g, w)
		}
	}
}

const (
	bmH = "0190f3a2-7c1e-7d4b-9a2f-3b8e5c6d7e80"
	bmM = "0190f5c4-2b7a-7e10-8d3f-6a1c9e4b2d70"
)

func TestBrandRequest(t *testing.T) {
	out, code, r := bmRun(t, "POST /v1/brand", pgReply{200, bmBrand}, "brand", "https://example.com", "--include", "logos,colors", "--screenshot")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	bmWant(t, r, "POST", "/v1/brand", map[string]any{"url": "https://example.com", "include": []string{"logos", "colors"}, "screenshot": true})
	if !strings.Contains(bmCompact(out), `"warnings":["noiconsfound"]`) {
		t.Fatalf("--json must print the answer unchanged, got %s", out)
	}
	_, _, r = bmRun(t, "POST /v1/brand", pgReply{200, bmBrand}, "brand", "https://example.com")
	bmWant(t, r, "POST", "/v1/brand", map[string]any{"url": "https://example.com"})
}

func TestWebhooksRequests(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		route  string
		status int
		reply  string
		method string
		path   string
		body   map[string]any
		query  string
	}{
		{"create", []string{"webhooks", "create", "https://h.example.com/x", "--name", "prod", "--events", "batch.completed,monitor.failed", "--format", "slack"},
			"POST /v1/webhooks", 201, bmHookSecret, "POST", "/v1/webhooks",
			map[string]any{"url": "https://h.example.com/x", "name": "prod", "events": []string{"batch.completed", "monitor.failed"}, "format": "slack"}, ""},
		{"list", []string{"webhooks", "list"}, "GET /v1/webhooks", 200, `{"data":[]}`, "GET", "/v1/webhooks", nil, ""},
		{"get", []string{"webhooks", "get", bmH}, "GET /v1/webhooks/" + bmH, 200, bmHook, "GET", "/v1/webhooks/" + bmH, nil, ""},
		{"update disable", []string{"webhooks", "update", bmH, "--disable"}, "PATCH /v1/webhooks/" + bmH, 200, bmHookOff, "PATCH", "/v1/webhooks/" + bmH, map[string]any{"enabled": false}, ""},
		{"update enable+url", []string{"webhooks", "update", bmH, "--enable", "--url", "https://n.example.com"}, "PATCH /v1/webhooks/" + bmH, 200, bmHook, "PATCH", "/v1/webhooks/" + bmH,
			map[string]any{"enabled": true, "url": "https://n.example.com"}, ""},
		{"delete", []string{"webhooks", "delete", bmH, "--yes"}, "DELETE /v1/webhooks/" + bmH, 204, "", "DELETE", "/v1/webhooks/" + bmH, nil, ""},
		{"test", []string{"webhooks", "test", bmH}, "POST /v1/webhooks/" + bmH + "/test", 202, `{"delivery_id":"d1"}`, "POST", "/v1/webhooks/" + bmH + "/test", nil, ""},
		{"rotate", []string{"webhooks", "rotate-secret", bmH}, "POST /v1/webhooks/" + bmH + "/rotate-secret", 200, bmHookSecret, "POST", "/v1/webhooks/" + bmH + "/rotate-secret", nil, ""},
		{"deliveries", []string{"webhooks", "deliveries", bmH, "--limit", "5"}, "GET /v1/webhooks/" + bmH + "/deliveries", 200, bmDeliveries, "GET", "/v1/webhooks/" + bmH + "/deliveries", nil, "limit=5"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, code, r := bmRun(t, c.route, pgReply{c.status, c.reply}, c.args...)
			if code != 0 {
				t.Fatalf("exit %d, out %s", code, out)
			}
			bmWant(t, r, c.method, c.path, c.body)
			if r.Query != c.query {
				t.Fatalf("query %q, want %q", r.Query, c.query)
			}
		})
	}
}

func TestWebhooksSecretInJSON(t *testing.T) {
	out, _, _ := bmRun(t, "POST /v1/webhooks", pgReply{201, bmHookSecret}, "webhooks", "create", "https://h.example.com/x")
	if !strings.Contains(bmCompact(out), `"secret":"whsec_0123456789abcdef"`) {
		t.Fatalf("--json must include the secret: %s", out)
	}
}

func TestWebhooksUsageErrors(t *testing.T) {
	partnerStdin(t)
	for _, args := range [][]string{
		{"webhooks", "delete", bmH}, // no --yes, no terminal
		{"webhooks", "update", bmH}, // nothing to change
		{"webhooks", "update", bmH, "--enable", "--disable"},
		{"webhooks", "deliveries", bmH, "--limit", "0"},
		{"webhooks", "deliveries", bmH, "--limit", "101"},
	} {
		srv, reqs := pgFake(t, nil)
		_, _, code := runCLI(t, srv.URL, args...)
		if code != exitcode.Usage || len(recordedSoFar(reqs)) != 0 {
			t.Errorf("%v: exit %d with %d calls, want usage (2) and none", args, code, len(recordedSoFar(reqs)))
		}
	}
}

func TestMonitorEvery(t *testing.T) {
	for in, want := range map[string]int64{"1h": 3600, "30m": 1800, "1d": 86400, "2d": 172800, "90": 90, " 45s ": 45} {
		if got, err := monitorEvery(in); err != nil || got != want {
			t.Errorf("%q = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "soon", "1w", "xd"} {
		if _, err := monitorEvery(bad); ExitCode(err) != exitcode.Usage {
			t.Errorf("%q: exit %d, want usage (2)", bad, ExitCode(err))
		}
	}
}

func TestMonitorsRequests(t *testing.T) {
	const mp = "/v1/monitors/" + bmM
	cases := []struct {
		name   string
		args   []string
		route  string
		status int
		reply  string
		method string
		path   string
		body   map[string]any
		query  string
	}{
		{"create page 1h", []string{"monitors", "create", "https://example.com/p", "--every", "1h"}, "POST /v1/monitors", 201, bmMon, "POST", "/v1/monitors",
			map[string]any{"url": "https://example.com/p", "interval_seconds": float64(3600)}, ""},
		{"create page 30m", []string{"monitors", "create", "https://example.com/p", "--every", "30m"}, "POST /v1/monitors", 201, bmMon, "POST", "/v1/monitors",
			map[string]any{"url": "https://example.com/p", "interval_seconds": float64(1800)}, ""},
		{"create page 1d", []string{"monitors", "create", "https://example.com/p", "--every", "1d"}, "POST /v1/monitors", 201, bmMon, "POST", "/v1/monitors",
			map[string]any{"url": "https://example.com/p", "interval_seconds": float64(86400)}, ""},
		{"create page raw seconds", []string{"monitors", "create", "https://example.com/p", "--every", "900"}, "POST /v1/monitors", 201, bmMon, "POST", "/v1/monitors",
			map[string]any{"url": "https://example.com/p", "interval_seconds": float64(900)}, ""},
		{"create page all flags", []string{"monitors", "create", "https://example.com/p", "--name", "n", "--selector", ".x", "--webhook", bmH, "--confirm-checks", "3", "--js"},
			"POST /v1/monitors", 201, bmMon, "POST", "/v1/monitors",
			map[string]any{"url": "https://example.com/p", "name": "n", "selector": ".x", "webhook_endpoint_id": bmH, "confirm_checks": float64(3), "js_render": true}, ""},
		{"create site", []string{"monitors", "create", "https://example.com", "--site", "--max-pages", "200", "--include", "/blog/*", "--exclude", "/tag/*", "--exclude", "/old/*", "--max-credits-per-run", "500"},
			"POST /v1/monitors", 201, bmMonSite, "POST", "/v1/monitors",
			map[string]any{"url": "https://example.com", "scope": "site", "max_pages": float64(200), "include_paths": []string{"/blog/*"},
				"exclude_paths": []string{"/tag/*", "/old/*"}, "max_credits_per_run": float64(500)}, ""},
		{"update clears", []string{"monitors", "update", bmM, "--selector", "", "--webhook", "", "--max-credits-per-run", "0"}, "PATCH " + mp, 200, bmMon, "PATCH", mp,
			map[string]any{"selector": nil, "webhook_endpoint_id": nil, "max_credits_per_run": nil}, ""},
		{"update enable", []string{"monitors", "update", bmM, "--enable", "--every", "2h"}, "PATCH " + mp, 200, bmMon, "PATCH", mp,
			map[string]any{"enabled": true, "interval_seconds": float64(7200)}, ""},
		{"update disable", []string{"monitors", "update", bmM, "--disable"}, "PATCH " + mp, 200, bmMon, "PATCH", mp, map[string]any{"enabled": false}, ""},
		{"delete", []string{"monitors", "delete", bmM, "--yes"}, "DELETE " + mp, 204, "", "DELETE", mp, nil, ""},
		{"list", []string{"monitors", "list"}, "GET /v1/monitors", 200, `{"data":[]}`, "GET", "/v1/monitors", nil, ""},
		{"get", []string{"monitors", "get", bmM}, "GET " + mp, 200, bmMon, "GET", mp, nil, ""},
		{"check changed", []string{"monitors", "check", bmM}, "POST " + mp + "/check", 200, `{"outcome":"changed","event_id":"e1","credits":1,"run":null}`, "POST", mp + "/check", nil, ""},
		{"check unchanged", []string{"monitors", "check", bmM}, "POST " + mp + "/check", 200, `{"outcome":"unchanged","event_id":null,"credits":1,"run":null}`, "POST", mp + "/check", nil, ""},
		{"changes", []string{"monitors", "changes", bmM, "--limit", "7"}, "GET " + mp + "/changes", 200, bmChanges, "GET", mp + "/changes", nil, "limit=7"},
		{"pages cursor", []string{"monitors", "pages", bmM, "--cursor", "cur_1", "--limit", "50"}, "GET " + mp + "/pages", 200, bmPages, "GET", mp + "/pages", nil, "cursor=cur_1&limit=50"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, code, r := bmRun(t, c.route, pgReply{c.status, c.reply}, c.args...)
			if code != 0 {
				t.Fatalf("exit %d, out %s", code, out)
			}
			bmWant(t, r, c.method, c.path, c.body)
			if r.Query != c.query {
				t.Fatalf("query %q, want %q", r.Query, c.query)
			}
		})
	}
}

func TestMonitorCheckFailedExitsUpstream(t *testing.T) {
	out, code, _ := bmRun(t, "POST /v1/monitors/"+bmM+"/check", pgReply{200, `{"outcome":"failed","event_id":null,"credits":0,"run":null}`}, "monitors", "check", bmM)
	if code != exitcode.Upstream {
		t.Fatalf("exit %d, want upstream (6)", code)
	}
	if !strings.Contains(bmCompact(out), `"outcome":"failed"`) {
		t.Fatalf("the outcome must still be printed: %s", out)
	}
}

func TestMonitorsUsageErrors(t *testing.T) {
	partnerStdin(t)
	for _, args := range [][]string{
		{"monitors", "create", "https://e.com", "--every", "soon"},
		{"monitors", "create", "https://e.com", "--site"},           // needs --max-pages
		{"monitors", "create", "https://e.com", "--max-pages", "5"}, // site flag without --site
		{"monitors", "create", "https://e.com", "--confirm-checks", "11"},
		{"monitors", "update", bmM}, // nothing to change
		{"monitors", "update", bmM, "--enable", "--disable"},
		{"monitors", "delete", bmM}, // no --yes
		{"monitors", "changes", bmM, "--limit", "0"},
		{"monitors", "pages", bmM, "--limit", "501"},
	} {
		srv, reqs := pgFake(t, nil)
		_, _, code := runCLI(t, srv.URL, args...)
		if code != exitcode.Usage || len(recordedSoFar(reqs)) != 0 {
			t.Errorf("%v: exit %d with %d calls, want usage (2) and none", args, code, len(recordedSoFar(reqs)))
		}
	}
}
