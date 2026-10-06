// Package api is a thin HTTP client for the Spicrawl API.
//
// It deliberately has no per-endpoint methods: commands build the path, query
// and body themselves and decode what they need. That keeps the client one
// file and the wire shape visible at the call site. The authoritative shapes
// are in openapi.yaml at the repo root.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Version is stamped at build time with -ldflags "-X .../api.Version=v1.2.3".
var Version = "dev"

type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
	// Notify, when set, receives progress lines (DoWithRetry waiting between
	// attempts). The caller decides where they go and whether --quiet mutes them.
	Notify func(format string, a ...any)
}

func New(baseURL, apiKey string, timeout time.Duration) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: timeout, CheckRedirect: noMethodChangingRedirect},
	}
}

// noMethodChangingRedirect follows redirects that keep the method (307/308,
// or any redirect of a GET) and hands back the 3xx for the rest. Following a
// 301/302/303 would silently turn a POST into a GET: the call would "succeed"
// without the request ever being made. Do reports the 3xx as a *RedirectError.
func noMethodChangingRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if req.Method != via[0].Method {
		return http.ErrUseLastResponse
	}
	return nil
}

// RedirectError means the API answered with a redirect the CLI will not
// follow. It is almost always a wrong --base-url (http instead of https).
type RedirectError struct {
	Status   int
	Location string
}

func (e *RedirectError) Error() string {
	return fmt.Sprintf("the API answered %d and redirects to %s; the CLI does not follow redirects that would change POST to GET: use that URL as --base-url / SPICRAWL_BASE_URL", e.Status, e.Location)
}

// Response is a completed call that the API answered with a 2xx.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Decode unmarshals the body into v.
func (r *Response) Decode(v any) error {
	if err := json.Unmarshal(r.Body, v); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// Request describes one call. Body, when non-nil, is sent as JSON.
type Request struct {
	Method string
	Path   string // e.g. "/v1/scrape"
	Query  url.Values
	Body   any
	// Accept overrides the Accept header (default application/json).
	Accept string
	// AllowNonProblem accepts a non-2xx answer that is not a problem
	// document as a Response. /v1/scrape with original_status=true relays
	// the target's status on a successful scrape.
	AllowNonProblem bool
}

// NetworkError means the API could not be reached at all: DNS, connection
// refused, TLS handshake. The request was not sent.
type NetworkError struct{ Err error }

func (e *NetworkError) Error() string { return "cannot reach the Spicrawl API: " + e.Err.Error() }
func (e *NetworkError) Unwrap() error { return e.Err }

// ConnectionLostError means the connection failed after the request was
// written (reset, EOF, broken body). The server may have run the request,
// and billed it, so blindly retrying can pay twice. It exits like a timeout.
type ConnectionLostError struct{ Err error }

func (e *ConnectionLostError) Error() string {
	return "lost the connection to the Spicrawl API after the request was sent; the request may have run and been billed. " +
		"Check `spicrawl logs` before retrying (" + e.Err.Error() + ")"
}
func (e *ConnectionLostError) Unwrap() error { return e.Err }

// TimeoutError means the request was sent but no complete answer arrived
// before the client-side timeout (--timeout). The server may have run the
// request, and billed it, so blindly retrying can pay twice.
type TimeoutError struct{ Err error }

func (e *TimeoutError) Error() string {
	return "timed out waiting for the Spicrawl API; the request may have run and been billed. " +
		"Check `spicrawl logs` before retrying (" + e.Err.Error() + ")"
}
func (e *TimeoutError) Unwrap() error { return e.Err }

// isTimeout recognises a client-side timeout in a transport error.
func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return strings.Contains(err.Error(), "timeout awaiting response headers")
}

// transportError classifies a failure of the HTTP round trip. sent reports
// whether the request headers were written to the connection. Before that
// point (dial, DNS, refused, TLS) nothing reached the API: a NetworkError. After
// it the server may have the request and may have run it: a timeout is a
// TimeoutError, any other failure a ConnectionLostError. Neither is safe to retry.
func transportError(err error, sent bool) error {
	switch {
	case !sent:
		return &NetworkError{Err: err}
	case isTimeout(err):
		return &TimeoutError{Err: err}
	}
	return &ConnectionLostError{Err: err}
}

