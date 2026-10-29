package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/heynemann/agnthub/internal/adoption"
	"github.com/heynemann/agnthub/internal/agent"
	"github.com/heynemann/agnthub/internal/harness"
	"github.com/heynemann/agnthub/internal/platform/env"
)

// Apply failures. Either one means no dependent Agent may be written.
var (
	// ErrBackend reports a Skill installer that could not run or exited nonzero.
	ErrBackend = errors.New("skill installer failed")
	// ErrVerification reports installed Skills that are missing, different,
	// misplaced or not the copy the Harness loads, whatever the backend reported.
	ErrVerification = errors.New("skill verification failed")
)

// RunResult reports one backend invocation.
type RunResult struct {
	Skill     string
	Path      string
	Command   []string
	Harnesses []agent.HarnessID
	ExitCode  int
	// Error is set when the installer could not start or exited nonzero.
	Error string
	// Output is the tail of the installer's combined output.
	Output string
}

// Effect is one change observed in a watched Skill location during the backend
// runs. agnthub reports effects; it does not roll back a configured backend.
type Effect struct {
	Path   string
	Change string
}

// Effect changes.
const (
	EffectCreated = "created"
	EffectChanged = "changed"
	EffectRemoved = "removed"
)

// Outcome reports what applying Skill items did and proved.
type Outcome struct {
	Runs []RunResult
	// Failures explain each verification failure.
	Failures []string
	// Effects are the changes observed in watched locations, sorted by path.
	Effects []Effect
	// Items are the verified items, ready to be recorded.
	Items []Item
}

// Applier runs the trusted backend for planned installs and verifies every item.
type Applier struct {
	Registry *harness.Registry
	Context  harness.Context
	Scope    harness.Scope
}

const outputTail = 8 << 10

// Apply runs the backend once per distinct install directory, in plan order,
// stopping at the first failure, then independently verifies every applicable
// item against what its Harness now discovers. Blocking items are ignored: the
// caller only applies plans whose dependents of blocked Skills were removed.
func (a *Applier) Apply(ctx context.Context, items []Item) (*Outcome, error) {
	outcome := &Outcome{}
	var applicable []Item
	for _, item := range items {
		if !item.Blocking() {
			applicable = append(applicable, item)
		}
	}
	runs := groupRuns(applicable)
	if len(runs) == 0 {
		return outcome, a.verify(outcome, applicable)
	}
	watched := a.watchSet(applicable)
	before := snapshot(a.Context.ProjectRoot, watched)
	var runErr error
	for _, run := range runs {
		result := a.run(ctx, run)
		outcome.Runs = append(outcome.Runs, result)
		if result.Error != "" {
			runErr = fmt.Errorf("%w: %s for %s: %s", ErrBackend, run.Skill,
				strings.Join(harnessNames(run.Harnesses), ", "), result.Error)
			break
		}
	}
	outcome.Effects = diff(before, snapshot(a.Context.ProjectRoot, watched))
	if runErr != nil {
		return outcome, runErr
	}
	return outcome, a.verify(outcome, applicable)
}

type backendRun struct {
	Skill     string
	Path      string
	Command   []string
	Harnesses []agent.HarnessID
}

// groupRuns installs each directory once, even when several Harnesses share it.
func groupRuns(items []Item) []backendRun {
	var runs []backendRun
	index := map[string]int{}
	for _, item := range items {
		if item.Decision != DecisionInstall {
			continue
		}
		if i, ok := index[item.Path]; ok {
			runs[i].Harnesses = append(runs[i].Harnesses, item.Harness)
			continue
		}
		index[item.Path] = len(runs)
		runs = append(runs, backendRun{
			Skill: item.Skill, Path: item.Path, Command: item.Command,
			Harnesses: []agent.HarnessID{item.Harness},
		})
	}
	return runs
}

func harnessNames(ids []agent.HarnessID) []string {
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		names = append(names, string(id))
	}
	return names
}

