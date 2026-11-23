package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/OfficialSpicrawl/cli/internal/output"
)

var partnerUsersCmd = &cobra.Command{
	Use:   "users",
	Short: "Your users: their workspaces, caps, suspension and credits given to them",
	Long: `Each user of yours has one workspace. Create it (or fetch it) for the user, set the
tier and the display name, suspend it when the user disconnects, and give it
credits from your balance.`,
	Example: `  KEY=$(spicrawl partner users create user-42 -q)
  spicrawl partner users list --sort used --limit 20
  spicrawl partner users give user-42 500 --yes`,
}

var (
	partnerListLimit   int
	partnerListAfter   string
	partnerListQuery   string
	partnerListSort    string
	partnerListOffset  int
	partnerListAll     bool
	partnerCreateTier  string
	partnerCreateName  string
	partnerCreateNoKey bool
	partnerRenameClear bool
	partnerUsersFrom   string
	partnerUsersTo     string
	partnerGiveKey     string
	partnerYes         bool
)

var partnerUsersListCmd = &cobra.Command{
	Use:   "list",
	Short: "Your users' workspaces: cap and use this month (GET /v1/partner/workspaces)",
	Long: `List one page of users, or every page with --all. The default order is by user id, and
--after pages through it. --sort used lists the biggest use of the balance this
month first; page it with --offset. The API reads every workspace for that order,
so it is slower. --query keeps the ids that start with the text.

"used" is what the user drew from the balance this month, against the cap.
"given left" is the credits you gave the user that are still unspent.`,
	Example: `  spicrawl partner users list
  spicrawl partner users list --sort used --limit 20
  spicrawl partner users list --query user-4 --all --json | jq -r '.workspaces[].external_user_id'`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		f := cmd.Flags()
		if partnerListLimit < 1 || partnerListLimit > 200 {
			return Usagef("--limit must be between 1 and 200")
		}
		if partnerListQuery != "" && !partnerIDRE.MatchString(partnerListQuery) {
			return Usagef("--query: use 1 to 128 letters, digits, '.', '_', ':' or '-'")
		}
		q := url.Values{"limit": {strconv.Itoa(partnerListLimit)}}
		if partnerListQuery != "" {
			q.Set("q", partnerListQuery)
		}
		switch partnerListSort {
		case "id":
			if f.Changed("offset") {
				return Usagef("--offset works with --sort used; the default order pages with --after")
			}
			q.Set("sort", "external_user_id")
			if partnerListAfter != "" {
				if !partnerIDRE.MatchString(partnerListAfter) {
					return Usagef("--after: use a user id")
				}
				q.Set("after", partnerListAfter)
			}
		case "used":
			if partnerListAfter != "" {
				return Usagef("--after works with the default order; --sort used pages with --offset")
			}
			if partnerListOffset < 0 {
				return Usagef("--offset must be 0 or more")
			}
			q.Set("sort", "wallet_used")
			if partnerListOffset > 0 {
				q.Set("offset", strconv.Itoa(partnerListOffset))
			}
		default:
			return Usagef("--sort must be id or used, not %q", partnerListSort)
		}

		var all []json.RawMessage
		var nextAfter *string
		var nextOffset *int
		for {
			resp, err := partnerCall(cmd, http.MethodGet, "/v1/partner/workspaces", q, nil)
			if err != nil {
				return err
			}
			var page struct {
				Workspaces []json.RawMessage `json:"workspaces"`
				NextAfter  *string           `json:"next_after"`
				NextOffset *int              `json:"next_offset"`
			}
			if err := partnerDecode(resp.Body, &page); err != nil {
				return err
			}
			all = append(all, page.Workspaces...)
			nextAfter, nextOffset = page.NextAfter, page.NextOffset
			if !partnerListAll || (nextOffset == nil && (nextAfter == nil || *nextAfter == "")) {
				break
			}
			if nextOffset != nil {
				q.Set("offset", strconv.Itoa(*nextOffset))
			} else {
				q.Set("after", *nextAfter)
			}
		}
		if partnerListAll {
			nextAfter, nextOffset = nil, nil
		}
		if all == nil {
			all = []json.RawMessage{}
		}
		out := map[string]any{"workspaces": all}
		if nextAfter != nil && *nextAfter != "" {
			out["next_after"] = *nextAfter
		}
		if nextOffset != nil {
			out["next_offset"] = *nextOffset
		}

		p := Printer()
		if p.JSON {
			return p.Value(out)
		}
		if len(all) == 0 {
			p.Info("no users")
			return nil
		}
		rows := make([][]string, 0, len(all))
		for _, raw := range all {
			var ws partnerWorkspace
			_ = json.Unmarshal(raw, &ws)
			rows = append(rows, []string{
				ws.ExternalUserID, partnerName(ws.DisplayName), ws.Status, partnerTierName(ws.Tier),
				partnerCapText(ws.WalletCap), partnerNum(ws.WalletUsed), partnerNum(ws.OwnCredits),
			})
		}
		u := outUI(p)
		viewTable(p, u, []string{"USER", "NAME", "STATUS", "TIER", "CAP", "USED", "GIVEN LEFT"}, rows, func(col int, cell string) string {
			if col == 2 {
				return viewStatus(u, cell, []string{"active"}, []string{"suspended"})
			}
			return cell
		})
		if !partnerListAll && (nextAfter != nil || nextOffset != nil) {
			p.Info("more users exist; pass --all to fetch every page")
		}
		return nil
	},
}

