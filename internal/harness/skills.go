package harness

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// SkillLocation is one directory a Harness discovers Skills in: each child
// directory holding a SKILL.md is one Skill.
type SkillLocation struct {
	// Dir is the absolute directory holding Skill directories.
	Dir string
	// Scope is the installation scope the location belongs to.
	Scope Scope
	// Label describes the location for humans, e.g. "project .claude/skills".
	Label string
}

// SkillVisibility is a Harness's effective Skill discovery for one binding.
type SkillVisibility struct {
	// Locations are ordered by precedence: when several hold a Skill with the
	// same name, the Harness loads the first.
	Locations []SkillLocation
	// EveryCopyLoads reports a Harness that loads every same-named copy rather
	// than one winner, so each visible copy must be compatible.
	EveryCopyLoads bool
	// UniqueName reports a Harness that cannot select a Skill by name once two
	// copies are visible, so a second copy of any kind is a conflict.
	UniqueName bool
	// Hidden reports why the Harness configuration disables or hides the named
	// Skill, or "" when it is enabled. Nil hides nothing.
	Hidden func(name string) string
	// DisabledDirs are Skill directories the Harness configuration disables by path.
	DisabledDirs []string
	// Note explains locations the Harness normally discovers but skips in this
	// context, e.g. an untrusted workspace.
	Note string
}

// HiddenReason reports why the Harness would not load the named Skill from dir
// (dir may be empty), or "" when it would.
func (v SkillVisibility) HiddenReason(name, dir string) string {
	if dir != "" {
		for _, disabled := range v.DisabledDirs {
			if filepath.Clean(disabled) == filepath.Clean(dir) {
				return "the harness configuration disables " + dir
			}
		}
	}
	if v.Hidden == nil {
		return ""
	}
	return v.Hidden(name)
}

// SkillPlacement is where the Skill installer backend places Skills for a Harness.
type SkillPlacement struct {
	// Target is the backend's target identifier: the skills.sh --agent value. It
	// is not necessarily the agnthub Harness identifier.
	Target string
	// Dir is the discovery directory that Target installs <skill>/ into for the scope.
	Dir string
}

// SkillHarness is an Installer extension for Harnesses that load Skills.
type SkillHarness interface {
	// SkillPlacement resolves the backend target and destination for a scope.
	SkillPlacement(ctx Context, scope Scope, settings Settings) (SkillPlacement, error)
	// SkillVisibility resolves where an Agent installed in scope discovers Skills,
	// in the same Harness Settings context (e.g. omp profile) as the Agent.
	SkillVisibility(ctx Context, scope Scope, settings Settings) (SkillVisibility, error)
}

// scopedDir joins a project-relative directory for project scope or a
// home-relative directory for global scope.
func scopedDir(ctx Context, scope Scope, projectRel, homeRel string) (string, error) {
	switch scope {
	case ScopeProject:
		if ctx.ProjectRoot == "" {
			return "", errors.New("project scope requires a project directory")
		}
		return filepath.Join(ctx.ProjectRoot, filepath.FromSlash(projectRel)), nil
	case ScopeGlobal:
		home := ctx.Env.Home()
		if home == "" {
			return "", errors.New("global scope requires HOME")
		}
		return filepath.Join(home, filepath.FromSlash(homeRel)), nil
	default:
		return "", fmt.Errorf("unsupported scope %q", scope)
	}
}

// rel labels dir/suffix relative to the project root when dir is the root.
func rel(projectRoot, dir, suffix string) string {
	if filepath.Clean(dir) == filepath.Clean(projectRoot) {
		return suffix
	}
	return filepath.Join(dir, filepath.FromSlash(suffix))
}

// gitRoot returns the nearest ancestor of dir (inclusive) holding a .git entry.
func gitRoot(dir string) string {
	for current := filepath.Clean(dir); ; {
		if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

// ancestorsTo lists dir and its ancestors up to and including root, nearest first.
func ancestorsTo(dir, root string) []string {
	var dirs []string
	for current := filepath.Clean(dir); ; current = filepath.Dir(current) {
		dirs = append(dirs, current)
		if current == filepath.Clean(root) || filepath.Dir(current) == current {
			return dirs
		}
	}
}
