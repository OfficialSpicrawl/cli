package cmd

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/OfficialSpicrawl/cli/internal/api"
	"github.com/OfficialSpicrawl/cli/internal/output"
	"github.com/OfficialSpicrawl/cli/internal/ui"
)

// apiCall sends one request with the resolved client. The brand, webhook and
// monitor commands use it; the partner commands have their own copy.
func apiCall(cmd *cobra.Command, method, path string, q url.Values, body any) (*api.Response, error) {
	c, err := Client()
	if err != nil {
		return nil, err
	}
	return c.Do(cmd.Context(), api.Request{Method: method, Path: path, Query: q, Body: body})
}

var webhooksCmd = &cobra.Command{
	Use:   "webhooks",
	Short: "Webhook endpoints: where Spicrawl posts batch and monitor events",
	Long: `A webhook endpoint is an HTTPS URL that receives events: batch.completed, batch.failed,
monitor.changed and monitor.failed. Every delivery is signed with the endpoint's
secret. "create" and "rotate-secret" print the secret once; store it then.

"test" sends a ping to check the endpoint is reachable, and "deliveries" shows
whether each event arrived and what went wrong.`,
	Example: `  spicrawl webhooks create https://hooks.example.com/spicrawl --events batch.completed,monitor.changed
  spicrawl webhooks test 0190f3a2-7c1e-7d4b-9a2f-3b8e5c6d7e80
  spicrawl webhooks deliveries 0190f3a2-7c1e-7d4b-9a2f-3b8e5c6d7e80 --limit 5`,
}

var (
	webhookName            string
	webhookEvents          []string
	webhookFormat          string
	webhookURL             string
	webhookEnable          bool
	webhookDisable         bool
	webhookYes             bool
	webhookDeliveriesLimit int
)

// webhookEndpointOut is the part of an endpoint the human views read.
type webhookEndpointOut struct {
	ID                  string   `json:"id"`
	Name                *string  `json:"name"`
	URL                 string   `json:"url"`
	Events              []string `json:"events"`
	Format              string   `json:"format"`
	Enabled             bool     `json:"enabled"`
	DisabledReason      *string  `json:"disabled_reason"`
	ConsecutiveFailures int      `json:"consecutive_failures"`
	SecretVersion       int      `json:"secret_version"`
	LastSuccessAt       *string  `json:"last_success_at"`
	LastFailureAt       *string  `json:"last_failure_at"`
	Secret              string   `json:"secret"`
}

// webhookRows is one endpoint as key/value lines. A secret is shown only by
// the commands that issue one.
func webhookRows(e webhookEndpointOut) []ui.Row {
	status := "enabled"
	if !e.Enabled {
		status = "disabled"
	}
	rows := []ui.Row{
		{Key: "id", Val: e.ID},
		{Key: "name", Val: partnerName(e.Name)},
		{Key: "url", Val: e.URL},
		{Key: "events", Val: strings.Join(e.Events, ", ")},
		{Key: "format", Val: e.Format},
		{Key: "status", Val: status},
	}
	if e.DisabledReason != nil {
		rows = append(rows, ui.Row{Key: "disabled because", Val: *e.DisabledReason})
	}
	rows = append(rows,
		ui.Row{Key: "failures in a row", Val: strconv.Itoa(e.ConsecutiveFailures)},
		ui.Row{Key: "last success", Val: derefText(e.LastSuccessAt)},
		ui.Row{Key: "last failure", Val: derefText(e.LastFailureAt)},
		ui.Row{Key: "secret version", Val: strconv.Itoa(e.SecretVersion)},
	)
	return rows
}

