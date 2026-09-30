package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Spicrawl/cli/internal/config"
)

var mcpFlags struct {
	client string
	global bool
	print  bool
	url    string
	useEnv bool
	dir    string
}

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Connect AI agent clients to the hosted Spicrawl MCP server",
}

var mcpInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Add the hosted Spicrawl MCP server to a client's config",
	Long: `Merge a "spicrawl" server entry into an agent client's MCP config, keeping
every other server and key in the file. Re-running is safe: an entry that is
already correct is reported as unchanged.

The server URL is --mcp-url, else $SPICRAWL_MCP_URL, else <API origin>/mcp, where
the API origin is the scheme and host of the resolved base URL (--base-url,
$SPICRAWL_BASE_URL, config file) — the path self-hosted deployments serve the MCP
server on. With the default base URL (the hosted API) it is ` + agentPublicMCPURL + `.

Clients and files (project scope; --global for your user config):
  claude   .mcp.json               ~/.claude.json
  cursor   .cursor/mcp.json        ~/.cursor/mcp.json
  vscode   .vscode/mcp.json        <user config dir>/Code/User/mcp.json
  codex    .codex/config.toml      ~/.codex/config.toml ($CODEX_HOME)

By default the API key is referenced, not copied: Claude Code (.mcp.json) and
Cursor read ${SPICRAWL_API_KEY} from the environment, Codex uses
bearer_token_env_var, and VS Code prompts once for the key and stores it
securely. Claude Code's user config cannot expand variables, so --global
--client claude embeds the key and warns. --use-env=false embeds the literal
key everywhere.

Without --client, configures the clients detected in this directory and HOME.`,
	Example: `  spicrawl mcp install --client claude
  spicrawl mcp install --client all --global
  spicrawl mcp install --client cursor --print`,
	Args: cobra.NoArgs,
	RunE: runMCPInstall,
}

func init() {
	f := mcpInstallCmd.Flags()
	f.StringVar(&mcpFlags.client, "client", "", "claude, cursor, vscode, codex or all (comma-separated; default: detected clients)")
	f.BoolVar(&mcpFlags.global, "global", false, "write the user-level config instead of the project's")
	f.BoolVar(&mcpFlags.print, "print", false, "print the config snippet instead of writing it")
	f.StringVar(&mcpFlags.url, "mcp-url", "", "MCP server URL (default: $"+agentEnvMCPURL+", then <API origin>/mcp from the base URL; "+agentPublicMCPURL+" for the hosted API)")
	f.BoolVar(&mcpFlags.useEnv, "use-env", true, "reference $SPICRAWL_API_KEY instead of embedding the key, where the client supports it")
	f.StringVar(&mcpFlags.dir, "dir", "", "project directory (default: current directory)")
	mcpCmd.AddCommand(mcpInstallCmd)
	rootCmd.AddCommand(mcpCmd)
}

// mcpOptions controls how an entry is rendered.
type mcpOptions struct {
	URL    string
	UseEnv bool
	Key    string // literal key, used only when an entry must embed it
	Print  bool
}

func runMCPInstall(cmd *cobra.Command, _ []string) error {
	p := Printer()
	ep, err := agentResolveEndpoints(mcpFlags.url, "")
	if err != nil {
		return err
	}
	s, err := agentResolveScope(mcpFlags.dir, mcpFlags.global)
	if err != nil {
		return err
	}
	clients, err := agentSelectClients(mcpFlags.client, s)
	if err != nil {
		return err
	}
	key, _ := agentAPIKey()
	opt := mcpOptions{URL: ep.MCP, UseEnv: mcpFlags.useEnv, Key: key, Print: mcpFlags.print}

	if mcpFlags.print {
		return mcpPrint(p.JSON, clients, s, opt)
	}

	var rs []*agentResult
	for _, c := range clients {
		r, err := mcpPlan(c, s, opt)
		if err != nil {
			return err
		}
		rs = append(rs, r)
	}
	if err := agentApply(rs); err != nil {
		return err
	}
	if err := agentReport(p, rs, s); err != nil {
		return err
	}
	mcpEnvHint(p, rs)
	for _, r := range rs {
		if r.Action == agentActionSkipped && r.Reason == mcpNoKeyReason {
			return ErrNoKey
		}
	}
	return nil
}

