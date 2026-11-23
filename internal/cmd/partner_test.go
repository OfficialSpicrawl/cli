package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/OfficialSpicrawl/cli/internal/exitcode"
)

// partnerStdin makes os.Stdin a plain empty file for the test, so that a
// developer's terminal cannot turn a refusal into a prompt.
func partnerStdin(t *testing.T) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	prev := os.Stdin
	os.Stdin = f
	t.Cleanup(func() {
		os.Stdin = prev
		f.Close()
	})
}

type pgReply struct {
	status int
	body   string
}

// pgFake answers the routes ("METHOD path", decoded path) and records every request.
func pgFake(t *testing.T, routes map[string]pgReply) (*httptest.Server, *[]recordedRequest) {
	t.Helper()
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		rep, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			writeProblem(w, http.StatusNotFound, "ERR::REQUEST::NOT_FOUND", false)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rep.status)
		_, _ = io.WriteString(w, rep.body)
	})
	return srv, reqs
}

// pgJSONBody decodes a recorded request's body; nil when it had none.
func pgJSONBody(t *testing.T, r recordedRequest) map[string]any {
	t.Helper()
	if len(r.Body) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(r.Body, &m); err != nil {
		t.Fatalf("request body %q: %v", r.Body, err)
	}
	return m
}

var pgHexKey = regexp.MustCompile(`^[0-9a-f]{32}$`)

func TestPartnerUserPath(t *testing.T) {
	p, err := partnerUserPath("acct:7.eu_west")
	if err != nil || p != "/v1/partner/workspaces/acct%3A7.eu_west" {
		t.Fatalf("path = %q, %v; want the id percent-encoded", p, err)
	}
	for _, bad := range []string{"jane@example.com", "", "has space", strings.Repeat("a", 129)} {
		if _, err := partnerUserPath(bad); ExitCode(err) != exitcode.Usage {
			t.Errorf("%q: exit %d, want usage (2)", bad, ExitCode(err))
		}
	}
}

// The escaped path on the wire, not the decoded one, is what the API routes on.
func TestPartnerCreateRequestBuilding(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.EscapedPath())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, pgCreated)
	})

	_, _, code := runCLI(t, srv.URL, "partner", "users", "create", "acct:7.eu_west", "--tier", "Standard", "--name", " Jane ")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	mu.Lock()
	gotPath := paths[0]
	mu.Unlock()
	if gotPath != "/v1/partner/workspaces/acct%3A7.eu_west" {
		t.Errorf("escaped path = %q", gotPath)
	}
	if r := (*reqs)[0]; r.Method != http.MethodPut {
		t.Errorf("method = %s, want PUT", r.Method)
	}
	if b := pgJSONBody(t, (*reqs)[0]); b["tier"] != "Standard" || b["display_name"] != "Jane" || len(b) != 2 {
		t.Errorf("body = %v, want tier and the trimmed display_name only", b)
	}

	// No flags: no body at all, not a JSON null.
	if _, _, code := runCLI(t, srv.URL, "partner", "users", "create", "user-42"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if n := len((*reqs)[1].Body); n != 0 {
		t.Errorf("a create with no flags sent a body: %q", (*reqs)[1].Body)
	}
}

