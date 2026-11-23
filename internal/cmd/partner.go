package cmd

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/OfficialSpicrawl/cli/internal/api"
	"github.com/OfficialSpicrawl/cli/internal/output"
	"github.com/OfficialSpicrawl/cli/internal/ui"
)

// The partner API (partner key only): one workspace per user of a partner,
// the shared balance that pays for them, and the tiers that limit them. A
// workspace key cannot call any of it (ERR::AUTH::INSUFFICIENT_SCOPE, exit 3).

var partnerCmd = &cobra.Command{
	Use:   "partner",
	Short: "Resell Spicrawl: your users' workspaces, balance, credits and tiers",
	Long: `A partner gives each of its own users a workspace with its own API key, and pays
for their requests from one shared balance. These commands use the partner key,
from the same --api-key, SPICRAWL_API_KEY or config slot as everything else. A
workspace key is refused (exit 3).

"user" is your id for one of your users. "balance" is the credits you can give
your users; a "cap" is the most one user may draw from it in a month. Amounts are
whole credits.`,
	Example: `  spicrawl partner summary
  KEY=$(spicrawl partner users create user-42 --tier Standard -q)
  spicrawl partner users give user-42 500 --yes`,
}

var partnerUsageCmd = &cobra.Command{
	Use:   "usage",
	Short: "Credits your users spent, by day or by user (GET /v1/partner/usage)",
	Long: `Usage across all your users. --by day gives one row per UTC day; --by user gives one
row per user and splits each user's charge into free credits, credits you gave
them, and what came from the balance. The three add up to the credits column,
each part rounded down to whole credits, so free absorbs the rounding.

--from is inclusive and --to exclusive, as for "spicrawl usage". --limit, --sort
and --offset apply to --by user only.`,
	Example: `  spicrawl partner usage --from 2026-11-01 --to 2026-12-01
  spicrawl partner usage --by user --sort credits --limit 20
  spicrawl partner usage --json | jq .totals`,
	Args: cobra.NoArgs,
	RunE: partnerRunUsage,
}

var (
	partnerUsageBy     string
	partnerUsageFrom   string
	partnerUsageTo     string
	partnerUsageLimit  int
	partnerUsageSort   string
	partnerUsageOffset int
)

var partnerUsageSorts = []string{"credits", "requests", "fail_rate", "last_active"}

var partnerSummaryCmd = &cobra.Command{
	Use:   "summary",
	Short: "Your account at a glance: users, caps, limits and balance (GET /v1/partner/summary)",
	Long: `Your users by status, the monthly caps they add up to and how many users have no cap,
the account's maximums for tiers, and the balance. Read on each call.`,
	Example: `  spicrawl partner summary
  spicrawl partner summary --json | jq .caps`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		resp, err := partnerCall(cmd, http.MethodGet, "/v1/partner/summary", nil, nil)
		if err != nil {
			return err
		}
		p := Printer()
		if p.JSON {
			return p.RawJSON(resp.Body)
		}
		var s partnerSummary
		if err := partnerDecode(resp.Body, &s); err != nil {
			return err
		}
		rows := []ui.Row{
			{Key: "users", Val: fmt.Sprintf("%d total, %d active, %d suspended", s.Users.Total, s.Users.Active, s.Users.Suspended)},
			{Key: "caps", Val: fmt.Sprintf("%d credits committed to caps; %d users with no cap", s.Caps.Committed, s.Caps.UncappedUsers)},
			{Key: "balance", Val: fmt.Sprintf("%d credits, %d not yet committed to caps", s.Balance.BalanceCredits, s.Balance.BalanceCredits-s.Caps.Committed)},
			{Key: "workspaces", Val: fmt.Sprintf("%d created; up to %d new a day", s.WorkspaceCount, s.WorkspaceDailyLimit)},
			{Key: "free credits", Val: fmt.Sprintf("%d a month per user (your default)", s.FreeCredits)},
			{Key: "maximums", Val: fmt.Sprintf("%d requests a minute, %d running at once, %d free credits a user", s.Limits.MaxRequestsPerMinute, s.Limits.MaxConcurrency, s.Limits.MaxFreeCredits)},
		}
		fmt.Fprint(p.Out, viewRows(outUI(p), "", rows))
		return nil
	},
}

