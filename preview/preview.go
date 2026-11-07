// Package preview computes, in memory, the exact files `agnthub add` would create
// on a machine where nothing is installed yet: the same discovery, dependency
// resolution, conversion, skill planning and apply code as the CLI, run against
// a recorder and an empty, never-created project and home directory. It reads
// sources only through the given snapshot and opener, and never writes.
package preview

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/discovery"
	"github.com/agnthub-sh/agnthub/gitdiff"
	"github.com/agnthub-sh/agnthub/harness"
	"github.com/agnthub-sh/agnthub/install"
	"github.com/agnthub-sh/agnthub/platform/env"
	"github.com/agnthub-sh/agnthub/resolve"
	"github.com/agnthub-sh/agnthub/skills"
	"github.com/agnthub-sh/agnthub/source"
	"github.com/agnthub-sh/agnthub/state"
)

// AllAgents selects every agent of the source, as `--agent '*'` does.
const AllAgents = "*"

// Request is one `agnthub add <source> --agent … --harness … [--global]`.
type Request struct {
	// Snapshot is the open root source; its locator and revision are what the lock records.
	Snapshot *source.Snapshot
	// Agents are agent IDs, or AllAgents.
	Agents    []string
	Harnesses []agent.HarnessID
	Global    bool
	// Home is the home directory the install pretends to run in; it must not exist
	// here. A user-scope lock records absolute paths below it. Empty is a random
	// directory.
	Home string
	// Opener opens dependency and skill sources; a source it cannot open fails the
	// resolution (agents) or blocks the skill, as in the CLI.
	Opener resolve.Opener
	// Registry is nil for harness.Default().
	Registry *harness.Registry
	// AllowWithoutShell is --allow-without-shell.
	AllowWithoutShell bool
}

// Result is what the install would do on a fresh machine.
type Result struct {
	// Files are the files the install creates: paths relative to the project root,
	// or to the home directory for a global install. agnthub's private state
	// (ownership records) is left out.
	Files []gitdiff.File
	// Plan is the full plan, blocked items included.
	Plan *install.Plan
	// Blocked explains each agent or skill item left out because it is blocked.
	Blocked []string
	// SkillCommands are the skill installer invocations of the skills Files includes.
	SkillCommands [][]string
}

// ErrSelection reports an agent selection the source cannot satisfy.
var ErrSelection = errors.New("invalid agent selection")

