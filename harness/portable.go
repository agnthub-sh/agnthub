package harness

import "slices"

// harmlessPortableKeys are descriptive metadata that never change runtime behavior;
// conversion omits them and reports the omission.
var harmlessPortableKeys = map[string]bool{
	"metadata": true,
	"license":  true,
	"tags":     true,
	"version":  true,
	"author":   true,
	"homepage": true,
}

// descriptiveKeys are documentation-only keys that authors put in native agent
// files and that Copilot CLI and Cursor ignore: both load an agent declaring
// them and act on none (checked against Copilot CLI 1.0.105 and Cursor
// 2026.10.22). Their authored Variants install as written, with a note. Codex
// and Gemini CLI refuse to load agents with keys they do not define, so these
// keys keep their Variants invalid there.
var descriptiveKeys = map[string]bool{
	"color":        true,
	"title":        true,
	"kind":         true,
	"emoji":        true,
	"vibe":         true,
	"category":     true,
	"status":       true,
	"domain":       true,
	"owner":        true,
	"team":         true,
	"tier":         true,
	"last_updated": true,
	"display_name": true,
	"examples":     true,
}

// otherAgentKeys are agent fields of other harnesses' Markdown formats.
var otherAgentKeys = map[string]bool{
	"mode": true, "temperature": true, "top_p": true, "steps": true, "permission": true,
	"hidden": true, "disable": true, "readonly": true, "is_background": true,
	"target": true, "max_turns": true, "timeout_mins": true, "mcp_servers": true,
	"spawns": true, "autoload-skills": true, "thinking-level": true, "output": true,
	"disable-model-invocation": true, "user-invocable": true, "allowed-tools": true,
}

// KnownAgentKey reports whether key is a frontmatter field of some harness's
// agent format, or metadata authors commonly put in one: what a catalog
// layout's candidate files are expected to consist of (ADR 0017).
func KnownAgentKey(key string) bool {
	return slices.Contains(claudeFields, key) || descriptiveKeys[key] ||
		harmlessPortableKeys[key] || otherAgentKeys[key]
}
