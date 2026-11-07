package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/colorprofile"

	"github.com/agnthub-sh/agnthub/gitdiff"
	"github.com/agnthub-sh/agnthub/harness"
	"github.com/agnthub-sh/agnthub/install"
	"github.com/agnthub-sh/agnthub/skills"
	"github.com/agnthub-sh/agnthub/state"
)

// diffUsage describes --diff / -d on every command that plans and applies.
const diffUsage = "print the exact changes as a git diff and write nothing (with --yes: then apply)"

// diffJSON is the --json --diff addition to a plan: the patch as text and one
// entry per changed file.
type diffJSON struct {
	Diff  string         `json:"diff"`
	Files []diffFileJSON `json:"files"`
}

type diffFileJSON struct {
	// Path is the path shown in the diff: relative to the project root (project
	// scope) or the home directory (user scope); absolute outside them.
	Path   string `json:"path"`
	Action string `json:"action"`
	// SHA256 is the hex SHA-256 of the bytes written, or of the deleted bytes.
	SHA256 string `json:"sha256"`
}

func newDiffJSON(patch *gitdiff.Patch) *diffJSON {
	out := &diffJSON{Files: make([]diffFileJSON, 0, len(patch.Files))}
	if !patch.Empty() {
		out.Diff = patch.String()
	}
	for i := range patch.Files {
		file := &patch.Files[i]
		out.Files = append(out.Files, diffFileJSON{
			Path: file.Path, Action: string(file.Action), SHA256: file.SHA256(),
		})
	}
	return out
}

// diffNote adds context to a patch's header comments.
type diffNote struct {
	// command is the agnthub command shown in the first comment, e.g. "add".
	command string
	// skills are the Skill items the patch includes files for.
	skills []skills.Item
	// blocked reports items the patch leaves out because they are blocked.
	blocked bool
}

// diffBase is the directory diff paths are relative to: the project root, or the
// home directory in user scope.
func (a *App) diffBase(scope harness.Scope) string {
	if scope == harness.ScopeGlobal {
		return a.Env.Home()
	}
	return a.WorkDir
}

// stateDir is agnthub's private state directory, which diffs leave out.
func (a *App) stateDir() string {
	return filepath.Join(a.Env.StateHome(), "agnthub")
}

// patch turns recorded changes into the patch --diff prints.
func (a *App) patch(
	changes []state.Change,
	scope harness.Scope,
	note diffNote,
) (*gitdiff.Patch, error) {
	base := a.diffBase(scope)
	files, err := gitdiff.FromChanges(changes, gitdiff.Options{
		Base: base, Exclude: []string{a.stateDir()},
	})
	if err != nil {
		return nil, err
	}
	patch := &gitdiff.Patch{Files: files}
	if patch.Empty() {
		return patch, nil
	}
	patch.Comments = append(
		patch.Comments,
		"agnthub "+note.command+" --diff: the exact changes agnthub would make; nothing was written.",
	)
	if scope == harness.ScopeGlobal {
		patch.Comments = append(patch.Comments,
			"User scope: paths are relative to your home directory "+base+".")
	} else {
		patch.Comments = append(patch.Comments,
			"Paths are relative to the project root "+base+".")
	}
	patch.Comments = append(patch.Comments, "Check: "+applyCheck(base))
	var outside []string
	for _, file := range files {
		if filepath.IsAbs(filepath.FromSlash(file.Path)) && !slices.Contains(outside, file.Path) {
			outside = append(outside, file.Path)
		}
	}
	if len(outside) > 0 {
		patch.Comments = append(patch.Comments,
			"Outside "+base+", shown by absolute path (git apply cannot place them): "+
				strings.Join(outside, ", "))
	}
	patch.Comments = append(patch.Comments,
		"Not shown: agnthub's private state directory "+a.stateDir()+" (ownership records).")
	if installs := skillInstalls(note.skills); len(installs) > 0 {
		patch.Comments = append(patch.Comments,
			"Skill files ("+strings.Join(installs, ", ")+") are placed by the skill installer "+
				"(e.g. "+strings.Join(note.skills[0].Command, " ")+"), which may also write "+
				"its own bookkeeping (lock files, links) not shown here.")
	}
	if note.blocked {
		patch.Comments = append(patch.Comments,
			"Blocked items are left out: unattended, agnthub refuses to apply this plan.")
	}
	return patch, nil
}

// applyCheck is the git command that checks the patch against base. Inside a Git
// work tree git apply takes paths from the top of the work tree and skips the
// others, so a base below the top needs --directory.
func applyCheck(base string) string {
	for dir := base; ; {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			if dir == base {
				break
			}
			rel, err := filepath.Rel(dir, base)
			if err != nil {
				break
			}
			return "git -C " + dir + " apply --check --directory=" + filepath.ToSlash(rel)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "git -C " + base + " apply --check"
}

// skillInstalls names the Skills a plan installs, once each.
func skillInstalls(items []skills.Item) []string {
	var names []string
	for _, item := range items {
		if item.Decision == skills.DecisionInstall && !slices.Contains(names, item.Skill) {
			names = append(names, item.Skill)
		}
	}
	return names
}

// writeDiff prints patch on stdout, with standard diff colors only when stdout is
// a color terminal; NO_COLOR (even with CLICOLOR_FORCE), TERM=dumb and redirected
// output get plain text.
func (a *App) writeDiff(patch *gitdiff.Patch) error {
	if patch.Empty() {
		return nil
	}
	color := a.Env.Get("NO_COLOR") == "" &&
		colorprofile.Detect(a.Stdout, a.Env.Pairs()) > colorprofile.ASCII
	return patch.Write(a.Stdout, color)
}

// planPrinter prints plans on stdout, or on stderr when --diff owns stdout.
func (a *App) planPrinter(asJSON, diff bool) *printer {
	if diff && !asJSON {
		return a.printerTo(a.Stderr, false)
	}
	return a.printer(asJSON)
}

// applyDiff shows the exact changes of view's plan and writes nothing unless
// approved. A blocked plan shows its valid items' changes and is refused; a plan
// with nothing pending is up to date.
func (a *App) applyDiff(
	ctx context.Context,
	planner *install.Planner,
	view *planView,
	out *printer,
	approved, pending bool,
) error {
	plan := view.Plan
	blocked := plan.Blocked()
	if !blocked && !pending {
		if out.json {
			view.Diff = &diffJSON{Files: []diffFileJSON{}}
		}
		out.plan(view, statusUpToDate)
		return nil
	}
	applicable := plan
	if blocked {
		applicable, _, _ = plan.Reduce()
	}
	changes, err := planner.Preview(applicable, a.WorkDir, a.Now())
	if err != nil {
		return err
	}
	command := "add"
	if view.Report != nil {
		command = view.Report.Command
	}
	patch, err := a.patch(changes, plan.Scope, diffNote{
		command: command, skills: applicable.Skills, blocked: blocked,
	})
	if err != nil {
		return err
	}
	if out.json {
		view.Diff = newDiffJSON(patch)
	} else if err := a.writeDiff(patch); err != nil {
		return err
	}
	switch {
	case blocked:
		out.plan(view, statusRefused)
		return &ExitError{Code: 1}
	case !approved:
		out.plan(view, statusPlanned)
		return nil
	}
	return a.apply(ctx, planner, view, out)
}
