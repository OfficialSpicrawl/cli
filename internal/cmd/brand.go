package cmd

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/OfficialSpicrawl/cli/internal/ui"
)

var (
	brandInclude    []string
	brandScreenshot bool
)

var brandCmd = &cobra.Command{
	Use:   "brand <url>",
	Short: "A site's brand: name, tagline, logos, colours, fonts and socials (POST /v1/brand)",
	Long: `Read a site's brand from one rendered page load. Nothing is downloaded: logos, icons,
images and font files come back as URLs on the site, and video files as platform links.

--include limits the sections to identity, logos, icons, colors, fonts, images,
videos and socials (repeat the flag or separate the names with commas). The default
is all of them. --screenshot also returns a screenshot of the rendered page; it
renders in the full browser and costs more credits.

On a terminal the summary shows the name, tagline, top logo, primary colours, fonts
and socials. --json prints the API's answer unchanged, with every section and the
warnings.`,
	Example: `  spicrawl brand https://example.com
  spicrawl brand https://example.com --include logos,colors
  spicrawl brand https://example.com --json | jq '.logos[].url'`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		body := map[string]any{"url": args[0]}
		if len(brandInclude) > 0 {
			body["include"] = brandInclude
		}
		if brandScreenshot {
			body["screenshot"] = true
		}
		resp, err := apiCall(cmd, http.MethodPost, "/v1/brand", nil, body)
		if err != nil {
			return err
		}
		p := Printer()
		if p.JSON {
			return p.RawJSON(resp.Body)
		}
		var b brandAnswer
		if err := partnerDecode(resp.Body, &b); err != nil {
			return err
		}
		for _, w := range b.Warnings {
			p.Warn("%s", w)
		}
		text := viewRows(outUI(p), "", brandRows(b))
		if text == "" {
			p.Info("no brand assets found on %s", args[0])
			return nil
		}
		fmt.Fprint(p.Out, text)
		return nil
	},
}

// brandAnswer is the part of a brand answer the human view reads. JSON output
// prints the API's body unchanged.
type brandAnswer struct {
	Identity *struct {
		Name    string `json:"name"`
		Tagline string `json:"tagline"`
	} `json:"identity"`
	Logos []struct {
		URL   string  `json:"url"`
		Score float64 `json:"score"`
	} `json:"logos"`
	Colors []struct {
		Hex  string `json:"hex"`
		Role string `json:"role"`
	} `json:"colors"`
	Fonts []struct {
		Family string `json:"family"`
		Role   string `json:"role"`
	} `json:"fonts"`
	Socials []struct {
		Network string `json:"network"`
		URL     string `json:"url"`
	} `json:"socials"`
	Warnings []string `json:"warnings"`
}

// brandRows is the summary of a brand answer. A section the request left out,
// or the page did not have, is not shown.
func brandRows(b brandAnswer) []ui.Row {
	var rows []ui.Row
	if b.Identity != nil {
		rows = append(rows,
			ui.Row{Key: "name", Val: b.Identity.Name},
			ui.Row{Key: "tagline", Val: b.Identity.Tagline},
		)
	}
	// The logo with the highest score; the first one wins a tie.
	top, best := "", -1.0
	for _, l := range b.Logos {
		if l.Score > best {
			top, best = l.URL, l.Score
		}
	}
	rows = append(rows, ui.Row{Key: "top logo", Val: top})

	var primary []string
	for _, c := range b.Colors {
		if c.Role == "primary" {
			primary = append(primary, c.Hex)
		}
	}
	rows = append(rows, ui.Row{Key: "primary colors", Val: strings.Join(primary, ", ")})

	var fonts []string
	for _, f := range b.Fonts {
		fonts = append(fonts, fmt.Sprintf("%s (%s)", f.Family, f.Role))
	}
	rows = append(rows, ui.Row{Key: "fonts", Val: strings.Join(fonts, ", ")})

	for i, s := range b.Socials {
		key := ""
		if i == 0 {
			key = "socials"
		}
		rows = append(rows, ui.Row{Key: key, Val: s.Network + " " + s.URL})
	}
	return rows
}

func init() {
	f := brandCmd.Flags()
	f.StringSliceVar(&brandInclude, "include", nil, "sections to return: identity, logos, icons, colors, fonts, images, videos, socials (default: all)")
	f.BoolVar(&brandScreenshot, "screenshot", false, "also return a screenshot of the rendered page (full browser, more credits)")

	rootCmd.AddCommand(brandCmd)
}
