package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Spicrawl/cli/internal/api"
	"github.com/Spicrawl/cli/internal/output"
)

// sessionsRecord is the subset of the Session schema the human views read.
// JSON mode always prints the API's body unchanged.
type sessionsRecord struct {
	ID            string   `json:"id"`
	Engine        string   `json:"engine"`
	Status        string   `json:"status"`
	StickyKey     string   `json:"sticky_key"`
	CreatedAt     string   `json:"created_at"`
	LastUsedAt    *string  `json:"last_used_at"`
	ExpiresAt     string   `json:"expires_at"`
	HardExpiresAt string   `json:"hard_expires_at"`
	ReleasedAt    string   `json:"released_at"`
	UsageCount    int      `json:"usage_count"`
	Retired       string   `json:"retired"`
	Warnings      []string `json:"warnings"`
	Proxy         struct {
		Tier       string `json:"tier"`
		Country    string `json:"country"`
		RegionPool string `json:"region_pool"`
	} `json:"proxy"`
}

var (
	sessionsEngine    string
	sessionsTTL       int
	sessionsCountry   string
	sessionsPremium   bool
	sessionsRegion    string
	sessionsRotateIP  bool
	sessionsStickyKey string
	sessionsContext   string

	sessionsListLimit  int
	sessionsListEngine string
	sessionsListStatus string
	sessionsListAll    bool

	sessionsForce bool
	sessionsYes   bool
)

var sessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "Persisted browser sessions (cookies and storage reused across scrapes)",
	Long: `Sessions keep one engine, one exit and one cookie jar across many scrapes.
Pass a session's id to "spicrawl scrape --session ID".`,
	Example: `  spicrawl sessions create --engine chromium --ttl 7200 --country de
  spicrawl sessions list --status active
  spicrawl sessions release 01J9ZQ4M7R3T8VX2K5N6P0B1CD`,
}

var sessionsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a session (POST /v1/sessions)",
	Long: `Create a session. Only flags you set are sent; the API applies its defaults
(engine: first of obscura, chromium available; ttl 1800 s; exit pinned to the
session id).

--country needs the residential tier, so it implies --premium-proxy.
--rotate-ip keeps the cookie jar but lets the exit IP change; it cannot be
combined with --sticky-key.
--session-context seeds the session from a JSON file ("-" reads stdin): a
context object {"cookies": [...], "local_storage": {...}, "session_storage":
{...}, "indexed_db": {...}}, or the output of "spicrawl sessions context --json",
whose session_context member is sent.`,
	Example: `  # Pinned residential exit in Germany for two hours
  spicrawl sessions create --engine chromium --ttl 7200 --country de

  # A crawl that keeps cookies but rotates IPs
  spicrawl sessions create --engine obscura --rotate-ip

  # Import a saved login (cookies, local/session storage). Takes either a bare
  # context object or what "spicrawl sessions context ID --json" printed.
  spicrawl sessions context 01J9ZQ4M7R3T8VX2K5N6P0B1CD --json > login.json
  spicrawl sessions create --engine chromium --session-context login.json

  # Capture the id for later scrapes
  SID=$(spicrawl sessions create --json | jq -r .id)`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		f := cmd.Flags()
		if sessionsRotateIP && sessionsStickyKey != "" {
			return Usagef("--rotate-ip and --sticky-key cannot be combined: one lets the exit change, the other pins it")
		}
		body := map[string]any{}
		if f.Changed("engine") {
			e := strings.ToLower(sessionsEngine)
			switch e {
			case "fetch", "obscura", "chromium", "camoufox":
			default:
				return Usagef("--engine must be fetch, obscura, chromium or camoufox, not %q", sessionsEngine)
			}
			body["engine"] = e
		}
		if f.Changed("ttl") {
			if sessionsTTL < 30 {
				return Usagef("--ttl must be at least 30 seconds")
			}
			body["ttl_seconds"] = sessionsTTL
		}
		if f.Changed("country") {
			if len(sessionsCountry) != 2 {
				return Usagef("--country must be a two-letter ISO 3166-1 code such as de")
			}
			body["proxy_country"] = strings.ToLower(sessionsCountry)
			body["premium_proxy"] = true
		}
		if sessionsPremium {
			body["premium_proxy"] = true
		}
		if f.Changed("region-pool") {
			body["region_pool"] = sessionsRegion
		}
		if sessionsRotateIP {
			body["rotate_ip"] = true
		}
		if f.Changed("sticky-key") {
			body["sticky_key"] = sessionsStickyKey
		}
		if f.Changed("session-context") {
			ctx, err := readSessionContext(sessionsContext)
			if err != nil {
				return err
			}
			body["session_context"] = ctx
		}

		c, err := Client()
		if err != nil {
			return err
		}
		resp, err := c.Do(cmd.Context(), api.Request{Method: http.MethodPost, Path: "/v1/sessions", Body: body})
		if err != nil {
			return err
		}
		return sessionsPrintOne(resp.Body, true)
	},
}

var sessionsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the project's sessions, newest first (GET /v1/sessions)",
	Long: `List sessions. --status is filtered by the API after the page is read, so a
page can be short or empty and still have more behind it; --all keeps paging
until the API says there are no more.`,
	Example: `  spicrawl sessions list
  spicrawl sessions list --status active --engine chromium --all
  spicrawl sessions list --json | jq -r '.sessions[].id'`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		f := cmd.Flags()
		q := url.Values{}
		if f.Changed("limit") {
			if sessionsListLimit < 1 || sessionsListLimit > 200 {
				return Usagef("--limit must be between 1 and 200")
			}
			q.Set("limit", strconv.Itoa(sessionsListLimit))
		}
		if sessionsListEngine != "" {
			q.Set("engine", strings.ToLower(sessionsListEngine))
		}
		if sessionsListStatus != "" {
			s := strings.ToLower(sessionsListStatus)
			if s != "active" && s != "released" && s != "expired" {
				return Usagef("--status must be active, released or expired, not %q", sessionsListStatus)
			}
			q.Set("status", s)
		}
		c, err := Client()
		if err != nil {
			return err
		}
		var all []json.RawMessage
		var next string
		for {
			resp, err := c.Do(cmd.Context(), api.Request{Path: "/v1/sessions", Query: q})
			if err != nil {
				return err
			}
			var page struct {
				Sessions   []json.RawMessage `json:"sessions"`
				NextCursor string            `json:"next_cursor"`
			}
			if err := resp.Decode(&page); err != nil {
				return err
			}
			all = append(all, page.Sessions...)
			next = page.NextCursor
			if !sessionsListAll || next == "" {
				break
			}
			q.Set("cursor", next)
		}
		if all == nil {
			all = []json.RawMessage{}
		}
		out := map[string]any{"sessions": all}
		if next != "" {
			out["next_cursor"] = next
		}
		p := Printer()
		return p.Result(out, func(w io.Writer) {
			if len(all) == 0 {
				p.Info("no sessions")
				return
			}
			rows := make([][]string, 0, len(all))
			for _, raw := range all {
				var s sessionsRecord
				_ = json.Unmarshal(raw, &s)
				exit := s.Proxy.Tier
				if s.Proxy.Country != "" {
					exit += "/" + s.Proxy.Country
				}
				rows = append(rows, []string{s.ID, s.Engine, s.Status, exit, strconv.Itoa(s.UsageCount), s.ExpiresAt})
			}
			p.Table([]string{"ID", "ENGINE", "STATUS", "EXIT", "USES", "EXPIRES"}, rows)
			if next != "" {
				p.Info("more sessions exist; pass --all to fetch every page")
			}
		})
	},
}

var sessionsGetCmd = &cobra.Command{
	Use:     "get <id>",
	Short:   "Show one session's metadata (GET /v1/sessions/{id})",
	Example: `  spicrawl sessions get 01J9ZQ4M7R3T8VX2K5N6P0B1CD`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := Client()
		if err != nil {
			return err
		}
		resp, err := c.Do(cmd.Context(), api.Request{Path: "/v1/sessions/" + url.PathEscape(args[0])})
		if err != nil {
			return err
		}
		return sessionsPrintOne(resp.Body, false)
	},
}

