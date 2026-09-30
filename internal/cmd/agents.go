package cmd

// Shared registry of the AI agent clients that `spicrawl init`, `spicrawl mcp
// install` and `spicrawl skill install` know how to configure, plus the file
// helpers they share (order-preserving JSON merge, atomic writes).
//
// Config locations, verified against each client's docs (2026-09):
//
//	Claude Code  https://code.claude.com/docs/en/mcp, /en/skills
//	  project  .mcp.json {"mcpServers"}; ${VAR} expanded in url/headers
//	  user     ~/.claude.json {"mcpServers"}; NO env expansion there
//	  skills   .claude/skills/<name>/SKILL.md, ~/.claude/skills/<name>/SKILL.md
//	Cursor       https://cursor.com/docs/context/mcp, /docs/context/skills
//	  mcp      .cursor/mcp.json, ~/.cursor/mcp.json {"mcpServers"}; ${env:VAR} in headers
//	  skills   .cursor/skills, ~/.cursor/skills; also reads .agents/, .claude/, .codex/ skills
//	VS Code      https://code.visualstudio.com/docs/copilot/reference/mcp-configuration
//	  mcp      .vscode/mcp.json, <user config>/Code/User/mcp.json {"servers","inputs"};
//	           headers take ${input:id} (${env:VAR} in headers is broken: vscode#336232)
//	  skills   .github/skills, .claude/skills, .agents/skills; ~/.copilot/skills, ~/.claude/skills, ~/.agents/skills
//	Codex        https://learn.chatgpt.com/docs/extend/mcp, /docs/build-skills
//	  mcp      .codex/config.toml (trusted projects), $CODEX_HOME/config.toml [mcp_servers.<name>]
//	           url + bearer_token_env_var | http_headers
//	  skills   .agents/skills, ~/.agents/skills; AGENTS.md, $CODEX_HOME/AGENTS.md

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/Spicrawl/cli/internal/config"
	"github.com/Spicrawl/cli/internal/output"
)

const (
	agentServerName     = "spicrawl"
	agentSkillDirName   = "spicrawl"
	agentEnvMCPURL      = "SPICRAWL_MCP_URL"
	agentEnvSkillURL    = "SPICRAWL_SKILL_URL"
	agentMCPPath        = "/mcp"      // hosted MCP server, relative to the API origin
	agentSkillFile      = "/skill.md" // the skill, relative to the docs base
	agentVSCodeInputID  = "spicrawl-api-key"
	agentActionCreated  = "created"
	agentActionUpdated  = "updated"
	agentActionUnchange = "unchanged"
	agentActionSkipped  = "skipped"
)

// agentPublicMCPURL is the hosted product's MCP server, the default when the
// base URL is the hosted API (config.DefaultBase). The docs and the skill name
// this host; <API origin>/mcp reaches the same server there.
const agentPublicMCPURL = "https://mcp.spicrawl.com/mcp"

// agentEndpoints are the deployment URLs written into configs and the skill.
// Every URL derives from the resolved API base URL unless overridden, so a
// self-hosted deployment needs no other setting; the hosted API's MCP server
// and docs are the exceptions (agentPublicMCPURL, docsPublicURL).
type agentEndpoints struct {
	API    string // resolved API base URL, no trailing slash
	Origin string // scheme://host[:port] of API
	MCP    string // --mcp-url > $SPICRAWL_MCP_URL > <origin>/mcp (agentPublicMCPURL for the hosted API)
	Docs   string // $SPICRAWL_DOCS_URL > $SPICRAWL_DOCS_HOST/docs > <origin>/docs (docsPublicURL for the hosted API)
	Skill  string // --skill-url > $SPICRAWL_SKILL_URL > <docs>/skill.md
}