// derefText is a nullable text value, or "" when it is null (the row is then skipped).
func derefText(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// webhookShow prints one endpoint: the body unchanged in JSON, otherwise a
// heading and its rows. With secret set, the secret is printed as a marked row
// and a note says it is shown only now.
func webhookShow(p *output.Printer, body []byte, heading string, secret bool) error {
	if p.JSON {
		return p.RawJSON(body)
	}
	var e webhookEndpointOut
	if err := partnerDecode(body, &e); err != nil {
		return err
	}
	u := outUI(p)
	fmt.Fprintf(p.Out, "%s%s\n", viewLead(u, markOK), heading)
	rows := webhookRows(e)
	if secret {
		rows = append(rows, ui.Row{Key: "signing secret", Val: e.Secret + "   (shown once: store it now)"})
	}
	fmt.Fprint(p.Out, viewRows(u, "", rows))
	return nil
}

var webhooksCreateCmd = &cobra.Command{
	Use:   "create <url>",
	Short: "Register an endpoint and print its signing secret once (POST /v1/webhooks)",
	Long: `Register an HTTPS endpoint. --events picks the events it receives (default: the two
batch events). --format json sends the signed JSON document; slack and discord send
a text summary to an incoming-webhook URL of that service.

The signing secret is printed once, on stdout, as the "signing secret" line. It is
never returned again: keep it before you leave, or rotate it with "rotate-secret".`,
	Example: `  spicrawl webhooks create https://hooks.example.com/spicrawl --name prod --events batch.completed,batch.failed
  spicrawl webhooks create https://hooks.slack.com/services/T000/B000/XXX --format slack --events monitor.changed`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		body := map[string]any{"url": args[0]}
		if name := strings.TrimSpace(webhookName); name != "" {
			body["name"] = name
		}
		if len(webhookEvents) > 0 {
			body["events"] = webhookEvents
		}
		if webhookFormat != "" {
			body["format"] = webhookFormat
		}
		resp, err := apiCall(cmd, http.MethodPost, "/v1/webhooks", nil, body)
		if err != nil {
			return err
		}
		return webhookShow(Printer(), resp.Body, "Created webhook endpoint", true)
	},
}

var webhooksListCmd = &cobra.Command{
	Use:     "list",
	Short:   "Your webhook endpoints (GET /v1/webhooks)",
	Example: `  spicrawl webhooks list --json | jq -r '.data[].id'`,
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		resp, err := apiCall(cmd, http.MethodGet, "/v1/webhooks", nil, nil)
		if err != nil {
			return err
		}
		p := Printer()
		if p.JSON {
			return p.RawJSON(resp.Body)
		}
		var list struct {
			Data []webhookEndpointOut `json:"data"`
		}
		if err := partnerDecode(resp.Body, &list); err != nil {
			return err
		}
		if len(list.Data) == 0 {
			p.Info("no webhook endpoints: create one with spicrawl webhooks create")
			return nil
		}
		rows := make([][]string, 0, len(list.Data))
		for _, e := range list.Data {
			status := "enabled"
			if !e.Enabled {
				status = "disabled"
			}
			rows = append(rows, []string{e.ID, partnerName(e.Name), e.URL, strings.Join(e.Events, ","), e.Format, status})
		}
		u := outUI(p)
		viewTable(p, u, []string{"ID", "NAME", "URL", "EVENTS", "FORMAT", "STATUS"}, rows, func(col int, cell string) string {
			if col == 5 {
				return viewStatus(u, cell, []string{"enabled"}, []string{"disabled"})
			}
			return cell
		})
		return nil
	},
}

var webhooksGetCmd = &cobra.Command{
	Use:     "get <id>",
	Short:   "One webhook endpoint (GET /v1/webhooks/{webhookID})",
	Example: `  spicrawl webhooks get 0190f3a2-7c1e-7d4b-9a2f-3b8e5c6d7e80`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := apiCall(cmd, http.MethodGet, "/v1/webhooks/"+url.PathEscape(args[0]), nil, nil)
		if err != nil {
			return err
		}
		return webhookShow(Printer(), resp.Body, "Webhook endpoint "+args[0], false)
	},
}

var webhooksUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Change an endpoint's URL, name, events or format, or enable or disable it (PATCH /v1/webhooks/{webhookID})",
	Long: `Change the fields you pass; the rest stay as they are. --enable turns a disabled endpoint
back on and resets its failure count. --disable stops deliveries until you enable it.`,
	Example: `  spicrawl webhooks update 0190f3a2-7c1e-7d4b-9a2f-3b8e5c6d7e80 --events batch.completed,monitor.failed
  spicrawl webhooks update 0190f3a2-7c1e-7d4b-9a2f-3b8e5c6d7e80 --enable`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		f := cmd.Flags()
		body := map[string]any{}
		if f.Changed("url") {
			body["url"] = webhookURL
		}
		if f.Changed("name") {
			body["name"] = webhookName
		}
		if f.Changed("events") {
			body["events"] = webhookEvents
		}
		if f.Changed("format") {
			body["format"] = webhookFormat
		}
		if f.Changed("enable") && f.Changed("disable") {
			return Usagef("--enable and --disable are opposites: pass one")
		}
		if f.Changed("enable") {
			body["enabled"] = true
		}
		if f.Changed("disable") {
			body["enabled"] = false
		}
		if len(body) == 0 {
			return Usagef("nothing to change: pass a flag such as --url, --events, --enable or --disable")
		}
		resp, err := apiCall(cmd, http.MethodPatch, "/v1/webhooks/"+url.PathEscape(args[0]), nil, body)
		if err != nil {
			return err
		}
		return webhookShow(Printer(), resp.Body, "Updated webhook endpoint "+args[0], false)
	},
}

var webhooksDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a webhook endpoint (DELETE /v1/webhooks/{webhookID})",
	Long: `Delete an endpoint. Spicrawl stops sending to it. Asks for confirmation on a terminal;
without one you must pass --yes.`,
	Example: `  spicrawl webhooks delete 0190f3a2-7c1e-7d4b-9a2f-3b8e5c6d7e80 --yes`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !webhookYes && !output.IsStdinTerminal() {
			return Usagef("refusing to delete webhook endpoint %s without confirmation: pass --yes", args[0])
		}
		if !webhookYes && !partnerConfirm(fmt.Sprintf("Delete webhook endpoint %s? Spicrawl stops sending to it.", args[0])) {
			return Usagef("aborted")
		}
		if _, err := apiCall(cmd, http.MethodDelete, "/v1/webhooks/"+url.PathEscape(args[0]), nil, nil); err != nil {
			return err
		}
		p := Printer()
		return p.Result(map[string]any{"id": args[0], "deleted": true}, func(w io.Writer) {
			fmt.Fprintf(w, "%sDeleted webhook endpoint %s\n", viewLead(outUI(p), markOK), args[0])
		})
	},
}

var webhooksTestCmd = &cobra.Command{
	Use:     "test <id>",
	Short:   "Send a ping to an endpoint (POST /v1/webhooks/{webhookID}/test)",
	Long:    `Queue a ping delivery to the endpoint. It returns at once; "webhooks deliveries" shows whether it arrived.`,
	Example: `  spicrawl webhooks test 0190f3a2-7c1e-7d4b-9a2f-3b8e5c6d7e80`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := apiCall(cmd, http.MethodPost, "/v1/webhooks/"+url.PathEscape(args[0])+"/test", nil, nil)
		if err != nil {
			return err
		}
		p := Printer()
		if p.JSON {
			return p.RawJSON(resp.Body)
		}
		var queued struct {
			DeliveryID string `json:"delivery_id"`
		}
		if err := partnerDecode(resp.Body, &queued); err != nil {
			return err
		}
		fmt.Fprintf(p.Out, "%sQueued a test ping, delivery %s\n", viewLead(outUI(p), markOK), queued.DeliveryID)
		p.Info("see whether it arrived: spicrawl webhooks deliveries %s", args[0])
		return nil
	},
}

var webhooksRotateCmd = &cobra.Command{
	Use:   "rotate-secret <id>",
	Short: "Issue a new signing secret and print it once (POST /v1/webhooks/{webhookID}/rotate-secret)",
	Long: `Issue a new secret for the endpoint. It is printed once, as the "signing secret" line, and
the deliveries that follow are signed with it. Update the receiving end before or as
you rotate, or its checks will fail.`,
	Example: `  spicrawl webhooks rotate-secret 0190f3a2-7c1e-7d4b-9a2f-3b8e5c6d7e80`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := apiCall(cmd, http.MethodPost, "/v1/webhooks/"+url.PathEscape(args[0])+"/rotate-secret", nil, nil)
		if err != nil {
			return err
		}
		return webhookShow(Printer(), resp.Body, "Issued a new signing secret for "+args[0], true)
	},
}