func (a *Applier) run(ctx context.Context, run backendRun) RunResult {
	result := RunResult{
		Skill: run.Skill, Path: run.Path, Command: run.Command, Harnesses: run.Harnesses,
	}
	executable, err := LookPath(run.Command[0], a.Context.Env)
	if err != nil {
		result.ExitCode, result.Error = -1, err.Error()
		return result
	}
	// The configured backend is trusted user code: it runs without a shell, with
	// the user's environment. When the user opted out of sharing, every opt-out
	// variable is set (DO_NOT_TRACK=1, DISABLE_TELEMETRY=1) so skills.sh or any
	// other backend disables its own telemetry too.
	//nolint:gosec // G204: executable and argument templates come from the user's own config
	cmd := exec.CommandContext(ctx, executable, run.Command[1:]...)
	cmd.Env = adoption.ChildEnv(a.Context.Env).Pairs()
	cmd.Dir = a.Context.ProjectRoot
	output := &tailBuffer{limit: outputTail}
	cmd.Stdout, cmd.Stderr = output, output
	err = cmd.Run()
	result.Output = output.String()
	if err != nil {
		result.ExitCode = -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		}
		result.Error = err.Error()
	}
	return result
}

// LookPath resolves the configured executable against the environment's PATH.
func LookPath(executable string, e env.Env) (string, error) {
	if filepath.IsAbs(executable) {
		return executable, nil
	}
	for _, dir := range filepath.SplitList(e.Get("PATH")) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		candidate := filepath.Join(dir, executable)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() &&
			info.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("skill installer %q was not found in PATH", executable)
}

// verify proves each applicable item from disk, whatever the backend reported:
// the copy the Harness loads in this binding must carry the required identity
// and version, be the content agnthub validated or reused, and not be disabled.
// An install must also have placed the exact content of the validated source
// revision at its planned path, in the requested scope.
func (a *Applier) verify(outcome *Outcome, items []Item) error {
	accepted := map[string]map[string]bool{}
	for _, item := range items {
		if accepted[item.Skill] == nil {
			accepted[item.Skill] = map[string]bool{}
		}
		accepted[item.Skill][item.Digest] = true
	}
	for _, item := range items {
		if why := a.verifyItem(&item, accepted[item.Skill]); why != "" {
			outcome.Failures = append(outcome.Failures,
				fmt.Sprintf("%s for %s: %s", item.Skill, item.Harness, why))
			continue
		}
		outcome.Items = append(outcome.Items, item)
	}
	if len(outcome.Failures) > 0 {
		return fmt.Errorf("%w: %s", ErrVerification, strings.Join(outcome.Failures, "; "))
	}
	return nil
}

func (a *Applier) verifyItem(item *Item, accepted map[string]bool) string {
	installer, err := a.Registry.Installer(item.Harness)
	if err != nil {
		return err.Error()
	}
	skillHarness, ok := installer.(harness.SkillHarness)
	if !ok {
		return installer.Name() + " does not support skill dependencies"
	}
	visibility, err := skillHarness.SkillVisibility(a.Context, a.Scope, item.Settings)
	if err != nil {
		return err.Error()
	}
	candidates := scan(visibility, item.Skill)
	if len(candidates) == 0 {
		return fmt.Sprintf("not found in any location %s discovers (expected %s)",
			item.Harness, item.Path)
	}
	if item.Decision == DecisionInstall {
		if why := verifyInstalled(item, candidates); why != "" {
			return why
		}
	}
	loaded := candidates[:1]
	if visibility.EveryCopyLoads || visibility.UniqueName {
		loaded = candidates
	}
	if visibility.UniqueName && len(candidates) > 1 {
		return fmt.Sprintf("%s now sees %d copies and cannot select it by name",
			item.Harness, len(candidates))
	}
	for _, c := range loaded {
		if why := acceptable(item, c, accepted); why != "" {
			return fmt.Sprintf("%s loads %s, which %s", item.Harness, c.path, why)
		}
		if reason := visibility.HiddenReason(item.Skill, c.path); reason != "" {
			return reason
		}
	}
	item.Version = candidates[0].content.Version
	return ""
}

// verifyInstalled checks that the planned path holds the validated revision's
// exact content in the requested scope.
func verifyInstalled(item *Item, candidates []candidate) string {
	for _, c := range candidates {
		if filepath.Clean(c.path) != filepath.Clean(item.Path) {
			continue
		}
		switch {
		case c.err != nil:
			return fmt.Sprintf("%s cannot be read: %v", c.path, c.err)
		case c.content.Digest != item.Digest:
			return fmt.Sprintf("%s does not hold the content of %s", c.path,
				revisionLabel(item.Source, item.Revision))
		case c.location.Scope != item.Scope:
			return fmt.Sprintf("%s is in %s scope, not %s", c.path, c.location.Scope, item.Scope)
		}
		return ""
	}
	return fmt.Sprintf("the installer did not create %s", item.Path)
}