// agentResolveEndpoints resolves the base URL like every other command
// (--base-url > $SPICRAWL_BASE_URL > config file > default) and derives the MCP,
// docs and skill URLs from it.
func agentResolveEndpoints(mcpFlag, skillFlag string) (agentEndpoints, error) {
	r, err := config.Resolve(flagAPIKey, flagBaseURL)
	if err != nil {
		return agentEndpoints{}, err
	}
	origin, err := agentOrigin(r.BaseURL)
	if err != nil {
		return agentEndpoints{}, Usagef("invalid base URL %q (from %s): %v", r.BaseURL, r.BaseURLSource, err)
	}
	ep := agentEndpoints{API: strings.TrimRight(r.BaseURL, "/"), Origin: origin}
	mcpDerived := origin + agentMCPPath
	if origin == config.DefaultBase {
		mcpDerived = agentPublicMCPURL
	}
	ep.MCP, err = agentPickURL("--mcp-url", mcpFlag, agentEnvMCPURL, mcpDerived)
	if err != nil {
		return agentEndpoints{}, err
	}
	if ep.Docs, err = docsOverride(); err != nil {
		return agentEndpoints{}, err
	}
	if ep.Docs == "" {
		ep.Docs = docsDerived(origin)
	}
	ep.Skill, err = agentPickURL("--skill-url", skillFlag, agentEnvSkillURL, ep.Docs+agentSkillFile)
	if err != nil {
		return agentEndpoints{}, err
	}
	return ep, nil
}

// agentPickURL applies flag > env > derived, validating an explicit value.
func agentPickURL(flagName, flagVal, env, derived string) (string, error) {
	v, from := strings.TrimSpace(flagVal), flagName
	if v == "" {
		v, from = strings.TrimSpace(os.Getenv(env)), "$"+env
	}
	if v == "" {
		return derived, nil
	}
	if _, err := agentOrigin(v); err != nil {
		return "", Usagef("invalid %s %q: %v", from, v, err)
	}
	return v, nil
}

// agentOrigin returns scheme://host[:port] of an absolute http(s) URL.
func agentOrigin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("want an absolute http:// or https:// URL")
	}
	return u.Scheme + "://" + u.Host, nil
}

// agentScope says where files go: the project directory, or the user's home.
type agentScope struct {
	Global bool
	Dir    string // project directory (absolute)
	Home   string // user home directory
}

func (s agentScope) base() string {
	if s.Global {
		return s.Home
	}
	return s.Dir
}

// agentMCPFormat is how a client's MCP config file is shaped.
type agentMCPFormat int

const (
	agentMCPServersJSON agentMCPFormat = iota // {"mcpServers": {...}}
	agentVSCodeJSON                           // {"servers": {...}, "inputs": [...]}
	agentCodexTOML                            // [mcp_servers.spicrawl]
)

// agentClient describes one supported client.
type agentClient struct {
	ID   string
	Name string
	// Markers that show the client is in use, relative to the project dir
	// and to HOME respectively.
	ProjectMarkers []string
	HomeMarkers    func(s agentScope) []string
	MCPFormat      agentMCPFormat
	MCPPath        func(s agentScope) string
	// SkillDirs are the skill roots the client reads, in preference order;
	// the first is where we write. Relative to the scope base.
	ProjectSkillDirs []string
	GlobalSkillDirs  []string
}

func (c *agentClient) skillRoots(s agentScope) []string {
	dirs := c.ProjectSkillDirs
	if s.Global {
		dirs = c.GlobalSkillDirs
	}
	out := make([]string, len(dirs))
	for i, d := range dirs {
		out[i] = filepath.Join(s.base(), d)
	}
	return out
}

func (c *agentClient) skillPath(s agentScope) string {
	return filepath.Join(c.skillRoots(s)[0], agentSkillDirName, "SKILL.md")
}

// agentCodexHome honours CODEX_HOME, defaulting to ~/.codex.
func agentCodexHome(s agentScope) string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	return filepath.Join(s.Home, ".codex")
}