// webhookDeliveryOut is one delivery row of the deliveries list.
type webhookDeliveryOut struct {
	ID          string  `json:"id"`
	EventType   string  `json:"event_type"`
	Status      string  `json:"status"`
	Attempt     int     `json:"attempt"`
	MaxAttempts int     `json:"max_attempts"`
	LastError   *string `json:"last_error"`
	CreatedAt   string  `json:"created_at"`
}

var webhooksDeliveriesCmd = &cobra.Command{
	Use:   "deliveries <id>",
	Short: "Recent deliveries to an endpoint, newest first (GET /v1/webhooks/{webhookID}/deliveries)",
	Long: `One row per event, not per attempt. STATUS is the current state: pending, delivering,
delivered, failed (it will be retried) or dead (the retries ran out).`,
	Example: `  spicrawl webhooks deliveries 0190f3a2-7c1e-7d4b-9a2f-3b8e5c6d7e80 --limit 50`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if webhookDeliveriesLimit < 1 || webhookDeliveriesLimit > 100 {
			return Usagef("--limit must be between 1 and 100")
		}
		q := url.Values{"limit": {strconv.Itoa(webhookDeliveriesLimit)}}
		resp, err := apiCall(cmd, http.MethodGet, "/v1/webhooks/"+url.PathEscape(args[0])+"/deliveries", q, nil)
		if err != nil {
			return err
		}
		p := Printer()
		if p.JSON {
			return p.RawJSON(resp.Body)
		}
		var list struct {
			Data []webhookDeliveryOut `json:"data"`
		}
		if err := partnerDecode(resp.Body, &list); err != nil {
			return err
		}
		if len(list.Data) == 0 {
			p.Info("no deliveries yet")
			return nil
		}
		rows := make([][]string, 0, len(list.Data))
		for _, d := range list.Data {
			rows = append(rows, []string{
				d.ID, d.EventType, d.Status, fmt.Sprintf("%d/%d", d.Attempt, d.MaxAttempts),
				d.CreatedAt, derefText(d.LastError),
			})
		}
		u := outUI(p)
		viewTable(p, u, []string{"DELIVERY", "EVENT", "STATUS", "ATTEMPTS", "CREATED", "LAST ERROR"}, rows, func(col int, cell string) string {
			if col == 2 {
				return viewStatus(u, cell, []string{"delivered"}, []string{"failed", "dead"})
			}
			return cell
		})
		return nil
	},
}

func init() {
	cf := webhooksCreateCmd.Flags()
	cf.StringVar(&webhookName, "name", "", "a label for the endpoint (up to 100 characters)")
	cf.StringSliceVar(&webhookEvents, "events", nil, "events to receive: batch.completed, batch.failed, monitor.changed, monitor.failed")
	cf.StringVar(&webhookFormat, "format", "", "body shape: json (default), slack or discord")

	uf := webhooksUpdateCmd.Flags()
	uf.StringVar(&webhookURL, "url", "", "new HTTPS URL")
	uf.StringVar(&webhookName, "name", "", "new label")
	uf.StringSliceVar(&webhookEvents, "events", nil, "replace the events the endpoint receives")
	uf.StringVar(&webhookFormat, "format", "", "new body shape: json, slack or discord")
	uf.BoolVar(&webhookEnable, "enable", false, "turn the endpoint back on")
	uf.BoolVar(&webhookDisable, "disable", false, "stop deliveries to the endpoint")

	webhooksDeleteCmd.Flags().BoolVarP(&webhookYes, "yes", "y", false, "do not ask for confirmation")
	webhooksDeliveriesCmd.Flags().IntVar(&webhookDeliveriesLimit, "limit", 20, "deliveries to show, 1 to 100")

	webhooksCmd.AddCommand(webhooksCreateCmd, webhooksListCmd, webhooksGetCmd, webhooksUpdateCmd,
		webhooksDeleteCmd, webhooksTestCmd, webhooksRotateCmd, webhooksDeliveriesCmd)
	rootCmd.AddCommand(webhooksCmd)
}
