package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check the API is reachable, ready, and accepts your key",
	Long: `Check the whole path from this machine to a working API:

  GET /readyz              reachable and ready, and whether the cloud
                           browser (CDP, /v1/browser) is enabled (no key needed)
  GET /v1/workers          worker kinds present and ready
  GET /v1/requests?limit=1 the key is accepted

Exit codes: 0 all good, 3 the key is missing or rejected, 10 the API could not
be reached; it also exits with the transport error's code when the connection
fails during the key check. A reachable API that reports not ready still exits 0; read
"ready" and "checks".`,
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
		rep := statusReport{BaseURL: r.BaseURL, KeySource: r.APIKeySource}

		p := Printer()
		sp := spin(p, "Checking the API")
		defer sp.Stop()
		var readyErr error
		rep.APIReachable, rep.Ready, rep.Checks, rep.CDP, readyErr = statusReadyz(ctx, c)
		if !rep.APIReachable {
			sp.Stop()
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
		if err := statusPrint(p, &rep); err != nil {
			return err
		}
		if rep.KeyValid != nil && !*rep.KeyValid {
			if r.APIKey == "" {
				return ErrNoKey
			}
			return &ExitError{Code: exitcode.Auth, Err: errors.New("the API rejected the key: " + rep.KeyError)}
		}
		return keyCheckErr
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

// statusReadyText is the ready value: yes, or no with the checks that failed.
func statusReadyText(rep *statusReport) string {
	ready := statusYN(rep.Ready)
	if !rep.Ready && len(rep.Checks) > 0 {
		var bad []string
		for k, v := range rep.Checks {
			if v != "ok" {
				bad = append(bad, k+"="+v)
			}
		}
		sort.Strings(bad)
		if len(bad) > 0 {
			ready += " (" + strings.Join(bad, ", ") + ")"
		}
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

var statusWorkerKinds = []string{"browser", "egress", "extract", "batch"}

func statusPrint(p *output.Printer, rep *statusReport) error {
	return p.Result(rep, func(w io.Writer) {
		if u := outUI(p); u.On() {
			statusPrintLive(w, u, rep)
			return
		}
		fmt.Fprintf(w, "%-12s %s\n", "base url:", rep.BaseURL)
		fmt.Fprintf(w, "%-12s %s\n", "reachable:", statusYN(rep.APIReachable))
		if !rep.APIReachable {
			return
		}
		fmt.Fprintf(w, "%-12s %s\n", "ready:", statusReadyText(rep))
		fmt.Fprintf(w, "%-12s %s\n", "cdp:", statusCDPText(rep))
		fmt.Fprintf(w, "%-12s %s\n", "key:", statusKeyText(rep))
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
	}
	check(key, "key", statusKeyText(rep))

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
			case v.Ready == 0:
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
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