const mcpNoKeyReason = "this config must embed the API key and none is configured"

// mcpServerEntry keeps the conventional key order (type, url, headers).
type mcpServerEntry struct {
	Type    string            `json:"type,omitempty"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// mcpEntry renders the server entry for a client and reports whether it
// holds a literal key.
func mcpEntry(c *agentClient, s agentScope, opt mcpOptions) (entry *mcpServerEntry, secret bool, err error) {
	literal := func() (string, error) {
		if opt.Key == "" {
			if opt.Print {
				return "Bearer <" + config.EnvAPIKey + ">", nil
			}
			return "", ErrNoKey
		}
		return "Bearer " + opt.Key, nil
	}
	var auth string
	switch c.ID {
	case "claude":
		entry = &mcpServerEntry{Type: "http", URL: opt.URL}
		if opt.UseEnv && !s.Global {
			auth = "Bearer ${" + config.EnvAPIKey + "}"
		} else {
			secret = true
		}
	case "cursor":
		entry = &mcpServerEntry{URL: opt.URL}
		if opt.UseEnv {
			auth = "Bearer ${env:" + config.EnvAPIKey + "}"
		} else {
			secret = true
		}
	case "vscode":
		entry = &mcpServerEntry{Type: "http", URL: opt.URL}
		if opt.UseEnv {
			auth = "Bearer ${input:" + agentVSCodeInputID + "}"
		} else {
			secret = true
		}
	default:
		return nil, false, fmt.Errorf("client %s has no JSON entry", c.ID)
	}
	if secret {
		if auth, err = literal(); err != nil {
			return nil, true, err
		}
	}
	entry.Headers = map[string]string{"Authorization": auth}
	return entry, secret, nil
}

func mcpVSCodeInput() any {
	return struct {
		Type        string `json:"type"`
		ID          string `json:"id"`
		Description string `json:"description"`
		Password    bool   `json:"password"`
	}{"promptString", agentVSCodeInputID, "Spicrawl API key", true}
}

// mcpPlan computes the new file content and action for one client.
func mcpPlan(c *agentClient, s agentScope, opt mcpOptions) (*agentResult, error) {
	r := &agentResult{Client: c.ID, Kind: "mcp", File: c.MCPPath(s)}
	var (
		content []byte
		secret  bool
		err     error
	)
	// The merges return the file's current bytes when nothing changes;
	// agentPlanFile then reports "unchanged" (or tightens a loose key file).
	if c.MCPFormat == agentCodexTOML {
		content, secret, _, err = mcpMergeTOML(r.File, s, opt)
	} else {
		content, secret, _, err = mcpMergeJSON(c, r.File, s, opt)
	}
	if err == ErrNoKey {
		r.Action, r.Reason = agentActionSkipped, mcpNoKeyReason
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	r.Secret = secret
	mode := os.FileMode(0o644)
	if secret {
		mode = 0o600
	}
	if err := agentPlanFile(r, content, mode); err != nil {
		return nil, err
	}
	if secret && r.Action != agentActionUnchange && r.Reason == "" {
		r.Reason = "contains the literal API key"
	}
	return r, nil
}

// mcpMergeJSON merges the spicrawl entry into a JSON config, preserving key
// order and everything else in the file.
func mcpMergeJSON(c *agentClient, path string, s agentScope, opt mcpOptions) ([]byte, bool, bool, error) {
	entry, secret, err := mcpEntry(c, s, opt)
	if err != nil {
		return nil, secret, false, err
	}
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, secret, false, fmt.Errorf("read %s: %w", path, err)
	}
	root, err := agentParseObject(old)
	if err != nil {
		return nil, secret, false, fmt.Errorf("parse %s: %v (it must be plain JSON without comments; fix it or merge the output of --print by hand)", path, err)
	}
	serversKey := "mcpServers"
	if c.MCPFormat == agentVSCodeJSON {
		serversKey = "servers"
	}
	servers := &agentJSONObject{vals: map[string]json.RawMessage{}}
	if raw, ok := root.get(serversKey); ok && string(raw) != "null" {
		if servers, err = agentParseObject(raw); err != nil {
			return nil, secret, false, fmt.Errorf("parse %s: %q is not an object", path, serversKey)
		}
	}
	desired := agentRaw(entry)
	current, had := servers.get(agentServerName)
	changed := !had || !agentJSONEqual(current, desired)
	servers.set(agentServerName, desired)
	root.set(serversKey, agentRaw(servers))

	if c.MCPFormat == agentVSCodeJSON && opt.UseEnv {
		var inputs []json.RawMessage
		if raw, ok := root.get("inputs"); ok && string(raw) != "null" {
			if err := json.Unmarshal(raw, &inputs); err != nil {
				return nil, secret, false, fmt.Errorf("parse %s: \"inputs\" is not an array", path)
			}
		}
		found := false
		for _, in := range inputs {
			var probe struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(in, &probe) == nil && probe.ID == agentVSCodeInputID {
				found = true
				break
			}
		}
		if !found {
			inputs = append(inputs, agentRaw(mcpVSCodeInput()))
			root.set("inputs", agentRaw(inputs))
			changed = true
		}
	}
	if !changed {
		return old, secret, false, nil
	}
	out, err := agentIndent(root)
	return out, secret, true, err
}

// mcpTOMLBlock renders the [mcp_servers.spicrawl] table.
func mcpTOMLBlock(opt mcpOptions) ([]string, bool, error) {
	lines := []string{
		"[mcp_servers." + agentServerName + "]",
		"url = " + string(agentRaw(opt.URL)),
	}
	if opt.UseEnv {
		lines = append(lines, "bearer_token_env_var = "+string(agentRaw(config.EnvAPIKey)))
		return lines, false, nil
	}
	auth, err := mcpTOMLAuth(opt)
	if err != nil {
		return nil, true, err
	}
	lines = append(lines, `http_headers = { "Authorization" = `+string(agentRaw(auth))+" }")
	return lines, true, nil
}

// mcpTOMLAuth is the literal Authorization value for --use-env=false.
func mcpTOMLAuth(opt mcpOptions) (string, error) {
	key := opt.Key
	if key == "" {
		if !opt.Print {
			return "", ErrNoKey
		}
		key = "<" + config.EnvAPIKey + ">"
	}
	return "Bearer " + key, nil
}

// ---- Codex config.toml ----
//
// The merge is textual so comments, ordering and unrelated tables survive.
// A small scanner splits the file into statements (table headers and
// key/value pairs, including multi-line strings and arrays) and resolves each
// key's full dotted path, so every way of spelling the spicrawl entry is found:
//
//	[mcp_servers.spicrawl]              table: edited in place
//	[mcp_servers] spicrawl = { … }      inline table: edited in place
//	mcp_servers.spicrawl = { … }        inline table: edited in place
//	mcp_servers.spicrawl.url = …        dotted keys: refused
//	mcp_servers = { spicrawl = … }      inline mcp_servers: refused
//
// Only url, bearer_token_env_var and the Authorization header are ours; every
// other key in the entry (enabled, startup_timeout_sec, tools, …) is kept.
// Anything the merge cannot edit safely is refused with an error rather than
// appended as a duplicate, and the result is re-scanned before it is written.

// tomlStmt is one statement of a TOML document.
type tomlStmt struct {
	start, end int      // line range, inclusive
	header     bool     // [table] or [[array]]
	array      bool     // [[array]]
	table      []string // key: the table it belongs to
	path       []string // header: table path; key: full path (table + key)
	key        []string // key: path relative to its table
	rawKey     string   // key: the key as written
	indent     string   // key: leading whitespace
	value      string   // key: raw value text, without a trailing comment
	comment    string   // key: trailing comment on its last line
}

func tomlScan(lines []string) ([]tomlStmt, error) {
	var out []tomlStmt
	var table []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		t := strings.TrimLeft(line, " \t")
		if t == "" || t[0] == '#' {
			continue
		}
		if t[0] == '[' {
			arr := strings.HasPrefix(t, "[[")
			rest, closer := t[1:], "]"
			if arr {
				rest, closer = t[2:], "]]"
			}
			path, after, err := tomlParseKey(rest)
			if err != nil {
				return nil, fmt.Errorf("line %d: %v", i+1, err)
			}
			after = strings.TrimLeft(after, " \t")
			if !strings.HasPrefix(after, closer) {
				return nil, fmt.Errorf("line %d: malformed table header", i+1)
			}
			if after = strings.TrimSpace(after[len(closer):]); after != "" && after[0] != '#' {
				return nil, fmt.Errorf("line %d: unexpected text after table header", i+1)
			}
			table = path
			out = append(out, tomlStmt{start: i, end: i, header: true, array: arr, path: path})
			continue
		}
		key, after, err := tomlParseKey(t)
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", i+1, err)
		}
		after = strings.TrimLeft(after, " \t")
		if !strings.HasPrefix(after, "=") {
			return nil, fmt.Errorf("line %d: expected '=' after key", i+1)
		}
		end, value, comment, err := tomlScanValue(lines, i, after[1:])
		if err != nil {
			return nil, err
		}
		if value == "" {
			return nil, fmt.Errorf("line %d: missing value", i+1)
		}
		out = append(out, tomlStmt{
			start: i, end: end,
			table:  append([]string(nil), table...),
			path:   append(append([]string(nil), table...), key...),
			key:    key,
			rawKey: strings.TrimSpace(t[:len(t)-len(after)]),
			indent: line[:len(line)-len(t)],
			value:  value, comment: comment,
		})
		i = end
	}
	return out, nil
}

// tomlParseKey reads a (dotted) key: bare, "basic" or 'literal' parts.
func tomlParseKey(s string) ([]string, string, error) {
	var parts []string
	for {
		s = strings.TrimLeft(s, " \t")
		if s == "" {
			return nil, "", errors.New("missing key")
		}
		switch s[0] {
		case '"':
			j := 1
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(s) {
				return nil, "", errors.New("unterminated quoted key")
			}
			k, err := strconv.Unquote(s[:j+1])
			if err != nil {
				k = s[1:j]
			}
			parts, s = append(parts, k), s[j+1:]
		case '\'':
			j := strings.IndexByte(s[1:], '\'')
			if j < 0 {
				return nil, "", errors.New("unterminated quoted key")
			}
			parts, s = append(parts, s[1:j+1]), s[j+2:]
		default:
			j := 0
			for j < len(s) && (s[j] == '_' || s[j] == '-' || s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z' || s[j] >= '0' && s[j] <= '9') {
				j++
			}
			if j == 0 {
				return nil, "", fmt.Errorf("invalid key near %q", tomlClip(s))
			}
			parts, s = append(parts, s[:j]), s[j:]
		}
		rest := strings.TrimLeft(s, " \t")
		if !strings.HasPrefix(rest, ".") {
			return parts, s, nil
		}
		s = rest[1:]
	}
}

func tomlClip(s string) string {
	if len(s) > 20 {
		return s[:20] + "…"
	}
	return s
}

// tomlScanValue finds where a value that starts in s (on line i) ends,
// following strings, arrays and inline tables across lines. It returns the
// last line, the value text and the trailing comment.
func tomlScanValue(lines []string, i int, s string) (int, string, string, error) {
	const (
		normal = iota
		basic
		literal
		mlBasic
		mlLiteral
	)
	mode, depth := normal, 0
	var b strings.Builder
	for line, cur := i, s; ; {
		comment := ""
		for j := 0; j < len(cur); j++ {
			c := cur[j]
			switch mode {
			case normal:
				switch {
				case strings.HasPrefix(cur[j:], `"""`):
					mode, j = mlBasic, j+2
				case strings.HasPrefix(cur[j:], `'''`):
					mode, j = mlLiteral, j+2
				case c == '"':
					mode = basic
				case c == '\'':
					mode = literal
				case c == '[' || c == '{':
					depth++
				case c == ']' || c == '}':
					depth--
				case c == '#':
					comment, cur = strings.TrimSpace(cur[j:]), cur[:j]
				}
			case basic:
				if c == '\\' {
					j++
				} else if c == '"' {
					mode = normal
				}
			case literal:
				if c == '\'' {
					mode = normal
				}
			case mlBasic:
				if c == '\\' {
					j++
				} else if strings.HasPrefix(cur[j:], `"""`) {
					mode, j = normal, j+2
					for j+1 < len(cur) && cur[j+1] == '"' {
						j++
					}
				}
			case mlLiteral:
				if strings.HasPrefix(cur[j:], `'''`) {
					mode, j = normal, j+2
					for j+1 < len(cur) && cur[j+1] == '\'' {
						j++
					}
				}
			}
		}
		b.WriteString(cur)
		if mode == basic || mode == literal {
			return 0, "", "", fmt.Errorf("line %d: unterminated string", line+1)
		}
		if depth < 0 {
			return 0, "", "", fmt.Errorf("line %d: unbalanced brackets", line+1)
		}
		if mode == normal && depth == 0 {
			return line, strings.TrimSpace(b.String()), comment, nil
		}
		if line++; line >= len(lines) {
			return 0, "", "", fmt.Errorf("line %d: value is not terminated", i+1)
		}
		b.WriteByte('\n')
		cur = lines[line]
	}
}

// tomlEntry is a key/value pair of a table or inline table, and after an
// update, what to emit for it.
type tomlEntry struct {
	key    []string
	rawKey string
	value  string
	raw    string // inline tables: the pair as written
	orig   int    // index of the source pair, -1 for a new one
	text   string // "" keeps the source pair; otherwise the new "key = value"
}

func (e tomlEntry) render() string {
	switch {
	case e.text != "":
		return e.text
	case e.raw != "":
		return e.raw
	}
	return e.rawKey + " = " + e.value
}

// tomlParseInline splits a one-line inline table into its pairs.
func tomlParseInline(v string) ([]tomlEntry, bool) {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "{") || !strings.HasSuffix(v, "}") || strings.Contains(v, "\n") {
		return nil, false
	}
	var out []tomlEntry
	for _, part := range tomlSplitTop(v[1 : len(v)-1]) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, after, err := tomlParseKey(part)
		if err != nil {
			return nil, false
		}
		after = strings.TrimLeft(after, " \t")
		if !strings.HasPrefix(after, "=") {
			return nil, false
		}
		val := strings.TrimSpace(after[1:])
		if val == "" {
			return nil, false
		}
		out = append(out, tomlEntry{key: key, rawKey: strings.TrimSpace(part[:len(part)-len(after)]), value: val, raw: part, orig: len(out)})
	}
	return out, true
}

// tomlSplitTop splits s on commas outside strings, arrays and inline tables.
func tomlSplitTop(s string) []string {
	var parts []string
	depth, start := 0, 0
	var quote byte
	for j := 0; j < len(s); j++ {
		c := s[j]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' {
				j++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '[' || c == '{':
			depth++
		case c == ']' || c == '}':
			depth--
		case c == ',' && depth == 0:
			parts, start = append(parts, s[start:j]), j+1
		}
	}
	return append(parts, s[start:])
}

func tomlRenderInline(es []tomlEntry) string {
	if len(es) == 0 {
		return "{}"
	}
	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = e.render()
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// tomlString decodes a one-line basic or literal string value.
func tomlString(v string) (string, bool) {
	v = strings.TrimSpace(v)
	switch {
	case len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' && !strings.HasPrefix(v, "'''"):
		return v[1 : len(v)-1], true
	case len(v) >= 2 && v[0] == '"' && !strings.HasPrefix(v, `"""`):
		s, err := strconv.Unquote(v)
		return s, err == nil
	}
	return "", false
}

