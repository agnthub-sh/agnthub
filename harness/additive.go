package harness

import (
	"fmt"
	"slices"
	"strings"
)

// MemoryScope is where an Agent's persistent memory lives, with Claude Code's
// meaning: user (every project), project (shared through version control) or
// local (this project and machine, kept out of version control).
type MemoryScope string

// Memory scopes.
const (
	MemoryUser    MemoryScope = "user"
	MemoryProject MemoryScope = "project"
	MemoryLocal   MemoryScope = "local"
)

// readMemoryScope reads a memory scope the way Claude Code 2.1.309 does: any
// other value is ignored with a warning, leaving the agent without memory.
func readMemoryScope(value any) (MemoryScope, bool) {
	text, _ := value.(string)
	switch scope := MemoryScope(text); scope {
	case MemoryUser, MemoryProject, MemoryLocal:
		return scope, true
	}
	return "", false
}

// claudeUserMemory is the folder of Claude Code's user-scope agent memories.
const claudeUserMemory = "~/.claude/agent-memory/"

// claudeMaxSkills is how many distinct skills Claude Code 2.1.309 preloads.
const claudeMaxSkills = 32

// readSkills reads a preloaded skills list: distinct names in authored order.
func readSkills(sem *Semantics, field string, value any) {
	names, ok := nameList(value)
	if !ok {
		sem.unsupported(field, "a skills list agnthub cannot read")
		return
	}
	for _, name := range names {
		if name != "" && !slices.Contains(sem.Skills, name) {
			sem.Skills = append(sem.Skills, name)
		}
	}
}

