package cmd

import (
	"fmt"
	"io"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/Spicrawl/cli/internal/api"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the CLI version, Go version and platform",
	Example: `  spicrawl version
  spicrawl version --json`,
	Args: cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		v := map[string]any{
			"version": api.Version,
			"go":      runtime.Version(),
			"os":      runtime.GOOS,
			"arch":    runtime.GOARCH,
		}
		return Printer().Result(v, func(w io.Writer) {
			fmt.Fprintf(w, "spicrawl %s (%s, %s/%s)\n", api.Version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		})
	},
}

func init() { rootCmd.AddCommand(versionCmd) }
