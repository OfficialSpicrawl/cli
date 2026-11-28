package cmd

import (
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/OfficialSpicrawl/cli/internal/api"
)

var (
	feedbackUp      bool
	feedbackDown    bool
	feedbackKind    string
	feedbackImpact  string
	feedbackIntent  string
	feedbackComment string
)

var feedbackKinds = []string{"broken", "wrong_result", "missing", "confusing", "other"}
var feedbackImpacts = []string{"blocked", "worked_around", "friction"}

// Limits the API enforces; checked here so a long value fails before it is sent.
const (
	feedbackIntentMax  = 300
	feedbackCommentMax = 4000
	feedbackAgentMax   = 200
)

var feedbackCmd = &cobra.Command{
	Use:   "feedback <request-id>",
	Short: "Report how a request went: thumbs up or down, and what went wrong (POST /v1/feedback)",
	Long: `Tell Spicrawl how a request went. A thumbs-down on a request that was charged
refunds its credits (up to the refund cap for the period); the response says how
many were refunded. Reporting the same request again is harmless: the answer says
"Already reported."

--down is the default. --kind says what kind of problem it was, --impact how much
it cost you, --intent what you were trying to get (up to 300 characters), and
-m/--comment adds detail (up to 4000 characters). The agent field names this CLI
and, when SPICRAWL_AGENT is set, that value.

Report each problem once per task, not once per request. When a failed request can
be reported, the error prints the exact command to run.`,
	Example: `  spicrawl feedback 0190f3a2-7c1e-7d4b-9a2f-3b8e5c6d7e80 --kind broken --intent "the price table"
  spicrawl feedback 0190f3a2-7c1e-7d4b-9a2f-3b8e5c6d7e80 --up
  spicrawl feedback 0190f3a2-7c1e-7d4b-9a2f-3b8e5c6d7e80 --kind wrong_result --impact worked_around -m "returned the nav menu" --json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if feedbackUp && feedbackDown {
			return Usagef("use --up or --down, not both")
		}
		if feedbackKind != "" && !slices.Contains(feedbackKinds, feedbackKind) {
			return Usagef("--kind must be one of %s", strings.Join(feedbackKinds, ", "))
		}
		if feedbackImpact != "" && !slices.Contains(feedbackImpacts, feedbackImpact) {
			return Usagef("--impact must be one of %s", strings.Join(feedbackImpacts, ", "))
		}
		if n := utf8.RuneCountInString(feedbackIntent); n > feedbackIntentMax {
			return Usagef("--intent is %d characters; the limit is %d", n, feedbackIntentMax)
		}
		if n := utf8.RuneCountInString(feedbackComment); n > feedbackCommentMax {
			return Usagef("--comment is %d characters; the limit is %d", n, feedbackCommentMax)
		}

		rating := "down"
		if feedbackUp {
			rating = "up"
		}
		body := map[string]any{
			"request_id": args[0],
			"rating":     rating,
			"agent":      feedbackAgent(),
		}
		if feedbackKind != "" {
			body["kind"] = feedbackKind
		}
		if feedbackImpact != "" {
			body["impact"] = feedbackImpact
		}
		if feedbackIntent != "" {
			body["intent"] = feedbackIntent
		}
		if feedbackComment != "" {
			body["comment"] = feedbackComment
		}

		resp, err := apiCall(cmd, http.MethodPost, "/v1/feedback", nil, body)
		if err != nil {
			return err
		}
		p := Printer()
		if p.JSON {
			return p.RawJSON(resp.Body)
		}
		var a struct {
			FeedbackID       string `json:"feedback_id"`
			AlreadySubmitted bool   `json:"already_submitted"`
			CreditsRefunded  int    `json:"credits_refunded"`
			RefundCapReached bool   `json:"refund_cap_reached"`
		}
		if err := partnerDecode(resp.Body, &a); err != nil {
			return err
		}
		if a.AlreadySubmitted {
			fmt.Fprintln(p.Out, "Already reported.")
			return nil
		}
		fmt.Fprintf(p.Out, "Reported (feedback %s). Refunded %d credits.\n", a.FeedbackID, a.CreditsRefunded)
		if a.RefundCapReached {
			fmt.Fprintln(p.Out, "The refund cap for this period is reached.")
		}
		return nil
	},
}

// feedbackAgent identifies this CLI, and the agent running it when SPICRAWL_AGENT
// says so. The value is cut to the API's limit rather than refused.
func feedbackAgent() string {
	agent := "spicrawl-cli/" + api.Version
	if v := strings.TrimSpace(os.Getenv("SPICRAWL_AGENT")); v != "" {
		agent += " " + v
	}
	if utf8.RuneCountInString(agent) > feedbackAgentMax {
		agent = string([]rune(agent)[:feedbackAgentMax])
	}
	return agent
}

func init() {
	f := feedbackCmd.Flags()
	f.BoolVar(&feedbackUp, "up", false, "the request went well")
	f.BoolVar(&feedbackDown, "down", false, "the request did not go well (the default)")
	f.StringVar(&feedbackKind, "kind", "", "what kind of problem: broken, wrong_result, missing, confusing or other")
	f.StringVar(&feedbackImpact, "impact", "", "how much it cost: blocked, worked_around or friction")
	f.StringVar(&feedbackIntent, "intent", "", "what you were trying to get (up to 300 characters)")
	f.StringVarP(&feedbackComment, "comment", "m", "", "more detail (up to 4000 characters)")

	rootCmd.AddCommand(feedbackCmd)
}
