package cmd

import (
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/OfficialSpicrawl/cli/internal/exitcode"
)

func TestUnknownGroupSubcommandIsUsageError(t *testing.T) {
	for _, group := range []string{"batch", "sessions", "browser", "mcp", "skill", "completion"} {
		_, errb, code := runCLI(t, "http://127.0.0.1:1", group, "lst")
		if code != exitcode.Usage || !strings.Contains(errb, "unknown command") {
			t.Errorf("%s lst: exit %d, want %d: %s", group, code, exitcode.Usage, errb)
		}
	}
	_, errb, _ := runCLI(t, "http://127.0.0.1:1", "batch", "lst")
	if !strings.Contains(errb, "did you mean get or list") {
		t.Errorf("no suggestion: %s", errb)
	}
	rootCmd.SetOut(io.Discard) // the group's help
	defer rootCmd.SetOut(nil)
	if _, errb, code := runCLI(t, "http://127.0.0.1:1", "batch"); code != 0 {
		t.Errorf("bare group should still print help, exit %d: %s", code, errb)
	}
}

func TestInterruptExits130(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("cannot signal self on Windows")
	}
	inflight, release := make(chan struct{}), make(chan struct{})
	srv, _ := newRecordingServer(t, func(_ http.ResponseWriter, _ *http.Request) {
		select {
		case inflight <- struct{}{}:
		default:
		}
		<-release
	})
	defer close(release)
	go func() {
		<-inflight // Execute is running and its signal handler is installed
		p, _ := os.FindProcess(os.Getpid())
		_ = p.Signal(os.Interrupt)
	}()
	_, errb, code := runCLI(t, srv.URL, "usage")
	if code != exitcode.Interrupted || strings.Contains(errb, "context canceled") {
		t.Errorf("exit %d, want %d, stderr %q", code, exitcode.Interrupted, errb)
	}
}

// A gateway 504 page is an API/infra error: not the target's status (exit 6
// with --allowed-status advice) and not resent.
func TestGatewayPageWithOriginalStatus(t *testing.T) {
	var n atomic.Int32
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusGatewayTimeout)
		_, _ = io.WriteString(w, "<html>504 Gateway Time-out</html>")
	})
	_, errb, code := runCLI(t, srv.URL, "scrape", "https://example.com", "--original-status", "--retry", "3")
	if code != exitcode.Internal || strings.Contains(errb, "allowed-status") {
		t.Errorf("exit %d, want %d: %s", code, exitcode.Internal, errb)
	}
	if got := n.Load(); got != 1 {
		t.Errorf("%d requests, want 1", got)
	}
}

func TestDropAfterSendIsExit12(t *testing.T) {
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		c, _, _ := w.(http.Hijacker).Hijack()
		c.Close()
	})
	_, errb, code := runCLI(t, srv.URL, "scrape", "https://example.com")
	if code != exitcode.Timeout || !strings.Contains(errb, "spicrawl logs") || strings.Contains(errb, "nothing was sent") {
		t.Errorf("exit %d, want %d: %s", code, exitcode.Timeout, errb)
	}
}
