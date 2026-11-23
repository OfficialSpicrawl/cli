package cmd

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/OfficialSpicrawl/cli/internal/output"
	"github.com/OfficialSpicrawl/cli/internal/ui"
)

var partnerTiersCmd = &cobra.Command{
	Use:   "tiers",
	Short: "Tiers: the limits and free credits you give groups of users",
	Long: `A tier is a named set of limits, free credits and a cap on the balance, given to any
number of users. The tier of a user can be changed at any time. A <tier> argument
is a tier id or a name (names match without case).`,
	Example: `  spicrawl partner tiers list
  spicrawl partner tiers create Standard --requests-per-minute 60 --concurrency 2 --free-credits 250 --cap 500 --default`,
}

// partnerTier is one tier of GET /v1/partner/tiers.
type partnerTier struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	RequestsPerMinute int64  `json:"requests_per_minute"`
	Concurrency       int64  `json:"concurrency"`
	FreeCredits       int64  `json:"free_credits"`
	WalletCap         *int64 `json:"wallet_cap"`
	Default           bool   `json:"default"`
	Workspaces        int64  `json:"workspaces"`
}

// partnerTierOpts are the tier flags. Create and update share them; the
// create-only and update-only ones are registered by partnerAddTierFlags.
type partnerTierOpts struct {
	rpm, concurrency, freeCredits int
	capCredits                    int
	noCap, isDefault              bool
	rename                        string
}

var partnerTierFlags partnerTierOpts

// partnerAddTierFlags registers the tier flags on c. create adds --default; an
// update adds --rename instead.
func partnerAddTierFlags(c *cobra.Command, o *partnerTierOpts, create bool) {
	f := c.Flags()
	f.IntVar(&o.rpm, "requests-per-minute", 0, "requests a user may make a minute (at least 1)")
	f.IntVar(&o.concurrency, "concurrency", 0, "requests a user may run at once (at least 1)")
	f.IntVar(&o.freeCredits, "free-credits", 0, "free credits a user gets each month")
	f.IntVar(&o.capCredits, "cap", 0, "most credits a user may draw from the balance each month")
	f.BoolVar(&o.noCap, "no-cap", false, "the tier sets no cap on the balance")
	if create {
		f.BoolVar(&o.isDefault, "default", false, "new workspaces start on this tier")
	} else {
		f.StringVar(&o.rename, "rename", "", "a new name for the tier")
	}
}

// partnerTierBody is the request body of a tier create or update, from the
// flags that were set. A create needs a cap rule (--cap or --no-cap); an update
// sends only what it changes.
func partnerTierBody(cmd *cobra.Command, o *partnerTierOpts, create bool) (map[string]any, error) {
	f := cmd.Flags()
	if f.Changed("cap") && o.noCap {
		return nil, Usagef("--cap and --no-cap cannot be combined: one sets a cap, the other says there is none")
	}
	body := map[string]any{}
	switch {
	case f.Changed("cap"):
		if o.capCredits < 0 || o.capCredits > 100_000_000 {
			return nil, Usagef("--cap must be from 0 to 100000000 credits")
		}
		body["wallet_cap"] = o.capCredits
	case o.noCap:
		body["wallet_cap"] = nil
	case create:
		return nil, Usagef("one of --cap or --no-cap is required: --cap N sets the most credits a user may draw from the balance each month, --no-cap sets none")
	}
	if f.Changed("requests-per-minute") {
		if o.rpm < 1 {
			return nil, Usagef("--requests-per-minute must be at least 1")
		}
		body["requests_per_minute"] = o.rpm
	}
	if f.Changed("concurrency") {
		if o.concurrency < 1 {
			return nil, Usagef("--concurrency must be at least 1")
		}
		body["concurrency"] = o.concurrency
	}
	if f.Changed("free-credits") {
		if o.freeCredits < 0 {
			return nil, Usagef("--free-credits must be 0 or more")
		}
		body["free_credits"] = o.freeCredits
	}
	if create && o.isDefault {
		body["default"] = true
	}
	if f.Changed("rename") {
		n := strings.TrimSpace(o.rename)
		if n == "" || utf8.RuneCountInString(n) > 60 {
			return nil, Usagef("--rename must be 1 to 60 characters")
		}
		body["name"] = n
	}
	return body, nil
}

