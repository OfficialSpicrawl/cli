package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func post(c *Client, ctx context.Context, attempts int, allowNonProblem bool) (*Response, error) {
	return c.DoWithRetry(ctx, Request{Method: http.MethodPost, Path: "/v1/scrape", Body: map[string]any{}, AllowNonProblem: allowNonProblem}, attempts)
}

// A connection that dies after the request was written is not "nothing was
// sent": the server had the request, so a retry can double-bill.
func TestDropAfterWriteIsNotNetworkError(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	}))
	defer srv.Close()
	_, err := post(New(srv.URL, "k", time.Second), context.Background(), 3, false)
	var le *ConnectionLostError
	var ne *NetworkError
	if !errors.As(err, &le) || errors.As(err, &ne) || strings.Contains(err.Error(), "nothing was sent") {
		t.Fatalf("err = %T %v, want *ConnectionLostError", err, err)
	}
	if n.Load() != 1 {
		t.Errorf("%d attempts, want 1: a dropped POST must not be retried", n.Load())
	}
}

// A gateway page is an API/infra error: not retried for a POST, and not the
// scraped target's status even with AllowNonProblem. X-Target-Status marks
// the target's own status.
func TestGatewayPageIsNotATargetNorRetriedPost(t *testing.T) {
	var n atomic.Int32
	withHeader := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Type", "text/html")
		if withHeader {
			w.Header().Set("X-Target-Status", "504")
		}
		w.WriteHeader(504)
		_, _ = fmt.Fprint(w, "<html>Gateway Time-out</html>")
	}))
	defer srv.Close()
	c := New(srv.URL, "k", time.Second)

	_, err := post(c, context.Background(), 3, true)
	if p, ok := AsProblem(err); !ok || p.Retryable {
		t.Fatalf("err = %v, want a non-retryable *Problem", err)
	}
	if n.Load() != 1 {
		t.Errorf("%d attempts, want 1", n.Load())
	}

	withHeader = true
	if r, err := post(c, context.Background(), 1, true); err != nil || r.Status != 504 {
		t.Errorf("with X-Target-Status: resp %v err %v, want the relayed 504", r, err)
	}
}

func TestRetryAfterIsCappedAndAnnounced(t *testing.T) {
	old := maxRetryWait
	maxRetryWait = 20 * time.Millisecond
	defer func() { maxRetryWait = old }()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if n.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/problem+json")
			w.Header().Set("Retry-After", "100000")
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"code":"ERR::LIMIT::RATE","retryable":true,"status":429,"title":"x"}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "k", time.Second)
	var notes []string
	c.Notify = func(f string, a ...any) { notes = append(notes, fmt.Sprintf(f, a...)) }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.DoWithRetry(ctx, Request{Path: "/v1/usage"}, 2); err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "retrying in 20ms") {
		t.Errorf("notes = %q", notes)
	}
}

// A 301/302/303 would turn the POST into a GET and "succeed"; 307/308 keep
// the method and are followed.
func TestRedirectDoesNotChangePostToGet(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		switch r.URL.Path {
		case "/v1/scrape":
			http.Redirect(w, r, "/moved", 301)
		case "/v1/temp":
			http.Redirect(w, r, "/final", 307)
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "k", time.Second)

	_, err := post(c, context.Background(), 1, false)
	var re *RedirectError
	if !errors.As(err, &re) || re.Location != "/moved" || !strings.Contains(err.Error(), "/moved") {
		t.Fatalf("err = %T %v, want *RedirectError naming the Location", err, err)
	}
	if len(methods) != 1 {
		t.Fatalf("methods = %v, want only the POST", methods)
	}
	if _, err := c.Do(context.Background(), Request{Method: http.MethodPost, Path: "/v1/temp", Body: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if got := methods[1:]; len(got) != 2 || got[1] != http.MethodPost {
		t.Errorf("307 methods = %v, want POST kept", got)
	}
}