func TestPartnerListQueryAndAll(t *testing.T) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("after") == "" {
			_, _ = io.WriteString(w, `{"workspaces":[`+pgWorkspace+`],"next_after":"user-42"}`)
			return
		}
		_, _ = io.WriteString(w, `{"workspaces":[`+pgWorkspaceNoTier+`]}`)
	})
	out, _, code := runCLI(t, srv.URL, "partner", "users", "list", "--query", "user-4", "--limit", "5", "--all")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(*reqs) != 2 {
		t.Fatalf("%d requests, want 2 pages", len(*reqs))
	}
	q0, _ := url.ParseQuery((*reqs)[0].Query)
	if q0.Get("q") != "user-4" || q0.Get("limit") != "5" || q0.Get("sort") != "external_user_id" || q0.Get("after") != "" {
		t.Errorf("first page query = %v", q0)
	}
	q1, _ := url.ParseQuery((*reqs)[1].Query)
	if q1.Get("after") != "user-42" {
		t.Errorf("second page query = %v, want after=user-42", q1)
	}
	var got struct {
		Workspaces []json.RawMessage `json:"workspaces"`
		NextAfter  *string           `json:"next_after"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Workspaces) != 2 || got.NextAfter != nil {
		t.Errorf("--all printed %d workspaces, next_after %v; want both pages and no cursor", len(got.Workspaces), got.NextAfter)
	}
}

func TestPartnerListSortAndOffsetRules(t *testing.T) {
	srv, reqs := pgFake(t, map[string]pgReply{"GET /v1/partner/workspaces": {200, `{"workspaces":[]}`}})
	if _, _, code := runCLI(t, srv.URL, "partner", "users", "list", "--sort", "used", "--offset", "10", "--limit", "5"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	q, _ := url.ParseQuery((*reqs)[0].Query)
	if q.Get("sort") != "wallet_used" || q.Get("offset") != "10" || q.Get("limit") != "5" || q.Has("after") {
		t.Errorf("used-sort query = %v", q)
	}
	// Each of these is refused before anything is sent.
	for _, args := range [][]string{
		{"partner", "users", "list", "--sort", "used", "--after", "user-1"},
		{"partner", "users", "list", "--offset", "3"},
		{"partner", "users", "list", "--sort", "name"},
	} {
		n := len(*reqs)
		if _, _, code := runCLI(t, srv.URL, args...); code != exitcode.Usage || len(*reqs) != n {
			t.Errorf("%v: exit %d, %d requests sent; want usage and none", args, code, len(*reqs)-n)
		}
	}
}

func TestPartnerTierCapRule(t *testing.T) {
	srv, reqs := pgFake(t, map[string]pgReply{"POST /v1/partner/tiers": {201, pgTier}})
	base := []string{"partner", "tiers", "create", "Std", "--requests-per-minute", "60", "--concurrency", "2", "--free-credits", "250"}

	if _, _, code := runCLI(t, srv.URL, base...); code != exitcode.Usage {
		t.Errorf("no --cap or --no-cap: exit %d, want usage", code)
	}
	if _, _, code := runCLI(t, srv.URL, append(append([]string{}, base...), "--cap", "5", "--no-cap")...); code != exitcode.Usage {
		t.Errorf("--cap with --no-cap: exit %d, want usage", code)
	}
	if len(*reqs) != 0 {
		t.Fatalf("a refused tier create sent %d requests", len(*reqs))
	}
	if _, _, code := runCLI(t, srv.URL, append(append([]string{}, base...), "--no-cap")...); code != 0 {
		t.Fatalf("--no-cap: exit %d", code)
	}
	if b := (*reqs)[0].Body; !strings.Contains(string(b), `"wallet_cap":null`) {
		t.Errorf("--no-cap body = %s, want wallet_cap null", b)
	}
	if _, _, code := runCLI(t, srv.URL, append(append([]string{}, base...), "--cap", "500")...); code != 0 {
		t.Fatalf("--cap: exit %d", code)
	}
	if b := pgJSONBody(t, (*reqs)[1]); b["wallet_cap"] != float64(500) || b["name"] != "Std" || b["default"] != nil {
		t.Errorf("--cap body = %v", b)
	}
}

func TestPartnerTierNameResolution(t *testing.T) {
	srv, reqs := pgFake(t, map[string]pgReply{
		"GET /v1/partner/tiers":                              {200, pgTierList},
		"PATCH /v1/partner/tiers/01JDN2K7M3Q9R8T6V4W1X0Y5Z2": {200, pgTier},
	})
	// A name matches without case; the PATCH needs the id, so the list comes first.
	if _, _, code := runCLI(t, srv.URL, "partner", "tiers", "default", "STANDARD"); code != 0 {
		t.Fatalf("by name: exit %d", code)
	}
	if len(*reqs) != 2 || (*reqs)[0].Method != http.MethodGet || (*reqs)[1].Path != "/v1/partner/tiers/01JDN2K7M3Q9R8T6V4W1X0Y5Z2" {
		t.Fatalf("by name: requests %+v", *reqs)
	}
	if b := pgJSONBody(t, (*reqs)[1]); b["default"] != true || len(b) != 1 {
		t.Errorf("default body = %v, want only default true", b)
	}
	// An id is used as it is.
	if _, _, code := runCLI(t, srv.URL, "partner", "tiers", "default", "01JDN2K7M3Q9R8T6V4W1X0Y5Z2"); code != 0 {
		t.Fatalf("by id: exit %d", code)
	}
	// An unknown name sends only the list.
	n := len(*reqs)
	if _, _, code := runCLI(t, srv.URL, "partner", "tiers", "default", "Nope"); code != exitcode.Usage || len(*reqs) != n+1 {
		t.Errorf("unknown name: exit %d, %d requests; want usage and only the list", code, len(*reqs)-n)
	}
}

// The key goes to stdout once, alone on a line; everything else to stderr.
func TestPartnerCreateKeyPrintedOnce(t *testing.T) {
	srv, _ := pgFake(t, map[string]pgReply{"PUT /v1/partner/workspaces/user-42": {201, pgCreated}})

	out, errb, code := runHuman(t, srv.URL, false, "partner", "users", "create", "user-42")
	if code != 0 || out != pgKey+"\n" {
		t.Errorf("human: exit %d, stdout %q; want the key alone on a line", code, out)
	}
	if strings.Contains(errb, pgKey) || !strings.Contains(errb, "Created user user-42") {
		t.Errorf("human: stderr %q; want the status and no key", errb)
	}

	out, errb, code = runHuman(t, srv.URL, false, "partner", "users", "create", "user-42", "--no-key")
	if code != 0 || out != "" || strings.Contains(errb, pgKey) {
		t.Errorf("--no-key: exit %d, stdout %q, stderr %q; want the status only", code, out, errb)
	}

	out, errb, code = runHuman(t, srv.URL, false, "partner", "users", "create", "user-42", "-q")
	if code != 0 || out != pgKey+"\n" || errb != "" {
		t.Errorf("-q: exit %d, stdout %q, stderr %q; want the bare key and nothing else", code, out, errb)
	}

	out, errb, code = runCLI(t, srv.URL, "partner", "users", "create", "user-42")
	if code != 0 || strings.Count(out, pgKey) != 1 || errb != "" {
		t.Errorf("json: exit %d, key count in stdout %d, stderr %q", code, strings.Count(out, pgKey), errb)
	}
	if strings.Contains(errb, pgKey) {
		t.Errorf("json: the key reached stderr")
	}
}

func TestPartnerGiveGeneratedKey(t *testing.T) {
	partnerStdin(t)
	srv, reqs := pgFake(t, map[string]pgReply{"POST /v1/partner/workspaces/user-42/credits": {200, pgGiven}})

	out, errb, code := runHuman(t, srv.URL, false, "partner", "users", "give", "user-42", "50", "--yes")
	if code != 0 || !strings.Contains(out, "Gave 50 credits to user-42") {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
	m := regexp.MustCompile(`Idempotency key: (\S+); pass it again to retry safely`).FindStringSubmatch(errb)
	if m == nil || !pgHexKey.MatchString(m[1]) {
		t.Fatalf("stderr %q: want a printed 32-character hex idempotency key", errb)
	}
	b := pgJSONBody(t, (*reqs)[0])
	if b["idempotency_key"] != m[1] || b["credits"] != float64(50) {
		t.Errorf("request body %v does not carry the printed key", b)
	}

	// JSON mode: stderr stays empty, and the next generated key is a new one.
	_, errb, code = runCLI(t, srv.URL, "partner", "users", "give", "user-42", "50", "--yes")
	if code != 0 || errb != "" {
		t.Errorf("json give: exit %d, stderr %q", code, errb)
	}
	if pgJSONBody(t, (*reqs)[1])["idempotency_key"] == m[1] {
		t.Errorf("two generated keys were equal")
	}
}

func TestPartnerGiveNothingMovedIsExitZero(t *testing.T) {
	srv, _ := pgFake(t, map[string]pgReply{"POST /v1/partner/workspaces/user-42/credits": {200, pgGivenReplay}})
	out, errb, code := runHuman(t, srv.URL, false, "partner", "users", "give", "user-42", "50", "--idempotency-key", "grant-2026-11-20-42", "--yes")
	if code != 0 || out != "" || !strings.Contains(errb, "Nothing moved: this request was already carried out.") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errb)
	}
}

// A give, a suspend and a tier delete without --yes need a terminal to ask on;
// without one they are usage errors, and nothing is sent.
func TestPartnerRefusesWithoutTerminal(t *testing.T) {
	partnerStdin(t)
	srv, reqs := pgFake(t, map[string]pgReply{})
	for _, args := range [][]string{
		{"partner", "users", "give", "user-42", "50"},
		{"partner", "users", "suspend", "user-42"},
		{"partner", "tiers", "delete", "Standard"},
	} {
		if _, _, code := runCLI(t, srv.URL, args...); code != exitcode.Usage {
			t.Errorf("%v: exit %d, want usage (2)", args, code)
		}
	}
	if len(*reqs) != 0 {
		t.Errorf("%d requests were sent by refused commands", len(*reqs))
	}
}

func TestPartnerGiveRejectsBadValues(t *testing.T) {
	srv, reqs := pgFake(t, map[string]pgReply{})
	for _, args := range [][]string{
		{"partner", "users", "give", "user-42", "0", "--yes"},
		{"partner", "users", "give", "user-42", "100000001", "--yes"},
		{"partner", "users", "give", "user-42", "1.5", "--yes"},
		{"partner", "users", "give", "user-42", "5", "--idempotency-key", "short", "--yes"},
		{"partner", "users", "give", "user-42", "5", "--idempotency-key", "has a space", "--yes"},
	} {
		if _, _, code := runCLI(t, srv.URL, args...); code != exitcode.Usage {
			t.Errorf("%v: exit %d, want usage (2)", args, code)
		}
	}
	if len(*reqs) != 0 {
		t.Errorf("%d requests sent for refused gives", len(*reqs))
	}
}

func TestPartnerSplitAddsUp(t *testing.T) {
	for _, c := range []struct {
		total, given, balance int64
		t, free, g, b         int64
	}{
		{14_000_000, 0, 0, 14, 14, 0, 0},
		{5_000_000, 2_500_000, 2_500_000, 5, 1, 2, 2},
		{9_000_000, 5_000_000, 2_500_000, 9, 2, 5, 2},
	} {
		tt, free, g, b := partnerSplit(c.total, c.given, c.balance)
		if tt != c.t || free != c.free || g != c.g || b != c.b || tt != free+g+b {
			t.Errorf("split(%d, %d, %d) = %d %d %d %d; want %d %d %d %d", c.total, c.given, c.balance, tt, free, g, b, c.t, c.free, c.g, c.b)
		}
	}
}

// "auth status" names the key type only when GET /v1/key answers; an API without
// it keeps the old output.
func TestAuthStatusShowsKeyType(t *testing.T) {
	srv, _ := pgFake(t, map[string]pgReply{
		"GET /v1/requests": {200, `{"data":[]}`},
		"GET /v1/key":      {200, pgKeyPartner},
	})
	out, _, code := runCLI(t, srv.URL, "auth", "status")
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	scopes, _ := m["scopes"].([]any)
	if code != 0 || m["key_type"] != "partner" || len(scopes) != 2 {
		t.Errorf("exit %d, output %s; want key_type partner and two scopes", code, out)
	}

	old, _ := pgFake(t, map[string]pgReply{"GET /v1/requests": {200, `{"data":[]}`}})
	out, _, code = runCLI(t, old.URL, "auth", "status")
	m = map[string]any{}
	_ = json.Unmarshal([]byte(out), &m)
	if _, has := m["key_type"]; has || code != 0 {
		t.Errorf("an API without /v1/key: exit %d, output %s", code, out)
	}
}

// An empty list is silent on stdout in human mode, and says so on stderr.
func TestPartnerEmptyListsSpeakOnStderr(t *testing.T) {
	srv, _ := pgFake(t, map[string]pgReply{
		"GET /v1/partner/workspaces": {200, `{"workspaces":[]}`},
		"GET /v1/partner/tiers":      {200, `{"tiers":[]}`},
	})
	out, errb, code := runHuman(t, srv.URL, false, "partner", "users", "list")
	if code != 0 || out != "" || !strings.Contains(errb, "no users") {
		t.Errorf("users list: exit %d, stdout %q, stderr %q", code, out, errb)
	}
	out, errb, code = runHuman(t, srv.URL, false, "partner", "tiers", "list")
	if code != 0 || out != "" || !strings.Contains(errb, "no tiers") {
		t.Errorf("tiers list: exit %d, stdout %q, stderr %q", code, out, errb)
	}
}
