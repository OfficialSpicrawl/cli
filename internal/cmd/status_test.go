package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

// The checklist is for a person at a terminal. Piped, or with the live output
// off, the old key/value lines come out byte for byte.
func TestStatusHumanPlainIsUnchanged(t *testing.T) {
	srv, _ := statusServer(t, true)
	out, errb, code := runCLIHuman(t, srv.URL, "status")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	want := "base url:    " + srv.URL + "\n" +
		"reachable:   yes\n" +
		"ready:       no (redis=unreachable)\n" +
		"cdp:         disabled (cloud browser /v1/browser is not available on this deployment)\n" +
		"key:         valid (from env)\n" +
		"workers:\n" +
		"  browser    1 ready of 2\n" +
		"  egress     0 ready of 0\n"
	if out != want {
		t.Errorf("stdout changed:\n%q\nwant\n%q", out, want)
	}
	noEscapes(t, "stdout", out)
	noEscapes(t, "stderr", errb)
}

func TestStatusLiveChecklist(t *testing.T) {
	liveUI(t)
	srv, _ := statusServer(t, true)
	out, errb, code := runCLIHuman(t, srv.URL, "status")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("no colour on a terminal: %q", out)
	}
	plain := stripANSI(out)
	for _, want := range []string{
		"Spicrawl API  " + srv.URL,
		"✓ reachable  yes",
		"✗ ready      no (redis=unreachable)",
		"– cdp        disabled",
		"✓ key        valid (from env)",
		"workers",
		"! browser    1 ready of 2",
		"– egress     0 ready of 0",
		"✗ 1 check failed",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("missing %q in\n%s", want, plain)
		}
	}
}

func TestStatusLiveAllGood(t *testing.T) {
	liveUI(t)
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/readyz":
			_, _ = w.Write([]byte(`{"status":"ready","checks":{"postgres":"ok"},"cdp":"enabled"}`))
		case "/v1/workers":
			_, _ = w.Write([]byte(`{"kinds":[{"kind":"browser","present":2,"ready":2}]}`))
		default:
			_, _ = w.Write([]byte(`{"data":[],"page":{}}`))
		}
	})
	out, _, code := runCLIHuman(t, srv.URL, "status")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	plain := stripANSI(out)
	if !strings.Contains(plain, "✓ cdp        enabled") || !strings.Contains(plain, "✓ browser    2 ready of 2") || !strings.Contains(plain, "✓ all checks passed") {
		t.Errorf("\n%s", plain)
	}
}

func TestStatusLiveUnreachableAndBadKey(t *testing.T) {
	liveUI(t)
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	out, _, code := runCLIHuman(t, base, "status")
	if code != 10 || !strings.Contains(stripANSI(out), "✗ reachable  no") || !strings.Contains(stripANSI(out), "could not be reached") {
		t.Errorf("exit %d\n%s", code, stripANSI(out))
	}

	bad, _ := statusServer(t, false)
	out, _, code = runCLIHuman(t, bad.URL, "status")
	if code != 3 || !strings.Contains(stripANSI(out), "✗ key") {
		t.Errorf("exit %d\n%s", code, stripANSI(out))
	}
}

// JSON, --json and --quiet leave both streams free of escapes even when the
// streams would be terminals.
func TestStatusLiveNeverTouchesMachineOutput(t *testing.T) {
	spinNow(t)
	srv, _ := statusServer(t, true)
	out, errb, code := runCLI(t, srv.URL, "status")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	var rep statusReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	noEscapes(t, "stdout", out)
	noEscapes(t, "stderr", errb)

	out, errb, _ = runCLIHuman(t, srv.URL, "-q", "status")
	noEscapes(t, "quiet stdout", out)
	noEscapes(t, "quiet stderr", errb)
}

func TestStatusSpinnerNamesTheWait(t *testing.T) {
	spinNow(t)
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond)
		switch r.URL.Path {
		case "/readyz":
			_, _ = w.Write([]byte(`{"status":"ready","checks":{},"cdp":"enabled"}`))
		case "/v1/workers":
			_, _ = w.Write([]byte(`{"kinds":[]}`))
		default:
			_, _ = w.Write([]byte(`{"data":[],"page":{}}`))
		}
	})
	out, errb, code := runCLIHuman(t, srv.URL, "status")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	for _, want := range []string{"Checking the API", "Checking your key", "Reading worker status"} {
		if !strings.Contains(errb, want) {
			t.Errorf("stderr lacks %q: %q", want, errb)
		}
	}
	if strings.Contains(out, "Checking") {
		t.Errorf("spinner text on stdout: %q", out)
	}
	if !strings.Contains(errb, "\x1b[?25h") {
		t.Errorf("cursor not restored: %q", errb)
	}
}
