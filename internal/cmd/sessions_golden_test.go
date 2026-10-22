package cmd

import (
	"net/http"
	"testing"
)

const (
	gSessionID = "01J9ZQ4M7R3T8VX2K5N6P0B1CD"

	gSession = `{"id":"` + gSessionID + `","project_id":"proj_01J9Z","engine":"chromium","status":"active","sticky_key":"login-1",` +
		`"proxy":{"tier":"residential","country":"de","region_pool":"eu"},"created_at":"2026-09-22T00:00:00Z","last_used_at":"2026-09-22T00:30:00Z",` +
		`"expires_at":"2026-09-22T02:00:00Z","hard_expires_at":"2026-09-23T00:00:00Z","usage_count":7,"context":null}`

	gSessionNew = `{"id":"` + gSessionID + `","project_id":"proj_01J9Z","engine":"obscura","status":"active","sticky_key":null,` +
		`"proxy":{"tier":"datacenter"},"created_at":"2026-09-22T00:00:00Z","last_used_at":null,"expires_at":"2026-09-22T00:30:00Z",` +
		`"usage_count":0,"warnings":["engine obscura cannot persist IndexedDB"]}`

	gSessionReleased = `{"id":"` + gSessionID + `","project_id":"proj_01J9Z","engine":"chromium","status":"released","sticky_key":"login-1",` +
		`"proxy":{"tier":"residential","country":"de"},"created_at":"2026-09-22T00:00:00Z","last_used_at":"2026-09-22T00:30:00Z",` +
		`"expires_at":"2026-09-22T02:00:00Z","released_at":"2026-09-22T00:45:00Z","retired":"released by the caller","usage_count":7}`

	gSessionContext = `{"session_id":"` + gSessionID + `","session_context":{"cookies":[{"name":"sid","value":"abc123","domain":"example.com","path":"/"}],"local_storage":{"theme":"dark"}}}`
)

func init() {
	one := func(method, path, body string, status int) func() http.HandlerFunc {
		return goldenServe(map[string]http.HandlerFunc{method + " " + path: gJSON(status, body)})
	}
	list := func(body string) func() http.HandlerFunc { return one("GET", "/v1/sessions", body, 200) }
	page := `{"sessions":[` + gSession + `,` + gSessionReleased + `]}`
	more := `{"sessions":[` + gSession + `],"next_cursor":"1790000000000000000.ABC"}`
	del := goldenServe(map[string]http.HandlerFunc{"DELETE /v1/sessions/{id}": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }})

	goldenAdd(
		goldenCase{name: "sessions/create", args: []string{"sessions", "create", "--engine", "obscura"}, server: one("POST", "/v1/sessions", gSessionNew, 201)},
		goldenCase{name: "sessions/create_session_context_file", args: []string{"sessions", "create", "--session-context", "{dir}/ctx.json"}, server: one("POST", "/v1/sessions", gSession, 201),
			setup: func(t *testing.T, e *goldenEnv) { goldenWrite(t, e.Dir, "ctx.json", gSessionContext) }},
		goldenCase{name: "sessions/create_usage_error", args: []string{"sessions", "create", "--rotate-ip", "--sticky-key", "k1"}, stderr: true},
		goldenCase{name: "sessions/list", args: []string{"sessions", "list"}, server: list(page)},
		goldenCase{name: "sessions/list_empty", args: []string{"sessions", "list", "--status", "expired"}, server: list(`{"sessions":[]}`)},
		goldenCase{name: "sessions/list_next_cursor", args: []string{"sessions", "list", "--limit", "1"}, server: list(more)},
		goldenCase{name: "sessions/list_all_pages", args: []string{"sessions", "list", "--all"},
			server: goldenServe(map[string]http.HandlerFunc{"GET /v1/sessions": func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("cursor") == "" {
					gJSON(200, more)(w, r)
					return
				}
				gJSON(200, `{"sessions":[`+gSessionReleased+`]}`)(w, r)
			}})},
		goldenCase{name: "sessions/get", args: []string{"sessions", "get", gSessionID}, server: one("GET", "/v1/sessions/{id}", gSession, 200)},
		goldenCase{name: "sessions/get_not_found", args: []string{"sessions", "get", gSessionID}, stderr: true,
			server: goldenServe(map[string]http.HandlerFunc{"GET /v1/sessions/{id}": gProblem(404, "ERR::SESSION::NOT_FOUND", "no such session")})},
		goldenCase{name: "sessions/context", args: []string{"sessions", "context", gSessionID}, server: one("GET", "/v1/sessions/{id}/context", gSessionContext, 200)},
		goldenCase{name: "sessions/release", args: []string{"sessions", "release", gSessionID}, server: one("POST", "/v1/sessions/{id}/release", gSessionReleased, 200)},
		goldenCase{name: "sessions/delete_yes", args: []string{"sessions", "delete", gSessionID, "--yes"}, server: del},
		goldenCase{name: "sessions/delete_needs_yes_without_terminal", args: []string{"sessions", "delete", gSessionID}, stderr: true, server: del},

		goldenCase{name: "sessions_human/create", human: true, args: []string{"sessions", "create"}, server: one("POST", "/v1/sessions", gSessionNew, 201)},
		goldenCase{name: "sessions_human/list", human: true, args: []string{"sessions", "list"}, server: list(page)},
		goldenCase{name: "sessions_human/list_empty", human: true, args: []string{"sessions", "list"}, server: list(`{"sessions":[]}`)},
		goldenCase{name: "sessions_human/list_next_cursor", human: true, args: []string{"sessions", "list"}, server: list(more)},
		goldenCase{name: "sessions_human/get", human: true, args: []string{"sessions", "get", gSessionID}, server: one("GET", "/v1/sessions/{id}", gSession, 200)},
		goldenCase{name: "sessions_human/get_released", human: true, args: []string{"sessions", "get", gSessionID}, server: one("GET", "/v1/sessions/{id}", gSessionReleased, 200)},
		goldenCase{name: "sessions_human/context", human: true, args: []string{"sessions", "context", gSessionID}, server: one("GET", "/v1/sessions/{id}/context", gSessionContext, 200)},
		goldenCase{name: "sessions_human/release", human: true, args: []string{"sessions", "release", gSessionID}, server: one("POST", "/v1/sessions/{id}/release", gSessionReleased, 200)},
		goldenCase{name: "sessions_human/delete_yes", human: true, args: []string{"sessions", "delete", gSessionID, "--yes"}, server: del},
	)
}
