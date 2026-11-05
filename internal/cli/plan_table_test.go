package cli

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/internal/uninstall"
	"github.com/agnthub-sh/agnthub/platform/env"
	"github.com/agnthub-sh/agnthub/state"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/plantable/*.golden")

const (
	goldenProject = "/work/shop"
	goldenHome    = "/home/dev"
)

// Terminal environments: a forced color terminal, NO_COLOR on a terminal, and
// redirected output (no TERM, no terminal).
var (
	colorTerminal = map[string]string{
		"TERM": "xterm-256color", "CLICOLOR_FORCE": "1", "COLUMNS": "200",
	}
	noColorTerminal = map[string]string{
		"TERM": "xterm-256color", "TTY_FORCE": "1", "NO_COLOR": "1", "COLUMNS": "200",
	}
	redirected = map[string]string{}
)

func withColumns(vars map[string]string, columns string) map[string]string {
	out := map[string]string{"COLUMNS": columns}
	for key, value := range vars {
		if key != "COLUMNS" {
			out[key] = value
		}
	}
	return out
}

// goldenPrinter is the human printer the App builds for vars, writing to out.
func goldenPrinter(vars map[string]string, out *bytes.Buffer) *printer {
	values := map[string]string{"HOME": goldenHome}
	for key, value := range vars {
		values[key] = value
	}
	app := &App{Env: env.FromMap(values), WorkDir: goldenProject, Stdout: out}
	return app.printer(false)
}

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "plantable", name+".golden")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/cli -run TestPlanTable -update)", err)
	}
	if got != string(want) {
		t.Fatalf("%s differs from %s:\n--- got\n%s\n--- want\n%s", name, path, got, want)
	}
}

var goldenSource = sourceJSON{
	Kind: "github", Locator: "wshobson/agents", Revision: "7e2fb559cf4b5a1d2e3f",
}

// screenshotPlan is 4 Agents × 7 Harnesses converted from Claude Code Variants,
// with one note shared by most rows, two Agent-specific notes and omp's shared
// project directory.
func screenshotPlan(status string) *planJSON {
	agents := []string{
		"comprehensive-review-code-reviewer", "architect", "ai-engineer", "accessibility-expert",
	}
	payload := &planJSON{Status: status, Scope: "project", Source: goldenSource}
	for _, id := range agent.AllHarnesses {
		h := harnessJSON{ID: string(id), Name: string(id), Detected: id != agent.Cursor}
		if id == agent.OMP {
			h.Settings = []settingJSON{{Key: "profile", Value: "default", Source: "native default"}}
		}
		payload.Harnesses = append(payload.Harnesses, h)
	}
	dirs := map[agent.HarnessID]string{
		agent.Claude: ".claude/agents/%s.md", agent.OpenCode: ".opencode/agents/%s.md",
		agent.Codex: ".codex/agents/%s.toml", agent.Copilot: ".github/agents/%s.agent.md",
		agent.Cursor: ".cursor/agents/%s.md", agent.Gemini: ".gemini/agents/%s.md",
		agent.OMP: ".omp/agents/%s.md",
	}
	for _, name := range agents {
		for _, id := range agent.AllHarnesses {
			item := itemJSON{
				Agent: name, Harness: string(id), Action: "create", Source: goldenSource,
				Origin:      "direct",
				Path:        filepath.Join(goldenProject, strings.ReplaceAll(dirs[id], "%s", name)),
				Destination: "project",
			}
			if id == agent.OMP {
				item.Destination = "project, shared by every omp profile; verify with default profile"
			}
			if id != agent.Claude {
				item.Notes = append(item.Notes, "converted from the claude variant")
				switch name {
				case "comprehensive-review-code-reviewer":
					item.Notes = append(item.Notes, `model "opus" is specific to claude; `+
						"the agent uses the harness default model")
				case "accessibility-expert":
					item.Notes = append(item.Notes, "omitted color: cosmetic")
				}
			}
			payload.Items = append(payload.Items, item)
		}
	}
	return payload
}

func TestPlanTableGoldens(t *testing.T) {
	for _, tc := range []struct {
		name    string
		vars    map[string]string
		payload *planJSON
	}{
		{"create-4x7-color", colorTerminal, screenshotPlan(statusPlanned)},
		{"create-4x7-applied-color", colorTerminal, screenshotPlan(statusApplied)},
		{"create-4x7-no-color", noColorTerminal, screenshotPlan(statusPlanned)},
		{"create-4x7-redirected", redirected, screenshotPlan(statusPlanned)},
		{"create-4x7-80-columns", withColumns(noColorTerminal, "80"), screenshotPlan(statusPlanned)},
		{"create-4x7-60-columns", withColumns(noColorTerminal, "60"), screenshotPlan(statusPlanned)},
		{"refusals-color", colorTerminal, refusalPlan()},
		{"refusals-50-columns", withColumns(noColorTerminal, "50"), refusalPlan()},
		{"update-color", colorTerminal, updatePlan()},
		{"paths", redirected, pathsPlan()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			goldenPrinter(tc.vars, &out).humanPlan(tc.payload)
			got := out.String()
			if !strings.Contains(tc.name, "color") || strings.Contains(tc.name, "no-color") {
				if strings.Contains(got, "\x1b") || strings.ContainsAny(got, "📋✅❌") {
					t.Fatalf("plain output must carry no escape sequences or emoji:\n%s", got)
				}
			}
			checkGolden(t, tc.name, got)
		})
	}
}

// refusalPlan mixes installs with an incompatible Agent and a conflicting file.
func refusalPlan() *planJSON {
	payload := &planJSON{
		Status: statusPlanned, Scope: "project", Source: goldenSource,
		Harnesses: []harnessJSON{
			{ID: "claude", Detected: true}, {ID: "codex", Detected: true},
		},
	}
	add := func(name, harnessID, action, path, reason string) {
		item := itemJSON{
			Agent: name, Harness: harnessID, Action: action, Source: goldenSource,
			Origin: "direct", Reason: reason, Destination: "project",
		}
		if path != "" {
			item.Path = filepath.Join(goldenProject, path)
		}
		payload.Items = append(payload.Items, item)
	}
	add("reviewer", "claude", "create", ".claude/agents/reviewer.md", "")
	add("reviewer", "codex", "create", ".codex/agents/reviewer.toml", "")
	add("hooked", "claude", "create", ".claude/agents/hooked.md", "")
	add("hooked", "codex", "incompatible", "",
		"incompatible with codex: hooks run shell commands on tool events, which codex agents "+
			"cannot declare")
	add("planner", "claude", "conflict", ".claude/agents/planner.md",
		".claude/agents/planner.md exists and is not managed by agnthub; move it away or "+
			"remove it first")
	return payload
}

// updatePlan replaces changed content, leaves current files, adds a new
// Dependency and keeps a file the source no longer provides.
func updatePlan() *planJSON {
	locked := goldenSource
	locked.Ref = "v1"
	resolved := locked
	resolved.Revision = "9a8b7c6d5e4f3a2b1c0d"
	path := func(rel string) string { return filepath.Join(goldenProject, rel) }
	payload := &planJSON{
		Status: statusPlanned, Scope: "project", Source: resolved,
		Harnesses: []harnessJSON{{ID: "claude", Detected: true}, {ID: "codex", Detected: true}},
		Items: []itemJSON{
			{
				Agent: "reviewer", Harness: "claude", Action: "replace", Origin: "direct",
				Source: resolved, Path: path(".claude/agents/reviewer.md"), Destination: "project",
			},
			{
				Agent: "reviewer", Harness: "codex", Action: "replace", Origin: "direct",
				Source: resolved, Path: path(".codex/agents/reviewer.toml"), Destination: "project",
				Notes: []string{"converted from the claude variant"},
			},
			{
				Agent: "planner", Harness: "claude", Action: "unchanged", Origin: "direct",
				Source: resolved, Path: path(".claude/agents/planner.md"), Destination: "project",
			},
			{
				Agent: "linter", Harness: "claude", Action: "create", Origin: "dependency",
				RequiredBy: []string{"reviewer"}, Source: resolved,
				Path: path(".claude/agents/linter.md"), Destination: "project",
			},
		},
		Changes: &lifecycleReport{
			Command: "update", Lock: state.LockFileName,
			Agents: []agentReport{
				{
					Agent: "reviewer", Source: sourceJSON(locked), Origin: "direct",
					Status: agentUpdateAvailable, Revision: resolved.Revision,
					Artifacts: []artifactReport{
						{
							Harness: "claude", Path: path(".claude/agents/reviewer.md"),
							Change: changeContent, Local: localOK,
						},
						{
							Harness: "codex", Path: path(".codex/agents/reviewer.toml"),
							Change: changeConverter, Local: "modified",
							Reason: "converter codex/1 → codex/2",
						},
					},
				},
				{
					Agent: "planner", Source: sourceJSON(locked), Origin: "direct",
					Status: agentUpdateAvailable, Revision: resolved.Revision,
					Artifacts: []artifactReport{
						{
							Harness: "claude", Path: path(".claude/agents/planner.md"),
							Change: changeNone, Local: localOK,
						},
						{
							Harness: "codex", Path: path(".codex/agents/planner.toml"),
							Change: changeRemoved, Local: localOK,
						},
					},
				},
			},
			Skills: []skillChange{{Skill: "code-review", Harness: "claude", Change: changeAdded}},
		},
	}
	return payload
}

// pathsPlan installs into user scope: paths under home show as ~/..., inside the
// project relative to it, and anywhere else absolute.
func pathsPlan() *planJSON {
	payload := &planJSON{
		Status: statusPlanned, Scope: "global", Source: goldenSource,
		Harnesses: []harnessJSON{
			{ID: "claude", Detected: true},
			{ID: "codex", Detected: true},
			{ID: "gemini", Detected: true},
		},
	}
	for _, item := range []struct{ harness, path string }{
		{"claude", filepath.Join(goldenHome, ".claude", "agents", "reviewer.md")},
		{"codex", filepath.Join("/srv", "codex", "agents", "reviewer.toml")},
		{"gemini", filepath.Join(goldenProject, ".gemini", "agents", "reviewer.md")},
	} {
		payload.Items = append(payload.Items, itemJSON{
			Agent: "reviewer", Harness: item.harness, Action: "create", Origin: "direct",
			Source: goldenSource, Path: item.path, Destination: "user",
		})
	}
	return payload
}

func TestRemovePlanTableGolden(t *testing.T) {
	source := state.LockedSource(goldenSource)
	unit := func(name, harnessID, path string, settings map[string]string) *uninstall.Unit {
		u := &uninstall.Unit{
			Kind: uninstall.KindAgent, Name: name, Key: name, Source: source,
			Origin: state.OriginDirect, Harness: harnessID,
			Path: filepath.Join(goldenProject, path),
		}
		if settings != nil {
			u.Record = &state.Record{Settings: settings}
		}
		return u
	}
	plan := &uninstall.Plan{Entries: []*uninstall.Entry{
		{
			Unit:   unit("reviewer", "claude", ".claude/agents/reviewer.md", nil),
			Action: uninstall.ActionDelete,
		},
		{
			Unit:   unit("reviewer", "codex", ".codex/agents/reviewer.toml", nil),
			Action: uninstall.ActionDelete,
		},
		{
			Unit: unit(
				"reviewer",
				"omp",
				".omp/agents/reviewer.md",
				map[string]string{"profile": "work"},
			),
			Action: uninstall.ActionUnbind,
			Reason: "the default omp profile still uses it",
		},
		{
			Unit:   unit("planner", "claude", ".claude/agents/planner.md", nil),
			Action: uninstall.ActionRefuse,
			Reason: ".claude/agents/planner.md was modified locally",
		},
	}}
	for _, tc := range []struct {
		name   string
		vars   map[string]string
		status string
	}{
		{"remove-color", colorTerminal, statusPlanned},
		{"remove-no-color", noColorTerminal, statusPlanned},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			r := &removal{
				app: &App{WorkDir: goldenProject}, opts: &removeOptions{},
				lockPath: filepath.Join(goldenProject, state.LockFileName),
				inv:      &uninstall.Inventory{},
			}
			r.print(goldenPrinter(tc.vars, &out), plan, tc.status)
			checkGolden(t, tc.name, out.String())
		})
	}
	failed := *plan.Entries[0]
	failed.Err = errors.New("permission denied")
	applied := &uninstall.Plan{Entries: []*uninstall.Entry{&failed, plan.Entries[1]}}
	var out bytes.Buffer
	r := &removal{
		app: &App{WorkDir: goldenProject}, opts: &removeOptions{},
		lockPath: filepath.Join(goldenProject, state.LockFileName), inv: &uninstall.Inventory{},
	}
	r.print(goldenPrinter(noColorTerminal, &out), applied, statusFailed)
	checkGolden(t, "remove-failed", out.String())
}

func TestTruncateMiddleKeepsTheEndsWithinWidth(t *testing.T) {
	for _, tc := range []struct {
		path  string
		width int
		want  string
	}{
		{".claude/agents/reviewer.md", 40, ".claude/agents/reviewer.md"},
		{".claude/agents/comprehensive-review-code-reviewer.md", 20, ".claude/…reviewer.md"},
		{"/srv/very/long/directory/agents/reviewer.toml", 16, "/srv/v…ewer.toml"},
	} {
		got := truncateMiddle(tc.path, tc.width)
		if got != tc.want {
			t.Errorf("truncateMiddle(%q, %d) = %q, want %q", tc.path, tc.width, got, tc.want)
		}
	}
}

func TestDisplayPath(t *testing.T) {
	for _, tc := range []struct{ root, path, want string }{
		{goldenProject, goldenProject + "/.claude/agents/a.md", ".claude/agents/a.md"},
		{goldenProject, goldenHome + "/.claude/agents/a.md", "~/.claude/agents/a.md"},
		{goldenProject, "/srv/codex/a.toml", "/srv/codex/a.toml"},
		// The home directory is no project: its files show as ~/...
		{goldenHome, goldenHome + "/.omp/agents/a.md", "~/.omp/agents/a.md"},
		// A project inside home shows project-relative paths.
		{goldenHome + "/src/shop", goldenHome + "/src/shop/.codex/a.toml", ".codex/a.toml"},
	} {
		if got := displayPath(tc.root, goldenHome, tc.path); got != tc.want {
			t.Errorf("displayPath(%q, %q) = %q, want %q", tc.root, tc.path, got, tc.want)
		}
	}
}
