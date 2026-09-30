package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Spicrawl/cli/internal/api"
	"github.com/Spicrawl/cli/internal/config"
	"github.com/Spicrawl/cli/internal/exitcode"
	"github.com/Spicrawl/cli/internal/output"
)

// authKeysURL is where a person creates an API key.
const authKeysURL = "https://app.spicrawl.com/dashboard/keys"

// Swapped out by tests.
var (
	authStdin   io.Reader = os.Stdin
	authIsTTY             = output.IsStdinTerminal
	authPromptW io.Writer // nil means stderr
)

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Save an API key to the config file (validates it first)",
	Long: `Save an API key to the config file so later commands can use it.

The key comes from --api-key, then $SPICRAWL_API_KEY. With neither, and only when
stdin is a terminal, login asks for it (create one at ` + authKeysURL + `).
Without a terminal and without a key it fails with exit code 2: it never waits
for input an agent cannot give.

The key is checked against the API before it is saved (exit 3 if the API
rejects it, 10 if the API cannot be reached). --base-url, when given, is saved
too. The file is written with mode 0600; see "spicrawl config path".`,
	Example: `  spicrawl login --api-key spicrawl_live_...
  SPICRAWL_API_KEY=spicrawl_live_... spicrawl login
  spicrawl login --api-key spicrawl_live_... --base-url http://localhost:8080
  spicrawl login                      # interactive: paste the key`,
	Args: cobra.NoArgs,
	RunE: authRunLogin,
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove the saved API key (keeps base_url)",
	Long: `Remove api_key from the config file. A saved base_url is kept.

Keys supplied by $SPICRAWL_API_KEY or --api-key are not affected; logout warns
when one is still set.`,
	Example: `  spicrawl logout`,
	Args:    cobra.NoArgs,
	RunE:    authRunLogout,
}

var authStatusOffline bool

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Inspect authentication",
	Args:  cobra.NoArgs,
	Run:   func(cmd *cobra.Command, _ []string) { _ = cmd.Help() },
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show which key and base URL are in use, where they come from, and whether the key works",
	Long: `Show the API key (masked) and base URL this CLI would use, where each came
from (flag, env, file, default), the config file path, and whether the key is
accepted by the API (one GET /v1/requests?limit=1; --offline skips it).

Exit code: 0 when a key is configured and works (or is configured, with
--offline); 3 when no key is configured or the API rejects it; 10 when the API
cannot be reached. The status is printed to stdout in every case.`,
	Example: `  spicrawl auth status
  spicrawl auth status --offline
  spicrawl auth status --json | jq .key_valid`,
	Args: cobra.NoArgs,
	RunE: authRunStatus,
}

func init() {
	authStatusCmd.Flags().BoolVar(&authStatusOffline, "offline", false, "do not call the API to check the key")
	authCmd.AddCommand(authStatusCmd)
	rootCmd.AddCommand(authLoginCmd, authLogoutCmd, authCmd)
}

func authRunLogin(cmd *cobra.Command, _ []string) error {
	p := Printer()
	key := strings.TrimSpace(flagAPIKey)
	if key == "" {
		key = strings.TrimSpace(os.Getenv(config.EnvAPIKey))
	}
	if key == "" {
		// TODO(device-flow): when the API grows a device-code endpoint, start
		// it here for TTY users (open the browser, poll for the key) and keep
		// the paste prompt as the fallback.
		if !authIsTTY() {
			return Usagef("no API key given and stdin is not a terminal: pass --api-key or set %s (create a key at %s)", config.EnvAPIKey, authKeysURL)
		}
		k, err := authPromptKey()
		if err != nil {
			return err
		}
		key = k
	}
	if key == "" {
		return Usagef("empty API key")
	}

	r, err := config.Resolve(key, flagBaseURL)
	if err != nil {
		return err
	}
	if err := authCheckKey(rootCmd.Context(), r.BaseURL, key); err != nil {
		return err
	}

	f, err := config.Load()
	if err != nil {
		return err
	}
	f.APIKey = key
	if flagBaseURL != "" {
		f.BaseURL = strings.TrimRight(flagBaseURL, "/")
	}
	if err := config.Save(f); err != nil {
		return err
	}
	res := map[string]any{"saved": r.Path, "key": config.MaskKey(key), "base_url": r.BaseURL}
	return p.Result(res, func(w io.Writer) {
		fmt.Fprintf(w, "Logged in. Key %s saved to %s\n", config.MaskKey(key), r.Path)
		fmt.Fprintf(w, "Base URL: %s\n", r.BaseURL)
	})
}

