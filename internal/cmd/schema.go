package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/OfficialSpicrawl/cli/internal/schema"
)

var schemaList bool

// schemaDescriptions says which body each schema describes.
var schemaDescriptions = map[string]string{
	"scrape":     "POST /v1/scrape body (spicrawl scrape --body)",
	"batch":      "POST /v1/batch and POST /v1/batch/{id}/items body",
	"batch-item": "one element of a batch body's items[] (one line of a JSONL batch file)",
	"session":    "POST /v1/sessions body",
}

var schemaCmd = &cobra.Command{
	Use:   "schema [scrape|batch|batch-item|session]",
	Short: "Print the JSON Schema of a request body",
	Long: `Print the JSON Schema (draft 2020-12) of an API request body so a body can be
validated before it is sent. The schema is embedded in the CLI (no network)
and fully resolved: every $ref is inlined, except the recursive extract-rule
definition, which lives under $defs in the same document.

The output is the schema itself in both human and JSON mode. With --list (or
no argument) it lists the available names.`,
	Example: `  spicrawl schema scrape > scrape.schema.json
  spicrawl schema batch-item | jq '.properties | keys'
  spicrawl schema --list`,
	Args:      cobra.MaximumNArgs(1),
	ValidArgs: schema.Names(),
	RunE: func(_ *cobra.Command, args []string) error {
		p := Printer()
		names := schema.Names()
		if schemaList || len(args) == 0 {
			var items []map[string]string
			var rows [][]string
			for _, n := range names {
				items = append(items, map[string]string{"name": n, "description": schemaDescriptions[n]})
				rows = append(rows, []string{n, schemaDescriptions[n]})
			}
			return p.Result(items, func(io.Writer) { p.Table([]string{"NAME", "DESCRIBES"}, rows) })
		}
		b, ok := schema.Get(args[0])
		if !ok {
			return Usagef("unknown schema %q (available: %s)", args[0], strings.Join(names, ", "))
		}
		if p.JSON {
			return p.RawJSON(b)
		}
		_, err := fmt.Fprint(p.Out, string(b))
		return err
	},
}

func init() {
	schemaCmd.Flags().BoolVar(&schemaList, "list", false, "list the available schema names")
	rootCmd.AddCommand(schemaCmd)
}
