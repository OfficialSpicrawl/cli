package cmd

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OfficialSpicrawl/cli/internal/exitcode"
)

func TestExitCodesJSON(t *testing.T) {
	out, errb, code := runCLI(t, "https://api.example.test", "exit-codes")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	var got []exitcode.Entry
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not a JSON array: %v\n%s", err, out)
	}
	if len(got) != len(exitcode.Table) {
		t.Fatalf("%d entries, want %d", len(got), len(exitcode.Table))
	}
	for i, e := range got {
		if e.Code != exitcode.Table[i].Code || e.Name == "" || e.Meaning == "" {
			t.Errorf("entry %d: %+v", i, e)
		}
	}
	if got[12].Name != "timeout" {
		t.Errorf("12 = %+v", got[12])
	}
}

func TestExitCodesHuman(t *testing.T) {
	out, _, code := runCLIHuman(t, "https://api.example.test", "exit-codes")
	if code != 0 || !strings.Contains(out, "12  timeout") || strings.HasPrefix(strings.TrimSpace(out), "[") {
		t.Errorf("exit %d, stdout %s", code, out)
	}
}

func TestMalformedConfigIsUsage(t *testing.T) {
	path := t.TempDir() + "/config.json"
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPICRAWL_CONFIG", path)
	var out, errb strings.Builder
	stdout, stderr = &out, &errb
	defer func() { stdout, stderr = os.Stdout, os.Stderr; resetFlags(rootCmd) }()
	rootCmd.SetArgs([]string{"--json", "--base-url", "http://127.0.0.1:1", "usage"})
	if code := Execute(); code != exitcode.Usage {
		t.Errorf("exit %d, want %d: %s", code, exitcode.Usage, errb.String())
	}
	if !strings.Contains(errb.String(), path) {
		t.Errorf("error should name the file: %s", errb.String())
	}
}

func TestUnavailableCapabilityIsEngine(t *testing.T) {
	for _, tc := range []struct {
		retryable bool
		want      int
	}{{false, exitcode.Engine}, {true, exitcode.Internal}} {
		srv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
			writeProblem(w, http.StatusServiceUnavailable, "ERR::INTERNAL::UNAVAILABLE", tc.retryable)
		})
		_, errb, code := runCLI(t, srv.URL, "usage")
		if code != tc.want {
			t.Errorf("retryable=%v: exit %d, want %d: %s", tc.retryable, code, tc.want, errb)
		}
	}
}

func TestTimeoutAfterSendIsExit12(t *testing.T) {
	release := make(chan struct{})
	var n atomic.Int32 // the recorder's slice is only safe to read under its lock
	srv, _ := newRecordingServer(t, func(_ http.ResponseWriter, _ *http.Request) { n.Add(1); <-release })
	defer close(release)
	_, errb, code := runCLI(t, srv.URL, "--timeout", "150ms", "scrape", "https://example.com")
	if code != exitcode.Timeout {
		t.Fatalf("exit %d, want %d: %s", code, exitcode.Timeout, errb)
	}
	if !strings.Contains(errb, "spicrawl logs") {
		t.Errorf("stderr should point at spicrawl logs: %s", errb)
	}
	time.Sleep(20 * time.Millisecond)
	if got := n.Load(); got != 1 {
		t.Errorf("%d requests sent, want 1 (a timed-out POST must not be retried)", got)
	}
}

func TestConnectionRefusedIsNetwork(t *testing.T) {
	_, errb, code := runCLI(t, "http://127.0.0.1:1", "usage")
	if code != exitcode.Network {
		t.Errorf("exit %d, want %d: %s", code, exitcode.Network, errb)
	}
}