// authPromptKey asks for the key on the terminal. Without golang.org/x/term
// the input cannot be hidden, so the prompt says so.
func authPromptKey() (string, error) {
	w := authPromptW
	if w == nil {
		w = stderr
	}
	fmt.Fprintf(w, "Create an API key at %s\n", authKeysURL)
	fmt.Fprintln(w, "warning: the key will be visible as you type or paste it")
	fmt.Fprint(w, "API key: ")
	line, err := bufio.NewReader(authStdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read key: %w", err)
	}
	return strings.TrimSpace(line), nil
}

// authCheckKey makes one cheap authenticated call. A 401 always maps to
// exit 3, even when a proxy answered without a problem document.
func authCheckKey(ctx context.Context, baseURL, key string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c := api.New(baseURL, key, flagTimeout)
	_, err := c.Do(ctx, api.Request{Method: http.MethodGet, Path: "/v1/requests", Query: url.Values{"limit": {"1"}}})
	if prob, ok := api.AsProblem(err); ok && prob.Status == http.StatusUnauthorized && exitcode.ForCode(prob.Code) != exitcode.Auth {
		prob.Code = "ERR::AUTH::INVALID_KEY"
	}
	return err
}

func authRunLogout(_ *cobra.Command, _ []string) error {
	p := Printer()
	path, err := config.Path()
	if err != nil {
		return err
	}
	f, err := config.Load()
	if err != nil {
		return err
	}
	removed := f.APIKey != ""
	if removed {
		f.APIKey = ""
		if err := config.Save(f); err != nil {
			return err
		}
	}
	if os.Getenv(config.EnvAPIKey) != "" {
		p.Warn("%s is still set in the environment and will keep being used", config.EnvAPIKey)
	}
	return p.Result(map[string]any{"path": path, "removed": removed}, func(w io.Writer) {
		if removed {
			fmt.Fprintf(w, "Removed the API key from %s\n", path)
		} else {
			fmt.Fprintf(w, "No API key saved in %s\n", path)
		}
	})
}

func authRunStatus(cmd *cobra.Command, _ []string) error {
	p := Printer()
	r, err := config.Resolve(flagAPIKey, flagBaseURL)
	if err != nil {
		return err
	}
	res := map[string]any{
		"api_key":         nil,
		"api_key_source":  nil,
		"base_url":        r.BaseURL,
		"base_url_source": r.BaseURLSource,
		"config_path":     r.Path,
		"key_valid":       nil, // null = not checked
	}
	var fail error
	switch {
	case r.APIKey == "":
		fail = ErrNoKey
	case !authStatusOffline:
		err := authCheckKey(rootCmd.Context(), r.BaseURL, r.APIKey)
		if prob, ok := api.AsProblem(err); ok && exitcode.ForCode(prob.Code) == exitcode.Auth {
			res["key_valid"] = false
			res["check_error"] = prob.Error()
			fail = &ExitError{Code: exitcode.Auth, Err: fmt.Errorf("the API rejected the key: %s", prob.Error())}
		} else if err != nil {
			res["check_error"] = err.Error()
			fail = err
		} else {
			res["key_valid"] = true
		}
	}
	if r.APIKey != "" {
		res["api_key"] = config.MaskKey(r.APIKey)
		res["api_key_source"] = r.APIKeySource
	}
	perr := p.Result(res, func(w io.Writer) {
		key := "(none)"
		if r.APIKey != "" {
			key = config.MaskKey(r.APIKey) + "  (from " + r.APIKeySource + ")"
		}
		valid := "not checked"
		switch res["key_valid"] {
		case true:
			valid = "yes"
		case false:
			valid = "no"
		}
		p.Table([]string{"FIELD", "VALUE"}, [][]string{
			{"api key", key},
			{"base url", r.BaseURL + "  (from " + r.BaseURLSource + ")"},
			{"config", r.Path},
			{"key works", valid},
		})
	})
	if perr != nil {
		return perr
	}
	return fail
}