// agentVSCodeUserDir is VS Code's user profile directory (where the user
// mcp.json lives): ~/.config/Code/User, ~/Library/Application Support/Code/User
// or %APPDATA%\Code\User.
func agentVSCodeUserDir(s agentScope) string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(s.Home, ".config")
	}
	return filepath.Join(dir, "Code", "User")
}

var agentClients = []*agentClient{
	{
		ID: "claude", Name: "Claude Code",
		ProjectMarkers: []string{".claude", ".mcp.json", "CLAUDE.md"},
		HomeMarkers: func(s agentScope) []string {
			return []string{filepath.Join(s.Home, ".claude"), filepath.Join(s.Home, ".claude.json")}
		},
		MCPFormat: agentMCPServersJSON,
		MCPPath: func(s agentScope) string {
			if s.Global {
				return filepath.Join(s.Home, ".claude.json")
			}
			return filepath.Join(s.Dir, ".mcp.json")
		},
		ProjectSkillDirs: []string{".claude/skills"},
		GlobalSkillDirs:  []string{".claude/skills"},
	},
	{
		ID: "cursor", Name: "Cursor",
		ProjectMarkers: []string{".cursor", ".cursorrules"},
		HomeMarkers: func(s agentScope) []string {
			return []string{filepath.Join(s.Home, ".cursor")}
		},
		MCPFormat: agentMCPServersJSON,
		MCPPath: func(s agentScope) string {
			return filepath.Join(s.base(), ".cursor", "mcp.json")
		},
		ProjectSkillDirs: []string{".cursor/skills", ".agents/skills", ".claude/skills", ".codex/skills"},
		GlobalSkillDirs:  []string{".cursor/skills", ".agents/skills", ".claude/skills", ".codex/skills"},
	},
	{
		ID: "vscode", Name: "VS Code (Copilot)",
		ProjectMarkers: []string{".vscode", ".github/copilot-instructions.md"},
		HomeMarkers: func(s agentScope) []string {
			return []string{agentVSCodeUserDir(s), filepath.Join(s.Home, ".copilot")}
		},
		MCPFormat: agentVSCodeJSON,
		MCPPath: func(s agentScope) string {
			if s.Global {
				return filepath.Join(agentVSCodeUserDir(s), "mcp.json")
			}
			return filepath.Join(s.Dir, ".vscode", "mcp.json")
		},
		ProjectSkillDirs: []string{".github/skills", ".claude/skills", ".agents/skills"},
		GlobalSkillDirs:  []string{".copilot/skills", ".claude/skills", ".agents/skills"},
	},
	{
		ID: "codex", Name: "Codex",
		ProjectMarkers: []string{".codex", "AGENTS.md"},
		HomeMarkers: func(s agentScope) []string {
			return []string{agentCodexHome(s)}
		},
		MCPFormat: agentCodexTOML,
		MCPPath: func(s agentScope) string {
			if s.Global {
				return filepath.Join(agentCodexHome(s), "config.toml")
			}
			return filepath.Join(s.Dir, ".codex", "config.toml")
		},
		ProjectSkillDirs: []string{".agents/skills"},
		GlobalSkillDirs:  []string{".agents/skills"},
	},
}

func agentClientIDs() []string {
	ids := make([]string, len(agentClients))
	for i, c := range agentClients {
		ids[i] = c.ID
	}
	return ids
}

func agentClientByID(id string) *agentClient {
	for _, c := range agentClients {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// agentParseClients turns a --client value ("claude,cursor", "all") into
// clients in registry order. An empty value returns nil.
func agentParseClients(v string) ([]*agentClient, error) {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		return nil, nil
	}
	want := map[string]bool{}
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		switch part {
		case "":
			continue
		case "all":
			for _, c := range agentClients {
				want[c.ID] = true
			}
		case "claude-code", "claudecode":
			want["claude"] = true
		case "code", "vs-code", "copilot":
			want["vscode"] = true
		default:
			if agentClientByID(part) == nil {
				return nil, Usagef("unknown --client %q (want %s or all)", part, strings.Join(agentClientIDs(), ", "))
			}
			want[part] = true
		}
	}
	var out []*agentClient
	for _, c := range agentClients {
		if want[c.ID] {
			out = append(out, c)
		}
	}
	return out, nil
}

