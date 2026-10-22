package cmd

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigSetGetUnset(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	if _, e, code := authRun(t, cfg, "", "config", "set", "base_url", "http://localhost:8080/"); code != 0 {
		t.Fatalf("set: %d %s", code, e)
	}
	out, _, _ := authRun(t, cfg, "", "config", "get", "base_url")
	if m := authDecode(t, out); m["value"] != "http://localhost:8080" {
		t.Fatalf("get: %v", m)
	}
	authRun(t, cfg, "", "config", "set", "api_key", "spicrawl_live_abcdefghijklmnop_wxyz")
	out, _, _ = authRun(t, cfg, "", "config", "get")
	m := authDecode(t, out)
	if m["api_key"] != "spicrawl_live_abcd…wxyz" || m["base_url"] != "http://localhost:8080" {
		t.Fatalf("get all: %v", m)
	}
	out, _, _ = authRun(t, cfg, "", "config", "get", "api_key", "--show-secret")
	if authDecode(t, out)["value"] != "spicrawl_live_abcdefghijklmnop_wxyz" {
		t.Fatalf("show-secret: %s", out)
	}
	if _, _, code := authRun(t, cfg, "", "config", "unset", "base_url"); code != 0 {
		t.Fatal("unset failed")
	}
	out, _, _ = authRun(t, cfg, "", "config", "get", "base_url")
	if authDecode(t, out)["value"] != nil {
		t.Fatalf("after unset: %s", out)
	}
}

func TestConfigUsageErrors(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	for _, args := range [][]string{
		{"config", "get", "nope"},
		{"config", "set", "nope", "x"},
		{"config", "unset", "nope"},
		{"config", "set", "base_url", "ftp://x"},
		{"config", "set", "base_url"},
	} {
		if _, _, code := authRun(t, cfg, "", args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

func TestConfigPath(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	out, _, code := authRun(t, cfg, "", "config", "path")
	if code != 0 || authDecode(t, out)["path"] != cfg {
		t.Fatalf("exit %d %s", code, out)
	}
}

func TestConfigHumanPlainAndLive(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	out, _, _ := authRunHuman(t, cfg, "", "config", "set", "base_url", "http://localhost:8080/")
	if want := "Set base_url = http://localhost:8080 in " + cfg + "\n"; out != want {
		t.Errorf("plain set: %q, want %q", out, want)
	}
	plainTable, _, _ := authRunHuman(t, cfg, "", "config", "get")
	noEscapes(t, "plain table", plainTable)
	if val, _, _ := authRunHuman(t, cfg, "", "config", "get", "base_url"); val != "http://localhost:8080\n" {
		t.Errorf("get <key> must stay a bare value, got %q", val)
	}

	liveUI(t)
	out, _, _ = authRunHuman(t, cfg, "", "config", "set", "base_url", "http://localhost:9090")
	if want := "✓ Set base_url = http://localhost:9090 in " + cfg + "\n"; stripANSI(out) != want {
		t.Errorf("live set: %q, want %q", stripANSI(out), want)
	}
	table, _, _ := authRunHuman(t, cfg, "", "config", "get")
	if !strings.Contains(table, "\x1b[") || !strings.Contains(stripANSI(table), "base_url  http://localhost:9090") {
		t.Errorf("live table: %q", table)
	}
	if val, _, _ := authRunHuman(t, cfg, "", "config", "get", "base_url"); val != "http://localhost:9090\n" {
		t.Errorf("get <key> must stay a bare value on a terminal too, got %q", val)
	}
	if p, _, _ := authRunHuman(t, cfg, "", "config", "path"); p != cfg+"\n" {
		t.Errorf("path must stay bare, got %q", p)
	}
	out, _, _ = authRunHuman(t, cfg, "", "config", "unset", "base_url")
	if want := "✓ Removed base_url from " + cfg + "\n"; stripANSI(out) != want {
		t.Errorf("live unset: %q", stripANSI(out))
	}

	// Machine output is untouched.
	jout, _, _ := authRun(t, cfg, "", "config", "set", "base_url", "http://localhost:7070")
	noEscapes(t, "json", jout)
	authDecode(t, jout)
}
