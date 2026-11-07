package resolve

import (
	"slices"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/install"
	"github.com/agnthub-sh/agnthub/source"
	"github.com/agnthub-sh/agnthub/state"
)

// Targets turns the closure into plan targets; locked gives each Member's
// credential-free lock source. Companions keep their own source but share the
// user's Harnesses, scope and Settings. Each Target carries what it requires per
// Harness so the lock can tell later whether a Dependency is still needed.
func (c *Closure) Targets(locked func(*source.Snapshot) state.LockedSource) []install.Target {
	keys := make(map[string]string, len(c.Members))
	sources := make([]state.LockedSource, len(c.Members))
	for i, member := range c.Members {
		sources[i] = locked(member.Snapshot)
		keys[member.Label] = state.LockedAgent{
			Source: sources[i], Agent: member.Definition.ID,
		}.Key()
	}
	requires := map[string]map[agent.HarnessID][]string{}
	add := func(label string, harnessID agent.HarnessID, entry string) {
		byHarness := requires[label]
		if byHarness == nil {
			byHarness = map[agent.HarnessID][]string{}
			requires[label] = byHarness
		}
		if !slices.Contains(byHarness[harnessID], entry) {
			byHarness[harnessID] = append(byHarness[harnessID], entry)
		}
	}
	for _, member := range c.Members {
		for harnessID, labels := range member.Requires {
			for _, label := range labels {
				add(member.Label, harnessID, state.RequireAgent(keys[label]))
			}
		}
	}
	for _, requirement := range c.Skills {
		for _, need := range requirement.Needs {
			add(need.RequiredBy, need.Harness, state.RequireSkill(requirement.Skill))
		}
	}
	targets := make([]install.Target, 0, len(c.Members))
	for i, member := range c.Members {
		target := install.Target{
			Definition: member.Definition,
			Origin:     state.OriginDependency,
			Source:     sources[i],
			Harnesses:  member.Harnesses,
			RequiredBy: member.RequiredBy,
			Requires:   requires[member.Label],
		}
		if target.Requires == nil {
			target.Requires = map[agent.HarnessID][]string{}
		}
		if member.Direct {
			target.Origin = state.OriginDirect
		}
		targets = append(targets, target)
	}
	return targets
}
