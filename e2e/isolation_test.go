//go:build e2e && linux

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// harnessFootprint is where each Harness's installation lives in the images
// (e2e/Dockerfile): files that must be absent from every other Harness's image.
var harnessFootprint = map[string][]string{
	"claude":   {"/usr/local/lib/node_modules/@anthropic-ai/claude-code"},
	"opencode": {"/opt/opencode"},
	"codex":    {"/usr/local/lib/node_modules/@openai/codex"},
	"copilot":  {"/usr/local/lib/node_modules/@github/copilot"},
	"cursor":   {"/opt/cursor", "/opt/cursor-local"},
	"gemini":   {"/usr/local/lib/node_modules/@google/gemini-cli"},
	"omp":      {"/opt/bun/install/global/node_modules/@oh-my-pi"},
}

// harnessConfigDirs are each Harness's user configuration directories; none may
// exist in the image's own home directory.
var harnessConfigDirs = []string{
	".claude", ".claude.json", ".codex", ".config/opencode", ".copilot", ".cursor",
	".gemini", ".omp",
}

// checkImageIsolation requires the image to hold only this Harness: no other
// Harness's CLI on PATH, none of their installations, and no Harness
// configuration in the image user's home.
func checkImageIsolation(t *testing.T, harness string) {
	for other, binary := range harnessBinaries {
		path, err := exec.LookPath(binary)
		if other == harness {
			if err != nil {
				t.Errorf("%s is not on PATH: %v", binary, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("the %s image contains %s's CLI at %s", harness, other, path)
		}
		for _, dir := range harnessFootprint[other] {
			if _, err := os.Stat(dir); err == nil {
				t.Errorf("the %s image contains %s's installation %s", harness, other, dir)
			}
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range harnessConfigDirs {
		if _, err := os.Stat(filepath.Join(home, dir)); err == nil {
			t.Errorf("the image has Harness configuration %s", filepath.Join(home, dir))
		}
	}
}

// ownDirectories are the project- and home-relative prefixes agnthub may write
// when it installs into one Harness: that Harness's agent and skill directories
// plus agnthub's own lock, configuration and state.
var ownDirectories = map[string][]string{
	"claude":   {"project/.claude/", "home/.claude/"},
	"opencode": {"project/.opencode/", "home/.config/opencode/", agentsSkills, agentsSkillsHome},
	"codex":    {"project/.codex/", "home/.codex/", agentsSkills, agentsSkillsHome},
	"copilot":  {"project/.github/agents/", "home/.copilot/", agentsSkills, agentsSkillsHome},
	"cursor":   {"project/.cursor/", "home/.cursor/", agentsSkills, agentsSkillsHome},
	"gemini":   {"project/.gemini/", "home/.gemini/", agentsSkills, agentsSkillsHome},
	"omp":      {"project/.omp/", "home/.omp/", agentsSkills, agentsSkillsHome},
}

const (
	agentsSkills     = "project/.agents/skills/"
	agentsSkillsHome = "home/.agents/skills/"
)

var agnthubFiles = []string{
	"project/agnthub-lock.json", "project/skills-lock.json",
	"home/.config/agnthub/", "home/.local/state/agnthub/",
}

// requireOwnDirectories fails for every file outside the selected Harness's
// directories and agnthub's own files: an install into one Harness never
// touches another Harness's configuration.
func requireOwnDirectories(t *testing.T, harness string, files map[string]file) {
	t.Helper()
	allowed := append(append([]string{}, ownDirectories[harness]...), agnthubFiles...)
	for name := range files {
		ok := false
		for _, prefix := range allowed {
			if name == prefix ||
				(strings.HasSuffix(prefix, "/") && strings.HasPrefix(name, prefix)) {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("%s is outside %s's directories", name, harness)
		}
	}
}