func tomlPathIs(p []string, want ...string) bool {
	if len(p) != len(want) {
		return false
	}
	for i := range p {
		if p[i] != want[i] {
			return false
		}
	}
	return true
}

func tomlPathHas(p []string, prefix ...string) bool {
	return len(p) >= len(prefix) && tomlPathIs(p[:len(prefix)], prefix...)
}

// mcpTOMLLocate finds the spicrawl entry: the index of its [mcp_servers.spicrawl]
// header or of its inline-table pair (-1 when absent). It refuses the forms it
// cannot edit safely.
func mcpTOMLLocate(stmts []tomlStmt) (table, inline int, err error) {
	table, inline = -1, -1
	name := "mcp_servers." + agentServerName
	for k, st := range stmts {
		p := st.path
		switch {
		case st.header && st.array && (tomlPathHas(p, "mcp_servers", agentServerName) || tomlPathIs(p, "mcp_servers")):
			return 0, 0, fmt.Errorf("[[%s]] is an array of tables, which Codex does not accept", strings.Join(p, "."))
		case st.header && tomlPathIs(p, "mcp_servers", agentServerName):
			if table >= 0 {
				return 0, 0, fmt.Errorf("[%s] is defined twice (line %d and %d)", name, stmts[table].start+1, st.start+1)
			}
			table = k
		case st.header && tomlPathIs(p, "mcp_servers", agentServerName, "http_headers"):
			return 0, 0, fmt.Errorf("[%s.http_headers] is a separate table", name)
		case st.header:
		case tomlPathIs(p, "mcp_servers"):
			return 0, 0, fmt.Errorf("line %d defines mcp_servers as an inline table", st.start+1)
		case tomlPathIs(p, "mcp_servers", agentServerName):
			if inline >= 0 {
				return 0, 0, fmt.Errorf("%s is defined twice (line %d and %d)", name, stmts[inline].start+1, st.start+1)
			}
			inline = k
		case tomlPathHas(p, "mcp_servers", agentServerName) && !tomlPathHas(st.table, "mcp_servers", agentServerName):
			return 0, 0, fmt.Errorf("line %d sets %s with dotted keys (%s)", st.start+1, name, strings.Join(p, "."))
		}
	}
	if table >= 0 && inline >= 0 {
		return 0, 0, fmt.Errorf("%s is defined both as a table (line %d) and inline (line %d)", name, stmts[table].start+1, stmts[inline].start+1)
	}
	return table, inline, nil
}