// acceptable re-applies the plan's compatibility rules to a loaded copy without
// fetching: identity, every bound, and for unbounded requirements content agnthub
// validated (the source revision) or reused.
func acceptable(item *Item, c candidate, accepted map[string]bool) string {
	if c.err != nil {
		return "cannot be read: " + c.err.Error()
	}
	if c.content.Name != item.Skill {
		return fmt.Sprintf("is named %q", c.content.Name)
	}
	bounded := false
	for _, bounds := range item.bounds {
		if bounds.Bounded() {
			bounded = true
			if why := bounds.Check(c.content.Version, c.content.HasVersion); why != "" {
				return why
			}
		}
	}
	if !bounded && !accepted[c.content.Digest] {
		return "holds content agnthub neither validated nor reused"
	}
	return ""
}

// watchSet lists the directories whose children are compared before and after
// the backend runs: every Skill location of the involved bindings, plus the
// project root and ".agents" directories, where skills.sh keeps its lock files.
func (a *Applier) watchSet(items []Item) []string {
	var dirs []string
	add := func(dir string) {
		if dir != "" && !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	add(a.Context.ProjectRoot)
	for _, item := range items {
		installer, err := a.Registry.Installer(item.Harness)
		if err != nil {
			continue
		}
		skillHarness, ok := installer.(harness.SkillHarness)
		if !ok {
			continue
		}
		visibility, err := skillHarness.SkillVisibility(a.Context, a.Scope, item.Settings)
		if err != nil {
			continue
		}
		for _, location := range visibility.Locations {
			add(location.Dir)
			if parent := filepath.Dir(location.Dir); filepath.Base(parent) == ".agents" {
				add(parent)
			}
		}
		if item.Path != "" {
			add(filepath.Dir(item.Path))
		}
	}
	return dirs
}

// snapshot digests each child of the watched directories. Child directories of
// the project root are not descended into: only its files are watched there.
func snapshot(projectRoot string, dirs []string) map[string]string {
	state := map[string]string{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			info, err := os.Stat(path)
			if err != nil {
				state[path] = "unreadable"
				continue
			}
			switch {
			case info.Mode().IsRegular():
				state[path] = fileState(path)
			case info.IsDir() && dir != projectRoot && !slices.Contains(dirs, path):
				state[path] = dirState(path)
			}
		}
	}
	return state
}

func fileState(path string) string {
	data, err := readLimited(path, maxSkillBytes)
	if err != nil {
		return "unreadable"
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// dirState digests every file below dir, following links within the same limits
// used for Skills.
func dirState(dir string) string {
	h := sha256.New()
	files := 0
	var walk func(rel string, depth int) bool
	walk = func(rel string, depth int) bool {
		if depth > maxSkillDepth {
			return false
		}
		entries, err := os.ReadDir(filepath.Join(dir, rel))
		if err != nil {
			return false
		}
		for _, entry := range entries {
			child := filepath.Join(rel, entry.Name())
			info, err := os.Stat(filepath.Join(dir, child))
			if err != nil {
				return false
			}
			if info.IsDir() {
				h.Write([]byte("d:" + child + "\x00"))
				if !walk(child, depth+1) {
					return false
				}
				continue
			}
			files++
			if files > maxSkillFiles {
				return false
			}
			h.Write([]byte("f:" + child + "\x00" + fileState(filepath.Join(dir, child)) + "\n"))
		}
		return true
	}
	if !walk(".", 0) {
		return "unreadable"
	}
	return hex.EncodeToString(h.Sum(nil))
}

func diff(before, after map[string]string) []Effect {
	var effects []Effect
	for path, state := range after {
		previous, existed := before[path]
		switch {
		case !existed:
			effects = append(effects, Effect{Path: path, Change: EffectCreated})
		case previous != state:
			effects = append(effects, Effect{Path: path, Change: EffectChanged})
		}
	}
	for path := range before {
		if _, exists := after[path]; !exists {
			effects = append(effects, Effect{Path: path, Change: EffectRemoved})
		}
	}
	slices.SortFunc(effects, func(a, b Effect) int { return strings.Compare(a.Path, b.Path) })
	return effects
}

// tailBuffer keeps at most limit bytes of the most recent output.
type tailBuffer struct {
	buf   []byte
	limit int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	if over := len(b.buf) - b.limit; over > 0 {
		b.buf = append(b.buf[:0], b.buf[over:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	return string(b.buf)
}
