package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSchemaEachBody(t *testing.T) {
	for _, name := range []string{"scrape", "batch", "batch-item", "session"} {
		out, errOut, code := runCLI(t, "http://127.0.0.1:1", "schema", name)
		if code != 0 {
			t.Fatalf("%s: exit %d %s", name, code, errOut)
		}
		m := authDecode(t, out)
		if m["$schema"] == nil || m["properties"] == nil && m["allOf"] == nil {
			t.Fatalf("%s: not a schema: %.200s", name, out)
		}
		if strings.Contains(out, "#/components/") {
			t.Fatalf("%s: unresolved $ref", name)
		}
	}
	out, _, _ := runCLI(t, "http://127.0.0.1:1", "schema", "scrape")
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	_ = json.Unmarshal([]byte(out), &s)
	if s.Properties["url"] == nil {
		t.Fatal("scrape schema has no url property")
	}
}

func TestSchemaList(t *testing.T) {
	out, _, code := runCLI(t, "http://127.0.0.1:1", "schema", "--list")
	var items []map[string]string
	if err := json.Unmarshal([]byte(out), &items); err != nil || code != 0 || len(items) != 4 {
		t.Fatalf("exit %d %s", code, out)
	}
}

func TestSchemaUnknownIsUsage(t *testing.T) {
	_, errOut, code := runCLI(t, "http://127.0.0.1:1", "schema", "nope")
	if code != 2 || !strings.Contains(errOut, "batch-item") {
		t.Fatalf("exit %d %s", code, errOut)
	}
}
