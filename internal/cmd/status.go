package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/OfficialSpicrawl/cli/internal/api"
	"github.com/OfficialSpicrawl/cli/internal/config"
	"github.com/OfficialSpicrawl/cli/internal/exitcode"
	"github.com/OfficialSpicrawl/cli/internal/output"
	"github.com/OfficialSpicrawl/cli/internal/ui"
)

type statusWorkers struct {
	Present int `json:"present"`
	Ready   int `json:"ready"`
}

type statusReport struct {
	APIReachable bool              `json:"api_reachable"`
	Ready        bool              `json:"ready"`
	Checks       map[string]string `json:"checks,omitempty"`
	// CDP is whether GET /v1/browser can serve a session: enabled, disabled
	// (the deployment has no CDP browser; /v1/browser answers 501 or is
	// absent) or unknown. Empty when the API predates the field.
	CDP          string                   `json:"cdp"`
	KeyValid     *bool                    `json:"key_valid"`
	KeyError     string                   `json:"key_error,omitempty"`
	KeySource    string                   `json:"key_source"`
	BaseURL      string                   `json:"base_url"`
	Workers      map[string]statusWorkers `json:"workers"`
	WorkersError string                   `json:"workers_error,omitempty"`
	// MCP is which agent clients are in use here (project directory and home)
	// and which of them have a Spicrawl MCP server configured. Client ids.
	MCP statusMCP `json:"mcp"`
	// Repairs has one entry per failed check, in the order to run them.
	Repairs []statusRepair `json:"repairs"`
}

type statusMCP struct {
	Detected  []string `json:"detected"`
	Connected []string `json:"connected"`
}

// statusRepair is a command that fixes a failed check. UnattendedSafe says an
// agent may run it without asking: false means the user must act (supply a
// key) or decide.
type statusRepair struct {
	Check          string `json:"check"`
	Command        string `json:"command"`
	UnattendedSafe bool   `json:"unattended_safe"`
	Why            string `json:"why"`
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check the API is reachable, ready, and accepts your key",
	Long: `Check the whole path from this machine to a working API:

  GET /readyz              reachable and ready, and whether the cloud
                           browser (CDP, /v1/browser) is enabled (no key needed)
  GET /v1/workers          worker kinds present and ready
  GET /v1/requests?limit=1 the key is accepted

It also looks for agent clients in this directory and your home directory
(Claude Code, Cursor, VS Code, Codex) and checks each has a Spicrawl MCP server
configured; with no client found that check is skipped.

For every failed check it prints a repair command, marked unattended_safe
true (an agent may run it) or false (the user must act, for example supply an
API key). With --json they are the "repairs" array of {check, command,
unattended_safe, why}, in the order to run them. Run the commands as written;
placeholders such as <YOUR_API_KEY> are for the user to fill in.

Exit codes: 0 all checks passed, 3 the key is missing or rejected, 10 the API
could not be reached, and the transport error's code when the connection fails
during the key check. Any other failed check (the API reports not ready, a
worker kind has none ready, no MCP server configured for a client) exits 1; read
"repairs".`,
	Example: `  spicrawl status
  spicrawl status --json | jq .workers
  spicrawl status --base-url http://localhost:8080 --api-key "$KEY"`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		r, err := config.Resolve(flagAPIKey, flagBaseURL)
		if err != nil {
			return err
		}
		ctx := cmd.Context()
		c := api.New(r.BaseURL, r.APIKey, flagTimeout)
		rep := statusReport{BaseURL: r.BaseURL, KeySource: r.APIKeySource, MCP: statusMCPCheck(), Repairs: []statusRepair{}}

		p := Printer()
		sp := spin(p, "Checking the API")
		defer sp.Stop()
		var readyErr error
		rep.APIReachable, rep.Ready, rep.Checks, rep.CDP, readyErr = statusReadyz(ctx, c)
		if !rep.APIReachable {
			sp.Stop()
			rep.Repairs = statusRepairs(&rep)
			if err := statusPrint(p, &rep); err != nil {
				return err
			}
			return &ExitError{Code: exitcode.Network, Err: &api.NetworkError{Err: readyErr}}
		}

		f := false
		var keyCheckErr error // the key check could not reach the API
		if r.APIKey == "" {
			rep.KeyValid = &f
			rep.KeyError = "no API key configured"
		} else {
			sp.Label("Checking your key")
			_, err := c.Do(ctx, api.Request{Path: "/v1/requests", Query: url.Values{"limit": {"1"}}})
			t := true
			switch prob, isProb := api.AsProblem(err); {
			case err == nil:
				rep.KeyValid = &t
			case isProb && prob.Code == "ERR::AUTH::INSUFFICIENT_SCOPE":
				rep.KeyValid = &t
				rep.KeyError = "key is valid but lacks the read scope"
			case isProb && strings.HasPrefix(prob.Code, "ERR::AUTH::"):
				rep.KeyValid = &f
				rep.KeyError = prob.Error()
			default:
				rep.KeyError = err.Error() // could not tell; key_valid stays null
				if !isProb {
					keyCheckErr = err // transport failure, not an API answer
				}
			}

			if rep.KeyValid != nil && *rep.KeyValid {
				sp.Label("Reading worker status")
				rep.Workers, err = statusFleet(ctx, c)
				if err != nil {
					rep.WorkersError = err.Error()
				}
			}
		}

		sp.Stop()
		rep.Repairs = statusRepairs(&rep)
		if err := statusPrint(p, &rep); err != nil {
			return err
		}
		if rep.KeyValid != nil && !*rep.KeyValid {
			if r.APIKey == "" {
				return ErrNoKey
			}
			return &ExitError{Code: exitcode.Auth, Err: errors.New("the API rejected the key: " + rep.KeyError)}
		}
		if keyCheckErr != nil {
			return keyCheckErr
		}
		if n := len(rep.Repairs); n > 0 {
			return &ExitError{Code: exitcode.Internal, Err: fmt.Errorf("%d %s failed: run the repairs listed in the status output", n, map[bool]string{true: "check", false: "checks"}[n == 1])}
		}
		return nil
	},
}

