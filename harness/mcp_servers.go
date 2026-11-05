package harness

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Agent-scoped MCP servers (ADR 0012) carry only to Gemini CLI, whose local
// agents declare their own servers in mcp_servers. A declaration converts
// when every setting has a Gemini CLI equivalent; anything else, including a
// reference to a server configured elsewhere, keeps the Agent refused.

// claudeMCPServers reads Claude Code's mcpServers: a list whose entries are
// a server name (a server configured in Claude Code) or a one-key map from
// a name to an inline configuration; a map of names is read the same way.
// It returns why a declaration cannot be read, or "".
func claudeMCPServers(value any) ([]MCPServer, string) {
	var entries []any
	switch value := value.(type) {
	case []any:
		entries = value
	case map[string]any:
		for _, name := range slices.Sorted(maps.Keys(value)) {
			entries = append(entries, map[string]any{name: value[name]})
		}
	default:
		return nil, "a value agnthub cannot read"
	}
	servers := make([]MCPServer, 0, len(entries))
	for _, entry := range entries {
		switch entry := entry.(type) {
		case string:
			return nil, fmt.Sprintf("it references %q, a server configured in Claude Code "+
				"that other harnesses cannot look up", entry)
		case map[string]any:
			if len(entry) != 1 {
				return nil, "an entry agnthub cannot read"
			}
			for name, config := range entry {
				server, reason := readMCPServer(name, config, claudeMCPKeys, claudeTransports)
				if reason != "" {
					return nil, reason
				}
				servers = append(servers, server)
			}
		default:
			return nil, "an entry agnthub cannot read"
		}
	}
	return servers, ""
}

// copilotMCPServers reads Copilot CLI's mcp-servers: a map from server names
// to configurations.
func copilotMCPServers(value any) ([]MCPServer, string) {
	configs, ok := value.(map[string]any)
	if !ok {
		return nil, "a value agnthub cannot read"
	}
	servers := make([]MCPServer, 0, len(configs))
	for _, name := range slices.Sorted(maps.Keys(configs)) {
		server, reason := readMCPServer(name, configs[name], copilotMCPKeys, copilotTransports)
		if reason != "" {
			return nil, reason
		}
		servers = append(servers, server)
	}
	return servers, ""
}

// claudeMCPKeys and copilotMCPKeys are the server settings with a Gemini CLI
// equivalent, per source.
var (
	claudeMCPKeys  = []string{"type", "command", "args", "env", "url", "headers"}
	copilotMCPKeys = []string{"type", "command", "args", "env", "cwd", "url", "headers", "tools"}
)

// claudeTransports and copilotTransports map each source's type values to
// a transport; "" is a server without a type.
var (
	claudeTransports = map[string]string{
		"": "stdio", "stdio": "stdio", "http": "http", "sse": "sse",
	}
	copilotTransports = map[string]string{
		"": "stdio", "local": "stdio", "stdio": "stdio", "http": "http", "sse": "sse",
	}
)

