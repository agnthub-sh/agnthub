// Package env provides an immutable snapshot of process environment variables so
// that the rest of the code never reads the process environment directly.
package env

import (
	"os"
	"path/filepath"
	"strings"
)

// Env is an immutable view of environment variables.
type Env struct {
	values map[string]string
}

// FromProcess snapshots the current process environment.
func FromProcess() Env {
	return FromList(os.Environ()) //nolint:forbidigo // the single reader of the process environment
}

// FromList builds an Env from KEY=VALUE pairs.
func FromList(pairs []string) Env {
	values := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			continue
		}
		values[key] = value
	}
	return Env{values: values}
}

// FromMap builds an Env from a map, copying it.
func FromMap(values map[string]string) Env {
	copied := make(map[string]string, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return Env{values: copied}
}

// Get returns the value of key, or "" when unset.
func (e Env) Get(key string) string {
	return e.values[key]
}

// Lookup returns the value of key and whether it is set.
func (e Env) Lookup(key string) (string, bool) {
	value, ok := e.values[key]
	return value, ok
}

// Pairs returns the environment as KEY=VALUE pairs, for child processes.
func (e Env) Pairs() []string {
	pairs := make([]string, 0, len(e.values))
	for key, value := range e.values {
		pairs = append(pairs, key+"="+value)
	}
	return pairs
}

// With returns a copy of e with key set to value.
func (e Env) With(key, value string) Env {
	copied := FromMap(e.values)
	copied.values[key] = value
	return copied
}

// Home returns the user's home directory.
func (e Env) Home() string {
	if home := e.Get("HOME"); home != "" {
		return home
	}
	return e.Get("USERPROFILE")
}

// ConfigHome returns the XDG configuration directory.
func (e Env) ConfigHome() string {
	if dir := e.Get("XDG_CONFIG_HOME"); filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(e.Home(), ".config")
}

// StateHome returns the XDG state directory.
func (e Env) StateHome() string {
	if dir := e.Get("XDG_STATE_HOME"); filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(e.Home(), ".local", "state")
}
