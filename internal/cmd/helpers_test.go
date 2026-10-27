package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TestMain gives the whole package an empty home, so what is installed on the
// developer's machine (agent clients under ~, $CODEX_HOME, how npm started
// the tests) never changes a result. Tests that need files in a home set their own.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "spicrawl-test-home-")
	if err != nil {
		panic(err)
	}
	for _, k := range []string{"HOME", "USERPROFILE"} {
		os.Setenv(k, home)
	}
	for _, k := range []string{"XDG_CONFIG_HOME", "CODEX_HOME", "npm_command"} {
		os.Unsetenv(k)
	}
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

// testKey is the $SPICRAWL_API_KEY runCLI exports; a test clears it to run without a key.
var testKey = "spicrawl_test_key"

// testConfig, when set, is the config file runCLI uses, so that a key saved by
// one call is there for the next. Tests set it and clear it again.
var testConfig = ""

// runCLI executes the command tree in-process against baseURL and returns
// stdout, stderr and the exit code. It forces --json and a test key, points
// the config file at a temp dir, and resets every flag afterwards so tests
// do not leak state into each other.
func runCLI(t *testing.T, baseURL string, args ...string) (string, string, int) {
	t.Helper()
	cfg := t.TempDir() + "/config.json"
	if testConfig != "" {
		cfg = testConfig
	}
	t.Setenv("SPICRAWL_CONFIG", cfg)
	t.Setenv("SPICRAWL_API_KEY", testKey)
	var out, errb bytes.Buffer
	stdout, stderr = &out, &errb
	defer func() {
		stdout, stderr = os.Stdout, os.Stderr
		resetFlags(rootCmd)
	}()
	full := append([]string{"--json", "--base-url", baseURL}, args...)
	rootCmd.SetArgs(full)
	code := Execute()
	return out.String(), errb.String(), code
}

func resetFlags(c *cobra.Command) {
	reset := func(f *pflag.Flag) {
		if f.Changed {
			if sv, ok := f.Value.(pflag.SliceValue); ok {
				_ = sv.Replace(nil)
			} else {
				_ = f.Value.Set(f.DefValue)
			}
			f.Changed = false
		}
	}
	c.Flags().VisitAll(reset)
	c.PersistentFlags().VisitAll(reset)
	for _, sub := range c.Commands() {
		resetFlags(sub)
	}
}

// recordingServer is an httptest server that records the last request and
// answers with handler.
type recordedRequest struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   []byte
}

// recordedMu orders the servers' appends against recordedSoFar. A reader that
// runs while a cancelled request is still inside its handler (a stopped watch)
// has no other happens-before edge to that append.
var recordedMu sync.Mutex

// recordedSoFar is *reqs for a reader that may overlap a handler still running.
// Appends only write past len, so the elements of the returned slice are stable.
func recordedSoFar(reqs *[]recordedRequest) []recordedRequest {
	recordedMu.Lock()
	defer recordedMu.Unlock()
	return *reqs
}

func newRecordingServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *[]recordedRequest) {
	t.Helper()
	var reqs []recordedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b bytes.Buffer
		_, _ = b.ReadFrom(r.Body)
		recordedMu.Lock()
		reqs = append(reqs, recordedRequest{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone(), b.Bytes()})
		recordedMu.Unlock()
		r.Body = http.NoBody
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &reqs
}

// writeProblem answers with an RFC 7807 body.
func writeProblem(w http.ResponseWriter, status int, code string, retryable bool) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type": "https://docs.spicrawl.com/errors", "title": "test problem", "status": status,
		"code": code, "retryable": retryable, "doc_url": "https://docs.spicrawl.com/errors",
		"target_status": nil,
	})
}
