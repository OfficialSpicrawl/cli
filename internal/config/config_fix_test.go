package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveTrimsKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv(EnvConfig, path)
	if err := os.WriteFile(path, []byte(`{"api_key":"filekey\r\n"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ flag, env, want, src string }{
		{"flagkey \n", "envkey", "flagkey", "flag"},
		{"", "envkey\r\n", "envkey", "env"},
		{"", "\n", "filekey", "file"}, // a blank env var must not shadow the file
	} {
		t.Setenv(EnvAPIKey, tc.env)
		r, err := Resolve(tc.flag, "")
		if err != nil || r.APIKey != tc.want || r.APIKeySource != tc.src {
			t.Errorf("%+v: got %q from %q, err %v", tc, r.APIKey, r.APIKeySource, err)
		}
	}
}

// With no $HOME there is no config dir; flags and env must still work.
func TestResolveWithoutHome(t *testing.T) {
	t.Setenv(EnvConfig, "")
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv(EnvAPIKey, "")
	r, err := Resolve("k", "http://localhost:1/")
	if err != nil || r.APIKey != "k" || r.BaseURL != "http://localhost:1" {
		t.Fatalf("got %+v, err %v", r, err)
	}
}

func TestSaveIgnoresStaleTmpAndIsOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv(EnvConfig, path)
	if err := os.WriteFile(path+".tmp", nil, 0o644); err != nil { // a stale world-readable leftover
		t.Fatal(err)
	}
	if err := Save(File{APIKey: "secret"}); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %v, err %v, want 0600", fi.Mode(), err)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".config-*.tmp")); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

func TestMaskKeyNeverShowsAShortKey(t *testing.T) {
	for _, k := range []string{"abc", "0123456789abcdef", "0123456789abcdefghij", "spicrawl_live_abcdefgh", "spicrawl_live_abcdefghijkl"} {
		if got := MaskKey(k); got != strings.Repeat("*", len(k)) {
			t.Errorf("MaskKey(%q) = %q, want all stars", k, got)
		}
	}
	long := "spicrawl_live_0123456789abcdef0123456789abcdef"
	if got := MaskKey(long); got != "spicrawl_live_0123…cdef" {
		t.Errorf("MaskKey(long) = %q", got)
	}
}
