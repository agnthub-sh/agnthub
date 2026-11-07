package harness

import (
	"fmt"
	"strings"

	"github.com/agnthub-sh/agnthub/agent"
)

// Manual-only invocation and worktree isolation (ADR 0012 amendment,
// harness-agent-formats.md "Manual-only invocation and worktree isolation").

// ReducedManualOnly is the reduction of a manual-only Agent on a target the
// model may delegate to it on: only its description asks the model not to.
const ReducedManualOnly = "manual-only invocation"

// ReducedIsolation is the reduction of an isolated Agent on a target that
// cannot isolate it: it is instructed to work in a git worktree it creates.
const ReducedIsolation = "isolation"

// manualOnlyPrefix starts the description of a manual-only Agent on a target
// without a native equivalent: delegating models read the description to
// decide when to use an agent.
const manualOnlyPrefix = "Use only when the user explicitly asks for this agent."

// manualOnlyNative reports whether target keeps an agent out of the model's
// reach natively: Copilot CLI's disable-model-invocation and OpenCode's
// primary mode (runtime-verified with Copilot CLI 1.0.105 and OpenCode 2.1.3).
func manualOnlyNative(target agent.HarnessID) bool {
	return target == agent.Copilot || target == agent.OpenCode
}

// isolationNative reports whether target runs one agent in its own worktree
// from the agent definition: only Claude Code's isolation: worktree does
// (Cursor, omp and the others isolate a session or a single delegation).
func isolationNative(target agent.HarnessID) bool {
	return target == agent.Claude
}

// lowerInvocation carries a manual-only Agent to a target without a native
// equivalent: the description, which delegating models read, asks them to
// use it only on request. The Agent's tools are unchanged.
func (c *renderCheck) lowerInvocation() {
	if !c.sem.ManualOnly || manualOnlyNative(c.target) {
		return
	}
	sem := *c.sem
	sem.ManualOnly = false
	sem.Description = manualOnlyPrefix + " " + strings.TrimLeft(sem.Description, " \t\r\n")
	c.sem = &sem
	c.reduce(ReducedManualOnly, fmt.Sprintf("%s may delegate to it automatically; the "+
		"description asks it not to", c.target))
}

// lowerIsolation carries an isolated Agent to a target that cannot isolate
// it. An Agent that can neither edit files nor run commands changes nothing,
// so isolation is moot. One with a shell is told, at the top of its
// instructions, to create a git worktree and work only there. One that edits
// without a shell it can create a worktree with is refused: its edits would
// land in the user's working tree.
func (c *renderCheck) lowerIsolation() error {
	if !c.sem.Isolation || isolationNative(c.target) {
		return nil
	}
	sem := *c.sem
	sem.Isolation = false
	c.sem = &sem
	edits := sem.Tools.Allows(CapEdit)
	shell := sem.Tools.Allows(CapShell)
	if c.target == agent.Cursor && !edits {
		// An agent without edits becomes readonly on Cursor, which blocks
		// every shell command too.
		shell = false
	}
	switch {
	case !edits && !shell:
		return nil
	case !shell || sem.Tools.Shell != nil:
		why := "without a shell"
		if shell {
			why = "with a shell limited to some commands"
		}
		return c.refuse("its %s declares isolation (runs the agent in an isolated worktree), "+
			"which %s cannot express, and %s the agent cannot create a git worktree to work in",
			sem.sourceLabel(), c.target, why)
	}
	newline := "\n"
	if strings.Contains(sem.Instructions, "\r\n") {
		newline = "\r\n"
	}
	sem.Instructions = isolationInstruction(sem.ID) + newline + newline +
		strings.TrimLeft(sem.Instructions, "\r\n")
	c.reduce(ReducedIsolation, fmt.Sprintf("%s can't isolate it; the agent is instructed to "+
		"work only in a git worktree", c.target))
	return nil
}

// isolationInstruction asks the Agent to do all its work in a git worktree of
// its own. It names no harness tool: each target's shell runs the commands,
// and shells that start every command in the original directory are told to
// cd first.
func isolationInstruction(id string) string {
	return fmt.Sprintf("IMPORTANT: Work only inside a dedicated git worktree. Before "+
		"changing anything, create one from the repository root with your shell tool, "+
		"e.g. `git worktree add ../<repo>-%[1]s-<short-id> -b agent/%[1]s-<short-id>`. "+
		"Make every edit, command and commit inside it: give file tools paths inside the "+
		"worktree, and start each shell command with `cd <worktree> &&` unless your shell "+
		"keeps its directory. Never modify files in the user's original working tree. When "+
		"done, report the worktree path and branch so the user can review and merge.", id)
}
