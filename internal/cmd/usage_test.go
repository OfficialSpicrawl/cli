package cmd

import (
	"net/http"
	"strings"
	"testing"
)

func TestUsageQuery(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"period":{"start":"2026-08-01","end":"2026-09-01","days":31,"bounds":"[start, end)"},"group_by":"engine","groups":[],"totals":{},"unattributed":{},"usage_as_of":null,"stale_seconds":null}`))
	})
	_, errb, code := runCLI(t, srv.URL, "usage", "--from", "2026-08-01", "--to", "2026-09-01", "--group-by", "engine", "--project", "P1", "--metrics", "credits,requests")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	r := (*reqs)[0]
	if r.Path != "/v1/usage" || r.Query != "from=2026-08-01&group_by=engine&metrics=credits%2Crequests&project_id=P1&to=2026-09-01" {
		t.Errorf("got %s?%s", r.Path, r.Query)
	}
}

func TestUsageGroupByKey(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"period":{"start":"2026-08-01","end":"2026-09-01","days":31,"bounds":"[start, end)"},"group_by":"key","keys":[{"key_id":"K1","name":"ci-key","prefix":"spicrawl_live_ab12","project_id":"P1","revoked":false,"days":[{"day":"2026-08-18","metrics":{"requests":7,"client_cli":3}}],"totals":{"requests":7,"client_cli":3}}]}`))
	})
	out, errb, code := runCLI(t, srv.URL, "usage", "--group-by", "key", "--metrics", "requests,client_cli,feature_pdf")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	r := (*reqs)[0]
	if r.Query != "group_by=key&metrics=requests%2Cclient_cli%2Cfeature_pdf" {
		t.Errorf("query %s", r.Query)
	}
	if !strings.Contains(out, "ci-key") || !strings.Contains(out, "spicrawl_live_ab12") {
		t.Errorf("output lacks key name/prefix: %s", out)
	}
}

func TestUsageSubcommandPaths(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	runCLI(t, srv.URL, "usage", "summary")
	runCLI(t, srv.URL, "usage", "reconciliation", "--day", "2026-09-21")
	if (*reqs)[0].Path != "/v1/usage/summary" || (*reqs)[0].Query != "" {
		t.Errorf("summary %s?%s", (*reqs)[0].Path, (*reqs)[0].Query)
	}
	if (*reqs)[1].Path != "/v1/usage/reconciliation" || (*reqs)[1].Query != "day=2026-09-21" {
		t.Errorf("reconciliation %s?%s", (*reqs)[1].Path, (*reqs)[1].Query)
	}
}

func TestUsageUsageErrors(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {})
	for _, args := range [][]string{
		{"usage", "--from", "08/01/2026"},
		{"usage", "--from", "2026-09-01", "--to", "2026-09-01"},
		{"usage", "--group-by", "week"},
		{"usage", "--metrics", "dollars"},
		{"usage", "reconciliation", "--day", "yesterday"},
	} {
		if _, _, code := runCLI(t, srv.URL, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	if len(*reqs) != 0 {
		t.Errorf("%d requests sent", len(*reqs))
	}
}

func TestUsageInsufficientScope(t *testing.T) {
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, 403, "ERR::AUTH::INSUFFICIENT_SCOPE", false)
	})
	_, errb, code := runCLI(t, srv.URL, "usage", "summary")
	if code != 3 || !strings.Contains(errb, "INSUFFICIENT_SCOPE") {
		t.Errorf("exit %d, stderr %s", code, errb)
	}
}
