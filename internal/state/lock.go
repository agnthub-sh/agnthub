package state

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
)

// LockFileName is the shareable project lock at the project root.
const LockFileName = "agnthub-lock.json"

// LockVersion is the current lock schema version.
const LockVersion = 1

// Origin records why an Agent was installed.
type Origin string

// Installation origins.
const (
	OriginDirect     Origin = "direct"
	OriginDependency Origin = "dependency"
)

// Variant kinds recorded for each Harness artifact.
const (
	VariantNative    = "native"
	VariantGenerated = "generated"
)

// Lock is the deterministic, shareable record of resolved sources and generated
// artifacts. It never contains anonymous identities or credentials.
type Lock struct {
	Version int           `json:"version"`
	Agents  []LockedAgent `json:"agents"`
	// Skills are the verified Skill Dependencies of the locked Agents.
	Skills []LockedSkill `json:"skills,omitempty"`
}

// Skill provenance modes.
const (
	// SkillInstalled marks a Skill agnthub's configured backend installed.
	SkillInstalled = "installed"
	// SkillReused marks a compatible existing Skill agnthub does not own.
	SkillReused = "reused"
)

// LockedSkill records one verified Skill directory that locked Agents require.
type LockedSkill struct {
	Skill  string       `json:"skill"`
	Source LockedSource `json:"source"`
	// Version is the declared metadata.version, when any.
	Version string `json:"version,omitempty"`
	// Digest covers the Skill directory's installable files.
	Digest string `json:"digest"`
	// Path is project-relative inside the project, "~/"-relative inside the home
	// directory, and absolute otherwise.
	Path      string   `json:"path"`
	Scope     string   `json:"scope"`
	Harnesses []string `json:"harnesses"`
	// Mode is SkillInstalled or SkillReused.
	Mode string `json:"mode"`
}

// UpsertSkill records skill for harness, merging Harnesses of the same directory.
func (l *Lock) UpsertSkill(skill LockedSkill, harness string) {
	for i := range l.Skills {
		locked := &l.Skills[i]
		if locked.Skill != skill.Skill || locked.Path != skill.Path {
			continue
		}
		harnesses := locked.Harnesses
		if locked.Mode == SkillInstalled && skill.Mode == SkillReused {
			skill.Mode = SkillInstalled
		}
		*locked = skill
		locked.Harnesses = harnesses
		for _, h := range harnesses {
			if h == harness {
				return
			}
		}
		locked.Harnesses = append(locked.Harnesses, harness)
		return
	}
	skill.Harnesses = []string{harness}
	l.Skills = append(l.Skills, skill)
}

// LockedSource identifies the exact source content used.
type LockedSource struct {
	Kind string `json:"kind"`
	// Locator is credential-free: owner/repo, host/path or a local path.
	Locator  string `json:"locator"`
	Ref      string `json:"ref,omitempty"`
	Revision string `json:"revision,omitempty"`
}

// LockedArtifact records one installed Harness artifact and how it was produced.
type LockedArtifact struct {
	Harness string `json:"harness"`
	// Variant is "native" for an authored Variant, "generated" for conversion.
	Variant      string `json:"variant"`
	SourcePath   string `json:"sourcePath"`
	SourceDigest string `json:"sourceDigest"`
	Converter    string `json:"converter,omitempty"`
	// Path is relative to the project root for project scope, absolute otherwise.
	Path   string `json:"path"`
	Digest string `json:"digest"`
	// Requires lists what this artifact's Agent needs in the same Harness binding:
	// RequireAgent(key) companions and RequireSkill(name) Skills. Empty means none;
	// null (a lock written before requirements were recorded) means unknown.
	Requires []string `json:"requires"`
}

// RequireAgent is the Requires entry for a companion Agent's LockedAgent.Key().
func RequireAgent(key string) string { return "agent:" + key }

// RequireSkill is the Requires entry for a Skill Dependency.
func RequireSkill(name string) string { return "skill:" + name }

// LockedAgent records one installed logical Agent.
type LockedAgent struct {
	Source    LockedSource     `json:"source"`
	Agent     string           `json:"agent"`
	Origin    Origin           `json:"origin"`
	Artifacts []LockedArtifact `json:"artifacts"`
}

// Key identifies a locked Agent within a lock.
func (a LockedAgent) Key() string {
	return a.Source.Kind + ":" + a.Source.Locator + "::" + a.Agent
}

// LoadLock reads a lock file, returning an empty lock when absent.
func LoadLock(path string) (*Lock, error) {
	lock := &Lock{Version: LockVersion}
	found, err := readJSON(path, lock)
	if err != nil {
		return nil, err
	}
	if found && lock.Version != LockVersion {
		return nil, fmt.Errorf("%s: unsupported lock version %d", path, lock.Version)
	}
	return lock, nil
}