// partnerResolveTier finds the tier a <tier> argument names: an id, or a name
// matched without case. It makes one list call.
func partnerResolveTier(cmd *cobra.Command, ref string) (partnerTier, error) {
	ref = strings.TrimSpace(ref)
	resp, err := partnerCall(cmd, http.MethodGet, "/v1/partner/tiers", nil, nil)
	if err != nil {
		return partnerTier{}, err
	}
	var list struct {
		Tiers []partnerTier `json:"tiers"`
	}
	if err := partnerDecode(resp.Body, &list); err != nil {
		return partnerTier{}, err
	}
	for _, t := range list.Tiers {
		if t.ID == ref {
			return t, nil
		}
	}
	for _, t := range list.Tiers {
		if strings.EqualFold(strings.TrimSpace(t.Name), ref) {
			return t, nil
		}
	}
	return partnerTier{}, Usagef("no tier %q: see spicrawl partner tiers list", ref)
}

// partnerTierRows is one tier as key/value lines.
func partnerTierRows(t partnerTier) []ui.Row {
	def := "no"
	if t.Default {
		def = "yes"
	}
	return []ui.Row{
		{Key: "id", Val: t.ID},
		{Key: "default", Val: def},
		{Key: "requests a minute", Val: strconv.FormatInt(t.RequestsPerMinute, 10)},
		{Key: "concurrency", Val: strconv.FormatInt(t.Concurrency, 10)},
		{Key: "free credits", Val: fmt.Sprintf("%d a month", t.FreeCredits)},
		{Key: "cap", Val: partnerCapLine(t.WalletCap)},
		{Key: "users", Val: strconv.FormatInt(t.Workspaces, 10)},
	}
}

// partnerShowTier prints a tier answer: the body unchanged in JSON, otherwise a
// heading and the tier's rows.
func partnerShowTier(p *output.Printer, body []byte, heading string) error {
	if p.JSON {
		return p.RawJSON(body)
	}
	var t partnerTier
	if err := partnerDecode(body, &t); err != nil {
		return err
	}
	u := outUI(p)
	fmt.Fprintf(p.Out, "%s%s\n", viewLead(u, markOK), heading)
	fmt.Fprint(p.Out, viewRows(u, "", partnerTierRows(t)))
	return nil
}

var partnerTiersListCmd = &cobra.Command{
	Use:     "list",
	Short:   "Your tiers, the default first (GET /v1/partner/tiers)",
	Example: `  spicrawl partner tiers list`,
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		resp, err := partnerCall(cmd, http.MethodGet, "/v1/partner/tiers", nil, nil)
		if err != nil {
			return err
		}
		p := Printer()
		if p.JSON {
			return p.RawJSON(resp.Body)
		}
		var list struct {
			Tiers []partnerTier `json:"tiers"`
		}
		if err := partnerDecode(resp.Body, &list); err != nil {
			return err
		}
		if len(list.Tiers) == 0 {
			p.Info("no tiers: create one with spicrawl partner tiers create")
			return nil
		}
		rows := make([][]string, 0, len(list.Tiers))
		for _, t := range list.Tiers {
			def := "-"
			if t.Default {
				def = "default"
			}
			rows = append(rows, []string{
				t.Name, def, strconv.FormatInt(t.RequestsPerMinute, 10), strconv.FormatInt(t.Concurrency, 10),
				strconv.FormatInt(t.FreeCredits, 10), partnerCapText(t.WalletCap), strconv.FormatInt(t.Workspaces, 10),
			})
		}
		viewTable(p, outUI(p), []string{"NAME", "DEFAULT", "REQ/MIN", "CONCURRENCY", "FREE", "CAP", "USERS"}, rows, nil)
		return nil
	},
}

var partnerTiersCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a tier: the limits and free credits of a group of users (POST /v1/partner/tiers)",
	Long: `Create a tier. --requests-per-minute, --concurrency and --free-credits are required,
and so is one of --cap N (the most credits a user may draw from the balance each
month) or --no-cap (the tier sets none). The first tier you create is the default,
which new workspaces start on; --default makes this one the default.`,
	Example: `  spicrawl partner tiers create Standard --requests-per-minute 60 --concurrency 2 --free-credits 250 --cap 500 --default
  spicrawl partner tiers create Unlimited --requests-per-minute 120 --concurrency 4 --free-credits 0 --no-cap`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.TrimSpace(args[0])
		if name == "" || utf8.RuneCountInString(name) > 60 {
			return Usagef("the tier name must be 1 to 60 characters")
		}
		body, err := partnerTierBody(cmd, &partnerTierFlags, true)
		if err != nil {
			return err
		}
		body["name"] = name
		resp, err := partnerCall(cmd, http.MethodPost, "/v1/partner/tiers", nil, body)
		if err != nil {
			return err
		}
		return partnerShowTier(Printer(), resp.Body, "Created tier "+name)
	},
}

