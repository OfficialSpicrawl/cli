package cmd

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/OfficialSpicrawl/cli/internal/config"
)

// configCmdKeys are the settable keys, in display order.
var configCmdKeys = []string{"api_key", "base_url"}

var configCmdShowSecret bool

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Read and write the config file (api_key, base_url)",
	Long: `Read and write the config file. Keys: api_key, base_url.

Values here are the lowest-precedence source: --api-key/--base-url flags and
$SPICRAWL_API_KEY/$SPICRAWL_BASE_URL override them ("spicrawl auth status" shows
what is actually in use). The file path is $SPICRAWL_CONFIG, else
the OS config dir (~/.config/spicrawl/config.json on Linux, ~/Library/Application Support/spicrawl/config.json on macOS); it is written with mode 0600. "spicrawl config path" prints it.`,
	Args: cobra.NoArgs,
	Run:  func(cmd *cobra.Command, _ []string) { _ = cmd.Help() },
}

var configCmdGet = &cobra.Command{
	Use:   "get [key]",
	Short: "Print one saved value, or all of them",
	Long: `Print a value saved in the config file (not the flag/env override).
api_key is masked unless --show-secret is given. An unset key prints an empty
value (null in JSON).`,
	Example: `  spicrawl config get
  spicrawl config get base_url
  spicrawl config get api_key --show-secret`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		f, err := config.Load()
		if err != nil {
			return err
		}
		p := Printer()
		if len(args) == 1 {
			key := args[0]
			if err := configCmdCheckKey(key); err != nil {
				return err
			}
			v := configCmdValue(f, key)
			var jv any
			if v != "" {
				jv = v
			}
			return p.Result(map[string]any{"key": key, "value": jv}, func(w io.Writer) {
				fmt.Fprintln(w, v)
			})
		}
		all := map[string]any{}
		var rows [][]string
		for _, k := range configCmdKeys {
			v := configCmdValue(f, k)
			if v == "" {
				all[k] = nil
			} else {
				all[k] = v
			}
			rows = append(rows, []string{k, v})
		}
		return p.Result(all, func(io.Writer) { p.Table([]string{"KEY", "VALUE"}, rows) })
	},
}

var configCmdSet = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Save a value",
	Long: `Save a value to the config file. Unlike "spicrawl login", set api_key does not
check the key against the API.`,
	Example: `  spicrawl config set base_url http://localhost:8080
  spicrawl config set api_key spicrawl_live_...`,
	Args: cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		key, val := args[0], strings.TrimSpace(args[1])
		if err := configCmdCheckKey(key); err != nil {
			return err
		}
		if val == "" {
			return Usagef("empty value: use \"spicrawl config unset %s\" to remove it", key)
		}
		if key == "base_url" {
			u, err := url.Parse(val)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return Usagef("base_url must be an http(s) URL, got %q", val)
			}
			val = strings.TrimRight(val, "/")
		}
		return configCmdWrite(key, val)
	},
}

var configCmdUnset = &cobra.Command{
	Use:     "unset <key>",
	Short:   "Remove a value",
	Example: `  spicrawl config unset base_url`,
	Args:    cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		if err := configCmdCheckKey(args[0]); err != nil {
			return err
		}
		return configCmdWrite(args[0], "")
	},
}

var configCmdPath = &cobra.Command{
	Use:     "path",
	Short:   "Print the config file path",
	Example: `  spicrawl config path`,
	Args:    cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		path, err := config.Path()
		if err != nil {
			return err
		}
		return Printer().Result(map[string]any{"path": path}, func(w io.Writer) {
			fmt.Fprintln(w, path)
		})
	},
}

func init() {
	configCmdGet.Flags().BoolVar(&configCmdShowSecret, "show-secret", false, "print api_key unmasked")
	configCmd.AddCommand(configCmdGet, configCmdSet, configCmdUnset, configCmdPath)
	rootCmd.AddCommand(configCmd)
}

func configCmdCheckKey(key string) error {
	for _, k := range configCmdKeys {
		if k == key {
			return nil
		}
	}
	return Usagef("unknown config key %q (keys: %s)", key, strings.Join(configCmdKeys, ", "))
}

func configCmdValue(f config.File, key string) string {
	switch key {
	case "api_key":
		if f.APIKey != "" && !configCmdShowSecret {
			return config.MaskKey(f.APIKey)
		}
		return f.APIKey
	case "base_url":
		return f.BaseURL
	}
	return ""
}

// configCmdWrite sets key to val ("" removes it) and reports the change.
func configCmdWrite(key, val string) error {
	f, err := config.Load()
	if err != nil {
		return err
	}
	switch key {
	case "api_key":
		f.APIKey = val
	case "base_url":
		f.BaseURL = val
	}
	if err := config.Save(f); err != nil {
		return err
	}
	path, _ := config.Path()
	shown := val
	if key == "api_key" && val != "" {
		shown = config.MaskKey(val)
	}
	var jv any
	if shown != "" {
		jv = shown
	}
	return Printer().Result(map[string]any{"path": path, "key": key, "value": jv}, func(w io.Writer) {
		if val == "" {
			fmt.Fprintf(w, "Removed %s from %s\n", key, path)
		} else {
			fmt.Fprintf(w, "Set %s = %s in %s\n", key, shown, path)
		}
	})
}
