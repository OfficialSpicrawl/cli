package cmd

import (
	"net/http"
	"strings"
	"testing"
)

// An empty list in human mode is not data (DESIGN.md: stdout is data only):
// stdout stays empty, so `cmd | wc -l` is 0, and the message goes to stderr.
// JSON mode is unchanged: it prints the empty collection.
func TestHumanEmptyResultsAreStderrOnly(t *testing.T) {
	cases := []struct {
		name string
		args []string
		body string
		want string
	}{
		{"sessions list", []string{"sessions", "list"}, `{"sessions":[]}`, "no sessions"},
		{"logs", []string{"logs"}, `{"data":[],"page":{"has_more":false,"retention_hours":24}}`, "no requests in the last 24 hours"},
		{"batch list", []string{"batch", "list"}, `{"batches":[]}`, "no batch jobs"},
		{"batch results", []string{"batch", "results", "B1"}, ``, "no finished items yet"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(c.body))
			})
			clearContexts(rootCmd)
			out, errb, code := runCLIHuman(t, srv.URL, c.args...)
			if code != 0 || out != "" || strings.TrimSpace(errb) != c.want {
				t.Errorf("exit %d, stdout %q, stderr %q; want 0, empty, %q", code, out, errb, c.want)
			}

			clearContexts(rootCmd)
			out, errb, code = runCLIHuman(t, srv.URL, append([]string{"-q"}, c.args...)...)
			if code != 0 || out != "" || errb != "" {
				t.Errorf("-q: exit %d, stdout %q, stderr %q; want 0, empty, empty", code, out, errb)
			}
		})
	}
}
