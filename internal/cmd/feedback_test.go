package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const fbID = "0190f3a2-7c1e-7d4b-9a2f-3b8e5c6d7e80"

func fbHandler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func TestFeedbackFlagValidation(t *testing.T) {
	srv, reqs := newRecordingServer(t, fbHandler(201, `{}`))
	cases := map[string][]string{
		"both":    {"feedback", fbID, "--up", "--down"},
		"kind":    {"feedback", fbID, "--kind", "nope"},
		"impact":  {"feedback", fbID, "--impact", "nope"},
		"intent":  {"feedback", fbID, "--intent", strings.Repeat("a", 301)},
		"comment": {"feedback", fbID, "-m", strings.Repeat("a", 4001)},
		"noarg":   {"feedback"},
	}
	for name, args := range cases {
		_, _, code := runCLI(t, srv.URL, args...)
		if code == 0 {
			t.Errorf("%s: exit 0, want a usage failure", name)
		}
	}
	if n := len(recordedSoFar(reqs)); n != 0 {
		t.Errorf("%d requests sent for invalid input", n)
	}
	// Exactly at the limits is accepted (counted in characters, not bytes).
	_, stderr, code := runCLI(t, srv.URL, "feedback", fbID, "--intent", strings.Repeat("é", 300), "-m", strings.Repeat("a", 4000))
	if code != 0 {
		t.Errorf("limits: exit %d, %s", code, stderr)
	}
}

func fbBody(t *testing.T, reqs *[]recordedRequest) map[string]any {
	t.Helper()
	r := recordedSoFar(reqs)
	if len(r) != 1 || r[0].Method != "POST" || r[0].Path != "/v1/feedback" {
		t.Fatalf("requests = %+v", r)
	}
	var m map[string]any
	if err := json.Unmarshal(r[0].Body, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestFeedbackRequestBody(t *testing.T) {
	srv, reqs := newRecordingServer(t, fbHandler(201, `{"feedback_id":"f1","credits_refunded":2}`))
	t.Setenv("SPICRAWL_AGENT", "")
	_, stderr, code := runCLI(t, srv.URL, "feedback", fbID)
	if code != 0 {
		t.Fatalf("exit %d, %s", code, stderr)
	}
	m := fbBody(t, reqs)
	if m["request_id"] != fbID || m["rating"] != "down" {
		t.Errorf("body = %v", m)
	}
	if a, _ := m["agent"].(string); !strings.HasPrefix(a, "spicrawl-cli/") || strings.Contains(a, " ") {
		t.Errorf("agent = %q", a)
	}
	for _, k := range []string{"kind", "impact", "intent", "comment"} {
		if _, ok := m[k]; ok {
			t.Errorf("%s sent though not set", k)
		}
	}
}

func TestFeedbackRequestBodyAllFlags(t *testing.T) {
	srv, reqs := newRecordingServer(t, fbHandler(201, `{"feedback_id":"f1","credits_refunded":2}`))
	t.Setenv("SPICRAWL_AGENT", "  my-agent/1  ")
	_, stderr, code := runCLI(t, srv.URL, "feedback", fbID, "--up", "--kind", "wrong_result", "--impact", "friction",
		"--intent", "the price", "-m", "nav menu")
	if code != 0 {
		t.Fatalf("exit %d, %s", code, stderr)
	}
	m := fbBody(t, reqs)
	want := map[string]string{"rating": "up", "kind": "wrong_result", "impact": "friction", "intent": "the price", "comment": "nav menu"}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %v, want %s", k, m[k], v)
		}
	}
	if a, _ := m["agent"].(string); !strings.HasSuffix(a, " my-agent/1") {
		t.Errorf("agent = %q", a)
	}
}

func TestFeedbackAgentTruncated(t *testing.T) {
	t.Setenv("SPICRAWL_AGENT", strings.Repeat("é", 500))
	if n := len([]rune(feedbackAgent())); n != 200 {
		t.Errorf("agent length = %d, want 200", n)
	}
}

func TestFeedbackHumanOutput(t *testing.T) {
	cases := []struct {
		name, body, want string
		status           int
	}{
		{"created", `{"feedback_id":"f1","credits_refunded":3}`, "Reported (feedback f1). Refunded 3 credits.\n", 201},
		{"already", `{"feedback_id":"f1","already_submitted":true}`, "Already reported.\n", 200},
		{"cap", `{"feedback_id":"f1","credits_refunded":0,"refund_cap_reached":true}`,
			"Reported (feedback f1). Refunded 0 credits.\nThe refund cap for this period is reached.\n", 201},
	}
	for _, c := range cases {
		srv, _ := newRecordingServer(t, fbHandler(c.status, c.body))
		out, stderr, code := runCLIHuman(t, srv.URL, "feedback", fbID)
		if code != 0 || out != c.want {
			t.Errorf("%s: exit %d out %q stderr %q", c.name, code, out, stderr)
		}
	}
}

