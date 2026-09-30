// Package config resolves the API key and base URL.
//
// Precedence, highest first: command-line flag, environment variable, config
// file. The file lives at $XDG_CONFIG_HOME/spicrawl/config.json (or
// ~/.config/spicrawl/config.json; ~/Library/Application Support/spicrawl on macOS)
// and is written with mode 0600.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	EnvAPIKey   = "SPICRAWL_API_KEY"
	EnvBaseURL  = "SPICRAWL_BASE_URL"
	EnvConfig   = "SPICRAWL_CONFIG"
	DefaultBase = "https://api.spicrawl.com"
)

// File is the on-disk shape. Unknown keys are preserved by callers that
// read-modify-write through Load and Save.
type File struct {
	APIKey  string `json:"api_key,omitempty"`
	BaseURL string `json:"base_url,omitempty"`
}

// Resolved is what a command actually uses, with where each value came from
// so `spicrawl auth status` can explain itself.
type Resolved struct {
	APIKey        string
	APIKeySource  string // "flag" | "env" | "file" | ""
	BaseURL       string
	BaseURLSource string // "flag" | "env" | "file" | "default"
	Path          string
}

// Path returns the config file location.
func Path() (string, error) {
	if p := os.Getenv(EnvConfig); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate config dir: %w", err)
	}
	return filepath.Join(dir, "spicrawl", "config.json"), nil
}

// FileError means the config file exists but could not be read or parsed.
// It is the user's to fix (the CLI exits with the usage code), and the
// message names the file.
type FileError struct {
	Path string
	Err  error
}

func (e *FileError) Error() string {
	return fmt.Sprintf("config file %s: %v (fix or delete it, or point %s elsewhere)", e.Path, e.Err, EnvConfig)
}
func (e *FileError) Unwrap() error { return e.Err }

// Load reads the config file. A missing file is an empty config, not an
// error; an unreadable or malformed one is a *FileError.
func Load() (File, error) {
	var f File
	p, err := Path()
	if err != nil {
		return f, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, &FileError{Path: p, Err: fmt.Errorf("cannot read: %w", err)}
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, &FileError{Path: p, Err: fmt.Errorf("not valid JSON: %w", err)}
	}
	return f, nil
}

// Save writes the config file atomically with owner-only permissions.
func Save(f File) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(p), err)
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	return os.Rename(tmp, p)
}

// Resolve applies flag > env > file > default precedence.
func Resolve(flagKey, flagBase string) (Resolved, error) {
	r := Resolved{}
	p, _ := Path()
	r.Path = p
	f, err := Load()
	if err != nil {
		return r, err
	}
	switch {
	case flagKey != "":
		r.APIKey, r.APIKeySource = flagKey, "flag"
	case os.Getenv(EnvAPIKey) != "":
		r.APIKey, r.APIKeySource = os.Getenv(EnvAPIKey), "env"
	case f.APIKey != "":
		r.APIKey, r.APIKeySource = f.APIKey, "file"
	}
	switch {
	case flagBase != "":
		r.BaseURL, r.BaseURLSource = flagBase, "flag"
	case os.Getenv(EnvBaseURL) != "":
		r.BaseURL, r.BaseURLSource = os.Getenv(EnvBaseURL), "env"
	case f.BaseURL != "":
		r.BaseURL, r.BaseURLSource = f.BaseURL, "file"
	default:
		r.BaseURL, r.BaseURLSource = DefaultBase, "default"
	}
	r.BaseURL = strings.TrimRight(r.BaseURL, "/")
	return r, nil
}

// MaskKey shows enough of a key to recognise it and no more: the key's
// prefix plus four secret characters. The width is derived from the prefix
// rather than hard-coded, so a prefix rename cannot silently shift how much
// secret is shown.
func MaskKey(k string) string {
	keep := 16
	if n := keyPrefixLen(k); n > 0 {
		keep = n + 4
	}
	if len(k) <= keep {
		return strings.Repeat("*", len(k))
	}
	return k[:keep] + "…" + k[len(k)-4:]
}

// keyPrefixLen returns the length of a recognised key prefix, or 0 when the
// key has no known shape (which keeps the historical 16-character width).
func keyPrefixLen(k string) int {
	for _, p := range []string{"spicrawl_live_", "spicrawl_test_", "webora_live_", "webora_test_"} {
		if strings.HasPrefix(k, p) {
			return len(p)
		}
	}
	return 0
}
