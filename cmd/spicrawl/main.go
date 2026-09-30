// Command spicrawl (built from this directory) is the Spicrawl command-line
// client for the Spicrawl web data API.
//
// It is built for two readers at once: a person at a terminal and an AI agent
// driving a shell. See DESIGN.md for the contract agents rely on (JSON when
// stdout is not a terminal, data on stdout only, stable exit codes).
package main

import (
	"os"

	"github.com/Spicrawl/cli/internal/cmd"
)

func main() {
	os.Exit(cmd.Execute())
}
