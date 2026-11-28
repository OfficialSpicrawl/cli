package cmd

import (
	"encoding/json"
	"github.com/OfficialSpicrawl/cli/internal/api"
	"strings"
	"testing"
)

func TestRequestBodyJSONObjectIsSentAsObject(t *testing.T) {
	srv, reqs := newRecordingServer(t, scrapeMarkdownHandler)
	_, stderr, code := scrapeRunCLI(t, srv.URL, "scrape", "https://example.com", "--method", "POST", "--request-body", `{"q":"shoes","n":2}`)
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
	b := scrapeTestBody(t, reqs)
	obj, ok := b["body"].(map[string]any)
	if !ok || obj["q"] != "shoes" || obj["n"] != float64(2) {
		t.Errorf("body = %#v, want the JSON object", b["body"])
	}
	if b["method"] != "POST" {
		t.Errorf("method = %v", b["method"])
	}
}

func TestRequestBodyPlainTextIsSentAsString(t *testing.T) {
	srv, reqs := newRecordingServer(t, scrapeMarkdownHandler)
	_, stderr, code := scrapeRunCLI(t, srv.URL, "scrape", "https://example.com", "--method", "POST", "--request-body", "q=shoes&n=2")
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
	if got := scrapeTestBody(t, reqs)["body"]; got != "q=shoes&n=2" {
		t.Errorf("body = %#v, want the string", got)
	}
}

func TestRequestBodyFileFromStdin(t *testing.T) {
	srv, reqs := newRecordingServer(t, scrapeMarkdownHandler)
	scrapeWithStdin(t, `{"from":"stdin"}`)
	_, stderr, code := scrapeRunCLI(t, srv.URL, "scrape", "https://example.com", "--method", "POST", "--request-body-file", "-")
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
	obj, ok := scrapeTestBody(t, reqs)["body"].(map[string]any)
	if !ok || obj["from"] != "stdin" {
		t.Errorf("body = %#v", scrapeTestBody(t, reqs)["body"])
	}
}

func TestRequestBodyFlagsAreExclusive(t *testing.T) {
	_, stderr, code := scrapeRunCLI(t, "http://127.0.0.1:1", "scrape", "https://example.com",
		"--request-body", "a", "--request-body-file", "-")
	if code != 2 || !strings.Contains(stderr, "mutually exclusive") {
		t.Fatalf("exit %d, stderr %s; want usage error 2", code, stderr)
	}
}

func TestBatchSubmitDoesNotTakeRequestBody(t *testing.T) {
	for _, name := range []string{"request-body", "request-body-file"} {
		if batchSubmitCmd.Flags().Lookup(name) != nil {
			t.Errorf("--%s is registered on batch submit", name)
		}
	}
}

func fixServer(t *testing.T, fix any) string {
	return fbProblemServer(t, map[string]any{"fix": fix})
}

func TestReportPrintsCorrectedRequest(t *testing.T) {
	url := fixServer(t, map[string]any{"method": "POST"})
	_, stderr, code := runCLIHuman(t, url, "scrape", "https://example.com")
	if code == 0 || !strings.Contains(stderr, "Corrected request:") || !strings.Contains(stderr, `{"method":"POST"}`) {
		t.Errorf("exit %d, stderr %s; want the corrected request", code, stderr)
	}
	_, stderr, _ = runCLIHuman(t, fbProblemServer(t, nil), "scrape", "https://example.com")
	if strings.Contains(stderr, "Corrected request:") {
		t.Errorf("no fix, but stderr prints one: %s", stderr)
	}
}

func TestJSONModeHasFixFieldNotText(t *testing.T) {
	_, stderr, _ := runCLI(t, fixServer(t, map[string]any{"method": "POST"}), "scrape", "https://example.com")
	if strings.Contains(stderr, "Corrected request:") {
		t.Errorf("--json prints the text line: %s", stderr)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stderr)), &m); err != nil || m["fix"] == nil {
		t.Errorf("doc %v (%v) lacks fix", m, err)
	}
}

func TestCorrectedRequestShortensLargeBody(t *testing.T) {
	big := strings.Repeat("x", 60000)
	out := correctedRequestOf(t, `{"method":"POST","body":"`+big+`"}`)
	if out != `{"body":"<your body, 60000 bytes>","method":"POST"}` {
		t.Errorf("got %.120s", out)
	}
	small := `{"method":"POST","body":"a=1"}`
	if got := correctedRequestOf(t, small); got != small {
		t.Errorf("small fix changed: %s", got)
	}
}

func correctedRequestOf(t *testing.T, fix string) string {
	t.Helper()
	return correctedRequest(&api.Problem{Fix: json.RawMessage(fix)})
}