var partnerBalanceCmd = &cobra.Command{
	Use:   "balance",
	Short: "The balance: credits you can give your users (GET /v1/partner/wallet)",
	Long: `The balance is what has been added to your account, less what your users spent from
it. Only the balance can be given to users; the credit line cannot.`,
	Example: `  spicrawl partner balance
  spicrawl partner balance --json | jq .balance_credits`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		resp, err := partnerCall(cmd, http.MethodGet, "/v1/partner/wallet", nil, nil)
		if err != nil {
			return err
		}
		p := Printer()
		if p.JSON {
			return p.RawJSON(resp.Body)
		}
		var b partnerBalance
		if err := partnerDecode(resp.Body, &b); err != nil {
			return err
		}
		rows := []ui.Row{
			{Key: "balance", Val: fmt.Sprintf("%d credits (what you can give users)", b.BalanceCredits)},
			{Key: "available", Val: fmt.Sprintf("%d credits (balance plus credit line)", b.AvailableCredits)},
			{Key: "funded", Val: fmt.Sprintf("%d credits", b.FundedCredits)},
			{Key: "spent", Val: fmt.Sprintf("%d credits by your users", b.SpentCredits)},
			{Key: "credit line", Val: fmt.Sprintf("%d credits", b.CreditLineCredits)},
		}
		fmt.Fprint(p.Out, viewRows(outUI(p), "", rows))
		return nil
	},
}

// partnerIDRE is a user id: the partner's own id for the user.
var partnerIDRE = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// partnerKeyRE is an idempotency key: 8 to 100 characters, no spaces.
var partnerKeyRE = regexp.MustCompile(`^\S{8,100}$`)

// partnerUserPath is the workspace route of one user, with the id escaped. A
// bad id is refused here, before anything is sent.
func partnerUserPath(id string) (string, error) {
	if !partnerIDRE.MatchString(id) {
		return "", Usagef("user id %q: use 1 to 128 letters, digits, '.', '_', ':' or '-' (no '@' or spaces)", id)
	}
	// PathEscape leaves ':' alone; the wire form encodes it too (the API routes on the escaped path).
	return "/v1/partner/workspaces/" + strings.ReplaceAll(url.PathEscape(id), ":", "%3A"), nil
}

// partnerCall makes one call of the partner API with the configured key.
func partnerCall(cmd *cobra.Command, method, path string, q url.Values, body any) (*api.Response, error) {
	c, err := Client()
	if err != nil {
		return nil, err
	}
	return c.Do(cmd.Context(), api.Request{Method: method, Path: path, Query: q, Body: body})
}

// partnerDecode reads a 2xx answer. Its error names nothing from the body: a
// body that is not JSON may hold a key, and no error path may echo one.
func partnerDecode(body []byte, v any) error {
	if err := json.Unmarshal(body, v); err != nil {
		return errors.New("the API answered with a body that is not the expected JSON")
	}
	return nil
}

