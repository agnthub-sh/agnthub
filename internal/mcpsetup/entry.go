// Package mcpsetup adds agnthub's hosted MCP server to the configuration of each
// Harness, finds it there and removes it again. It uses the Harness's own `mcp`
// command where that is reliable, and otherwise edits the configuration file in
// place: only the agnthub entry changes, the previous file is backed up and the
// new one is written atomically with the old file mode.
package mcpsetup

import (
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/harness"
)

// ServerName is the name of agnthub's entry in every Harness.
const ServerName = "agnthub"

// URL is the hosted MCP server.
const URL = "https://mcp.agnthub.sh"

// legacyURLs are former addresses of the hosted server.
var legacyURLs = []string{"https://api.agnthub.sh/mcp", "https://mcp.agnthub.sh/mcp"}

// State is what a configuration holds under the agnthub name.
type State string

// States of the agnthub entry in one configuration.
const (
	// Absent: no agnthub entry.
	Absent State = "absent"
	// Installed: the hosted server, as this Harness needs it.
	Installed State = "installed"
	// Outdated: the hosted server's URL in a shape this Harness does not read as
	// such (e.g. Claude Code without "type": "http"), or without the requested
	// API key.
	Outdated State = "outdated"
	// Legacy: a former URL of the hosted server.
	Legacy State = "legacy"
	// LegacyStdio: the local stdio server (`npx agnthub-cli mcp`), removed in v0.5.0.
	LegacyStdio State = "legacy-stdio"
	// Foreign: an agnthub entry agnthub did not write (another URL or command).
	Foreign State = "foreign"
)

// Ours reports an entry agnthub wrote, in any version.
func (s State) Ours() bool {
	return s == Installed || s == Outdated || s == Legacy || s == LegacyStdio
}

// spec is how one Harness keeps MCP servers.
type spec struct {
	id agent.HarnessID
	// toml: config.toml tables (Codex); otherwise JSON with comments allowed.
	toml bool
	// servers is the path of the object (or TOML table) holding servers by name.
	servers []string
	// headers is the entry field holding HTTP headers.
	headers string
	// projectFiles are the project-scope configuration files, relative to the
	// project; the first is created when none exists.
	projectFiles []string
	// base is the entry without credentials.
	base object
	// headerOnly are fields written only with an API key.
	headerOnly object
	cli        *cliSpec
	// signIn says how to sign in after an install without an API key.
	signIn string
}

// cliSpec is a Harness's own `mcp` command, used when its executable is on PATH
// and no secret would go on its command line.
type cliSpec struct {
	binary string
	// project reports a command that also writes the project-scope file.
	project bool
	add     func(scope harness.Scope) []string
	remove  func(scope harness.Scope) []string
}

func cliScope(scope harness.Scope) string {
	if scope == harness.ScopeProject {
		return "project"
	}
	return "user"
}

var specs = map[agent.HarnessID]*spec{
	// Claude Code 2.1.309: `claude mcp add` keeps ~/.claude.json, which Claude Code
	// also uses for its own state, consistent; -s project writes .mcp.json.
	agent.Claude: {
		id:           agent.Claude,
		servers:      []string{"mcpServers"},
		headers:      "headers",
		projectFiles: []string{".mcp.json"},
		base:         object{{"type", "http"}, {"url", URL}},
		cli: &cliSpec{
			binary:  "claude",
			project: true,
			add: func(scope harness.Scope) []string {
				return []string{
					"mcp",
					"add",
					"--transport",
					"http",
					"--scope",
					cliScope(scope),
					ServerName,
					URL,
				}
			},
			remove: func(scope harness.Scope) []string {
				return []string{"mcp", "remove", "--scope", cliScope(scope), ServerName}
			},
		},
		signIn: "run `claude mcp login agnthub`, or `/mcp` in Claude Code, select agnthub and authenticate",
	},
	// OpenCode has `mcp add` but no `mcp remove`, and rewrites opencode.jsonc
	// without its comments: the file is edited instead. OpenCode 2 nests servers
	// under mcp.servers and still reads OpenCode 1's mcp.<name>.
	agent.OpenCode: {
		id:           agent.OpenCode,
		servers:      []string{"mcp", "servers"},
		headers:      "headers",
		projectFiles: []string{"opencode.json", "opencode.jsonc"},
		base:         object{{"type", "remote"}, {"url", URL}},
		headerOnly:   object{{"oauth", false}},
		signIn:       "run `opencode mcp auth agnthub`, or `/mcps` in OpenCode, select agnthub and sign in",
	},
	// Codex 0.164's `codex mcp add` drops comments of other entries and can only
	// reference an API key by environment variable: config.toml is edited instead.
	agent.Codex: {
		id:           agent.Codex,
		toml:         true,
		servers:      []string{"mcp_servers"},
		headers:      "http_headers",
		projectFiles: []string{filepath.Join(".codex", "config.toml")},
		base:         object{{"url", URL}},
		signIn:       "run `codex mcp login agnthub`",
	},
	// Copilot CLI 1.0.105: `copilot mcp add` writes the user file only; the
	// project file is .github/mcp.json (.mcp.json is Claude Code's).
	agent.Copilot: {
		id:           agent.Copilot,
		servers:      []string{"mcpServers"},
		headers:      "headers",
		projectFiles: []string{filepath.Join(".github", "mcp.json")},
		base:         object{{"type", "http"}, {"url", URL}, {"tools", []string{"*"}}},
		cli: &cliSpec{
			binary: "copilot",
			add: func(harness.Scope) []string {
				return []string{"mcp", "add", "--transport", "http", ServerName, URL}
			},
			remove: func(harness.Scope) []string {
				return []string{"mcp", "remove", ServerName}
			},
		},
		signIn: "run `/mcp auth agnthub` in Copilot CLI",
	},
	// Cursor has no command that adds a server.
	agent.Cursor: {
		id:           agent.Cursor,
		servers:      []string{"mcpServers"},
		headers:      "headers",
		projectFiles: []string{filepath.Join(".cursor", "mcp.json")},
		base:         object{{"url", URL}},
		signIn:       "run `cursor-agent mcp login agnthub`, or `/mcp list` in the Cursor CLI, select agnthub and log in",
	},
	// Gemini CLI 0.66: `gemini mcp add -s user` keeps settings.json comments; its
	// project scope works only in trusted folders, so the project file is edited.
	agent.Gemini: {
		id:           agent.Gemini,
		servers:      []string{"mcpServers"},
		headers:      "headers",
		projectFiles: []string{filepath.Join(".gemini", "settings.json")},
		base:         object{{"url", URL}, {"type", "http"}},
		cli: &cliSpec{
			binary: "gemini",
			add: func(harness.Scope) []string {
				return []string{
					"mcp",
					"add",
					"--scope",
					"user",
					"--transport",
					"http",
					ServerName,
					URL,
				}
			},
			remove: func(harness.Scope) []string {
				return []string{"mcp", "remove", "--scope", "user", ServerName}
			},
		},
		signIn: "run `/mcp auth agnthub` in Gemini CLI",
	},
	// omp has no shell command for MCP servers; each profile has its own mcp.json.
	agent.OMP: {
		id:           agent.OMP,
		servers:      []string{"mcpServers"},
		headers:      "headers",
		projectFiles: []string{filepath.Join(".omp", "mcp.json")},
		base:         object{{"type", "http"}, {"url", URL}},
		signIn:       "run `/mcp reauth agnthub` in omp",
	},
}

