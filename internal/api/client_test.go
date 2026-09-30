package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestTimeoutAwaitingHeadersIsTimeoutError(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	_, err := New(srv.URL, "k", 100*time.Millisecond).Do(context.Background(), Request{Method: http.MethodPost, Path: "/v1/scrape", Body: map[string]any{}})
	var te *TimeoutError
	if !errors.As(err, &te) {
		t.Fatalf("err = %T %v, want *TimeoutError", err, err)
	}
	var ne *NetworkError
	if errors.As(err, &ne) {
		t.Error("a timeout must not also be a NetworkError")
	}
}

func TestTimeoutReadingBodyIsTimeoutError(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("{"))
		w.(http.Flusher).Flush()
		<-release
	}))
	defer srv.Close()
	defer close(release)
	_, err := New(srv.URL, "k", 100*time.Millisecond).Do(context.Background(), Request{Path: "/v1/usage"})
	var te *TimeoutError
	if !errors.As(err, &te) {
		t.Fatalf("err = %T %v, want *TimeoutError", err, err)
	}
}

func TestRefusedIsNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL
	srv.Close()
	_, err := New(u, "k", time.Second).Do(context.Background(), Request{Path: "/"})
	var ne *NetworkError
	if !errors.As(err, &ne) {
		t.Fatalf("err = %T %v, want *NetworkError", err, err)
	}
}

func TestIsTimeoutRecognisesHeaderTimeout(t *testing.T) {
	if !isTimeout(errors.New("net/http: timeout awaiting response headers")) || !isTimeout(context.DeadlineExceeded) {
		t.Error("isTimeout missed a timeout")
	}
	if transportError(context.DeadlineExceeded, false) == nil {
		t.Fatal("nil")
	}
	var ne *NetworkError
	if !errors.As(transportError(context.DeadlineExceeded, false), &ne) {
		t.Error("a timeout before the request was written is a connection failure")
	}
}

func TestDoWithRetryDoesNotRetryTimeout(t *testing.T) {
	var n atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { n.Add(1); <-release }))
	defer srv.Close()
	defer close(release)
	_, err := New(srv.URL, "k", 100*time.Millisecond).DoWithRetry(context.Background(), Request{Method: http.MethodPost, Path: "/v1/scrape", Body: map[string]any{}}, 3)
	var te *TimeoutError
	if !errors.As(err, &te) {
		t.Fatalf("err = %T %v", err, err)
	}
	if got := n.Load(); got != 1 {
		t.Errorf("%d attempts, want 1", got)
	}
}

func TestDoWithRetryRetriesRetryableProblem(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if n.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/problem+json")
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"code":"ERR::LIMIT::CONCURRENCY","retryable":true,"status":503,"title":"x"}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := New(srv.URL, "k", time.Second).DoWithRetry(ctx, Request{Path: "/v1/usage"}, 2); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 2 {
		t.Errorf("%d attempts, want 2", n.Load())
	}
}