// mcpTOMLUpdate applies our keys to an entry's pairs, keeping everything
// else: url is set in place, bearer_token_env_var or the Authorization header
// is set or dropped per opt.UseEnv, and missing keys are added after url.
func mcpTOMLUpdate(entries []tomlEntry, opt mcpOptions) ([]tomlEntry, error) {
	q := func(s string) string { return string(agentRaw(s)) }
	auth := ""
	if !opt.UseEnv {
		var err error
		if auth, err = mcpTOMLAuth(opt); err != nil {
			return nil, err
		}
	}
	isAuth := func(k []string, n int) bool { return len(k) == n+1 && strings.EqualFold(k[n], "authorization") }
	var out []tomlEntry
	urlAt := -1
	haveBearer, haveAuth, dottedHeaders, inlineHeaders := false, false, false, false
	for _, e := range entries {
		switch {
		case tomlPathIs(e.key, "command"):
			return nil, fmt.Errorf("it is a stdio server (command = %s); rename or remove it", e.value)
		case tomlPathIs(e.key, "url"):
			if urlAt >= 0 {
				return nil, errors.New("url is set twice")
			}
			if s, ok := tomlString(e.value); !ok || s != opt.URL {
				e.text = e.rawKey + " = " + q(opt.URL)
			}
			out = append(out, e)
			urlAt = len(out)
		case tomlPathIs(e.key, "bearer_token_env_var"):
			if !opt.UseEnv {
				continue
			}
			haveBearer = true
			if s, ok := tomlString(e.value); !ok || s != config.EnvAPIKey {
				e.text = e.rawKey + " = " + q(config.EnvAPIKey)
			}
			out = append(out, e)
		case tomlPathIs(e.key, "http_headers"):
			hs, ok := tomlParseInline(e.value)
			if !ok {
				return nil, errors.New("http_headers is not a one-line inline table")
			}
			inlineHeaders = true
			var kept []tomlEntry
			changed := false
			for _, h := range hs {
				if !isAuth(h.key, 0) {
					kept = append(kept, h)
					continue
				}
				if opt.UseEnv || haveAuth {
					changed = true
					continue
				}
				haveAuth = true
				if s, ok := tomlString(h.value); !ok || s != auth {
					h.text, changed = h.rawKey+" = "+q(auth), true
				}
				kept = append(kept, h)
			}
			if !opt.UseEnv && !haveAuth {
				haveAuth, changed = true, true
				kept = append(kept, tomlEntry{orig: -1, text: `"Authorization" = ` + q(auth)})
			}
			switch {
			case !changed:
				out = append(out, e)
			case len(kept) > 0:
				e.text = e.rawKey + " = " + tomlRenderInline(kept)
				out = append(out, e)
			}
		case isAuth(e.key, 1) && e.key[0] == "http_headers":
			dottedHeaders = true
			if opt.UseEnv || haveAuth {
				continue
			}
			haveAuth = true
			if s, ok := tomlString(e.value); !ok || s != auth {
				e.text = e.rawKey + " = " + q(auth)
			}
			out = append(out, e)
		default:
			if len(e.key) > 1 && e.key[0] == "http_headers" {
				dottedHeaders = true
			}
			out = append(out, e)
		}
	}
	if dottedHeaders && inlineHeaders {
		return nil, errors.New("http_headers is set both inline and with dotted keys")
	}
	var add []tomlEntry
	if urlAt < 0 {
		add = append(add, tomlEntry{orig: -1, text: "url = " + q(opt.URL)})
	}
	switch {
	case opt.UseEnv && !haveBearer:
		add = append(add, tomlEntry{orig: -1, text: "bearer_token_env_var = " + q(config.EnvAPIKey)})
	case !opt.UseEnv && !haveAuth && dottedHeaders:
		add = append(add, tomlEntry{orig: -1, text: `http_headers.Authorization = ` + q(auth)})
	case !opt.UseEnv && !haveAuth:
		add = append(add, tomlEntry{orig: -1, text: `http_headers = { "Authorization" = ` + q(auth) + " }"})
	}
	at := urlAt
	if at < 0 {
		at = 0
	}
	res := append(append(append([]tomlEntry{}, out[:at]...), add...), out[at:]...)
	return res, nil
}

