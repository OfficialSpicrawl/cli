package cmd

import (
	"net/http"
	"strings"
	"testing"
)

// The API answers /readyz but drops the connection on the key check: status
// cannot vouch for the key, so it must not exit 0.
func TestStatusKeyCheckNetworkFailureExitsNonZero(t *testing.T) {
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			_, _ = w.Write([]byte(`{"status":"ready","checks":{},"cdp":"enabled"}`))
			return
		}
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	})
	out, errb, code := runCLI(t, srv.URL, "status")
	if code == 0 || !strings.Contains(out, `"key_valid": null`) {
		t.Errorf("exit %d, stdout %s, stderr %s", code, out, errb)
	}
}