// memoryDir is the folder holding the Agent's notes: Claude Code's own folder
// for project and local scope, so both harnesses share the Agent's notes, and
// the target's user folder for user scope. The name is sanitized like Claude
// Code does.
func memoryDir(scope MemoryScope, id, userDir string) string {
	name := strings.Map(func(r rune) rune {
		if r == '_' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, id)
	switch scope {
	case MemoryUser:
		return userDir + name + "/"
	case MemoryLocal:
		return ".claude/agent-memory-local/" + name + "/"
	default:
		return ".claude/agent-memory/" + name + "/"
	}
}

// additiveSupport is how a target carries persistent memory and preloaded
// skills, which add to what an Agent knows but never to what it may do.
type additiveSupport struct {
	// nativeMemory and nativeSkills report that the target writes
	// Semantics.Memory or Semantics.Skills into its own fields.
	nativeMemory, nativeSkills bool
	// userMemory is the folder holding user-scope memories on the target, or
	// "" when its agents cannot write outside the project without approval;
	// noUserMemory then says why.
	userMemory, noUserMemory string
	// loadSkill tells an agent on the target how to load a skill by name.
	loadSkill string
	// sessionSkills reports that the target offers skills to every agent,
	// whatever its tool policy.
	sessionSkills bool
	// tools is the tool policy the target renders.
	tools ToolPolicy
}

// skillTools returns the tool policy to render. Claude Code injects preloaded
// skills without its Skill tool; a target whose agents load them on demand
// gets its skill tool added to an allowlist, which never widens what the
// Agent can affect.
func (c *renderCheck) skillTools() ToolPolicy {
	tools := c.sem.Tools
	if len(c.sem.Skills) > 0 && tools.Restricted && tools.Allowed&CapSkill == 0 {
		tools.Allowed |= CapSkill
		c.skillGranted = true
	}
	return tools
}

// carry returns the instructions to render: the Agent's own, unchanged unless
// the target must emulate memory or preloaded skills, which then follow as
// clearly delimited sections. Every outcome other than a native field is
// noted. Memory is only emulated when the Agent can already read and edit the
// files, so it never widens tool access.
func (c *renderCheck) carry(support additiveSupport) string {
	sections := make([]string, 0, 2)
	if section := c.memorySection(support); section != "" {
		sections = append(sections, section)
	}
	if section := c.skillsSection(support); section != "" {
		sections = append(sections, section)
	}
	if len(sections) == 0 {
		return c.sem.Instructions
	}
	text := strings.Join(sections, "\n")
	newline := "\n"
	if strings.Contains(c.sem.Instructions, "\r\n") {
		newline = "\r\n"
		text = strings.ReplaceAll(text, "\n", newline)
	}
	return strings.TrimRight(c.sem.Instructions, "\r\n") + newline + newline + text
}

// memoryScopeLines are Claude Code's per-scope memory guidelines (2.1.309).
var memoryScopeLines = map[MemoryScope]string{
	MemoryUser: "This memory is user-scope, so keep learnings general since they apply " +
		"across all projects.",
	MemoryProject: "This memory is project-scope and shared with your team via version " +
		"control, so tailor your memories to this project.",
	MemoryLocal: "This memory is local-scope (not checked into version control), so tailor " +
		"your memories to this project and machine.",
}

// memoryWhere completes a memory note with where the notes live.
var memoryWhere = map[MemoryScope]string{
	MemoryUser: "in the user's home directory, for every project",
	MemoryProject: "Claude Code's folder, inside the project and tracked by version control " +
		"unless ignored",
	MemoryLocal: "Claude Code's folder; agnthub does not add it to .gitignore",
}

func (c *renderCheck) memorySection(support additiveSupport) string {
	scope := c.sem.Memory
	if scope == "" || support.nativeMemory {
		return ""
	}
	const lost = "the agent starts each session without its notes"
	switch {
	case support.tools.reach()&CapRead == 0 || !support.tools.Allows(CapEdit):
		c.note("persistent memory (memory: %s) isn't available on %s: the agent cannot read "+
			"and edit files, and agnthub never grants tools for memory; %s", scope, c.target, lost)
		return ""
	case scope == MemoryUser && support.userMemory == "":
		c.note("persistent memory (memory: %s) isn't available on %s: %s; %s",
			scope, c.target, support.noUserMemory, lost)
		return ""
	}
	dir := memoryDir(scope, c.sem.ID, support.userMemory)
	c.memoryDir = dir
	c.note("persistent memory (memory: %s) emulated: %s has no per-agent memory, so the "+
		"instructions keep the agent's notes in %s (%s)",
		scope, c.target, dir, memoryWhere[scope])
	where := "at the project root"
	if scope == MemoryUser {
		where = "in your home directory"
	}
	return fmt.Sprintf(
		`<!-- agnthub: persistent memory (memory: %[1]s, converted from the %[2]s) -->
# Persistent Agent Memory

You have a persistent, file-based memory in `+"`%[3]s`"+` %[4]s. Use it to build up knowledge across conversations: what you learn about the user, their feedback on your work, and project context that the code and its history don't show.

- At the start of each task, read `+"`%[3]sMEMORY.md`"+` if it exists and check your memory before you start working. If the user asks you to remember something, save it right away; if they ask you to forget something, remove it.
- Save each memory to its own short file in that folder (for example `+"`feedback_testing.md`"+`), then add a one-line pointer to it in `+"`MEMORY.md`: `- [Title](file.md) — one-line hook`"+`. `+"`MEMORY.md`"+` is an index, not a memory: keep it under 200 lines. Create the folder if it does not exist.
- Update or remove memories that turn out to be wrong or outdated, and don't write duplicates. Don't save what the code, git history or project instructions already say, or what only matters for the current task, and never save secrets.
- %[5]s
<!-- /agnthub -->
`,
		scope,
		c.sem.sourceLabel(),
		dir,
		where,
		memoryScopeLines[scope],
	)
}

func (c *renderCheck) skillsSection(support additiveSupport) string {
	if len(c.sem.Skills) == 0 {
		return ""
	}
	names := strings.Join(c.sem.Skills, ", ")
	need := fmt.Sprintf("they must be installed for %s: agnthub does not install skills "+
		"from this list", c.target)
	if support.nativeSkills {
		c.note("preloads skills %s; %s", names, need)
		return ""
	}
	if !support.sessionSkills && !support.tools.Allows(CapSkill) {
		c.note("preloaded skills %s aren't available on %s: it cannot preload skills and the "+
			"agent withholds its skill tool", names, c.target)
		return ""
	}
	granted := ""
	if c.skillGranted {
		granted = " (its skill tool is allowed)"
	}
	c.note("preloaded skills %s: %s cannot preload skills, so the instructions tell the agent "+
		"to load them%s; %s", names, c.target, granted, need)
	quoted := make([]string, len(c.sem.Skills))
	for i, name := range c.sem.Skills {
		quoted[i] = "`" + name + "`"
	}
	return fmt.Sprintf(`<!-- agnthub: preloaded skills (converted from the %s) -->
# Skills

Before you start a task, load these skills and follow them: %s. %s If one of them isn't available, continue without it.
<!-- /agnthub -->
`, c.sem.sourceLabel(), strings.Join(quoted, ", "), support.loadSkill)
}
