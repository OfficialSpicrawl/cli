package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func statusServer(t *testing.T, keyOK bool) (*httptest.Server, *[]recordedRequest) {
	return newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/readyz":
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"not_ready","checks":{"postgres":"ok","redis":"unreachable"},"cdp":"disabled"}`))
		case "/v1/requests":
			if !keyOK {
				writeProblem(w, 401, "ERR::AUTH::INVALID_KEY", false)
				return
			}
			_, _ = w.Write([]byte(`{"data":[],"page":{"has_more":false,"retention_hours":24,"retained_since":"x"}}`))
		case "/v1/workers":
			_, _ = w.Write([]byte(`{"kinds":[{"kind":"browser","present":2,"ready":1,"workers":[]},{"kind":"egress","present":0,"ready":0,"workers":[]}]}`))
		default:
			w.WriteHeader(404)
		}
	})
}

func TestStatusReport(t *testing.T) {
	srv, reqs := statusServer(t, true)
	out, errb, code := runCLI(t, srv.URL, "status")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	var rep statusReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	if !rep.APIReachable || rep.Ready || rep.KeyValid == nil || !*rep.KeyValid || rep.KeySource != "env" || rep.BaseURL != srv.URL {
		t.Errorf("report %+v", rep)
	}
	if rep.Checks["redis"] != "unreachable" || rep.Workers["browser"] != (statusWorkers{Present: 2, Ready: 1}) {
		t.Errorf("checks %v workers %v", rep.Checks, rep.Workers)
	}
	if (*reqs)[0].Header.Get("Authorization") != "" {
		t.Errorf("/readyz must be called without auth")
	}
	if (*reqs)[1].Path != "/v1/requests" || (*reqs)[1].Query != "limit=1" {
		t.Errorf("key check %s?%s", (*reqs)[1].Path, (*reqs)[1].Query)
	}
}

func TestStatusInvalidKeyExits3(t *testing.T) {
	srv, _ := statusServer(t, false)
	out, _, code := runCLI(t, srv.URL, "status")
	if code != 3 {
		t.Fatalf("exit %d, want 3", code)
	}
	var rep statusReport
	_ = json.Unmarshal([]byte(out), &rep)
	if rep.KeyValid == nil || *rep.KeyValid {
		t.Errorf("key_valid %v", rep.KeyValid)
	}
}

func TestStatusUnreachableExits10(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	out, _, code := runCLI(t, base, "status")
	if code != 10 {
		t.Fatalf("exit %d, want 10", code)
	}
	var rep statusReport
	_ = json.Unmarshal([]byte(out), &rep)
	if rep.APIReachable {
		t.Errorf("api_reachable true for a closed server")
	}
}

// 5.5: status names whether the cloud browser is available, so a deployment
// whose /v1/browser answers 501 does not read as a working browser.
func TestStatusReportsCDP(t *testing.T) {
	srv, _ := statusServer(t, true)
	out, errb, code := runCLI(t, srv.URL, "status")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	var rep statusReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	if rep.CDP != "disabled" {
		t.Errorf("cdp = %q, want disabled", rep.CDP)
	}
	human, _, _ := runCLIHuman(t, srv.URL, "status")
	if !strings.Contains(human, "cdp:") || !strings.Contains(human, "disabled") {
		t.Errorf("human output lacks the cdp line:\n%s", human)
	}
}
