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

// runCLI executes the command tree in-process against baseURL and returns
// stdout, stderr and the exit code. It forces --json and a test key, points
// the config file at a temp dir, and resets every flag afterwards so tests
// do not leak state into each other.
func runCLI(t *testing.T, baseURL string, args ...string) (string, string, int) {
	t.Helper()
	t.Setenv("SPICRAWL_CONFIG", t.TempDir()+"/config.json")
	t.Setenv("SPICRAWL_API_KEY", "spicrawl_test_key")
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

func newRecordingServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *[]recordedRequest) {
	t.Helper()
	var (
		reqs []recordedRequest
		mu   sync.Mutex
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b bytes.Buffer
		_, _ = b.ReadFrom(r.Body)
		mu.Lock()
		reqs = append(reqs, recordedRequest{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone(), b.Bytes()})
		mu.Unlock()
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
