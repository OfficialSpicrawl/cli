package cmd

import (
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
)

const sessionsTestBody = `{"id":"01J9ZQ4M7R3T8VX2K5N6P0B1CD","project_id":"p","engine":"chromium","status":"active","sticky_key":"s1","proxy":{"tier":"residential","country":"de"},"created_at":"2026-09-22T00:00:00Z","last_used_at":null,"expires_at":"2026-09-22T02:00:00Z","usage_count":0,"context":null}`

func TestSessionsCreateMapsFlags(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(sessionsTestBody))
	})
	out, errb, code := runCLI(t, srv.URL, "sessions", "create", "--engine", "Chromium", "--ttl", "7200", "--country", "DE", "--sticky-key", "login-1")
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, errb)
	}
	r := (*reqs)[0]
	if r.Method != http.MethodPost || r.Path != "/v1/sessions" {
		t.Fatalf("got %s %s", r.Method, r.Path)
	}
	var body map[string]any
	_ = json.Unmarshal(r.Body, &body)
	want := map[string]any{"engine": "chromium", "ttl_seconds": 7200.0, "proxy_country": "de", "premium_proxy": true, "sticky_key": "login-1"}
	if len(body) != len(want) {
		t.Fatalf("body %v, want %v", body, want)
	}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("%s = %v, want %v", k, body[k], v)
		}
	}
	if !strings.Contains(out, `"id": "01J9ZQ4M7R3T8VX2K5N6P0B1CD"`) {
		t.Errorf("stdout %s", out)
	}
}

func TestSessionsCreateEmptyBody(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(sessionsTestBody))
	})
	if _, errb, code := runCLI(t, srv.URL, "sessions", "create"); code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if got := string((*reqs)[0].Body); got != "{}" {
		t.Errorf("body %s, want {}", got)
	}
}

func TestSessionsCreateRotateAndStickyIsUsageError(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {})
	_, _, code := runCLI(t, srv.URL, "sessions", "create", "--rotate-ip", "--sticky-key", "k1")
	if code != 2 || len(*reqs) != 0 {
		t.Fatalf("exit %d, %d requests; want 2 and none", code, len(*reqs))
	}
}

func TestSessionsListAllFollowsCursor(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "" {
			_, _ = w.Write([]byte(`{"sessions":[` + sessionsTestBody + `],"next_cursor":"1790000000000000000.ABC"}`))
			return
		}
		_, _ = w.Write([]byte(`{"sessions":[` + sessionsTestBody + `]}`))
	})
	out, errb, code := runCLI(t, srv.URL, "sessions", "list", "--all", "--status", "active", "--engine", "chromium", "--limit", "1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if len(*reqs) != 2 {
		t.Fatalf("%d requests, want 2", len(*reqs))
	}
	if q := (*reqs)[0].Query; q != "engine=chromium&limit=1&status=active" {
		t.Errorf("first query %q", q)
	}
	if q := (*reqs)[1].Query; !strings.Contains(q, "cursor=1790000000000000000.ABC") {
		t.Errorf("second query %q", q)
	}
	var got struct{ Sessions []any }
	_ = json.Unmarshal([]byte(out), &got)
	if len(got.Sessions) != 2 {
		t.Errorf("got %d sessions, want 2", len(got.Sessions))
	}
}

func TestSessionsGetAndContextPaths(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sessionsTestBody))
	})
	runCLI(t, srv.URL, "sessions", "get", "S1")
	runCLI(t, srv.URL, "sessions", "context", "S1")
	if (*reqs)[0].Path != "/v1/sessions/S1" || (*reqs)[1].Path != "/v1/sessions/S1/context" {
		t.Errorf("paths %s, %s", (*reqs)[0].Path, (*reqs)[1].Path)
	}
}

func TestSessionsReleaseForce(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sessionsTestBody))
	})
	if _, errb, code := runCLI(t, srv.URL, "sessions", "release", "S1", "--force"); code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	r := (*reqs)[0]
	if r.Method != http.MethodPost || r.Path != "/v1/sessions/S1/release" || r.Query != "force=true" {
		t.Errorf("got %s %s?%s", r.Method, r.Path, r.Query)
	}
}

func TestSessionsReleaseNotFoundExits9(t *testing.T) {
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, 404, "ERR::SESSION::NOT_FOUND", false)
	})
	_, errb, code := runCLI(t, srv.URL, "sessions", "release", "nope")
	if code != 9 || !strings.Contains(errb, "ERR::SESSION::NOT_FOUND") {
		t.Errorf("exit %d, stderr %s", code, errb)
	}
}

func TestSessionsDeleteNeedsYesWithoutTTY(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	_, errb, code := runCLI(t, srv.URL, "sessions", "delete", "S1")
	if code != 2 || len(*reqs) != 0 || !strings.Contains(errb, "--yes") {
		t.Fatalf("exit %d, %d requests, stderr %s", code, len(*reqs), errb)
	}
	out, errb, code := runCLI(t, srv.URL, "sessions", "delete", "S1", "--yes")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	r := (*reqs)[0]
	if r.Method != http.MethodDelete || r.Path != "/v1/sessions/S1" || r.Query != "" {
		t.Errorf("got %s %s?%s", r.Method, r.Path, r.Query)
	}
	if !strings.Contains(out, `"deleted": true`) {
		t.Errorf("stdout %s", out)
	}
}

func TestSessionsCreateSessionContextFile(t *testing.T) {
	bare := `{"cookies":[{"name":"sid","value":"abc","domain":".example.com","path":"/"}],"local_storage":{"https://example.com":{"k":"v"}}}`
	// What `spicrawl sessions context ID --json` prints: the context under
	// session_context, beside sidecar fields the create call does not take.
	dumped := `{"session_id":"01J9","schema_version":1,"session_context":` + bare + `,"usage_count":3}`

	for name, content := range map[string]string{"bare": bare, "dumped": dumped} {
		t.Run(name, func(t *testing.T) {
			path := t.TempDir() + "/ctx.json"
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(sessionsTestBody))
			})
			if _, errb, code := runCLI(t, srv.URL, "sessions", "create", "--session-context", path); code != 0 {
				t.Fatalf("exit %d: %s", code, errb)
			}
			var body map[string]json.RawMessage
			_ = json.Unmarshal((*reqs)[0].Body, &body)
			if len(body) != 1 {
				t.Fatalf("body %s, want only session_context", (*reqs)[0].Body)
			}
			var got, want any
			_ = json.Unmarshal(body["session_context"], &got)
			_ = json.Unmarshal([]byte(bare), &want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("session_context = %v, want %v", got, want)
			}
		})
	}
}

func TestSessionsCreateSessionContextBadFileIsUsageError(t *testing.T) {
	dir := t.TempDir()
	notJSON := dir + "/bad.json"
	_ = os.WriteFile(notJSON, []byte("cookies=1"), 0o600)
	notObject := dir + "/arr.json"
	_ = os.WriteFile(notObject, []byte(`[1,2]`), 0o600)
	for _, path := range []string{dir + "/missing.json", notJSON, notObject} {
		srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {})
		_, errb, code := runCLI(t, srv.URL, "sessions", "create", "--session-context", path)
		if code != 2 || len(*reqs) != 0 {
			t.Errorf("%s: exit %d, %d requests; want 2 and none (%s)", path, code, len(*reqs), errb)
		}
		if !strings.Contains(errb, "--session-context") {
			t.Errorf("%s: stderr %q should name the flag", path, errb)
		}
	}
}