var partnerUsersCreateCmd = &cobra.Command{
	Use:   "create <id>",
	Short: "Create a user's workspace, or fetch it, and print its API key (PUT /v1/partner/workspaces/{id})",
	Long: `Create the workspace of one of your users, or fetch the one that exists: the call is
safe to repeat. The key it returns is the user's own key, the one its requests use.

On a terminal the key is printed once, alone on its own line on stdout; the status
and a hint go to stderr. Piped, the output is JSON with the key in api_key. To
capture the key without the JSON, pass -q, which prints the bare key even when
piped (as "browser url -q" does):

  KEY=$(spicrawl partner users create user-42 -q)

--no-key prints the status without the key. Store the key where your server keeps
its secrets: each create call returns the user's current key.

--tier sets the tier of a new workspace (an id or a name; an existing one keeps
its tier). --name sets the display name.`,
	Example: `  spicrawl partner users create user-42 --tier Standard
  KEY=$(spicrawl partner users create acct:7.eu_west -q)`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := partnerUserPath(args[0])
		if err != nil {
			return err
		}
		fields := map[string]any{}
		if t := strings.TrimSpace(partnerCreateTier); t != "" {
			fields["tier"] = t
		}
		if n := strings.TrimSpace(partnerCreateName); n != "" {
			fields["display_name"] = n
		}
		var body any
		if len(fields) > 0 {
			body = fields
		}
		resp, err := partnerCall(cmd, http.MethodPut, path, nil, body)
		if err != nil {
			return err
		}
		p := Printer()
		if p.Quiet && !flagJSON {
			p.JSON = false // -q prints the bare key, even when piped; only an explicit --json overrides it
		}
		if p.JSON {
			return p.RawJSON(resp.Body)
		}
		var ws partnerWorkspace
		if err := partnerDecode(resp.Body, &ws); err != nil {
			return err
		}
		var made struct {
			Created bool   `json:"created"`
			APIKey  string `json:"api_key"`
		}
		if err := partnerDecode(resp.Body, &made); err != nil {
			return err
		}
		if made.APIKey == "" {
			return fmt.Errorf("the API returned no key for user %s", args[0])
		}
		tier := "no tier: Spicrawl's default limits apply"
		if ws.Tier != nil {
			tier = "tier " + ws.Tier.Name
		}
		if made.Created {
			p.Info("Created user %s (%s)", args[0], tier)
		} else {
			p.Info("User %s already exists (%s)", args[0], tier)
		}
		if !partnerCreateNoKey {
			fmt.Fprintln(p.Out, made.APIKey)
			p.Info("Store the key: each create call returns the user's current key.")
		}
		return nil
	},
}

var partnerUsersGetCmd = &cobra.Command{
	Use:     "get <id>",
	Short:   "One user's workspace: tier, cap and use this month (GET /v1/partner/workspaces/{id})",
	Example: `  spicrawl partner users get user-42`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := partnerUserPath(args[0])
		if err != nil {
			return err
		}
		resp, err := partnerCall(cmd, http.MethodGet, path, nil, nil)
		if err != nil {
			return err
		}
		return partnerShowWorkspace(Printer(), resp.Body, "")
	},
}

var partnerUsersRenameCmd = &cobra.Command{
	Use:   "rename <id> [name]",
	Short: "Set or clear a user's display name (PATCH /v1/partner/workspaces/{id})",
	Long: `Set the name shown for a user in your lists, or remove it with --clear. Spicrawl
does not use the name.`,
	Example: `  spicrawl partner users rename user-42 "Jane Doe"
  spicrawl partner users rename user-42 --clear`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := partnerUserPath(args[0])
		if err != nil {
			return err
		}
		var body map[string]any
		switch {
		case partnerRenameClear && len(args) == 2:
			return Usagef("--clear removes the name: give a name or --clear, not both")
		case partnerRenameClear:
			body = map[string]any{"display_name": nil}
		case len(args) == 2 && strings.TrimSpace(args[1]) != "":
			body = map[string]any{"display_name": strings.TrimSpace(args[1])}
		default:
			return Usagef("give the new name, or --clear to remove it")
		}
		resp, err := partnerCall(cmd, http.MethodPatch, path, nil, body)
		if err != nil {
			return err
		}
		heading := "Renamed user " + args[0]
		if partnerRenameClear {
			heading = "Cleared the name of user " + args[0]
		}
		return partnerShowWorkspace(Printer(), resp.Body, heading)
	},
}