// mcpMergeTOML sets the spicrawl server in a Codex config.toml, editing an
// existing entry in place (whatever form it takes) or appending a
// [mcp_servers.spicrawl] table. The rest of the file is left byte-for-byte.
func mcpMergeTOML(path string, _ agentScope, opt mcpOptions) ([]byte, bool, bool, error) {
	secret := !opt.UseEnv
	block, _, err := mcpTOMLBlock(opt)
	if err != nil {
		return nil, secret, false, err
	}
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, secret, false, fmt.Errorf("read %s: %w", path, err)
	}
	refuse := func(err error) ([]byte, bool, bool, error) {
		return nil, secret, false, fmt.Errorf("%s: cannot update the %s MCP server safely: %v; edit it by hand (see \"spicrawl mcp install --client codex --print\") or remove it and re-run", path, agentServerName, err)
	}
	text := strings.ReplaceAll(string(old), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if text == "" {
		lines = nil
	}
	stmts, err := tomlScan(lines)
	if err != nil {
		return nil, secret, false, fmt.Errorf("parse %s: %v (fix it, or merge the output of --print by hand)", path, err)
	}
	table, inline, err := mcpTOMLLocate(stmts)
	if err != nil {
		return refuse(err)
	}

	var out string
	switch {
	case table < 0 && inline < 0:
		var b strings.Builder
		if body := strings.TrimRight(text, "\n"); body != "" {
			b.WriteString(body)
			b.WriteString("\n\n")
		}
		b.WriteString(strings.Join(block, "\n"))
		b.WriteString("\n")
		out = b.String()

	case inline >= 0:
		st := stmts[inline]
		entries, ok := tomlParseInline(st.value)
		if !ok || st.start != st.end {
			return refuse(fmt.Errorf("line %d: %s is not a one-line inline table", st.start+1, strings.Join(st.path, ".")))
		}
		res, err := mcpTOMLUpdate(entries, opt)
		if err != nil {
			return refuse(err)
		}
		line := st.indent + st.rawKey + " = " + tomlRenderInline(res)
		if st.comment != "" {
			line += " " + st.comment
		}
		lines[st.start] = line
		out = strings.Join(lines, "\n")

	default:
		// The table's pairs run until the next header.
		var body []tomlStmt
		for _, st := range stmts[table+1:] {
			if st.header {
				break
			}
			body = append(body, st)
		}
		entries := make([]tomlEntry, len(body))
		for i, st := range body {
			entries[i] = tomlEntry{key: st.key, rawKey: st.rawKey, value: st.value, orig: i}
		}
		res, err := mcpTOMLUpdate(entries, opt)
		if err != nil {
			return refuse(err)
		}
		// Where each source pair goes (or nothing, if dropped), and the new
		// pairs that follow it; new pairs before any source pair follow the header.
		emit := map[int]*tomlEntry{}
		after := map[int][]string{}
		prev := -1
		for i := range res {
			e := &res[i]
			if e.orig >= 0 {
				emit[e.orig], prev = e, e.orig
				continue
			}
			after[prev] = append(after[prev], e.text)
		}
		byStart := map[int]int{}
		for i, st := range body {
			byStart[st.start] = i
		}
		hdr := stmts[table].start
		var nl []string
		for i := 0; i < len(lines); i++ {
			k, isPair := byStart[i]
			if !isPair {
				nl = append(nl, lines[i])
				if i == hdr {
					nl = append(nl, after[-1]...)
				}
				continue
			}
			st := body[k]
			switch e := emit[k]; {
			case e == nil: // dropped
			case e.text == "":
				nl = append(nl, lines[st.start:st.end+1]...)
			default:
				line := st.indent + e.text
				if st.comment != "" {
					line += " " + st.comment
				}
				nl = append(nl, line)
			}
			nl = append(nl, after[k]...)
			i = st.end
		}
		out = strings.Join(nl, "\n")
	}
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	if out == string(old) {
		return old, secret, false, nil
	}
	// Never write something we would refuse to read back.
	check, err := tomlScan(strings.Split(strings.TrimSuffix(out, "\n"), "\n"))
	if err == nil {
		var t, in int
		if t, in, err = mcpTOMLLocate(check); err == nil && t < 0 && in < 0 {
			err = errors.New("entry missing after merge")
		}
	}
	if err != nil {
		return refuse(fmt.Errorf("the merged file would be invalid (%v)", err))
	}
	return []byte(out), secret, true, nil
}

