package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeOrderedKeepsMemberOrderAndNumbers(t *testing.T) {
	in := `{"z":1.0,"a":{"y":[{"b":1e3,"a":null}],"x":{}},"m":[],"s":"a<b>&c"}`
	dec := json.NewDecoder(strings.NewReader(in))
	dec.UseNumber()
	v, err := decodeOrdered(dec)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(buf.String()); got != in {
		t.Errorf("round trip changed it:\n got %s\nwant %s", got, in)
	}
}

// Human mode prints --extract data in the API's order, and so does the file
// -o writes (both come from document()).
func TestScrapeHumanDataKeepsAPIOrder(t *testing.T) {
	scrapeHumanMode(t)
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"u","status":200,"content":"","data":{"title":"T","price":9,"list":[{"z":1,"a":2}]}}`))
	})
	want := "{\n  \"title\": \"T\",\n  \"price\": 9,\n  \"list\": [\n    {\n      \"z\": 1,\n      \"a\": 2\n    }\n  ]\n}\n"
	out, stderr, code := scrapeRunCLI(t, srv.URL, "scrape", "https://example.com", "--extract", `{"title":"h1"}`)
	if code != 0 || out != want {
		t.Fatalf("exit %d, stderr %s\n got %q\nwant %q", code, stderr, out, want)
	}

	file := filepath.Join(t.TempDir(), "data.json")
	if _, stderr, code := scrapeRunCLI(t, srv.URL, "scrape", "https://example.com", "--extract", `{"title":"h1"}`, "-o", file); code != 0 {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
	if b, _ := os.ReadFile(file); strings.TrimSpace(string(b)) != strings.TrimSpace(want) {
		t.Errorf("-o file = %q, want %q", b, want)
	}
}

// JSON mode prints the API's bytes (re-indented) when the envelope is
// untouched, so the order was always the API's there.
func TestScrapeJSONDataKeepsAPIOrder(t *testing.T) {
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"u","status":200,"content":"","data":{"title":"T","price":9}}`))
	})
	out, _, code := scrapeRunCLI(t, srv.URL, "scrape", "https://example.com", "--extract", `{"title":"h1"}`)
	if i, j := strings.Index(out, `"title"`), strings.Index(out, `"price"`); code != 0 || i < 0 || i > j {
		t.Errorf("exit %d, data out of order:\n%s", code, out)
	}
}