// statusReadyz calls /readyz without auth. A 503 still means reachable; its
// body is plain JSON (not a problem document) naming the failed checks.
func statusReadyz(ctx context.Context, c *api.Client) (reachable, ready bool, checks map[string]string, cdp string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/readyz", nil)
	if err != nil {
		return false, false, nil, "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "spicrawl-cli/"+api.Version)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return false, false, nil, "", err
	}
	defer resp.Body.Close()
	var body struct {
		Status string         `json:"status"`
		Checks map[string]any `json:"checks"`
		CDP    string         `json:"cdp"`
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if json.Unmarshal(b, &body) != nil {
		// Something answered, but not the Spicrawl API's health endpoint.
		return true, false, nil, "", nil
	}
	checks = map[string]string{}
	for k, v := range body.Checks {
		checks[k] = fmt.Sprint(v)
	}
	return true, resp.StatusCode == http.StatusOK && body.Status == "ready", checks, body.CDP, nil
}

func statusFleet(ctx context.Context, c *api.Client) (map[string]statusWorkers, error) {
	resp, err := c.Do(ctx, api.Request{Path: "/v1/workers"})
	if err != nil {
		return nil, err
	}
	var fl struct {
		Kinds []struct {
			Kind    string `json:"kind"`
			Present int    `json:"present"`
			Ready   int    `json:"ready"`
		} `json:"kinds"`
	}
	if err := resp.Decode(&fl); err != nil {
		return nil, err
	}
	out := make(map[string]statusWorkers, len(fl.Kinds))
	for _, k := range fl.Kinds {
		out[k.Kind] = statusWorkers{Present: k.Present, Ready: k.Ready}
	}
	return out, nil
}

func statusYN(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// statusBadChecks lists the server checks that are not ok, sorted: "redis=unreachable".
func statusBadChecks(rep *statusReport) string {
	var bad []string
	for k, v := range rep.Checks {
		if v != "ok" {
			bad = append(bad, k+"="+v)
		}
	}
	sort.Strings(bad)
	return strings.Join(bad, ", ")
}

// statusReadyText is the ready value: yes, or no with the checks that failed.
func statusReadyText(rep *statusReport) string {
	ready := statusYN(rep.Ready)
	if bad := statusBadChecks(rep); !rep.Ready && bad != "" {
		ready += " (" + bad + ")"
	}
	return ready
}

func statusCDPText(rep *statusReport) string {
	cdp := rep.CDP
	switch cdp {
	case "":
		cdp = "unknown (this API does not report it)"
	case "disabled":
		cdp = "disabled (cloud browser /v1/browser is not available on this deployment)"
	case "unknown":
		cdp = "unknown (the browser worker could not be asked)"
	}
	return cdp
}

func statusKeyText(rep *statusReport) string {
	key := "unknown"
	if rep.KeyValid != nil {
		key = map[bool]string{true: "valid", false: "invalid"}[*rep.KeyValid]
	}
	if rep.KeySource != "" {
		key += " (from " + rep.KeySource + ")"
	}
	if rep.KeyError != "" {
		key += ": " + rep.KeyError
	}
	return key
}

// cliInvocation is how a repair or next step names this program: through npx
// when that is how it was started (it may not be on PATH), else "spicrawl".
func cliInvocation() string {
	if os.Getenv("npm_command") == "exec" {
		return "npx -y @spicrawl/cli@latest"
	}
	return "spicrawl"
}

// statusMCPCheck finds the agent clients in use here (project directory and
// home, the ones "init --all" would pick) and which have a Spicrawl MCP server
// configured, at project or user level.
func statusMCPCheck() statusMCP {
	m := statusMCP{Detected: []string{}, Connected: []string{}}
	s, err := agentResolveScope("", false)
	if err != nil {
		return m
	}
	g := agentScope{Global: true, Dir: s.Dir, Home: s.Home}
	for _, c := range agentDetect(s) {
		m.Detected = append(m.Detected, c.ID)
		if mcpConfigured(c, c.MCPPath(s)) || (s.Home != "" && mcpConfigured(c, c.MCPPath(g))) {
			m.Connected = append(m.Connected, c.ID)
		}
	}
	return m
}

// names are the display names of the detected clients with an MCP server
// (connected) or without one.
func (m statusMCP) names(connected bool) []string {
	var out []string
	for _, id := range m.Detected {
		has := false
		for _, c := range m.Connected {
			has = has || c == id
		}
		if has == connected {
			out = append(out, agentClientByID(id).Name)
		}
	}
	return out
}

func statusMCPText(m statusMCP) string {
	switch missing := m.names(false); {
	case len(m.Detected) == 0:
		return "no agent client found here"
	case len(missing) == 0:
		return "connected: " + strings.Join(m.names(true), ", ")
	default:
		return "not configured: " + strings.Join(missing, ", ")
	}
}

// statusKeyBad is a key check that failed: rejected or missing, or not
// answered (key_valid null with an error).
func statusKeyBad(rep *statusReport) bool {
	return rep.KeyValid != nil && !*rep.KeyValid || rep.KeyValid == nil && rep.KeyError != ""
}

// statusWorkerBad is a worker kind that runs here but has none ready.
func statusWorkerBad(v statusWorkers) bool { return v.Present > 0 && v.Ready == 0 }

// statusRepairs has one repair per failed check, in the order to run them.
// Only a repair that needs the user (a key to supply) or a decision is
// unattended_safe false. Commands use the placeholder <YOUR_API_KEY>, never a key.
func statusRepairs(rep *statusReport) []statusRepair {
	cli := cliInvocation()
	out := []statusRepair{}
	add := func(check, command string, safe bool, why string) {
		out = append(out, statusRepair{check, command, safe, why})
	}
	if !rep.APIReachable {
		add("reachable", "curl -sS -m 10 "+rep.BaseURL+"/readyz", true,
			rep.BaseURL+" did not answer: check the network, any proxy and the base URL (--base-url, $"+config.EnvBaseURL+")")
	} else {
		if !rep.Ready {
			add("ready", cli+" status", true,
				"the API reports it is not ready ("+statusBadChecks(rep)+"): that is on the server side, wait a minute and re-run; nothing to change here")
		}
		if statusKeyBad(rep) {
			login := cli + " login --api-key <YOUR_API_KEY>"
			switch {
			case rep.KeyValid == nil:
				add("key", cli+" status", true, "the key check got no answer ("+rep.KeyError+"): re-run it")
			case rep.KeySource == "":
				add("key", login, false, "no API key is configured: create one at "+authKeysURL+" and supply it")
			case rep.KeySource == "env":
				add("key", "export "+config.EnvAPIKey+"=<YOUR_API_KEY>", false,
					"the API rejected the key in $"+config.EnvAPIKey+" ("+rep.KeyError+"): create a new one at "+authKeysURL)
			default:
				add("key", login, false,
					"the API rejected the key from the "+rep.KeySource+" ("+rep.KeyError+"): create a new one at "+authKeysURL)
			}
		}
		for _, k := range statusWorkerKinds {
			if v, ok := rep.Workers[k]; ok && statusWorkerBad(v) {
				add("workers."+k, cli+" status", true,
					fmt.Sprintf("no %s worker is ready (0 of %d): that is on the server side, wait a minute and re-run; if it persists, tell Spicrawl support", k, v.Present))
			}
		}
	}
	if missing := rep.MCP.names(false); len(missing) > 0 {
		add("mcp", cli+" init --all --yes", true,
			"no Spicrawl MCP server is configured for "+strings.Join(missing, ", ")+"; this writes the MCP entry and the skill for every client found here")
	}
	return out
}

// statusPrintRepairs lists the repairs: a block under the checklist on a
// terminal, else greppable "key: value" lines. Nothing when none are needed.
func statusPrintRepairs(w io.Writer, u *ui.UI, rs []statusRepair) {
	if len(rs) == 0 {
		return
	}
	if u.On() {
		fmt.Fprintf(w, "\n%s\n", u.Bold("repairs"))
		for _, r := range rs {
			fmt.Fprintf(w, "  %s %s  %s\n", viewMark(u, markBad), viewKey(u, 9, r.Check), u.Cyan(r.Command))
			fmt.Fprintf(w, "    %s\n", u.Dim(fmt.Sprintf("unattended_safe: %t · %s", r.UnattendedSafe, r.Why)))
		}
		return
	}
	fmt.Fprintln(w, "repairs:")
	for _, r := range rs {
		fmt.Fprintf(w, "  - check: %s\n    command: %s\n    unattended_safe: %t\n    why: %s\n", r.Check, r.Command, r.UnattendedSafe, r.Why)
	}
}

var statusWorkerKinds = []string{"browser", "egress", "extract", "batch"}

func statusPrint(p *output.Printer, rep *statusReport) error {
	return p.Result(rep, func(w io.Writer) {
		if u := outUI(p); u.On() {
			statusPrintLive(w, u, rep)
			return
		}
		fmt.Fprintf(w, "%-12s %s\n", "base url:", rep.BaseURL)
		fmt.Fprintf(w, "%-12s %s\n", "reachable:", statusYN(rep.APIReachable))
		if rep.APIReachable {
			fmt.Fprintf(w, "%-12s %s\n", "ready:", statusReadyText(rep))
			fmt.Fprintf(w, "%-12s %s\n", "cdp:", statusCDPText(rep))
			fmt.Fprintf(w, "%-12s %s\n", "key:", statusKeyText(rep))
			if len(rep.MCP.Detected) > 0 {
				fmt.Fprintf(w, "%-12s %s\n", "mcp:", statusMCPText(rep.MCP))
			}
			if rep.WorkersError != "" {
				fmt.Fprintf(w, "%-12s %s\n", "workers:", rep.WorkersError)
			} else if len(rep.Workers) > 0 {
				fmt.Fprintln(w, "workers:")
				for _, k := range statusWorkerKinds {
					if v, ok := rep.Workers[k]; ok {
						fmt.Fprintf(w, "  %-10s %d ready of %d\n", k, v.Ready, v.Present)
					}
				}
			}
		}
		statusPrintRepairs(w, nil, rep.Repairs)
	})
}

// statusPrintLive is the checklist a person sees on a terminal: one mark per
// check and a closing line that counts the failures.
func statusPrintLive(w io.Writer, u *ui.UI, rep *statusReport) {
	failed := 0
	check := func(m int, label, val string) {
		if m == markBad {
			failed++
		}
		fmt.Fprintf(w, "  %s %s  %s\n", viewMark(u, m), viewKey(u, 9, label), val)
	}
	pick := func(ok bool) int {
		if ok {
			return markOK
		}
		return markBad
	}

	fmt.Fprintf(w, "%s  %s\n\n", u.Bold("Spicrawl API"), rep.BaseURL)
	check(pick(rep.APIReachable), "reachable", statusYN(rep.APIReachable))
	if !rep.APIReachable {
		fmt.Fprintf(w, "\n%s %s\n", u.Red("✗"), "the API could not be reached")
		statusPrintRepairs(w, u, rep.Repairs)
		return
	}
	check(pick(rep.Ready), "ready", statusReadyText(rep))
	cdp := markNeutral
	if rep.CDP == "enabled" {
		cdp = markOK
	}
	check(cdp, "cdp", statusCDPText(rep))
	key := markNeutral
	if rep.KeyValid != nil {
		key = pick(*rep.KeyValid)
	} else if statusKeyBad(rep) {
		key = markBad
	}
	check(key, "key", statusKeyText(rep))
	mcp := markNeutral
	if len(rep.MCP.Detected) > 0 {
		mcp = pick(len(rep.MCP.names(false)) == 0)
	}
	check(mcp, "mcp", statusMCPText(rep.MCP))

	switch {
	case rep.WorkersError != "":
		check(markNeutral, "workers", rep.WorkersError)
	case len(rep.Workers) > 0:
		fmt.Fprintf(w, "\n%s\n", u.Bold("workers"))
		for _, k := range statusWorkerKinds {
			v, ok := rep.Workers[k]
			if !ok {
				continue
			}
			m := markWarn
			switch {
			case v.Present == 0:
				m = markNeutral // none of this kind runs here
			case statusWorkerBad(v):
				m = markBad
			case v.Ready >= v.Present:
				m = markOK
			}
			if m == markBad {
				failed++
			}
			fmt.Fprintf(w, "  %s %s  %d ready of %d\n", viewMark(u, m), viewKey(u, 9, k), v.Ready, v.Present)
		}
	}

	if failed == 0 {
		fmt.Fprintf(w, "\n%s all checks passed\n", u.Green("✓"))
	} else {
		fmt.Fprintf(w, "\n%s %d %s failed\n", u.Red("✗"), failed, map[bool]string{true: "check", false: "checks"}[failed == 1])
	}
	statusPrintRepairs(w, u, rep.Repairs)
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
