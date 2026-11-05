package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agnthub-sh/agnthub/harness"
	"github.com/agnthub-sh/agnthub/state"
)

// The home directory is no project. Harnesses disagree about project files there:
// omp, OpenCode and Copilot CLI (outside Git repositories) walk up from the
// directory they run in and apply them to projects below the home directory, often
// ahead of the user's own agents, while ~/.claude/agents and ~/.gemini/agents are
// those harnesses' user directories. So commands run in the home directory (or
// above it) act on user scope instead. See docs/research/harness-agent-formats.md.

// errHomeProject refuses an explicit project-scope request in the home directory.
var errHomeProject = errors.New("refusing to install project-scope files into your " +
	"home directory: it is no project, and harnesses would apply files there to " +
	"projects below it or read them as user configuration. Run agnthub in the " +
	"project's directory, or install for your user with --global")

// inHome reports whether the working directory is the user's home directory or one
// of its ancestors, where no project can be.
func (a *App) inHome() bool {
	return homeOrAbove(a.WorkDir, a.Env.Home())
}

// homeOrAbove reports whether dir is home or one of its ancestors. Both are compared
// as files, so symbolic links and case-insensitive file systems do not hide a match.
func homeOrAbove(dir, home string) bool {
	if dir == "" || !filepath.IsAbs(home) {
		return false
	}
	target, err := os.Stat(dir)
	if err != nil {
		return false
	}
	for path := filepath.Clean(home); ; path = filepath.Dir(path) {
		if info, err := os.Stat(path); err == nil && os.SameFile(target, info) {
			return true
		}
		if filepath.Dir(path) == path {
			return false
		}
	}
}

// addScope is the scope add installs into. In the home directory an install without
// --global goes to user scope, saying so, and an explicit project-scope request
// (--global=false, or scope "project" over MCP) is refused.
func (a *App) addScope(global, explicitProject bool) (harness.Scope, error) {
	if global {
		return harness.ScopeGlobal, nil
	}
	if !a.inHome() {
		return harness.ScopeProject, nil
	}
	if explicitProject {
		return "", errHomeProject
	}
	a.notice(fmt.Sprintf("%s is your home directory, not a project: installing "+
		"into user scope (as --global does). Run agnthub in a project's directory to "+
		"install there.", a.WorkDir))
	return harness.ScopeGlobal, nil
}

// lockScope reports whether list, check, update, restore and remove use the
// user-scope lock. In the home directory they do, unless a project lock there still
// records installations (agnthub 0.4.7 and earlier wrote them): that lock is used so
// they can be listed, checked and removed, but commands that write agent files
// (update, restore) refuse to write into the home directory again.
func (a *App) lockScope(global, writes bool) (bool, error) {
	if global || !a.inHome() {
		return global, nil
	}
	path := state.LockPath(a.WorkDir, a.Env.ConfigHome(), false)
	lock, err := state.LoadLock(path)
	if err != nil {
		return false, err
	}
	if len(lock.Agents) == 0 && len(lock.Skills) == 0 {
		a.notice(fmt.Sprintf("%s is your home directory, not a project: using "+
			"user scope (as --global does).", a.WorkDir))
		return true, nil
	}
	if writes {
		return false, fmt.Errorf("%s records project-scope installations in your home "+
			"directory, which earlier agnthub versions wrote there; refusing to write "+
			"there again. Remove them with `agnthub remove` in this directory and "+
			"reinstall with --global", path)
	}
	a.notice(fmt.Sprintf("%s records project-scope installations in your home "+
		"directory, which earlier agnthub versions wrote there. Harnesses apply them to "+
		"some projects below it or read them as user configuration; remove them with "+
		"`agnthub remove` in this directory and reinstall with --global.", path))
	return false, nil
}

// notice says on stderr why a command acts in another scope than its flags ask for.
func (a *App) notice(message string) {
	fmt.Fprintln(a.Stderr, "note:", message)
}