var partnerUsersSuspendCmd = &cobra.Command{
	Use:   "suspend <id>",
	Short: "Stop a user's key from working (POST /v1/partner/workspaces/{id}/suspend)",
	Long: `Suspend a user's workspace, for when the user disconnects from your platform. Its key
stops working within about a minute. "users reactivate" undoes it. Asks for
confirmation on a terminal; without one you must pass --yes.`,
	Example: `  spicrawl partner users suspend user-42 --yes`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := partnerUserPath(args[0])
		if err != nil {
			return err
		}
		if !partnerYes {
			if !output.IsStdinTerminal() {
				return Usagef("refusing to suspend user %s without confirmation: pass --yes", args[0])
			}
			if !partnerConfirm(fmt.Sprintf("Suspend user %s? Its key stops working within about a minute.", args[0])) {
				return Usagef("aborted")
			}
		}
		resp, err := partnerCall(cmd, http.MethodPost, path+"/suspend", nil, nil)
		if err != nil {
			return err
		}
		return partnerShowWorkspace(Printer(), resp.Body, "Suspended user "+args[0])
	},
}

var partnerUsersReactivateCmd = &cobra.Command{
	Use:     "reactivate <id>",
	Short:   "Let a suspended user's key work again (POST /v1/partner/workspaces/{id}/reactivate)",
	Long:    `Reactivate a workspace you suspended. Spicrawl's own suspensions are not undone here.`,
	Example: `  spicrawl partner users reactivate user-42`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := partnerUserPath(args[0])
		if err != nil {
			return err
		}
		resp, err := partnerCall(cmd, http.MethodPost, path+"/reactivate", nil, nil)
		if err != nil {
			return err
		}
		return partnerShowWorkspace(Printer(), resp.Body, "Reactivated user "+args[0])
	},
}

var partnerUsersTierCmd = &cobra.Command{
	Use:   "tier <id> <tier>",
	Short: "Move a user to another tier (PUT /v1/partner/workspaces/{id}/tier)",
	Long: `Move one user to a tier. <tier> is a tier id or a name. Changes reach the user's
requests within about a minute.`,
	Example: `  spicrawl partner users tier user-42 Pro`,
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := partnerUserPath(args[0])
		if err != nil {
			return err
		}
		resp, err := partnerCall(cmd, http.MethodPut, path+"/tier", nil, map[string]any{"tier": args[1]})
		if err != nil {
			return err
		}
		return partnerShowWorkspace(Printer(), resp.Body, "Moved user "+args[0]+" to tier "+args[1])
	},
}

var partnerUsersUsageCmd = &cobra.Command{
	Use:   "usage <id>",
	Short: "One user's usage, one row per day (GET /v1/partner/workspaces/{id}/usage)",
	Long: `Credits one user spent, per UTC day. --from is inclusive and --to exclusive, as for
"spicrawl usage".`,
	Example: `  spicrawl partner users usage user-42 --from 2026-11-01 --to 2026-12-01`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := partnerUserPath(args[0])
		if err != nil {
			return err
		}
		q := url.Values{}
		if err := partnerSetWindow(q, partnerUsersFrom, partnerUsersTo); err != nil {
			return err
		}
		resp, err := partnerCall(cmd, http.MethodGet, path+"/usage", q, nil)
		if err != nil {
			return err
		}
		p := Printer()
		if p.JSON {
			return p.RawJSON(resp.Body)
		}
		var b partnerUsageBody
		if err := partnerDecode(resp.Body, &b); err != nil {
			return err
		}
		partnerPrintUsage(p, b.Period, false, b.Rows, b.Totals, nil)
		return nil
	},
}