// entry is the agnthub entry this Harness needs, with apiKey as a bearer header
// when given.
func (s *spec) entry(apiKey string) object {
	if apiKey == "" {
		return s.base
	}
	out := slices.Concat(s.base, s.headerOnly)
	return append(out, field{s.headers, object{{"Authorization", "Bearer " + apiKey}}})
}

// classify reads an existing agnthub entry against the one this Harness needs.
func (s *spec) classify(entry map[string]any, apiKey string) State {
	if entry == nil {
		return Absent
	}
	if url := entryURL(entry); url != "" {
		switch {
		case sameURL(url, URL):
		case slices.ContainsFunc(legacyURLs, func(legacy string) bool { return sameURL(url, legacy) }):
			return Legacy
		default:
			return Foreign
		}
		for _, f := range s.entry(apiKey) {
			got, ok := entry[f.key]
			if f.key == "url" {
				// Not under another key (Gemini's deprecated httpUrl).
				ok = got == url
			} else if ok {
				ok = reflect.DeepEqual(normalize(got), generic(f.value))
			}
			if !ok {
				return Outdated
			}
		}
		return Installed
	}
	if legacyStdio(entry) {
		return LegacyStdio
	}
	return Foreign
}

// entryURL is the server URL of a remote entry.
func entryURL(entry map[string]any) string {
	for _, key := range []string{"url", "httpUrl", "serverUrl"} {
		if url, ok := entry[key].(string); ok && url != "" {
			return url
		}
	}
	return ""
}

func sameURL(a, b string) bool {
	return strings.EqualFold(strings.TrimRight(strings.TrimSpace(a), "/"), b)
}

// legacyStdio reports the local server agnthub v0.4 ran: `npx … agnthub-cli mcp`,
// or the installed binary as `agnthub mcp`.
func legacyStdio(entry map[string]any) bool {
	var argv []string
	switch command := entry["command"].(type) {
	case string:
		argv = append(argv, command)
	case []any:
		for _, part := range command {
			if s, ok := part.(string); ok {
				argv = append(argv, s)
			}
		}
	}
	if args, ok := entry["args"].([]any); ok {
		for _, part := range args {
			if s, ok := part.(string); ok {
				argv = append(argv, s)
			}
		}
	}
	if len(argv) == 0 {
		return false
	}
	program := strings.TrimSuffix(filepath.Base(filepath.ToSlash(argv[0])), ".cmd")
	if program == "agnthub" && len(argv) > 1 && argv[1] == "mcp" {
		return true
	}
	if program != "npx" {
		return false
	}
	for i, arg := range argv[:len(argv)-1] {
		if (arg == "agnthub-cli" || strings.HasPrefix(arg, "agnthub-cli@")) && argv[i+1] == "mcp" {
			return true
		}
	}
	return false
}

// normalize converts TOML's typed values to the generic ones JSON decodes into.
func normalize(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, inner := range v {
			out[k] = normalize(inner)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, inner := range v {
			out[i] = normalize(inner)
		}
		return out
	default:
		return v
	}
}

// describe is a short account of a foreign entry: its URL or command.
func describe(entry map[string]any) string {
	if url := entryURL(entry); url != "" {
		return url
	}
	switch command := entry["command"].(type) {
	case string:
		return "command " + command
	case []any:
		if len(command) > 0 {
			if s, ok := command[0].(string); ok {
				return "command " + s
			}
		}
	}
	return "an unrecognized entry"
}

// SignIn says how to sign in to the hosted server in a Harness after an install
// without an API key; profile names a named omp profile.
func SignIn(id agent.HarnessID, profile string) string {
	s, ok := specs[id]
	if !ok {
		return ""
	}
	if id == agent.OMP && profile != "" && profile != "default" {
		return s.signIn + " (start it with `omp --profile " + profile + "`)"
	}
	return s.signIn
}

// HasCommand reports whether the Harness's own `mcp` command can configure it.
func HasCommand(id agent.HarnessID) bool {
	s, ok := specs[id]
	return ok && s.cli != nil
}