// readMCPServer reads one server configuration, or returns why it cannot be
// carried.
func readMCPServer(
	name string,
	value any,
	keys []string,
	transports map[string]string,
) (MCPServer, string) {
	config, ok := value.(map[string]any)
	if !ok || name == "" {
		return MCPServer{}, fmt.Sprintf("server %q has a configuration agnthub cannot read", name)
	}
	for _, key := range slices.Sorted(maps.Keys(config)) {
		if !slices.Contains(keys, key) {
			return MCPServer{}, fmt.Sprintf("server %q sets %s, which Gemini CLI has no "+
				"equivalent for", name, key)
		}
	}
	kind, _ := config["type"].(string)
	server := MCPServer{Name: name, Transport: transports[kind]}
	if server.Transport == "" {
		return MCPServer{}, fmt.Sprintf("server %q has type %q, which agnthub cannot carry",
			name, kind)
	}
	var reason string
	read := func(key string, into *string) {
		if v, set := config[key]; set && reason == "" {
			text, isString := v.(string)
			if !isString || text == "" {
				reason = fmt.Sprintf("server %q: %s must be a string", name, key)
			}
			*into = text
		}
	}
	read("command", &server.Command)
	read("url", &server.URL)
	read("cwd", &server.Cwd)
	if reason == "" {
		server.Args, reason = mcpStrings(name, "args", config["args"])
	}
	if reason == "" {
		server.Env, reason = mcpStringMap(name, "env", config["env"])
	}
	if reason == "" {
		server.Headers, reason = mcpStringMap(name, "headers", config["headers"])
	}
	if reason == "" {
		server.Tools, reason = mcpStrings(name, "tools", config["tools"])
		if slices.Contains(server.Tools, "*") {
			server.Tools = nil
		}
	}
	switch {
	case reason != "":
	case server.Transport == "stdio" && (server.Command == "" || server.URL != ""):
		reason = fmt.Sprintf("server %q: a local server needs a command and no url", name)
	case server.Transport != "stdio" && (server.URL == "" || server.Command != ""):
		reason = fmt.Sprintf("server %q: a remote server needs a url and no command", name)
	}
	if reason == "" {
		reason = mcpPortableValues(server)
	}
	return server, reason
}

func mcpStrings(server, key string, value any) ([]string, string) {
	if value == nil {
		return nil, ""
	}
	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Sprintf("server %q: %s must be a list of strings", server, key)
	}
	out := make([]string, len(list))
	for i, item := range list {
		text, isString := item.(string)
		if !isString {
			return nil, fmt.Sprintf("server %q: %s must be a list of strings", server, key)
		}
		out[i] = text
	}
	return out, ""
}

func mcpStringMap(server, key string, value any) (map[string]string, string) {
	if value == nil {
		return nil, ""
	}
	entries, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Sprintf("server %q: %s must map names to strings", server, key)
	}
	out := make(map[string]string, len(entries))
	for name, item := range entries {
		text, isString := item.(string)
		if !isString {
			return nil, fmt.Sprintf("server %q: %s must map names to strings", server, key)
		}
		out[name] = text
	}
	return out, ""
}

// mcpPortableValues refuses values whose substitution syntax Gemini CLI does
// not share: Claude Code's ${VAR:-default} and GitHub's ${{ secrets.X }}.
// Plain $VAR and ${VAR} expand from the environment in every harness.
func mcpPortableValues(server MCPServer) string {
	values := slices.Concat([]string{server.Command, server.URL, server.Cwd}, server.Args,
		sortedValues(server.Env), sortedValues(server.Headers))
	for _, value := range values {
		if strings.Contains(value, "${{") || strings.Contains(value, ":-") &&
			strings.Contains(value, "${") {
			return fmt.Sprintf("server %q uses a substitution Gemini CLI does not expand: %s",
				server.Name, value)
		}
	}
	return ""
}

// sortedValues lists m's values in key order, so the first bad value named is
// the same on every run.
func sortedValues(m map[string]string) []string {
	values := make([]string, 0, len(m))
	for _, key := range slices.Sorted(maps.Keys(m)) {
		values = append(values, m[key])
	}
	return values
}

// geminiMCPServers renders servers in Gemini CLI's mcp_servers form.
func geminiMCPServers(servers []MCPServer) map[string]any {
	out := make(map[string]any, len(servers))
	for _, server := range servers {
		config := map[string]any{}
		switch server.Transport {
		case "stdio":
			config["command"] = server.Command
		case "http":
			config["httpUrl"] = server.URL
		case "sse":
			config["url"] = server.URL
		}
		if len(server.Args) > 0 {
			config["args"] = server.Args
		}
		if len(server.Env) > 0 {
			config["env"] = server.Env
		}
		if server.Cwd != "" {
			config["cwd"] = server.Cwd
		}
		if len(server.Headers) > 0 {
			config["headers"] = server.Headers
		}
		if server.Tools != nil {
			config["includeTools"] = server.Tools
		}
		out[server.Name] = config
	}
	return out
}
