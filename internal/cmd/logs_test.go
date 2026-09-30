package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const logsTestEntry = `{"id":"R1","created_at":"2026-09-22T10:00:00.5Z","engine":"fetch","status":"blocked","http_status":403,"url":"https://example.com","credits_micro":0,"proxy":{"source":"pool","country":"de"},"spans":{"admission_ms":1,"dispatch_ms":null,"worker_ms":null,"queue_ms":0,"engine_ms":null,"upstream_ms":120,"extract_ms":null,"total_ms":130},"blocked":{"vendor":"cloudflare","rule":"r","signal":"cf-mitigated"}}`

func TestLogsErrorsQuery(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[` + logsTestEntry + `],"page":{"has_more":false,"retention_hours":24,"retained_since":"2026-09-21T10:00:00Z"}}`))
	})
	out, errb, code := runCLI(t, srv.URL, "logs", "--errors", "--limit", "20")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	r := (*reqs)[0]
	if r.Path != "/v1/requests" || r.Query != "limit=20&only_errors=true" {
		t.Errorf("got %s?%s", r.Path, r.Query)
	}
	if !strings.Contains(out, `"id": "R1"`) {
		t.Errorf("stdout %s", out)
	}
}

func TestLogsAllFollowsBothCursorsJSONL(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("before") == "" {
			_, _ = w.Write([]byte(`{"data":[` + logsTestEntry + `],"page":{"has_more":true,"next_before":"2026-09-22T10:00:00.5Z","next_before_id":"R1","retention_hours":24,"retained_since":"x"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[` + logsTestEntry + `],"page":{"has_more":false,"retention_hours":24,"retained_since":"x"}}`))
	})
	out, errb, code := runCLI(t, srv.URL, "logs", "--all", "--jsonl", "--status", "Blocked")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if len(*reqs) != 2 {
		t.Fatalf("%d requests", len(*reqs))
	}
	if q := (*reqs)[1].Query; q != "before=2026-09-22T10%3A00%3A00.5Z&before_id=R1&status=blocked" {
		t.Errorf("second query %q", q)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 JSONL lines, got %q", out)
	}
	for _, l := range lines {
		var v map[string]any
		if err := json.Unmarshal([]byte(l), &v); err != nil {
			t.Errorf("line %q: %v", l, err)
		}
	}
}

func TestLogsBadStatusIsUsageError(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {})
	if _, _, code := runCLI(t, srv.URL, "logs", "--status", "error"); code != 2 || len(*reqs) != 0 {
		t.Errorf("exit %d, %d requests", code, len(*reqs))
	}
}

func TestLogsGetPathAndNotFound(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, 404, "ERR::REQUEST::NOT_FOUND", false)
	})
	_, _, code := runCLI(t, srv.URL, "logs", "get", "R1")
	if (*reqs)[0].Path != "/v1/requests/R1" || code != 4 {
		t.Errorf("path %s exit %d", (*reqs)[0].Path, code)
	}
}

func TestLogsHumanNullSpansAsDash(t *testing.T) {
	var e logsEntry
	if err := json.Unmarshal([]byte(logsTestEntry), &e); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	logsPrintEntry(&b, &e)
	s := b.String()
	for _, want := range []string{"dispatch         —", "queue            0", "total            130", "cloudflare", "pool"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
}

// --all-projects asks for the org-wide list on every page of the walk: the
// cursor pages the list it came from.
func TestLogsAllProjectsOnEveryPage(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("before") == "" {
			_, _ = w.Write([]byte(`{"data":[` + logsTestEntry + `],"page":{"has_more":true,"next_before":"2026-09-22T10:00:00.5Z","next_before_id":"R1","retention_hours":24,"retained_since":"x"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[],"page":{"has_more":false,"retention_hours":24,"retained_since":"x"}}`))
	})
	if _, errb, code := runCLI(t, srv.URL, "logs", "--all-projects", "--all"); code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if len(*reqs) != 2 {
		t.Fatalf("%d requests", len(*reqs))
	}
	if q := (*reqs)[0].Query; q != "all_projects=true" {
		t.Errorf("first query %q", q)
	}
	if q := (*reqs)[1].Query; q != "all_projects=true&before=2026-09-22T10%3A00%3A00.5Z&before_id=R1" {
		t.Errorf("second query %q", q)
	}

	// Without the flag the parameter is not sent at all.
	if _, _, code := runCLI(t, srv.URL, "logs"); code != 0 || strings.Contains((*reqs)[2].Query, "all_projects") {
		t.Errorf("exit %d, query %q", code, (*reqs)[2].Query)
	}
}

// The human table labels each row with its project only in the org-wide list.
func TestLogsAllProjectsHumanShowsTheProject(t *testing.T) {
	entry := strings.Replace(logsTestEntry, `"id":"R1",`, `"id":"R1","project_id":"P9",`, 1)
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[` + entry + `],"page":{"has_more":false,"retention_hours":24,"retained_since":"x"}}`))
	})
	out, errb, code := runCLIHuman(t, srv.URL, "logs", "--all-projects")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if !strings.Contains(out, "PROJECT") || !strings.Contains(out, "P9") {
		t.Errorf("org-wide table has no project column:\n%s", out)
	}
	out, _, _ = runCLIHuman(t, srv.URL, "logs")
	if strings.Contains(out, "PROJECT") {
		t.Errorf("project table has a project column:\n%s", out)
	}
}
