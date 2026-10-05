package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OfficialSpicrawl/cli/internal/config"
)

// authRun is runCLI with control over the config path and env key, so a test
// can run several commands against one config file. key "" unsets the env.
func authRun(t *testing.T, cfgPath, envKey string, args ...string) (string, string, int) {
	t.Helper()
	t.Setenv("SPICRAWL_CONFIG", cfgPath)
	t.Setenv("SPICRAWL_API_KEY", envKey)
	t.Setenv("SPICRAWL_BASE_URL", "")
	var out, errb bytes.Buffer
	stdout, stderr = &out, &errb
	defer func() {
		stdout, stderr = os.Stdout, os.Stderr
		resetFlags(rootCmd)
	}()
	rootCmd.SetArgs(append([]string{"--json"}, args...))
	code := Execute()
	return out.String(), errb.String(), code
}

func authDecode(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("stdout is not a JSON object: %v\n%s", err, s)
	}
	return m
}

func authOKServer(t *testing.T) (string, *[]recordedRequest) {
	srv, reqs := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer spicrawl_live_good_key_123456" {
			writeProblem(w, 401, "ERR::AUTH::INVALID_KEY", false)
			return
		}
		_, _ = w.Write([]byte(`{"data":[],"page":{}}`))
	})
	return srv.URL, reqs
}

const authGoodKey = "spicrawl_live_good_key_123456"

func TestAuthLoginSavesValidatedKey(t *testing.T) {
	base, reqs := authOKServer(t)
	cfg := filepath.Join(t.TempDir(), "config.json")
	out, errOut, code := authRun(t, cfg, "", "login", "--api-key", authGoodKey, "--base-url", base)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if len(*reqs) != 1 || (*reqs)[0].Path != "/v1/requests" || (*reqs)[0].Query != "limit=1" {
		t.Fatalf("unexpected requests: %+v", *reqs)
	}
	m := authDecode(t, out)
	if m["saved"] != cfg || m["key"] != config.MaskKey(authGoodKey) || m["base_url"] != base {
		t.Fatalf("output: %v", m)
	}
	b, _ := os.ReadFile(cfg)
	var f config.File
	_ = json.Unmarshal(b, &f)
	if f.APIKey != authGoodKey || f.BaseURL != base {
		t.Fatalf("file: %s", b)
	}
	if st, _ := os.Stat(cfg); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
}

func TestAuthLoginUsesEnvKey(t *testing.T) {
	base, _ := authOKServer(t)
	cfg := filepath.Join(t.TempDir(), "config.json")
	if _, errOut, code := authRun(t, cfg, authGoodKey, "login", "--base-url", base); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
}

func TestAuthLoginRejectedKeyExit3(t *testing.T) {
	base, _ := authOKServer(t)
	cfg := filepath.Join(t.TempDir(), "config.json")
	_, _, code := authRun(t, cfg, "", "login", "--api-key", "spicrawl_live_bad", "--base-url", base)
	if code != 3 {
		t.Fatalf("exit %d, want 3", code)
	}
	if _, err := os.Stat(cfg); err == nil {
		t.Fatal("config written for a rejected key")
	}
}

func TestAuthLoginBare401Exit3(t *testing.T) {
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	})
	_, _, code := authRun(t, filepath.Join(t.TempDir(), "c.json"), "", "login", "--api-key", "k", "--base-url", srv.URL)
	if code != 3 {
		t.Fatalf("exit %d, want 3", code)
	}
}

func TestAuthLoginNetworkExit10(t *testing.T) {
	_, _, code := authRun(t, filepath.Join(t.TempDir(), "c.json"), "", "login", "--api-key", "k", "--base-url", "http://127.0.0.1:1")
	if code != 10 {
		t.Fatalf("exit %d, want 10", code)
	}
}

func TestAuthLoginNoKeyNoTTYIsUsage(t *testing.T) {
	old := authIsTTY
	authIsTTY = func() bool { return false }
	defer func() { authIsTTY = old }()
	_, errOut, code := authRun(t, filepath.Join(t.TempDir(), "c.json"), "", "login")
	if code != 2 || !strings.Contains(errOut, "--api-key") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
}

func TestAuthLoginPrompt(t *testing.T) {
	base, _ := authOKServer(t)
	oldTTY, oldIn, oldW := authIsTTY, authStdin, authPromptW
	var prompt bytes.Buffer
	authIsTTY = func() bool { return true }
	authStdin = strings.NewReader("  " + authGoodKey + "\n")
	authPromptW = &prompt
	defer func() { authIsTTY, authStdin, authPromptW = oldTTY, oldIn, oldW }()
	cfg := filepath.Join(t.TempDir(), "c.json")
	if _, errOut, code := authRun(t, cfg, "", "login", "--base-url", base); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(prompt.String(), authKeysURL) || !strings.Contains(prompt.String(), "visible") {
		t.Fatalf("prompt: %q", prompt.String())
	}
}

func TestAuthLogoutKeepsBaseURL(t *testing.T) {
	base, _ := authOKServer(t)
	cfg := filepath.Join(t.TempDir(), "config.json")
	if _, e, code := authRun(t, cfg, "", "login", "--api-key", authGoodKey, "--base-url", base); code != 0 {
		t.Fatal(e)
	}
	out, _, code := authRun(t, cfg, "", "logout")
	if code != 0 || authDecode(t, out)["removed"] != true {
		t.Fatalf("exit %d: %s", code, out)
	}
	f := func() config.File { b, _ := os.ReadFile(cfg); var f config.File; _ = json.Unmarshal(b, &f); return f }()
	if f.APIKey != "" || f.BaseURL != base {
		t.Fatalf("file after logout: %+v", f)
	}
	out, _, _ = authRun(t, cfg, "", "logout")
	if authDecode(t, out)["removed"] != false {
		t.Fatalf("second logout: %s", out)
	}
}

func TestAuthStatus(t *testing.T) {
	base, reqs := authOKServer(t)
	cfg := filepath.Join(t.TempDir(), "config.json")
	if _, e, code := authRun(t, cfg, "", "login", "--api-key", authGoodKey, "--base-url", base); code != 0 {
		t.Fatal(e)
	}
	out, errOut, code := authRun(t, cfg, "", "auth", "status")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	m := authDecode(t, out)
	if m["api_key_source"] != "file" || m["base_url_source"] != "file" || m["base_url"] != base ||
		m["key_valid"] != true || m["config_path"] != cfg || m["api_key"] != config.MaskKey(authGoodKey) {
		t.Fatalf("status: %v", m)
	}

	n := len(*reqs)
	out, _, code = authRun(t, cfg, "spicrawl_live_bad", "auth", "status", "--offline")
	m = authDecode(t, out)
	if code != 0 || len(*reqs) != n || m["api_key_source"] != "env" || m["key_valid"] != nil {
		t.Fatalf("offline: exit %d %v", code, m)
	}

	out, _, code = authRun(t, cfg, "spicrawl_live_bad", "auth", "status")
	if code != 3 || authDecode(t, out)["key_valid"] != false {
		t.Fatalf("bad key: exit %d %s", code, out)
	}
}

func TestAuthStatusNoKey(t *testing.T) {
	out, _, code := authRun(t, filepath.Join(t.TempDir(), "c.json"), "", "auth", "status")
	m := authDecode(t, out)
	if code != 3 || m["api_key"] != nil || m["base_url_source"] != "default" {
		t.Fatalf("exit %d %v", code, m)
	}
}
