package cmd

import (
	"bytes"
	"net/http"
	"os"
	"strings"
	"testing"
)

// runCLIPiped is runCLI without --json, like a script capturing stdout: the
// output is not a terminal, so the printer picks JSON unless told otherwise.
func runCLIPiped(t *testing.T, baseURL string, args ...string) (string, string, int) {
	t.Helper()
	t.Setenv("SPICRAWL_CONFIG", t.TempDir()+"/config.json")
	t.Setenv("SPICRAWL_API_KEY", "spicrawl_test_key")
	var out, errb bytes.Buffer
	stdout, stderr = &out, &errb
	defer func() {
		stdout, stderr = os.Stdout, os.Stderr
		resetFlags(rootCmd)
	}()
	rootCmd.SetArgs(append([]string{"--base-url", baseURL}, args...))
	code := Execute()
	return out.String(), errb.String(), code
}

// A server without CDP (no route, or 501 ERR::ENGINE::UNAVAILABLE) is a
// permanent condition: say so and exit 8, not "NOT_FOUND ... exit 4".
func TestBrowserURLWhenCDPIsDisabled(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"404": func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
		"501": func(w http.ResponseWriter, r *http.Request) { writeProblem(w, 501, "ERR::ENGINE::UNAVAILABLE", false) },
	} {
		srv, _ := newRecordingServer(t, h)
		_, errb, code := runCLI(t, srv.URL, "browser", "url")
		if code != 8 || !strings.Contains(errb, "cloud browser (CDP) is not enabled on this server") {
			t.Errorf("%s: exit %d, stderr %s", name, code, errb)
		}
	}
}

// The documented $(spicrawl browser url -q) prints the bare URL although
// stdout is piped; an explicit --json still gets JSON.
func TestBrowserURLQuietPrintsBareURLWhenPiped(t *testing.T) {
	srv := (&fakeTokenAPI{}).server(t)
	out, errb, code := runCLIPiped(t, srv.URL, "-q", "browser", "url")
	if code != 0 || !strings.HasPrefix(out, "ws://") || strings.Count(out, "\n") != 1 {
		t.Fatalf("exit %d stdout %q stderr %q", code, out, errb)
	}
	if out, _, _ := runCLIPiped(t, srv.URL, "-q", "--json", "browser", "url"); !strings.HasPrefix(out, "{") {
		t.Errorf("-q --json: %q", out)
	}
}