// agentDetect returns the clients that look in use in the project dir or HOME.
func agentDetect(s agentScope) []*agentClient {
	var out []*agentClient
	for _, c := range agentClients {
		found := false
		for _, m := range c.ProjectMarkers {
			if agentExists(filepath.Join(s.Dir, m)) {
				found = true
				break
			}
		}
		if !found && c.HomeMarkers != nil {
			for _, m := range c.HomeMarkers(s) {
				if agentExists(m) {
					found = true
					break
				}
			}
		}
		if found {
			out = append(out, c)
		}
	}
	return out
}

func agentExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// agentResolveScope builds the scope from --dir and --global.
func agentResolveScope(dir string, global bool) (agentScope, error) {
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return agentScope{}, err
		}
		dir = wd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return agentScope{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil && global {
		return agentScope{}, fmt.Errorf("locate home directory: %w", err)
	}
	return agentScope{Global: global, Dir: abs, Home: home}, nil
}

// agentSelectClients applies --client, else detection. It fails with a usage
// error when nothing is selected, so a command never silently does nothing.
func agentSelectClients(flag string, s agentScope) ([]*agentClient, error) {
	cs, err := agentParseClients(flag)
	if err != nil {
		return nil, err
	}
	if cs != nil {
		return cs, nil
	}
	cs = agentDetect(s)
	if len(cs) == 0 {
		return nil, Usagef("no agent client detected in %s or your home directory; pass --client %s or all", s.Dir, strings.Join(agentClientIDs(), "|"))
	}
	return cs, nil
}

// agentResult is one line of a command's report.
type agentResult struct {
	Client string `json:"client"`
	Kind   string `json:"kind"` // mcp | skill | agents_md
	File   string `json:"file"`
	Action string `json:"action"` // created | updated | unchanged | skipped
	Reason string `json:"reason,omitempty"`
	Secret bool   `json:"secret,omitempty"` // the file now holds a literal API key

	content  []byte // what to write when applied
	mode     os.FileMode
	prevMode os.FileMode // permissions of the file before a secret write
}

// agentApply writes every pending result.
func agentApply(rs []*agentResult) error {
	for _, r := range rs {
		if r.Action != agentActionCreated && r.Action != agentActionUpdated {
			continue
		}
		if err := agentWriteFile(r.File, r.content, r.mode, r.Secret); err != nil {
			return err
		}
	}
	return nil
}

// agentReport prints results (JSON list, or a table) plus secret guidance.
func agentReport(p *output.Printer, rs []*agentResult, s agentScope) error {
	if rs == nil {
		rs = []*agentResult{}
	}
	err := p.Result(rs, func(w io.Writer) {
		rows := make([][]string, 0, len(rs))
		for _, r := range rs {
			rows = append(rows, []string{r.Client, r.Kind, r.Action, agentDisplayPath(r.File, s), r.Reason})
		}
		p.Table([]string{"CLIENT", "KIND", "ACTION", "FILE", "NOTE"}, rows)
	})
	agentSecretGuidance(p, rs, s)
	return err
}

// agentSecretGuidance warns about literal keys, with .gitignore advice for
// files inside the project.
func agentSecretGuidance(p *output.Printer, rs []*agentResult, s agentScope) {
	for _, r := range rs {
		if !r.Secret || (r.Action != agentActionCreated && r.Action != agentActionUpdated) {
			continue
		}
		if r.prevMode != 0 && r.prevMode != 0o600 {
			p.Warn("%s now contains your API key in plain text; its permissions were changed from %04o to 0600 (owner only)", r.File, r.prevMode)
		} else {
			p.Warn("%s now contains your API key in plain text; its permissions are 0600 (owner only)", r.File)
		}
		if rel, err := filepath.Rel(s.Dir, r.File); err == nil && !strings.HasPrefix(rel, "..") {
			p.Warn("keep it out of version control: echo %s >> .gitignore", filepath.ToSlash(rel))
		}
	}
}

