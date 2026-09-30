package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
)

// fakeTokenAPI answers POST /v1/browser/token and records what it was asked.
type fakeTokenAPI struct {
	mu   sync.Mutex
	auth string
	body map[string]any
	hits int
}

func (f *fakeTokenAPI) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/browser/token" {
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.hits++
		f.auth = r.Header.Get("Authorization")
		f.body = map[string]any{}
		_ = json.Unmarshal(raw, &f.body)
		q := url.Values{}
		for k, v := range f.body {
			switch x := v.(type) {
			case string:
				q.Set(k, x)
			default:
				b, _ := json.Marshal(x)
				q.Set(k, string(b))
			}
		}
		f.mu.Unlock()
		q.Set("token", "wbt_testtoken")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"url":        "ws://internal:8080/v1/browser?" + q.Encode(),
			"path":       "/v1/browser?" + q.Encode(),
			"expires_at": "2026-09-24T12:00:00Z",
			"expires_in": 60,
			"single_use": true,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestBrowserURLMintsATokenAndNeverPrintsTheKey(t *testing.T) {
	api := &fakeTokenAPI{}
	srv := api.server(t)
	out, errb, code := runCLI(t, srv.URL, "browser", "url", "--engine", "obscura", "--region", "EU", "--ttl", "600", "--sticky-key", "crawl-1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if api.auth != "Bearer spicrawl_test_key" {
		t.Errorf("Authorization = %q, want the key in the header", api.auth)
	}
	want := map[string]any{"engine": "obscura", "proxy_region": "eu", "session_ttl": float64(600), "sticky_key": "crawl-1"}
	for k, v := range want {
		if api.body[k] != v {
			t.Errorf("body %s = %v, want %v", k, api.body[k], v)
		}
	}
	if strings.Contains(out+errb, "spicrawl_test_key") {
		t.Errorf("the key was printed:\nstdout %s\nstderr %s", out, errb)
	}
	var got struct {
		URL       string `json:"url"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout %s: %v", out, err)
	}
	u, err := url.Parse(got.URL)
	if err != nil {
		t.Fatal(err)
	}
	// Built from the CLI's base URL, not the API's own view of its host.
	base, _ := url.Parse(srv.URL)
	if u.Scheme != "ws" || u.Host != base.Host || u.Path != "/v1/browser" || u.Query().Get("token") != "wbt_testtoken" {
		t.Errorf("url %s", got.URL)
	}
	if u.Query().Has("apikey") || u.Query().Has("api_key") {
		t.Errorf("url carries a key parameter: %s", got.URL)
	}
	if got.ExpiresAt == "" {
		t.Error("expires_at missing")
	}
}

func TestBrowserWSURLSchemes(t *testing.T) {
	for base, want := range map[string]string{
		"https://api.example.test/": "wss://api.example.test/v1/browser?token=wbt_x",
		"http://localhost:8080":     "ws://localhost:8080/v1/browser?token=wbt_x",
		"https://h.example/prefix/": "wss://h.example/prefix/v1/browser?token=wbt_x",
	} {
		got, err := browserWSURL(base, "/v1/browser?token=wbt_x")
		if err != nil || got != want {
			t.Errorf("%s: %q, %v; want %q", base, got, err, want)
		}
	}
	if _, err := browserWSURL("https://a.test", "/v1/scrape?x=1"); err == nil {
		t.Error("a path without a token was accepted")
	}
}

func TestBrowserURLUsageErrors(t *testing.T) {
	api := &fakeTokenAPI{}
	srv := api.server(t)
	for _, args := range [][]string{
		{"--country", "de", "--region", "eu"},
		{"--engine", "camoufox"},
		{"--ttl", "30"},
		{"--engine", "obscura", "--headless=false"},
		{"--sticky-key", "bad_key"},
	} {
		_, _, code := runCLI(t, srv.URL, append([]string{"browser", "url"}, args...)...)
		if code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	if api.hits != 0 {
		t.Errorf("usage errors reached the API %d times", api.hits)
	}
}

func TestBrowserURLHumanHintsAndWSWarning(t *testing.T) {
	api := &fakeTokenAPI{}
	srv := api.server(t)
	out, errb, code := runCLIHuman(t, srv.URL, "browser", "url", "--country", "DE")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if !strings.HasPrefix(out, "ws://") || !strings.Contains(out, "token=wbt_testtoken") || !strings.Contains(out, "proxy_country=de") {
		t.Errorf("stdout %q", out)
	}
	if !strings.Contains(errb, "unencrypted") || !strings.Contains(errb, "works once") {
		t.Errorf("stderr %q", errb)
	}
	if !strings.Contains(errb, "connectOverCDP(process.env.SPICRAWL_BROWSER_URL)") {
		t.Errorf("hints missing: %s", errb)
	}

	out, errb, code = runCLIHuman(t, srv.URL, "-q", "browser", "url")
	if code != 0 || strings.Count(out, "\n") != 1 {
		t.Errorf("quiet: exit %d stdout %q", code, out)
	}
	if strings.Contains(errb, "connectOverCDP") {
		t.Errorf("quiet printed hints: %s", errb)
	}
}

// runCLIHuman is runCLI without --json, in human mode, with extra global args.
func runCLIHuman(t *testing.T, baseURL string, args ...string) (string, string, int) {
	t.Helper()
	t.Setenv("SPICRAWL_CONFIG", t.TempDir()+"/config.json")
	t.Setenv("SPICRAWL_API_KEY", "spicrawl_test_key")
	var out, errb bytes.Buffer
	stdout, stderr, humanOutput = &out, &errb, true
	defer func() {
		stdout, stderr, humanOutput = os.Stdout, os.Stderr, false
		resetFlags(rootCmd)
	}()
	rootCmd.SetArgs(append([]string{"--base-url", baseURL}, args...))
	code := Execute()
	return out.String(), errb.String(), code
}