var partnerTiersUpdateCmd = &cobra.Command{
	Use:   "update <tier>",
	Short: "Change a tier's limits, free credits, cap or name (PATCH /v1/partner/tiers/{tier_id})",
	Long: `Change the fields you pass; the rest stay as they are. --no-cap removes the cap,
--cap sets it. Changes reach requests within about a minute.`,
	Example: `  spicrawl partner tiers update Standard --cap 800
  spicrawl partner tiers update 01JDN2K7M3Q9R8T6V4W1X0Y5Z2 --no-cap --rename Pro`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		body, err := partnerTierBody(cmd, &partnerTierFlags, false)
		if err != nil {
			return err
		}
		if len(body) == 0 {
			return Usagef("nothing to change: pass a flag such as --cap, --requests-per-minute or --rename")
		}
		t, err := partnerResolveTier(cmd, args[0])
		if err != nil {
			return err
		}
		resp, err := partnerCall(cmd, http.MethodPatch, "/v1/partner/tiers/"+url.PathEscape(t.ID), nil, body)
		if err != nil {
			return err
		}
		return partnerShowTier(Printer(), resp.Body, "Updated tier "+t.Name)
	},
}

var partnerTiersDefaultCmd = &cobra.Command{
	Use:     "default <tier>",
	Short:   "Make a tier the one new workspaces start on (PATCH /v1/partner/tiers/{tier_id})",
	Long:    `Make a tier the default. The previous default stops being one. Only "true" can be sent: to change the default, name another tier.`,
	Example: `  spicrawl partner tiers default Pro`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		t, err := partnerResolveTier(cmd, args[0])
		if err != nil {
			return err
		}
		resp, err := partnerCall(cmd, http.MethodPatch, "/v1/partner/tiers/"+url.PathEscape(t.ID), nil, map[string]any{"default": true})
		if err != nil {
			return err
		}
		return partnerShowTier(Printer(), resp.Body, "Now the default tier: "+t.Name)
	},
}

var partnerTiersDeleteCmd = &cobra.Command{
	Use:   "delete <tier>",
	Short: "Delete a tier (DELETE /v1/partner/tiers/{tier_id})",
	Long: `Delete a tier. The API refuses while workspaces are on it (move them first) and while
it is the default and other tiers exist; its message is shown as it is. Asks for
confirmation on a terminal; without one you must pass --yes.`,
	Example: `  spicrawl partner tiers delete Trial --yes`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !partnerYes && !output.IsStdinTerminal() {
			return Usagef("refusing to delete tier %s without confirmation: pass --yes", args[0])
		}
		t, err := partnerResolveTier(cmd, args[0])
		if err != nil {
			return err
		}
		if !partnerYes && !partnerConfirm(fmt.Sprintf("Delete tier %s? Move its users to another tier first.", t.Name)) {
			return Usagef("aborted")
		}
		if _, err := partnerCall(cmd, http.MethodDelete, "/v1/partner/tiers/"+url.PathEscape(t.ID), nil, nil); err != nil {
			return err
		}
		p := Printer()
		return p.Result(map[string]any{"id": t.ID, "name": t.Name, "deleted": true}, func(w io.Writer) {
			fmt.Fprintf(w, "%sDeleted tier %s\n", viewLead(outUI(p), markOK), t.Name)
		})
	},
}

func init() {
	partnerAddTierFlags(partnerTiersCreateCmd, &partnerTierFlags, true)
	for _, name := range []string{"requests-per-minute", "concurrency", "free-credits"} {
		_ = partnerTiersCreateCmd.MarkFlagRequired(name)
	}
	partnerAddTierFlags(partnerTiersUpdateCmd, &partnerTierFlags, false)
	partnerTiersDeleteCmd.Flags().BoolVarP(&partnerYes, "yes", "y", false, "do not ask for confirmation")

	partnerTiersCmd.AddCommand(partnerTiersListCmd, partnerTiersCreateCmd, partnerTiersUpdateCmd,
		partnerTiersDefaultCmd, partnerTiersDeleteCmd)
}