// mcpSnippet renders what --print shows for one client.
func mcpSnippet(c *agentClient, s agentScope, opt mcpOptions) (string, bool, error) {
	if c.MCPFormat == agentCodexTOML {
		block, secret, err := mcpTOMLBlock(opt)
		return strings.Join(block, "\n") + "\n", secret, err
	}
	entry, secret, err := mcpEntry(c, s, opt)
	if err != nil {
		return "", secret, err
	}
	root := &agentJSONObject{vals: map[string]json.RawMessage{}}
	servers := &agentJSONObject{vals: map[string]json.RawMessage{}}
	servers.set(agentServerName, agentRaw(entry))
	if c.MCPFormat == agentVSCodeJSON {
		root.set("servers", agentRaw(servers))
		if opt.UseEnv {
			root.set("inputs", agentRaw([]any{mcpVSCodeInput()}))
		}
	} else {
		root.set("mcpServers", agentRaw(servers))
	}
	b, err := agentIndent(root)
	return string(b), secret, err
}

func mcpPrint(jsonMode bool, clients []*agentClient, s agentScope, opt mcpOptions) error {
	type snippet struct {
		Client  string `json:"client"`
		File    string `json:"file"`
		Format  string `json:"format"`
		Snippet string `json:"snippet"`
		Secret  bool   `json:"secret,omitempty"`
	}
	var out []snippet
	for _, c := range clients {
		text, secret, err := mcpSnippet(c, s, opt)
		if err != nil {
			return err
		}
		format := "json"
		if c.MCPFormat == agentCodexTOML {
			format = "toml"
		}
		out = append(out, snippet{c.ID, c.MCPPath(s), format, text, secret})
	}
	p := Printer()
	return p.Result(out, func(w io.Writer) {
		for i, sn := range out {
			if i > 0 {
				fmt.Fprintln(w)
			}
			fmt.Fprintf(w, "# %s: merge into %s\n%s", agentClientByID(sn.Client).Name, agentDisplayPath(sn.File, s), sn.Snippet)
		}
	})
}

// mcpEnvHint reminds the user that env-based configs need SPICRAWL_API_KEY
// exported where the client starts.
func mcpEnvHint(p interface{ Info(string, ...any) }, rs []*agentResult) {
	if os.Getenv(config.EnvAPIKey) != "" {
		return
	}
	var names []string
	for _, r := range rs {
		if r.Kind != "mcp" || r.Secret || r.Client == "vscode" {
			continue
		}
		if r.Action == agentActionCreated || r.Action == agentActionUpdated {
			names = append(names, agentClientByID(r.Client).Name)
		}
	}
	if len(names) > 0 {
		p.Info("note: %s read the key from $%s; export it in the shell or profile that starts the client", strings.Join(names, ", "), config.EnvAPIKey)
	}
}