var sessionsContextCmd = &cobra.Command{
	Use:   "context <id>",
	Short: "Dump a session's cookies and storage (GET /v1/sessions/{id}/context)",
	Long: `Print the session's live cookies and storage. The output is a credential:
it can replay a login. Pass it back as session_context when creating a new
session to carry a login over.`,
	Example: `  spicrawl sessions context 01J9ZQ4M7R3T8VX2K5N6P0B1CD > ctx.json`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := Client()
		if err != nil {
			return err
		}
		resp, err := c.Do(cmd.Context(), api.Request{Path: "/v1/sessions/" + url.PathEscape(args[0]) + "/context"})
		if err != nil {
			return err
		}
		p := Printer()
		if !p.JSON {
			p.Warn("this output contains live cookies; treat it like a password")
		}
		return p.RawJSON(resp.Body)
	},
}

var sessionsReleaseCmd = &cobra.Command{
	Use:   "release <id>",
	Short: "End a session and purge its context (POST /v1/sessions/{id}/release)",
	Long: `End a session. Its cookies and storage are purged and it cannot be revived;
the metadata stays readable. --force ends it even while a render holds its
lease (that render fails).`,
	Example: `  spicrawl sessions release 01J9ZQ4M7R3T8VX2K5N6P0B1CD
  spicrawl sessions release 01J9ZQ4M7R3T8VX2K5N6P0B1CD --force`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := Client()
		if err != nil {
			return err
		}
		var q url.Values
		if sessionsForce {
			q = url.Values{"force": {"true"}}
		}
		resp, err := c.Do(cmd.Context(), api.Request{
			Method: http.MethodPost,
			Path:   "/v1/sessions/" + url.PathEscape(args[0]) + "/release",
			Query:  q,
		})
		if err != nil {
			return err
		}
		return sessionsPrintOne(resp.Body, false)
	},
}

var sessionsDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a session and its context (DELETE /v1/sessions/{id})",
	Long: `Delete a session and everything stored with it. Asks for confirmation on a
terminal; without a terminal you must pass --yes.`,
	Example: `  spicrawl sessions delete 01J9ZQ4M7R3T8VX2K5N6P0B1CD --yes
  spicrawl sessions delete 01J9ZQ4M7R3T8VX2K5N6P0B1CD --yes --force`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		if !sessionsYes {
			if !output.IsStdinTerminal() {
				return Usagef("refusing to delete session %s without confirmation: pass --yes", id)
			}
			fmt.Fprintf(stderr, "Delete session %s and its cookies and storage? [y/N] ", id)
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
				return Usagef("aborted")
			}
		}
		c, err := Client()
		if err != nil {
			return err
		}
		var q url.Values
		if sessionsForce {
			q = url.Values{"force": {"true"}}
		}
		if _, err := c.Do(cmd.Context(), api.Request{
			Method: http.MethodDelete,
			Path:   "/v1/sessions/" + url.PathEscape(id),
			Query:  q,
		}); err != nil {
			return err
		}
		p := Printer()
		return p.Result(map[string]any{"id": id, "deleted": true}, func(w io.Writer) {
			fmt.Fprintf(w, "deleted session %s\n", id)
		})
	},
}

// readSessionContext loads the --session-context file. It accepts the bare
// context object POST /v1/sessions takes as `session_context`, or the whole
// GET /v1/sessions/{id}/context response, whose `session_context` member is the
// same object — so a dump can be fed straight back to clone a session. The
// object is otherwise passed through untouched: the API owns its validation.
func readSessionContext(path string) (json.RawMessage, error) {
	var (
		raw []byte
		err error
	)
	if path == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, Usagef("--session-context: %v", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, Usagef("--session-context %s is not a JSON object: %v", path, err)
	}
	if inner, ok := obj["session_context"]; ok {
		var check map[string]json.RawMessage
		if err := json.Unmarshal(inner, &check); err != nil {
			return nil, Usagef("--session-context %s: session_context is not a JSON object", path)
		}
		return inner, nil
	}
	return json.RawMessage(raw), nil
}