func TestFeedbackJSONPrintsBody(t *testing.T) {
	srv, _ := newRecordingServer(t, fbHandler(201, `{"feedback_id":"f1","credits_refunded":3}`))
	out, _, code := runCLI(t, srv.URL, "feedback", fbID)
	var m map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &m) != nil || m["feedback_id"] != "f1" || m["credits_refunded"] != 3.0 {
		t.Errorf("exit %d out %q", code, out)
	}
}

func TestFeedbackErrors(t *testing.T) {
	for _, status := range []int{404, 409, 429} {
		srv, _ := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "0")
			writeProblem(w, status, "FEEDBACK_ERR", false)
		})
		_, stderr, code := runCLIHuman(t, srv.URL, "feedback", fbID)
		if code == 0 || !strings.Contains(stderr, "FEEDBACK_ERR") {
			t.Errorf("%d: exit %d stderr %q", status, code, stderr)
		}
	}
}

func fbProblemServer(t *testing.T, extra map[string]any) string {
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		doc := map[string]any{"title": "Target failed", "status": 502, "code": "TARGET_FAILED", "retryable": false,
			"doc_url": "https://docs.spicrawl.com/errors", "target_status": nil}
		for k, v := range extra {
			doc[k] = v
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(502)
		_ = json.NewEncoder(w).Encode(doc)
	})
	return srv.URL
}

func TestReportHint(t *testing.T) {
	hint := `Report it: spicrawl feedback ` + fbID + ` --kind broken --intent "<what you wanted>"`
	cases := []struct {
		name  string
		extra map[string]any
		want  bool
	}{
		{"both", map[string]any{"request_id": fbID, "feedback_url": "https://api.spicrawl.com/v1/feedback"}, true},
		{"no url", map[string]any{"request_id": fbID}, false},
		{"no id", map[string]any{"feedback_url": "https://api.spicrawl.com/v1/feedback"}, false},
		{"neither", nil, false},
	}
	for _, c := range cases {
		_, stderr, code := runCLIHuman(t, fbProblemServer(t, c.extra), "scrape", "https://example.com")
		if code == 0 {
			t.Fatalf("%s: exit 0", c.name)
		}
		if got := strings.Contains(stderr, hint); got != c.want {
			t.Errorf("%s: hint present = %v, want %v\n%s", c.name, got, c.want, stderr)
		}
		if strings.Contains(stderr, "Report it:") != c.want {
			t.Errorf("%s: Report it line present = %v, want %v", c.name, !c.want, c.want)
		}
	}
}

func fbErrDoc(t *testing.T, extra map[string]any) map[string]any {
	t.Helper()
	_, stderr, code := runCLI(t, fbProblemServer(t, extra), "scrape", "https://example.com")
	if code == 0 {
		t.Fatal("exit 0")
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stderr)), &m); err != nil {
		t.Fatalf("stderr is not one JSON document: %v\n%s", err, stderr)
	}
	return m
}

func TestJSONErrorDocFeedbackFields(t *testing.T) {
	m := fbErrDoc(t, map[string]any{"request_id": fbID, "feedback_url": "https://api.spicrawl.com/v1/feedback"})
	if m["feedback_command"] != `spicrawl feedback `+fbID+` --kind broken --intent "<what you wanted>"` ||
		m["request_id"] != fbID || m["feedback_url"] != "https://api.spicrawl.com/v1/feedback" || m["code"] != "TARGET_FAILED" || m["title"] != "Target failed" {
		t.Errorf("doc = %v", m)
	}
	m = fbErrDoc(t, nil)
	if _, ok := m["feedback_command"]; ok {
		t.Errorf("feedback_command on a failure that cannot be reported: %v", m)
	}
	if id, ok := m["request_id"]; !ok || id != "" {
		t.Errorf("request_id must always be present, got %v (present %v)", id, ok)
	}
}

func TestScrapeStdinLineErrorCarriesFeedback(t *testing.T) {
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(502)
		_ = json.NewEncoder(w).Encode(map[string]any{"title": "x", "status": 502, "code": "TARGET_FAILED",
			"request_id": fbID, "feedback_url": "https://api.spicrawl.com/v1/feedback", "target_status": nil})
	})
	scrapeWithStdin(t, "https://a.example\n")
	out, _, code := scrapeRunCLI(t, srv.URL, "scrape", "-")
	if code == 0 {
		t.Fatal("exit 0")
	}
	var line struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &line); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if line.Error["request_id"] != fbID || line.Error["feedback_command"] == nil || line.Error["code"] != "TARGET_FAILED" {
		t.Errorf("error = %v", line.Error)
	}
}