var partnerUsersGiveCmd = &cobra.Command{
	Use:   "give <id> <credits>",
	Short: "Move credits from the balance to one user (POST /v1/partner/workspaces/{id}/credits)",
	Long: `Move whole credits from your balance to one user. The user spends them after the free
credits and before the balance; they do not reset each month. No command here
takes credits back.

Each give carries an idempotency key. Without --idempotency-key one is made, and
on a terminal it is printed on stderr before the request. To retry a give whose
answer was lost, run the same command again with that key: a key that was already
used changes nothing and reports "Nothing moved". Reusing a key for another
amount or user is refused. Nothing is retried for you.

Asks for confirmation on a terminal, showing the user and the balance; without
one you must pass --yes.`,
	Example: `  spicrawl partner users give user-42 500 --yes
  spicrawl partner users give user-42 500 --idempotency-key grant-2026-11-20-42 --yes`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := partnerUserPath(args[0])
		if err != nil {
			return err
		}
		n, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil || n < 1 || n > 100_000_000 {
			return Usagef("credits must be a whole number from 1 to 100000000, not %q", args[1])
		}
		key := partnerGiveKey
		generated := !cmd.Flags().Changed("idempotency-key")
		if generated {
			if key, err = partnerNewKey(); err != nil {
				return err
			}
		} else if !partnerKeyRE.MatchString(key) {
			return Usagef("--idempotency-key must be 8 to 100 characters with no spaces")
		}

		p := Printer()
		if !partnerYes {
			if !output.IsStdinTerminal() {
				return Usagef("refusing to give %d credits to user %s without confirmation: pass --yes", n, args[0])
			}
			wr, err := partnerCall(cmd, http.MethodGet, path, nil, nil)
			if err != nil {
				return err
			}
			var who partnerWorkspace
			if err := partnerDecode(wr.Body, &who); err != nil {
				return err
			}
			wl, err := partnerCall(cmd, http.MethodGet, "/v1/partner/wallet", nil, nil)
			if err != nil {
				return err
			}
			var bal partnerBalance
			if err := partnerDecode(wl.Body, &bal); err != nil {
				return err
			}
			label := args[0]
			if name := partnerName(who.DisplayName); name != "—" {
				label = name + " (" + args[0] + ")"
			}
			if !partnerConfirm(fmt.Sprintf("Give %d credits to %s? Your balance is %d credits.", n, label, bal.BalanceCredits)) {
				return Usagef("aborted")
			}
		}
		if generated {
			p.Info("Idempotency key: %s; pass it again to retry safely", key)
		}

		resp, err := partnerCall(cmd, http.MethodPost, path+"/credits", nil, map[string]any{"credits": n, "idempotency_key": key})
		if err != nil {
			return err
		}
		if p.JSON {
			return p.RawJSON(resp.Body)
		}
		var g struct {
			Applied          bool  `json:"applied"`
			UserCredits      int64 `json:"user_credits"`
			BalanceAvailable int64 `json:"balance_available"`
		}
		if err := partnerDecode(resp.Body, &g); err != nil {
			return err
		}
		if !g.Applied {
			p.Info("Nothing moved: this request was already carried out.")
			return nil
		}
		fmt.Fprintf(p.Out, "%sGave %d credits to %s. Their credits: %d. Balance: %d.\n",
			viewLead(outUI(p), markOK), n, args[0], g.UserCredits, g.BalanceAvailable)
		return nil
	},
}

func init() {
	lf := partnerUsersListCmd.Flags()
	lf.IntVar(&partnerListLimit, "limit", 50, "users per page, 1-200")
	lf.StringVar(&partnerListAfter, "after", "", "start after this user id (default order)")
	lf.StringVar(&partnerListQuery, "query", "", "only users whose id starts with this text")
	lf.StringVar(&partnerListSort, "sort", "id", "id (default order) or used (biggest use of the balance first)")
	lf.IntVar(&partnerListOffset, "offset", 0, "first row of the page (--sort used)")
	lf.BoolVar(&partnerListAll, "all", false, "follow every page")

	cf := partnerUsersCreateCmd.Flags()
	cf.StringVar(&partnerCreateTier, "tier", "", "tier of a new workspace: id or name (an existing one keeps its tier)")
	cf.StringVar(&partnerCreateName, "name", "", "display name, for your own lists")
	cf.BoolVar(&partnerCreateNoKey, "no-key", false, "print the status but not the key")

	partnerUsersRenameCmd.Flags().BoolVar(&partnerRenameClear, "clear", false, "remove the display name")

	uf := partnerUsersUsageCmd.Flags()
	uf.StringVar(&partnerUsersFrom, "from", "", "first UTC day, inclusive (YYYY-MM-DD)")
	uf.StringVar(&partnerUsersTo, "to", "", "end UTC day, EXCLUSIVE (YYYY-MM-DD)")

	gf := partnerUsersGiveCmd.Flags()
	gf.StringVar(&partnerGiveKey, "idempotency-key", "", "key for this give, 8-100 characters with no spaces (default: a new random one)")
	gf.BoolVarP(&partnerYes, "yes", "y", false, "do not ask for confirmation")

	partnerUsersSuspendCmd.Flags().BoolVarP(&partnerYes, "yes", "y", false, "do not ask for confirmation")

	partnerUsersCmd.AddCommand(partnerUsersListCmd, partnerUsersCreateCmd, partnerUsersGetCmd,
		partnerUsersRenameCmd, partnerUsersSuspendCmd, partnerUsersReactivateCmd,
		partnerUsersTierCmd, partnerUsersUsageCmd, partnerUsersGiveCmd)
}