func agentDisplayPath(p string, s agentScope) string {
	if rel, err := filepath.Rel(s.Dir, p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	if s.Home != "" {
		if rel, err := filepath.Rel(s.Home, p); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return p
}

// agentPlanFile compares desired content with what is on disk. A secret
// (mode 0600) file that exists with wider permissions is planned as an update
// even when its content is already right, so the key does not stay readable
// by other users.
func agentPlanFile(r *agentResult, content []byte, mode os.FileMode) error {
	r.content, r.mode = content, mode
	fi, statErr := os.Stat(r.File)
	old, err := os.ReadFile(r.File)
	switch {
	case errors.Is(err, os.ErrNotExist):
		r.Action = agentActionCreated
		return nil
	case err != nil:
		return fmt.Errorf("read %s: %w", r.File, err)
	}
	loose := r.Secret && statErr == nil && fi.Mode().Perm()&0o077 != 0
	if statErr == nil && r.Secret {
		r.prevMode = fi.Mode().Perm()
	}
	switch {
	case bytes.Equal(old, content) && !loose:
		r.Action = agentActionUnchange
		r.content = nil
	case bytes.Equal(old, content):
		r.Action, r.Reason = agentActionUpdated, "holds the API key: restrict permissions to 0600"
	default:
		r.Action = agentActionUpdated
	}
	return nil
}

// agentWriteFile writes atomically (temp file + rename). A non-secret write
// keeps the mode of an existing file; a secret one always ends up 0600.
func agentWriteFile(path string, data []byte, mode os.FileMode, secret bool) error {
	if fi, err := os.Stat(path); err == nil && !secret {
		mode = fi.Mode().Perm()
	}
	if secret {
		mode = 0o600
	}
	if mode == 0 {
		mode = 0o644
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if secret {
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("restrict permissions of %s: %w", path, err)
		}
	}
	return nil
}

// agentAPIKey returns the configured key (flag > env > file), or "".
func agentAPIKey() (key, source string) {
	r, err := config.Resolve(flagAPIKey, flagBaseURL)
	if err != nil {
		return "", ""
	}
	return r.APIKey, r.APIKeySource
}

// ---- order-preserving JSON object ----

// agentJSONObject is a JSON object that remembers key order, so merging into
// a user's config does not reshuffle it.
type agentJSONObject struct {
	keys []string
	vals map[string]json.RawMessage
}

func agentParseObject(b []byte) (*agentJSONObject, error) {
	o := &agentJSONObject{vals: map[string]json.RawMessage{}}
	if len(bytes.TrimSpace(b)) == 0 {
		return o, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not a JSON object")
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, _ := kt.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		o.set(k, raw)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing data after JSON object")
	}
	return o, nil
}

func (o *agentJSONObject) set(k string, v json.RawMessage) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *agentJSONObject) get(k string) (json.RawMessage, bool) {
	v, ok := o.vals[k]
	return v, ok
}

func (o *agentJSONObject) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		b.Write(o.vals[k])
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// agentIndent renders v as 2-space indented JSON with a trailing newline,
// without HTML escaping.
func agentIndent(v any) ([]byte, error) {
	var raw bytes.Buffer
	enc := json.NewEncoder(&raw)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	var compact, out bytes.Buffer
	if err := json.Compact(&compact, raw.Bytes()); err != nil {
		return nil, err
	}
	if err := json.Indent(&out, compact.Bytes(), "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// agentRaw marshals v without HTML escaping.
func agentRaw(v any) json.RawMessage {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return json.RawMessage(bytes.TrimSpace(b.Bytes()))
}

// agentJSONEqual compares two JSON documents semantically.
func agentJSONEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}