// sessionsPrintOne prints a Session body: unchanged in JSON mode, as
// key/value lines otherwise. Creation warnings always go to stderr.
func sessionsPrintOne(body []byte, created bool) error {
	p := Printer()
	var s sessionsRecord
	if err := json.Unmarshal(body, &s); err != nil {
		return fmt.Errorf("decode session: %w", err)
	}
	for _, w := range s.Warnings {
		p.Warn("%s", w)
	}
	if p.JSON {
		return p.RawJSON(body)
	}
	w := p.Out
	kv := func(k, v string) {
		if v != "" {
			fmt.Fprintf(w, "%-16s %s\n", k+":", v)
		}
	}
	kv("id", s.ID)
	kv("status", s.Status)
	kv("engine", s.Engine)
	exit := s.Proxy.Tier
	if s.Proxy.Country != "" {
		exit += " (" + s.Proxy.Country + ")"
	}
	if s.Proxy.RegionPool != "" {
		exit += " pool " + s.Proxy.RegionPool
	}
	kv("exit", exit)
	sticky := s.StickyKey
	if sticky == "" {
		sticky = "(rotating)"
	}
	kv("sticky key", sticky)
	kv("uses", strconv.Itoa(s.UsageCount))
	kv("created", s.CreatedAt)
	last := "never"
	if s.LastUsedAt != nil {
		last = *s.LastUsedAt
	}
	kv("last used", last)
	kv("expires", s.ExpiresAt)
	kv("hard expiry", s.HardExpiresAt)
	kv("released", s.ReleasedAt)
	kv("retired", s.Retired)
	if created {
		p.Info("use it with: spicrawl scrape <url> --session %s", s.ID)
	}
	return nil
}

func init() {
	cf := sessionsCreateCmd.Flags()
	cf.StringVar(&sessionsEngine, "engine", "", "engine to pin: fetch|obscura|chromium|camoufox")
	cf.IntVar(&sessionsTTL, "ttl", 0, "sliding lifetime in seconds (ttl_seconds, min 30, default 1800)")
	cf.StringVar(&sessionsCountry, "country", "", "exit country, ISO 3166-1 alpha-2 (implies --premium-proxy)")
	cf.BoolVar(&sessionsPremium, "premium-proxy", false, "residential exits instead of datacenter")
	cf.StringVar(&sessionsRegion, "region-pool", "", "regional exit pool to draw from")
	cf.BoolVar(&sessionsRotateIP, "rotate-ip", false, "keep cookies but let the exit IP change between requests")
	cf.StringVar(&sessionsStickyKey, "sticky-key", "", "pin the exit under this key (letters, digits, hyphens)")
	cf.StringVar(&sessionsContext, "session-context", "", "seed the session from a saved context JSON file (- for stdin)")

	lf := sessionsListCmd.Flags()
	lf.IntVar(&sessionsListLimit, "limit", 50, "page size, 1-200")
	lf.StringVar(&sessionsListEngine, "engine", "", "only sessions pinned to this engine")
	lf.StringVar(&sessionsListStatus, "status", "", "only active|released|expired sessions")
	lf.BoolVar(&sessionsListAll, "all", false, "follow next_cursor until every page is read")

	sessionsReleaseCmd.Flags().BoolVar(&sessionsForce, "force", false, "end it even while a render holds its lease")
	sessionsDeleteCmd.Flags().BoolVar(&sessionsForce, "force", false, "delete it even while a render holds its lease")
	sessionsDeleteCmd.Flags().BoolVarP(&sessionsYes, "yes", "y", false, "do not ask for confirmation")

	sessionsCmd.AddCommand(sessionsCreateCmd, sessionsListCmd, sessionsGetCmd, sessionsContextCmd, sessionsReleaseCmd, sessionsDeleteCmd)
	rootCmd.AddCommand(sessionsCmd)
}