// Do sends the request. A non-2xx answer is returned as *Problem.
func (c *Client) Do(ctx context.Context, req Request) (*Response, error) {
	u := c.BaseURL + req.Path
	if len(req.Query) > 0 {
		u += "?" + req.Query.Encode()
	}
	var body io.Reader
	if req.Body != nil {
		b, err := json.Marshal(req.Body)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(b)
	}
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	hr, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if c.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if req.Body != nil {
		hr.Header.Set("Content-Type", "application/json")
	}
	accept := req.Accept
	if accept == "" {
		accept = "application/json"
	}
	hr.Header.Set("Accept", accept)
	hr.Header.Set("User-Agent", "spicrawl-cli/"+Version)

	var sent atomic.Bool
	hr = hr.WithContext(httptrace.WithClientTrace(hr.Context(), &httptrace.ClientTrace{
		WroteHeaders: func() { sent.Store(true) },
	}))

	resp, err := c.HTTP.Do(hr)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, transportError(err, sent.Load())
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// The request was sent: only the answer is missing.
		return nil, transportError(fmt.Errorf("read response: %w", err), true)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		if req.AllowNonProblem && !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/problem+json") {
			// A target's status is relayed with X-Target-Status. A 3xx or 5xx without
			// it is the API's own redirect or a gateway page in front of it.
			if resp.Header.Get("X-Target-Status") != "" || (resp.StatusCode >= 400 && resp.StatusCode < 500) {
				return &Response{Status: resp.StatusCode, Header: resp.Header, Body: b}, nil
			}
		}
		if loc := resp.Header.Get("Location"); resp.StatusCode/100 == 3 && loc != "" {
			return nil, &RedirectError{Status: resp.StatusCode, Location: loc}
		}
		return nil, parseProblem(resp, b)
	}
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: b}, nil
}

// Problem is the API's RFC 7807 error body. Field names match the wire.
type Problem struct {
	Type              string          `json:"type,omitempty"`
	Title             string          `json:"title"`
	Status            int             `json:"status"`
	Code              string          `json:"code"`
	Detail            string          `json:"detail,omitempty"`
	Retryable         bool            `json:"retryable"`
	DocURL            string          `json:"doc_url,omitempty"`
	RequestID         string          `json:"request_id,omitempty"`
	Instance          string          `json:"instance,omitempty"`
	TargetStatus      *int            `json:"target_status"`
	RetryAfterSeconds int             `json:"retry_after_seconds,omitempty"`
	Warnings          []string        `json:"warnings,omitempty"`
	Diagnostics       json.RawMessage `json:"diagnostics,omitempty"`
}

func (p *Problem) Error() string {
	msg := p.Code
	if p.Detail != "" {
		msg += ": " + p.Detail
	} else if p.Title != "" {
		msg += ": " + p.Title
	}
	return msg
}

// Hint returns diagnostics.hint when the server supplied one.
func (p *Problem) Hint() string {
	var d struct {
		Hint string `json:"hint"`
	}
	if len(p.Diagnostics) > 0 && json.Unmarshal(p.Diagnostics, &d) == nil {
		return d.Hint
	}
	return ""
}

func parseProblem(resp *http.Response, b []byte) *Problem {
	p := &Problem{}
	if json.Unmarshal(b, p) != nil || p.Code == "" {
		// Not a problem document (a proxy in front of the API, most likely).
		p = &Problem{
			Status: resp.StatusCode,
			Code:   "ERR::INTERNAL::ERROR",
			Title:  http.StatusText(resp.StatusCode),
			Detail: strings.TrimSpace(truncate(string(b), 300)),
		}
		// A gateway page says nothing about whether the API ran the request, so
		// only a read is safe to resend; a POST may pay twice.
		p.Retryable = resp.StatusCode >= 500 && resp.Request != nil &&
			(resp.Request.Method == http.MethodGet || resp.Request.Method == http.MethodHead)
	}
	if p.Status == 0 {
		p.Status = resp.StatusCode
	}
	if p.RequestID == "" {
		p.RequestID = resp.Header.Get("X-Request-Id")
	}
	if p.RetryAfterSeconds == 0 {
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
			p.RetryAfterSeconds = s
		}
	}
	return p
}

// AsProblem reports whether err is an API problem.
func AsProblem(err error) (*Problem, bool) {
	var p *Problem
	ok := errors.As(err, &p)
	return p, ok
}

// maxRetryWait caps one wait between attempts: a Retry-After of an hour must
// not hang the CLI.
var maxRetryWait = time.Minute

// DoWithRetry retries only what the server marked retryable, honouring
// Retry-After (capped at maxRetryWait), with exponential backoff capped at 30 s. attempts <= 1 means
// no retry. Transport failures are never retried here; in particular a
// *TimeoutError is returned at once, because the request may already have
// run and been billed (a POST retried after a timeout can pay twice).
func (c *Client) DoWithRetry(ctx context.Context, req Request, attempts int) (*Response, error) {
	backoff := time.Second
	for i := 1; ; i++ {
		resp, err := c.Do(ctx, req)
		if err == nil || i >= attempts {
			return resp, err
		}
		var te *TimeoutError
		if errors.As(err, &te) {
			return resp, err
		}
		p, ok := AsProblem(err)
		if !ok || !p.Retryable {
			return resp, err
		}
		wait := backoff
		if p.RetryAfterSeconds > 0 {
			wait = time.Duration(p.RetryAfterSeconds) * time.Second
		}
		if wait > maxRetryWait {
			wait = maxRetryWait
		}
		if c.Notify != nil {
			c.Notify("retrying in %s (attempt %d of %d): %s", wait, i+1, attempts, p.Code)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		if backoff *= 2; backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