// partnerNewKey makes an idempotency key: 16 random bytes in hex.
func partnerNewKey() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("make an idempotency key: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// partnerConfirm asks on stderr and reads the answer from stdin. Callers check
// for a terminal first; only y or yes says yes.
func partnerConfirm(question string) bool {
	fmt.Fprint(stderr, question+" [y/N] ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes"
}

// partnerSetWindow sets from and to (UTC days, to exclusive) on q.
func partnerSetWindow(q url.Values, from, to string) error {
	for _, d := range []struct{ flag, val, param string }{{"from", from, "from"}, {"to", to, "to"}} {
		if d.val == "" {
			continue
		}
		if !usageValidDay(d.val) {
			return Usagef("--%s must be a date like 2026-08-01, not %q", d.flag, d.val)
		}
		q.Set(d.param, d.val)
	}
	if from != "" && to != "" && to <= from {
		return Usagef("--to is exclusive and must be after --from")
	}
	return nil
}

// partnerWhole is micro-credits as whole credits, rounded down.
func partnerWhole(micro int64) string {
	return strconv.FormatInt(micro/1_000_000, 10)
}

// partnerSplit shows a charge in whole credits: the total, the part given to
// the user, the part from the balance, and the free part. Each is rounded down,
// and free is what is left, so the four add up. Free absorbs the rounding, so
// it can be up to two credits off its exact value.
func partnerSplit(total, given, fromBalance int64) (t, free, g, b int64) {
	t, g, b = total/1_000_000, given/1_000_000, fromBalance/1_000_000
	free = t - g - b
	return t, free, g, b
}

// partnerDash shows an empty value as a dash.
func partnerDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func partnerName(s *string) string {
	if s == nil {
		return "—"
	}
	return partnerDash(*s)
}

func partnerNum(n *int64) string {
	if n == nil {
		return "—"
	}
	return strconv.FormatInt(*n, 10)
}

// partnerCapText is a cap in credits a month, or none.
func partnerCapText(n *int64) string {
	if n == nil {
		return "none"
	}
	return strconv.FormatInt(*n, 10)
}

// partnerTierRef is the tier a workspace is on.
type partnerTierRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// partnerWorkspace is the subset of a workspace the human views read. JSON
// output always prints the API's body unchanged.
type partnerWorkspace struct {
	WorkspaceID       string          `json:"workspace_id"`
	ExternalUserID    string          `json:"external_user_id"`
	DisplayName       *string         `json:"display_name"`
	Status            string          `json:"status"`
	FreeCredits       int64           `json:"free_credits"`
	WalletCap         *int64          `json:"wallet_cap"`
	WalletUsed        *int64          `json:"wallet_used"`
	FreeUsed          *int64          `json:"free_used"`
	OwnCredits        *int64          `json:"own_credits"`
	WindowResetsAt    string          `json:"window_resets_at"`
	Tier              *partnerTierRef `json:"tier"`
	RequestsPerMinute *int64          `json:"requests_per_minute"`
	Concurrency       *int64          `json:"concurrency"`
}

// partnerWorkspaceRows is one workspace as key/value lines.
func partnerWorkspaceRows(ws partnerWorkspace) []ui.Row {
	return []ui.Row{
		{Key: "user", Val: ws.ExternalUserID},
		{Key: "name", Val: partnerName(ws.DisplayName)},
		{Key: "status", Val: ws.Status},
		{Key: "tier", Val: partnerTierName(ws.Tier)},
		{Key: "cap", Val: partnerCapLine(ws.WalletCap)},
		{Key: "used this month", Val: partnerNum(ws.WalletUsed)},
		{Key: "credits given left", Val: partnerNum(ws.OwnCredits)},
		{Key: "free credits", Val: fmt.Sprintf("%d a month", ws.FreeCredits)},
		{Key: "free used this month", Val: partnerNum(ws.FreeUsed)},
		{Key: "requests a minute", Val: partnerNum(ws.RequestsPerMinute)},
		{Key: "concurrency", Val: partnerNum(ws.Concurrency)},
		{Key: "month ends", Val: ws.WindowResetsAt},
	}
}

// partnerCapLine is a cap as the line a person reads.
func partnerCapLine(n *int64) string {
	if n == nil {
		return "none"
	}
	return strconv.FormatInt(*n, 10) + " credits a month"
}

func partnerTierName(t *partnerTierRef) string {
	if t == nil {
		return "—"
	}
	return partnerDash(t.Name)
}

// partnerShowWorkspace prints a workspace answer: the body unchanged in JSON,
// otherwise an optional heading and the workspace's rows.
func partnerShowWorkspace(p *output.Printer, body []byte, heading string) error {
	if p.JSON {
		return p.RawJSON(body)
	}
	var ws partnerWorkspace
	if err := partnerDecode(body, &ws); err != nil {
		return err
	}
	u := outUI(p)
	if heading != "" {
		fmt.Fprintf(p.Out, "%s%s\n", viewLead(u, markOK), heading)
	}
	fmt.Fprint(p.Out, viewRows(u, "", partnerWorkspaceRows(ws)))
	return nil
}

// partnerSummary is the account overview.
type partnerSummary struct {
	FreeCredits         int64 `json:"free_credits"`
	WorkspaceDailyLimit int64 `json:"workspace_daily_limit"`
	WorkspaceCount      int64 `json:"workspace_count"`
	Limits              struct {
		MaxRequestsPerMinute int64 `json:"max_requests_per_minute"`
		MaxConcurrency       int64 `json:"max_concurrency"`
		MaxFreeCredits       int64 `json:"max_free_credits"`
	} `json:"limits"`
	Users struct {
		Total     int64 `json:"total"`
		Active    int64 `json:"active"`
		Suspended int64 `json:"suspended"`
	} `json:"users"`
	Caps struct {
		Committed     int64 `json:"committed"`
		UncappedUsers int64 `json:"uncapped_users"`
	} `json:"caps"`
	Balance partnerBalance `json:"balance"`
}

// partnerBalance is the balance figures of GET /v1/partner/wallet.
type partnerBalance struct {
	FundedCredits     int64 `json:"funded_credits"`
	CreditLineCredits int64 `json:"credit_line_credits"`
	SpentCredits      int64 `json:"spent_credits"`
	BalanceCredits    int64 `json:"balance_credits"`
	AvailableCredits  int64 `json:"available_credits"`
}

// partnerUsageRow is one day, one user, or the totals of a usage answer. The
// split of a charge is on the totals and the user rows; it is nil when the API
// left it out.
type partnerUsageRow struct {
	Day            string `json:"day"`
	ExternalUserID string `json:"external_user_id"`
	Requests       int64  `json:"requests"`
	RequestsFailed int64  `json:"requests_failed"`
	CreditsMicro   int64  `json:"credits_micro"`
	WalletMicro    *int64 `json:"credits_wallet_micro"`
	OwnMicro       *int64 `json:"credits_own_micro"`
}

// partnerUsageBody is the usage answer of GET /v1/partner/usage, and of a
// user's usage (which has no group_by or page).
type partnerUsageBody struct {
	Period  usagePeriod       `json:"period"`
	GroupBy string            `json:"group_by"`
	Totals  partnerUsageRow   `json:"totals"`
	Rows    []partnerUsageRow `json:"rows"`
	Page    *struct {
		NextOffset *int `json:"next_offset"`
	} `json:"page"`
}

// partnerPrintUsage writes usage as a table: one row per day, or per user with
// the charge split, then the total. next, when set, is the offset of the next page.
func partnerPrintUsage(p *output.Printer, period usagePeriod, byUser bool, rows []partnerUsageRow, totals partnerUsageRow, next *int) {
	by := "day"
	if byUser {
		by = "user"
	}
	fmt.Fprintf(p.Out, "period %s (%d days), by %s\n\n", period.String(), period.Days, by)
	if !byUser {
		body := make([][]string, 0, len(rows)+1)
		for _, r := range rows {
			body = append(body, []string{r.Day, strconv.FormatInt(r.Requests, 10), strconv.FormatInt(r.RequestsFailed, 10), partnerWhole(r.CreditsMicro)})
		}
		body = append(body, []string{"TOTAL", strconv.FormatInt(totals.Requests, 10), strconv.FormatInt(totals.RequestsFailed, 10), partnerWhole(totals.CreditsMicro)})
		p.Table([]string{"DAY", "REQUESTS", "FAILED", "CREDITS"}, body)
		return
	}
	body := make([][]string, 0, len(rows)+1)
	for _, r := range rows {
		body = append(body, partnerSplitCells(r.ExternalUserID, r))
	}
	body = append(body, partnerSplitCells("TOTAL", totals))
	p.Table([]string{"USER", "REQUESTS", "FAILED", "CREDITS", "FREE", "GIVEN", "FROM BALANCE"}, body)
	if next != nil {
		p.Info("more users exist; pass --offset %d for the next page", *next)
	}
}

// partnerSplitCells is one user's usage row with the charge split.
func partnerSplitCells(label string, r partnerUsageRow) []string {
	reqs, failed := strconv.FormatInt(r.Requests, 10), strconv.FormatInt(r.RequestsFailed, 10)
	if r.WalletMicro == nil || r.OwnMicro == nil {
		return []string{label, reqs, failed, partnerWhole(r.CreditsMicro), "—", "—", "—"}
	}
	t, free, g, b := partnerSplit(r.CreditsMicro, *r.OwnMicro, *r.WalletMicro)
	return []string{label, reqs, failed, strconv.FormatInt(t, 10), strconv.FormatInt(free, 10), strconv.FormatInt(g, 10), strconv.FormatInt(b, 10)}
}

func partnerRunUsage(cmd *cobra.Command, _ []string) error {
	f := cmd.Flags()
	q := url.Values{}
	if err := partnerSetWindow(q, partnerUsageFrom, partnerUsageTo); err != nil {
		return err
	}
	byUser := false
	switch partnerUsageBy {
	case "day":
	case "user":
		byUser = true
	default:
		return Usagef("--by must be day or user, not %q", partnerUsageBy)
	}
	if !byUser {
		if f.Changed("limit") || f.Changed("sort") || f.Changed("offset") {
			return Usagef("--limit, --sort and --offset apply to --by user")
		}
		q.Set("group_by", "day")
	} else {
		if partnerUsageLimit < 1 || partnerUsageLimit > 200 {
			return Usagef("--limit must be between 1 and 200")
		}
		if !slices.Contains(partnerUsageSorts, partnerUsageSort) {
			return Usagef("--sort must be one of %s, not %q", strings.Join(partnerUsageSorts, ", "), partnerUsageSort)
		}
		if partnerUsageOffset < 0 {
			return Usagef("--offset must be 0 or more")
		}
		q.Set("group_by", "workspace")
		q.Set("limit", strconv.Itoa(partnerUsageLimit))
		q.Set("sort", partnerUsageSort)
		q.Set("offset", strconv.Itoa(partnerUsageOffset))
	}
	resp, err := partnerCall(cmd, http.MethodGet, "/v1/partner/usage", q, nil)
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
	var next *int
	if b.Page != nil {
		next = b.Page.NextOffset
	}
	partnerPrintUsage(p, b.Period, byUser, b.Rows, b.Totals, next)
	return nil
}

// partnerKey is what GET /v1/key says the key in use is.
type partnerKey struct {
	Kind   string // partner | workspace | standard
	Scopes []string
}

// String is the key as a person reads it, with its scopes.
func (k partnerKey) String() string {
	name := map[string]string{"partner": "partner key", "workspace": "workspace key"}[k.Kind]
	if name == "" {
		name = "API key"
	}
	if len(k.Scopes) > 0 {
		name += " (scopes: " + strings.Join(k.Scopes, ", ") + ")"
	}
	return name
}

// partnerKeyInfo asks GET /v1/key what the key is. Only a 200 answers: an older
// API (404) or any failure reports nothing, and "auth status" stays as it was.
func partnerKeyInfo(ctx context.Context, baseURL, key string) (partnerKey, bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	c := api.New(baseURL, key, flagTimeout)
	resp, err := c.Do(ctx, api.Request{Path: "/v1/key"})
	if err != nil {
		return partnerKey{}, false
	}
	var k struct {
		Partner            bool     `json:"partner"`
		WorkspaceOfPartner bool     `json:"workspace_of_partner"`
		Scopes             []string `json:"scopes"`
	}
	if json.Unmarshal(resp.Body, &k) != nil {
		return partnerKey{}, false
	}
	out := partnerKey{Kind: "standard", Scopes: k.Scopes}
	switch {
	case k.Partner:
		out.Kind = "partner"
	case k.WorkspaceOfPartner:
		out.Kind = "workspace"
	}
	if out.Scopes == nil {
		out.Scopes = []string{}
	}
	return out, true
}

func init() {
	uf := partnerUsageCmd.Flags()
	uf.StringVar(&partnerUsageBy, "by", "day", "one row per day, or per user")
	uf.StringVar(&partnerUsageFrom, "from", "", "first UTC day, inclusive (YYYY-MM-DD; default 29 days ago)")
	uf.StringVar(&partnerUsageTo, "to", "", "end UTC day, EXCLUSIVE (YYYY-MM-DD; default tomorrow)")
	uf.IntVar(&partnerUsageLimit, "limit", 50, "users per page, 1-200 (--by user)")
	uf.StringVar(&partnerUsageSort, "sort", "credits", "credits, requests, fail_rate or last_active (--by user)")
	uf.IntVar(&partnerUsageOffset, "offset", 0, "first user to show (--by user)")

	partnerCmd.AddCommand(partnerSummaryCmd, partnerBalanceCmd, partnerUsageCmd, partnerUsersCmd, partnerTiersCmd)
	rootCmd.AddCommand(partnerCmd)
}
