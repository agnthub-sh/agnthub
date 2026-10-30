package state

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
)

// IdentitiesVersion is the format version of the installation identity file.
const IdentitiesVersion = 1

// IdentitiesPath is the user-local file of agent-scoped installation identities. It lives
// beside the ownership records, never in a shareable project lock.
func IdentitiesPath(stateHome string) string {
	return filepath.Join(stateHome, "agnthub", "identities.json")
}

// Identities are random installation identities, one per source and Agent. Nothing about
// the machine, user, paths, Harnesses or their settings goes into them, and no identity is
// shared across Agents, so reports can't be joined into a device profile.
type Identities struct {
	Version int `json:"version"`
	// IDs maps an IdentityKey to 32 lowercase hex digits (128 random bits).
	IDs     map[string]string `json:"identities"`
	path    string
	changed bool
}

// IdentityKey names the identity of agent installed from the source with key sourceKey.
func IdentityKey(sourceKey, agent string) string { return sourceKey + "::" + agent }

// LoadIdentities reads the identity file, returning an empty set when absent.
func LoadIdentities(path string) (*Identities, error) {
	ids := &Identities{Version: IdentitiesVersion}
	found, err := readJSON(path, ids)
	if err != nil {
		return nil, err
	}
	if found && ids.Version != IdentitiesVersion {
		return nil, fmt.Errorf("%s: unsupported identities version %d", path, ids.Version)
	}
	if ids.IDs == nil {
		ids.IDs = map[string]string{}
	}
	ids.path = path
	return ids, nil
}

// For returns the identity for key, drawing a new random one the first time.
func (i *Identities) For(key string) (string, error) {
	if id, ok := i.IDs[key]; ok && validIdentity(id) {
		return id, nil
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw[:])
	i.IDs[key] = id
	i.changed = true
	return id, nil
}

// Save writes the file with owner-only permissions when an identity was added.
func (i *Identities) Save() error {
	if !i.changed {
		return nil
	}
	i.Version = IdentitiesVersion
	if err := writeJSON(i.path, i, 0o600); err != nil {
		return err
	}
	i.changed = false
	return nil
}

func validIdentity(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