// Fresh plans req on a machine where nothing is installed and returns the files
// the install would create, exactly as `agnthub add … --diff` prints them there.
func Fresh(ctx context.Context, req Request) (*Result, error) {
	registry := req.Registry
	if registry == nil {
		registry = harness.Default()
	}
	inventory, err := discovery.Discover(req.Snapshot.Tree, registry, discovery.DefaultLimits())
	if err != nil {
		return nil, err
	}
	defs, err := selectAgents(inventory, req.Agents)
	if err != nil {
		return nil, err
	}
	settings, err := harness.ResolveSettings(registry, nil, req.Harnesses)
	if err != nil {
		return nil, err
	}
	closure, err := (&resolve.Resolver{
		Opener: req.Opener, Registry: registry, Limits: discovery.DefaultLimits(),
	}).Resolve(ctx, resolve.Root{Snapshot: req.Snapshot, Inventory: inventory}, defs,
		req.Harnesses)
	if err != nil {
		return nil, err
	}
	defer func() { _ = closure.Close() }()

	machine, err := freshMachine(req.Home)
	if err != nil {
		return nil, err
	}
	scope, base := harness.ScopeProject, machine.project
	if req.Global {
		scope, base = harness.ScopeGlobal, machine.home
	}
	hctx := harness.Context{
		Env: machine.env, ProjectRoot: machine.project,
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
	}
	lockPath := state.LockPath(machine.project, machine.env.ConfigHome(), req.Global)
	lock, err := state.LoadLock(lockPath)
	if err != nil {
		return nil, err
	}
	ownership, err := state.LoadOwnership(state.OwnershipPath(machine.env.StateHome()))
	if err != nil {
		return nil, err
	}
	planner := &install.Planner{
		Registry: registry, Lock: lock, LockPath: lockPath, Ownership: ownership,
	}
	locked := func(snapshot *source.Snapshot) state.LockedSource {
		if snapshot.Locator.Kind == source.KindLocal {
			loc := snapshot.Locator
			return state.LockedSource{
				Kind: string(
					loc.Kind,
				),
				Locator:  loc.Path,
				Ref:      loc.Ref,
				Revision: snapshot.Revision,
			}
		}
		return skills.LockedSource(snapshot.Locator, snapshot.Revision)
	}
	plan, err := planner.Build(install.Request{
		Source:            locked(req.Snapshot),
		Targets:           closure.Targets(locked),
		Harnesses:         req.Harnesses,
		Settings:          settings,
		Scope:             scope,
		Context:           hctx,
		AllowWithoutShell: req.AllowWithoutShell,
	})
	if err != nil {
		return nil, err
	}
	if len(closure.Skills) > 0 {
		plan.Skills = (&skills.Planner{
			Registry:  registry,
			Opener:    req.Opener,
			Installer: skills.DefaultInstaller(),
			Ownership: planner.Ownership,
			Lock:      planner.Lock,
		}).Plan(ctx, skills.Request{
			Requirements: closure.Skills,
			Harnesses:    req.Harnesses,
			Settings:     settings,
			Scope:        scope,
			Context:      hctx,
		})
	}
	plan.NoteBlockedSkills()
	result := &Result{Plan: plan}
	applicable := plan
	if plan.Blocked() {
		var dropped []install.Item
		var droppedSkills []skills.Item
		applicable, dropped, droppedSkills = plan.Reduce()
		for _, item := range dropped {
			result.Blocked = append(result.Blocked,
				fmt.Sprintf("%s for %s: %s", item.Agent, item.Harness, item.Reason))
		}
		for _, item := range droppedSkills {
			result.Blocked = append(result.Blocked,
				fmt.Sprintf("skill %s for %s: %s", item.Skill, item.Harness, item.Reason))
		}
	}
	for _, item := range applicable.Skills {
		if item.Decision == skills.DecisionInstall &&
			!slices.ContainsFunc(result.SkillCommands, func(c []string) bool {
				return slices.Equal(c, item.Command)
			}) {
			result.SkillCommands = append(result.SkillCommands, item.Command)
		}
	}
	changes, err := planner.Preview(applicable, machine.project, time.Time{})
	if err != nil {
		return nil, err
	}
	result.Files, err = gitdiff.FromChanges(changes, gitdiff.Options{
		Base: base, Exclude: []string{filepath.Join(machine.env.StateHome(), "agnthub")},
	})
	if err != nil {
		return nil, err
	}
	for _, file := range result.Files {
		if filepath.IsAbs(filepath.FromSlash(file.Path)) || file.Action != gitdiff.Create {
			return nil, fmt.Errorf("preview would %s %s outside a fresh %s", file.Action,
				file.Path, scope)
		}
	}
	return result, nil
}

// machine is an empty project and home that do not exist, so every planned
// file is a creation and nothing reads real configuration.
type machine struct {
	project, home string
	env           env.Env
}

func freshMachine(home string) (*machine, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	root := filepath.Join(os.TempDir(), "agnthub-preview-"+hex.EncodeToString(id[:]))
	if home == "" {
		home = filepath.Join(root, "home")
	}
	for _, dir := range []string{root, home} {
		if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("preview directory %s exists on this machine", dir)
		}
	}
	return &machine{
		project: filepath.Join(root, "project"),
		home:    home,
		env:     env.FromMap(map[string]string{"HOME": home}),
	}, nil
}

// selectAgents mirrors `agnthub add --agent`: '*' takes every agent when none is
// ambiguous or invalid; named agents must exist.
func selectAgents(inventory *discovery.Inventory, ids []string) ([]*agent.Definition, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: name at least one agent", ErrSelection)
	}
	if slices.Contains(ids, AllAgents) {
		var problems []string
		for _, problem := range inventory.Problems {
			if problem.Agent != "" {
				problems = append(problems, problem.String())
			}
		}
		if len(problems) > 0 {
			return nil, fmt.Errorf("%w: some agents are ambiguous or invalid: %s", ErrSelection,
				strings.Join(problems, "; "))
		}
		if len(inventory.Agents) == 0 {
			return nil, fmt.Errorf("%w: the source contains no installable agents", ErrSelection)
		}
		return inventory.Agents, nil
	}
	var defs []*agent.Definition
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		def := inventory.Find(id)
		if def == nil {
			return nil, fmt.Errorf("%w: agent %q cannot be installed from this source",
				ErrSelection, id)
		}
		defs = append(defs, def)
	}
	return defs, nil
}
