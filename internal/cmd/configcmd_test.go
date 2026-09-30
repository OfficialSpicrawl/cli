package cmd

import (
	"path/filepath"
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
