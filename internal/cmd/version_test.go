package cmd

import (
	"runtime"
	"testing"

	"github.com/Spicrawl/cli/internal/api"
)

func TestVersionJSON(t *testing.T) {
	out, _, code := runCLI(t, "http://127.0.0.1:1", "version")
	m := authDecode(t, out)
	if code != 0 || m["version"] != api.Version || m["go"] != runtime.Version() || m["os"] != runtime.GOOS || m["arch"] != runtime.GOARCH {
		t.Fatalf("exit %d %v", code, m)
	}
}
