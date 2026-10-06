package cmd

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// A job that ends failed or cancelled is not a success: wait and submit --wait
// print it but exit 4.
func TestBatchWaitFailedOrCancelledExitsNonZero(t *testing.T) {
	for _, status := range []string{"failed", "cancelled"} {
		srv, _ := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "POST" {
				w.WriteHeader(http.StatusAccepted)
			}
			fmt.Fprint(w, strings.Replace(batchJobJSON("JF", status, 1, 3), `"submitted_at"`, `"error_code":"ERR::LIMIT::BUDGET","submitted_at"`, 1))
		})
		file := batchWriteFile(t, "urls.txt", "https://example.com/1\n")
		for _, args := range [][]string{{"batch", "wait", "JF"}, {"batch", "submit", file, "--wait"}} {
			out, errOut, code := batchRunCLI(t, srv.URL, append(args, "--poll", "5ms")...)
			if code != 4 {
				t.Errorf("%v (%s): exit %d, want 4; stderr %s", args[:2], status, code, errOut)
			}
			if batchDecodeBody(t, []byte(out))["status"] != status || !strings.Contains(errOut, "batch JF "+status) || !strings.Contains(errOut, "ERR::LIMIT::BUDGET") {
				t.Errorf("%v (%s): stdout %s stderr %s", args[:2], status, out, errOut)
			}
		}
	}
}

// -o is written to a temp file and renamed on success: a 410 leaves the
// existing file intact and no temp file behind.
func TestBatchResultsKeepsExistingFileOnFailure(t *testing.T) {
	fail := true
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if fail {
			writeProblem(w, http.StatusGone, "ERR::REQUEST::BEYOND_RETENTION", false)
			return
		}
		fmt.Fprint(w, `{"seq":0,"url":"u","status":"succeeded"}`)
	})
	dir := t.TempDir()
	path := filepath.Join(dir, "results.jsonl")
	if err := os.WriteFile(path, []byte("previous\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := batchRunCLI(t, srv.URL, "batch", "results", "J", "-o", path); code != 4 {
		t.Fatalf("exit %d, want 4; stderr %s", code, errOut)
	}
	if b, _ := os.ReadFile(path); string(b) != "previous\n" {
		t.Errorf("existing file changed: %q", b)
	}
	fail = false
	if _, errOut, code := batchRunCLI(t, srv.URL, "batch", "results", "J", "-o", path); code != 0 {
		t.Fatalf("exit %d; stderr %s", code, errOut)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"seq":0`) {
		t.Errorf("file not replaced: %q", b)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o640 {
		t.Errorf("mode %v, want the old file's 0640", fi.Mode().Perm())
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Errorf("leftover files in %s: %v", dir, ents)
	}
}

// One 502 or dropped connection must not abort the wait; five in a row do.
func TestBatchWaitToleratesTransientFailures(t *testing.T) {
	var gets atomic.Int32
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch gets.Add(1) {
		case 1:
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, "<html>bad gateway</html>")
		case 2:
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close() // network error: the connection drops mid-request
		case 3:
			fmt.Fprint(w, batchJobJSON("JT", "running", 1, 2))
		case 4:
			w.WriteHeader(http.StatusGatewayTimeout)
		default:
			fmt.Fprint(w, batchJobJSON("JT", "completed", 2, 2))
		}
	})
	out, errOut, code := batchRunCLI(t, srv.URL, "batch", "wait", "JT", "--poll", "2ms")
	if code != 0 || batchDecodeBody(t, []byte(out))["status"] != "completed" {
		t.Fatalf("exit %d stdout %s stderr %s", code, out, errOut)
	}

	var dead atomic.Int32
	srv2, _ := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		dead.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	})
	if _, _, code := batchRunCLI(t, srv2.URL, "batch", "wait", "JD", "--poll", "1ms"); code == 0 || dead.Load() != batchWaitMaxFailures {
		t.Errorf("exit %d after %d polls, want non-zero after %d", code, dead.Load(), batchWaitMaxFailures)
	}
}

// --body null used to leave a nil map that batch submit then wrote into
// (panic). It is a usage error now, and nothing is sent.
func TestBatchSubmitBodyNullIsAUsageError(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {})
	file := batchWriteFile(t, "urls.txt", "https://example.com/1\n")
	if _, errOut, code := batchRunCLI(t, srv.URL, "batch", "submit", file, "--body", "null"); code != 2 || !strings.Contains(errOut, "null") {
		t.Errorf("exit %d: %s", code, errOut)
	}
	if len(*reqs) != 0 {
		t.Errorf("%d requests sent", len(*reqs))
	}
}
