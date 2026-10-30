package state

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// OwnershipVersion is the current ownership schema version.
const OwnershipVersion = 1

// Record proves agnthub created one artifact for one Harness binding. Records are
// user-local: they hold absolute paths and Harness Settings such as profiles.
type Record struct {
	// Path is the absolute artifact path.
	Path    string `json:"path"`
	Digest  string `json:"digest"`
	Harness string `json:"harness"`
	Scope   string `json:"scope"`
	// ProjectRoot is set for project-scope installs.
	ProjectRoot string `json:"projectRoot,omitempty"`
	// Settings are the effective Harness Settings of this binding.
	Settings    map[string]string `json:"settings,omitempty"`
	Destination string            `json:"destination"`
	Source      string            `json:"source"`
	Agent       string            `json:"agent"`
	// Skill names the Skill of a Skill directory agnthub's backend installed;
	// such records have no Agent.
	Skill       string    `json:"skill,omitempty"`
	Origin      Origin    `json:"origin"`
	InstalledAt time.Time `json:"installedAt"`
}

// Binding identifies the Harness binding a record belongs to. The installing
// project is part of it: a home directory Skill installed from two projects keeps
// one record per project.
func (r Record) Binding() string {
	return r.Path + "|" + r.Harness + "|" + SettingsKey(r.Settings) + "|" + r.ProjectRoot
}

// SettingsKey is a deterministic encoding of Harness Settings.
func SettingsKey(settings map[string]string) string {
	keys := make([]string, 0, len(settings))
	for key := range settings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+settings[key])
	}
	return strings.Join(parts, ",")
}

// Ownership is the user-local set of ownership records.
type Ownership struct {
	Version int      `json:"version"`
	Records []Record `json:"records"`
	path    string
}

// OwnershipPath returns the user-local ownership file.
func OwnershipPath(stateHome string) string {
	return filepath.Join(stateHome, "agnthub", "installations.json")
}

// LoadOwnership reads ownership records, returning an empty set when absent.
func LoadOwnership(path string) (*Ownership, error) {
	ownership := &Ownership{Version: OwnershipVersion, path: path}
	found, err := readJSON(path, ownership)
	if err != nil {
		return nil, err
	}
	if found && ownership.Version != OwnershipVersion {
		return nil, fmt.Errorf("%s: unsupported ownership version %d", path, ownership.Version)
	}
	ownership.path = path
	return ownership, nil
}

// Save writes the records deterministically with owner-only permissions.
func (o *Ownership) Save() error {
	o.Version = OwnershipVersion
	sort.Slice(o.Records, func(i, j int) bool {
		return o.Records[i].Binding() < o.Records[j].Binding()
	})
	if o.Records == nil {
		o.Records = []Record{}
	}
	return writeJSON(o.path, o, 0o600)
}

// ForPath returns every record bound to an artifact path.
func (o *Ownership) ForPath(path string) []Record {
	var records []Record
	for _, record := range o.Records {
		if record.Path == path {
			records = append(records, record)
		}
	}
	return records
}

// Put inserts or replaces the record with the same binding. Other bindings that
// share the path are updated to the new digest because the bytes are shared.
func (o *Ownership) Put(record Record) {
	for i := range o.Records {
		if o.Records[i].Path == record.Path {
			o.Records[i].Digest = record.Digest
		}
	}
	for i := range o.Records {
		if o.Records[i].Binding() == record.Binding() {
			o.Records[i] = record
			return
		}
	}
	o.Records = append(o.Records, record)
}

// Remove deletes the record with binding, reporting whether one existed.
func (o *Ownership) Remove(binding string) bool {
	for i := range o.Records {
		if o.Records[i].Binding() == binding {
			o.Records = append(o.Records[:i], o.Records[i+1:]...)
			return true
		}
	}
	return false
}
