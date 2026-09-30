package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Spicrawl/cli/internal/config"
)

// docsServer serves a docs site at the root of its host, the way
// docs.spicrawl.com does, and points $SPICRAWL_DOCS_URL at it.
func docsServer(t *testing.T) *[]recordedRequest {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/llms.txt":
			_, _ = w.Write([]byte("# Spicrawl\n- [Quickstart](https://docs.spicrawl.com/quickstart.md)\n"))
		case "/guides/anti-bot.md":
			_, _ = w.Write([]byte("# Anti-bot\n"))
		default:
			http.NotFound(w, r)
		}
	})
	t.Setenv(docsEnvURL, srv.URL+"/")
	t.Setenv(docsEnvHost, "")
	return reqs
}

func TestDocsTopic(t *testing.T) {
	reqs := docsServer(t)
	out, errOut, code := runCLI(t, "http://127.0.0.1:1", "docs", "guides/anti-bot")
	if code != 0 {
		t.Fatalf("exit %d %s", code, errOut)
	}
	m := authDecode(t, out)
	if m["topic"] != "guides/anti-bot" || m["markdown"] != "# Anti-bot\n" {
		t.Fatalf("%v", m)
	}
	if (*reqs)[0].Path != "/guides/anti-bot.md" {
		t.Fatalf("fetched %s, want the page appended to $SPICRAWL_DOCS_URL", (*reqs)[0].Path)
	}
	if (*reqs)[0].Header.Get("Authorization") != "" {
		t.Fatal("docs sent the API key")
	}
}

func TestDocsList(t *testing.T) {
	docsServer(t)
	out, _, code := runCLI(t, "http://127.0.0.1:1", "docs", "--list")
	m := authDecode(t, out)
	if code != 0 || m["topic"] != "llms.txt" || m["markdown"] == "" {
		t.Fatalf("exit %d %v", code, m)
	}
}

func TestDocsUnknownTopicIsUsage(t *testing.T) {
	docsServer(t)
	for _, topic := range []string{"missing", "../etc/passwd"} {
		if _, _, code := runCLI(t, "http://127.0.0.1:1", "docs", topic); code != 2 {
			t.Errorf("%s: exit %d, want 2", topic, code)
		}
	}
}

func TestDocsNetworkExit10(t *testing.T) {
	t.Setenv(docsEnvURL, "http://127.0.0.1:1")
	if _, _, code := runCLI(t, "http://127.0.0.1:1", "docs", "x"); code != 10 {
		t.Fatalf("exit %d", code)
	}
}

// Redirected stdout (not a terminal) still gets raw Markdown; only --json wraps.
func TestDocsRawMarkdownWithoutJSONFlag(t *testing.T) {
	docsServer(t)
	out, errOut, code := runCLI(t, "http://127.0.0.1:1", "docs", "guides/anti-bot", "--json=false")
	if code != 0 || out != "# Anti-bot\n" {
		t.Fatalf("exit %d, stdout %q, stderr %s", code, out, errOut)
	}
	out, _, code = runCLI(t, "http://127.0.0.1:1", "docs", "--list", "--json=false")
	if code != 0 || !strings.HasPrefix(out, "# Spicrawl\n") {
		t.Fatalf("--list: exit %d, stdout %q", code, out)
	}
}

// Without $SPICRAWL_DOCS_URL or $SPICRAWL_DOCS_HOST the docs are read from the
// API base URL's origin + /docs, so a self-hosted deployment needs no extra
// setting.
func TestDocsHostFollowsBaseURL(t *testing.T) {
	var got string
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		_, _ = w.Write([]byte("# Quickstart\n"))
	})
	t.Setenv(docsEnvURL, "")
	t.Setenv(docsEnvHost, "")
	out, errOut, code := runCLI(t, srv.URL+"/", "docs", "quickstart", "--json=false")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if got != "/docs/quickstart.md" || out != "# Quickstart\n" {
		t.Fatalf("path %q, out %q", got, out)
	}
}

// TestDocsURLWithAPath: $SPICRAWL_DOCS_URL is a base, not an origin: pages are
// appended to its path, and it wins over the legacy $SPICRAWL_DOCS_HOST.
func TestDocsURLWithAPath(t *testing.T) {
	var got string
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		_, _ = w.Write([]byte("# Quickstart\n"))
	})
	t.Setenv(docsEnvURL, srv.URL+"/kb/")
	t.Setenv(docsEnvHost, "http://127.0.0.1:1")
	if _, errOut, code := runCLI(t, "http://127.0.0.1:1", "docs", "quickstart"); code != 0 || got != "/kb/quickstart.md" {
		t.Fatalf("exit %d, path %q, stderr %q", code, got, errOut)
	}
}

// TestDocsLegacyHost: $SPICRAWL_DOCS_HOST keeps its old meaning, an origin
// whose /docs holds the docs, so an existing setting does not change.
func TestDocsLegacyHost(t *testing.T) {
	var got string
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		_, _ = w.Write([]byte("# Quickstart\n"))
	})
	t.Setenv(docsEnvURL, "")
	t.Setenv(docsEnvHost, srv.URL+"/")
	if _, errOut, code := runCLI(t, "http://127.0.0.1:1", "docs", "quickstart"); code != 0 || got != "/docs/quickstart.md" {
		t.Fatalf("exit %d, path %q, stderr %q", code, got, errOut)
	}
}

func TestDocsBadURLIsUsage(t *testing.T) {
	for _, bad := range []string{"docs.example.test", "ftp://docs.example.test", "https://docs.example.test/?x=1", "https://docs.example.test/#top", "https://u:p@docs.example.test"} {
		t.Setenv(docsEnvURL, bad)
		if _, _, code := runCLI(t, "http://127.0.0.1:1", "docs", "quickstart"); code != 2 {
			t.Errorf("$%s=%q: exit %d, want 2", docsEnvURL, bad, code)
		}
	}
}

// TestDocsDerived: the hosted API's docs are the root of docs.spicrawl.com; a
// self-hosted API's are at its own /docs.
func TestDocsDerived(t *testing.T) {
	for origin, want := range map[string]string{
		config.DefaultBase:       "https://docs.spicrawl.com",
		"http://192.0.2.10:8080": "http://192.0.2.10:8080/docs",
	} {
		if got := docsDerived(origin); got != want {
			t.Errorf("docsDerived(%q) = %q, want %q", origin, got, want)
		}
	}
	t.Setenv(docsEnvURL, "")
	t.Setenv(docsEnvHost, "")
	t.Setenv("SPICRAWL_CONFIG", t.TempDir()+"/config.json")
	t.Setenv("SPICRAWL_BASE_URL", "")
	if got, err := docsResolveURL(); err != nil || got != "https://docs.spicrawl.com" {
		t.Errorf("docsResolveURL() with the default base URL = %q, %v, want https://docs.spicrawl.com", got, err)
	}
}
