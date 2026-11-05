package harness_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/harness"
	"github.com/agnthub-sh/agnthub/platform/env"
)

// The agent directories each Harness loads, as its documentation and shipped
// binary show (docs/research/harness-agent-formats.md, "Agent directories").
func TestDestinationsAreWhereEachHarnessLoadsAgents(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".omp", "profiles", "work"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".alt", "profiles", "work"), 0o750); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(t.TempDir(), "project")
	at := func(parts ...string) string { return filepath.Join(append([]string{home}, parts...)...) }
	cases := []struct {
		name     string
		harness  agent.HarnessID
		vars     map[string]string
		settings harness.Settings
		scope    harness.Scope
		want     string
	}{
		{
			"claude project", agent.Claude, nil, nil, harness.ScopeProject,
			filepath.Join(project, ".claude", "agents"),
		},
		{"claude user", agent.Claude, nil, nil, harness.ScopeGlobal, at(".claude", "agents")},
		{
			"claude CLAUDE_CONFIG_DIR", agent.Claude,
			map[string]string{"CLAUDE_CONFIG_DIR": "/cfg/claude"},
			nil, harness.ScopeGlobal,
			filepath.FromSlash("/cfg/claude/agents"),
		},

		{
			"opencode project", agent.OpenCode, nil, nil, harness.ScopeProject,
			filepath.Join(project, ".opencode", "agents"),
		},
		{
			"opencode user", agent.OpenCode, nil, nil, harness.ScopeGlobal,
			at(".config", "opencode", "agents"),
		},
		{
			"opencode XDG_CONFIG_HOME", agent.OpenCode,
			map[string]string{"XDG_CONFIG_HOME": "/xdg"},
			nil, harness.ScopeGlobal,
			filepath.FromSlash("/xdg/opencode/agents"),
		},
		{
			"opencode relative XDG_CONFIG_HOME resolves in home", agent.OpenCode,
			map[string]string{"XDG_CONFIG_HOME": "xdg"},
			nil, harness.ScopeGlobal,
			at("xdg", "opencode", "agents"),
		},
		{
			"opencode OPENCODE_CONFIG_DIR replaces the user directory", agent.OpenCode,
			map[string]string{"XDG_CONFIG_HOME": "/xdg", "OPENCODE_CONFIG_DIR": "/oc"},
			nil,
			harness.ScopeGlobal, filepath.FromSlash("/oc/agents"),
		},
		{
			"opencode relative OPENCODE_CONFIG_DIR resolves in home", agent.OpenCode,
			map[string]string{"OPENCODE_CONFIG_DIR": "oc"},
			nil, harness.ScopeGlobal,
			at("oc", "agents"),
		},

		{
			"codex project", agent.Codex, nil, nil, harness.ScopeProject,
			filepath.Join(project, ".codex", "agents"),
		},
		{"codex user", agent.Codex, nil, nil, harness.ScopeGlobal, at(".codex", "agents")},
		{
			"codex CODEX_HOME", agent.Codex,
			map[string]string{"CODEX_HOME": "/opt/codex"},
			nil,
			harness.ScopeGlobal, filepath.FromSlash("/opt/codex/agents"),
		},

		{
			"copilot project", agent.Copilot, nil, nil, harness.ScopeProject,
			filepath.Join(project, ".github", "agents"),
		},
		{
			"copilot user ignores XDG", agent.Copilot,
			map[string]string{"XDG_CONFIG_HOME": "/xdg"},
			nil, harness.ScopeGlobal,
			at(".copilot", "agents"),
		},
		{
			"copilot COPILOT_HOME", agent.Copilot,
			map[string]string{"COPILOT_HOME": "/ch"},
			nil,
			harness.ScopeGlobal, filepath.FromSlash("/ch/agents"),
		},

		{
			"cursor project", agent.Cursor, nil, nil, harness.ScopeProject,
			filepath.Join(project, ".cursor", "agents"),
		},
		{"cursor user", agent.Cursor, nil, nil, harness.ScopeGlobal, at(".cursor", "agents")},

		{
			"gemini project", agent.Gemini, nil, nil, harness.ScopeProject,
			filepath.Join(project, ".gemini", "agents"),
		},
		{"gemini user", agent.Gemini, nil, nil, harness.ScopeGlobal, at(".gemini", "agents")},
		{
			"gemini GEMINI_CLI_HOME", agent.Gemini,
			map[string]string{"GEMINI_CLI_HOME": "/srv/g"},
			nil, harness.ScopeGlobal,
			filepath.FromSlash("/srv/g/.gemini/agents"),
		},

		{
			"omp project", agent.OMP, nil, nil, harness.ScopeProject,
			filepath.Join(project, ".omp", "agents"),
		},
		{"omp user", agent.OMP, nil, nil, harness.ScopeGlobal, at(".omp", "agent", "agents")},
		{
			"omp named profile", agent.OMP, nil,
			harness.Settings{"profile": "work"},
			harness.ScopeGlobal, at(".omp", "profiles", "work", "agent", "agents"),
		},
		{
			"omp OMP_PROFILE", agent.OMP,
			map[string]string{"OMP_PROFILE": "work"},
			nil,
			harness.ScopeGlobal, at(".omp", "profiles", "work", "agent", "agents"),
		},
		{
			"omp PI_CONFIG_DIR under home", agent.OMP,
			map[string]string{"PI_CONFIG_DIR": ".alt", "OMP_PROFILE": "work"},
			nil,
			harness.ScopeGlobal, at(".alt", "profiles", "work", "agent", "agents"),
		},
		{
			"omp PI_CONFIG_DIR joins home even when absolute", agent.OMP,
			map[string]string{"PI_CONFIG_DIR": "/.alt"},
			nil,
			harness.ScopeGlobal, at(".alt", "agent", "agents"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vars := map[string]string{"HOME": home}
			for key, value := range tc.vars {
				vars[key] = value
			}
			ctx := harness.Context{Env: env.FromMap(vars), ProjectRoot: project}
			dest, err := installer(t, tc.harness).Destination(ctx, tc.scope, tc.settings)
			if err != nil {
				t.Fatal(err)
			}
			if dest.Dir != tc.want {
				t.Fatalf("got %s, want %s", dest.Dir, tc.want)
			}
			if dest.Shared != (tc.scope == harness.ScopeProject) {
				t.Fatalf("shared = %v in %s scope", dest.Shared, tc.scope)
			}
		})
	}
}

// A relative configuration directory is resolved by Claude Code, Codex, Copilot CLI
// and Gemini CLI against the directory they run in, so there is no single user
// directory to install into: user scope is refused rather than written where the
// Harness never looks.
func TestUserScopeRefusesRelativeConfigDirectories(t *testing.T) {
	cases := map[agent.HarnessID]string{
		agent.Claude:  "CLAUDE_CONFIG_DIR",
		agent.Codex:   "CODEX_HOME",
		agent.Copilot: "COPILOT_HOME",
		agent.Gemini:  "GEMINI_CLI_HOME",
	}
	for id, key := range cases {
		t.Run(string(id), func(t *testing.T) {
			ctx := harness.Context{
				Env:         env.FromMap(map[string]string{"HOME": "/home/u", key: "rel/dir"}),
				ProjectRoot: "/work/p",
			}
			if dest, err := installer(
				t,
				id,
			).Destination(ctx, harness.ScopeGlobal, nil); err == nil {
				t.Fatalf("relative %s installed into %s", key, dest.Dir)
			}
			if _, err := installer(t, id).Destination(ctx, harness.ScopeProject, nil); err != nil {
				t.Fatalf("project scope does not depend on %s: %v", key, err)
			}
		})
	}
}