// Save writes the lock deterministically.
func (l *Lock) Save(path string) error {
	l.normalize()
	return writeJSON(path, l, 0o644)
}

func (l *Lock) normalize() {
	l.Version = LockVersion
	for i := range l.Agents {
		artifacts := l.Agents[i].Artifacts
		sort.Slice(artifacts, func(a, b int) bool {
			if artifacts[a].Harness != artifacts[b].Harness {
				return artifacts[a].Harness < artifacts[b].Harness
			}
			return artifacts[a].Path < artifacts[b].Path
		})
		for j := range artifacts {
			sort.Strings(artifacts[j].Requires)
		}
	}
	sort.Slice(l.Agents, func(i, j int) bool { return l.Agents[i].Key() < l.Agents[j].Key() })
	if l.Agents == nil {
		l.Agents = []LockedAgent{}
	}
	for i := range l.Skills {
		sort.Strings(l.Skills[i].Harnesses)
	}
	sort.Slice(l.Skills, func(i, j int) bool {
		if l.Skills[i].Skill != l.Skills[j].Skill {
			return l.Skills[i].Skill < l.Skills[j].Skill
		}
		return l.Skills[i].Path < l.Skills[j].Path
	})
}

// Find returns the locked Agent with key, or nil.
func (l *Lock) Find(key string) *LockedAgent {
	for i := range l.Agents {
		if l.Agents[i].Key() == key {
			return &l.Agents[i]
		}
	}
	return nil
}

// Upsert records artifact for the Agent, replacing an artifact at the same path.
// An existing direct origin is never downgraded to dependency.
func (l *Lock) Upsert(src LockedSource, agentID string, origin Origin, artifact LockedArtifact) {
	key := LockedAgent{Source: src, Agent: agentID}.Key()
	locked := l.Find(key)
	if locked == nil {
		l.Agents = append(l.Agents, LockedAgent{Source: src, Agent: agentID, Origin: origin})
		locked = &l.Agents[len(l.Agents)-1]
	}
	locked.Source = src
	if locked.Origin != OriginDirect {
		locked.Origin = origin
	}
	for i := range locked.Artifacts {
		if locked.Artifacts[i].Path == artifact.Path {
			locked.Artifacts[i] = artifact
			return
		}
	}
	locked.Artifacts = append(locked.Artifacts, artifact)
}

// DigestForPath returns the locked digest for an artifact path, if any.
func (l *Lock) DigestForPath(artifactPath string) (string, bool) {
	for _, locked := range l.Agents {
		for _, artifact := range locked.Artifacts {
			if artifact.Path == artifactPath {
				return artifact.Digest, true
			}
		}
	}
	return "", false
}

// RemoveArtifact drops the artifact at artifactPath from the Agent with key,
// dropping the Agent once it has no artifact left. It reports whether anything
// was removed.
func (l *Lock) RemoveArtifact(key, artifactPath string) bool {
	locked := l.Find(key)
	if locked == nil {
		return false
	}
	before := len(locked.Artifacts)
	locked.Artifacts = slices.DeleteFunc(locked.Artifacts, func(a LockedArtifact) bool {
		return a.Path == artifactPath
	})
	if len(locked.Artifacts) == before {
		return false
	}
	if len(locked.Artifacts) == 0 {
		l.Agents = slices.DeleteFunc(l.Agents, func(a LockedAgent) bool { return a.Key() == key })
	}
	return true
}

// RemoveSkillHarness drops harness from the locked Skill at skillPath, dropping
// the Skill once no Harness is left. It reports whether anything was removed.
func (l *Lock) RemoveSkillHarness(skill, skillPath, harness string) bool {
	for i := range l.Skills {
		locked := &l.Skills[i]
		if locked.Skill != skill || locked.Path != skillPath {
			continue
		}
		before := len(locked.Harnesses)
		locked.Harnesses = slices.DeleteFunc(locked.Harnesses, func(h string) bool {
			return h == harness
		})
		if len(locked.Harnesses) == before {
			return false
		}
		if len(locked.Harnesses) == 0 {
			l.Skills = slices.Delete(l.Skills, i, i+1)
		}
		return true
	}
	return false
}

// LockPath returns the lock location for a scope: the project root for project
// scope, the user configuration directory for global scope.
func LockPath(projectRoot, configHome string, global bool) string {
	if global {
		return filepath.Join(configHome, "agnthub", "global-lock.json")
	}
	return filepath.Join(projectRoot, LockFileName)
}
