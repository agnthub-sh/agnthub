package harness

import (
	"strings"

	"github.com/agnthub-sh/agnthub/agent"
)

// Display names (ADR 0017, harness-agent-formats.md "Agent names"). A file
// agnthub generates names the agent by its identifier wherever the harness
// invokes agents by name (Claude Code, Codex, Cursor, Gemini CLI, omp): two
// agents with one label would otherwise shadow each other, and delegation
// lists, conflicts and the lock all use the identifier. Copilot CLI's name
// and Gemini CLI's display_name are labels, so they keep it; on the other
// targets it leads the description, which is what models read to choose an
// agent.

// displayNameNative reports whether target has a field that shows a label
// without identifying the agent.
func displayNameNative(target agent.HarnessID) bool {
	return target == agent.Copilot || target == agent.Gemini
}

// InvocationName is the name the harness loads the agent file at filePath
// under, when it invokes agents by name (Claude Code, Codex, Cursor, Gemini
// CLI, omp): its declared name, display names included. It is "" for a
// file the harness identifies by its path (Copilot CLI, OpenCode) or that
// names no agent.
func InvocationName(adapter Adapter, filePath string, raw []byte) string {
	switch adapter.ID() {
	case agent.Copilot, agent.OpenCode:
		return ""
	}
	identity, _ := adapter.Identify(filePath, raw)
	if identity.DisplayName != "" {
		return identity.DisplayName
	}
	return identity.ID
}

// lowerDisplayName carries the source's display name to a target without a
// display field: it leads the description, unless the description already
// says it or it only spells the identifier ("Code Reviewer" for
// code-reviewer).
func (c *renderCheck) lowerDisplayName() {
	label := c.sem.DisplayName
	if label == "" || displayNameNative(c.target) {
		return
	}
	sem := *c.sem
	sem.DisplayName = ""
	c.sem = &sem
	description := strings.TrimLeft(sem.Description, " \t\r\n")
	if agent.NormalizeID(label) == sem.ID ||
		strings.Contains(strings.ToLower(description), strings.ToLower(label)) {
		c.note("display name %q: the agent is named %s", label, sem.ID)
		return
	}
	sem.Description = label + ": " + description
	c.note("display name %q: the agent is named %s and the description starts with the "+
		"display name", label, sem.ID)
}
