package state

import (
	"fmt"
	"path/filepath"
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
}

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
	}
	sort.Slice(l.Agents, func(i, j int) bool { return l.Agents[i].Key() < l.Agents[j].Key() })
	if l.Agents == nil {
		l.Agents = []LockedAgent{}
	}
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

// LockPath returns the lock location for a scope: the project root for project
// scope, the user configuration directory for global scope.
func LockPath(projectRoot, configHome string, global bool) string {
	if global {
		return filepath.Join(configHome, "agnthub", "global-lock.json")
	}
	return filepath.Join(projectRoot, LockFileName)
}
