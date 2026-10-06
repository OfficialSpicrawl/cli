// Package exitcode maps outcomes to the process exit codes agents branch on.
//
// The numbers are part of the CLI's public contract (DESIGN.md): never
// renumber one, only add.
package exitcode

import "strings"

const (
	OK       = 0  // success
	Internal = 1  // unexpected CLI failure, or ERR::INTERNAL::* (transient)
	Usage    = 2  // bad flags, arguments or config file; nothing was sent
	Auth     = 3  // ERR::AUTH::*, or no API key configured
	Request  = 4  // ERR::REQUEST::*, ERR::SECURITY::* — fix the request
	Limit    = 5  // ERR::LIMIT::* — rate, concurrency, quota, max_cost
	Upstream = 6  // ERR::UPSTREAM::* — the target site failed or challenged
	Proxy    = 7  // ERR::PROXY::*
	Engine   = 8  // ERR::ENGINE::*, ERR::EXTRACT::*, capability not on this deployment
	Session  = 9  // ERR::SESSION::*
	Network  = 10 // could not reach the Spicrawl API at all; nothing was sent
	Pending  = 11 // a --wait gave up before the job finished
	Timeout  = 12 // the request was sent but --timeout expired or the connection dropped; it may have run and been billed

	Interrupted = 130 // Ctrl-C (128 + SIGINT); the usual shell convention
)

// Entry describes one exit code for `spicrawl exit-codes`.
type Entry struct {
	Code    int    `json:"code"`
	Name    string `json:"name"`
	Meaning string `json:"meaning"`
}

// Table lists every exit code in order. It is the single source for the
// `exit-codes` command (text and JSON).
var Table = []Entry{
	{OK, "ok", "success"},
	{Internal, "internal", "CLI bug, or a transient ERR::INTERNAL::* (retryable)"},
	{Usage, "usage", "bad flags or arguments, or an unreadable/malformed config file; nothing was sent"},
	{Auth, "auth", "ERR::AUTH::*, or no API key configured"},
	{Request, "request", "ERR::REQUEST::*, ERR::SECURITY::*, ERR::EXTRACT::INVALID_RULES: fix the request"},
	{Limit, "limit", "ERR::LIMIT::*: rate, concurrency, quota or max_cost"},
	{Upstream, "upstream", "ERR::UPSTREAM::*: the target site failed or served a bot challenge"},
	{Proxy, "proxy", "ERR::PROXY::*"},
	{Engine, "engine", "ERR::ENGINE::*, ERR::EXTRACT::FAILED, or ERR::INTERNAL::UNAVAILABLE with retryable=false: capability not available on this deployment"},
	{Session, "session", "ERR::SESSION::*"},
	{Network, "network", "the Spicrawl API could not be reached (DNS, refused, TLS); nothing was sent"},
	{Pending, "pending", "a --wait gave up before the job finished"},
	{Timeout, "timeout", "timed out waiting for the API, or the connection dropped after the request was sent; the request may have run and been billed: check `spicrawl logs` before retrying"},
	{Interrupted, "interrupted", "stopped with Ctrl-C"},
}

// ForCode returns the exit code for an API error code such as
// "ERR::PROXY::EXHAUSTED". Prefer ForProblem when the problem's retryable
// flag is known.
func ForCode(code string) int {
	parts := strings.Split(code, "::")
	if len(parts) < 2 {
		return Internal
	}
	switch parts[1] {
	case "AUTH":
		return Auth
	case "REQUEST", "SECURITY":
		return Request
	case "LIMIT":
		return Limit
	case "UPSTREAM":
		return Upstream
	case "PROXY":
		return Proxy
	case "ENGINE", "EXTRACT":
		if code == "ERR::EXTRACT::INVALID_RULES" {
			return Request
		}
		return Engine
	case "SESSION":
		return Session
	default:
		return Internal
	}
}

// ForProblem is ForCode plus the problem's retryable flag. The API answers
// ERR::INTERNAL::UNAVAILABLE with retryable=false when a capability (such as
// ai_extract) is not configured on the deployment: that is a permanent
// property of the server, not a CLI or transient failure, so it maps to
// Engine rather than Internal.
func ForProblem(code string, retryable bool) int {
	if code == "ERR::INTERNAL::UNAVAILABLE" && !retryable {
		return Engine
	}
	return ForCode(code)
}
