package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"

	"github.com/agnthub-sh/agnthub/internal/cli"
	"github.com/agnthub-sh/agnthub/platform/env"
	"github.com/agnthub-sh/agnthub/state"
)

type harnessT struct {
	t       *testing.T
	home    string
	project string
	source  string
}

func newHarness(t *testing.T) *harnessT {
	t.Helper()
	base := t.TempDir()
	h := &harnessT{
		t:       t,
		home:    filepath.Join(base, "home"),
		project: filepath.Join(base, "project"),
		source:  filepath.Join(base, "source"),
	}
	for _, dir := range []string{h.home, h.project, h.source} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func (h *harnessT) write(rel, content string) {
	h.t.Helper()
	path := filepath.Join(h.source, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harnessT) run(args ...string) (int, string) {
	h.t.Helper()
	var out bytes.Buffer
	app := cli.NewApp(cli.App{
		Env:      env.FromMap(map[string]string{"HOME": h.home}),
		WorkDir:  h.project,
		Stdin:    strings.NewReader(""),
		Stdout:   &out,
		Stderr:   &out,
		Now:      func() time.Time { return time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC) },
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
	})
	code := app.Run(context.Background(), args)
	return code, out.String()
}

func (h *harnessT) installed(rel string) string {
	h.t.Helper()
	content, err := os.ReadFile(filepath.Join(h.project, rel))
	if err != nil {
		h.t.Fatal(err)
	}
	return string(content)
}

const reviewer = `---
name: reviewer
description: Reviews code
---
Review the diff carefully.
`

func TestAddInstallsPortableAgentAndRecordsReceipts(t *testing.T) {
	h := newHarness(t)
	h.write("agents/reviewer.md", reviewer)
	code, out := h.run("add", h.source, "--harness", "claude", "--yes")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	got := h.installed(".claude/agents/reviewer.md")
	if !strings.Contains(got, "name: reviewer") ||
		!strings.HasSuffix(got, "Review the diff carefully.\n") {
		t.Fatalf("unexpected artifact:\n%s", got)
	}
	lock, err := state.LoadLock(filepath.Join(h.project, state.LockFileName))
	if err != nil {
		t.Fatal(err)
	}
	if len(lock.Agents) != 1 || lock.Agents[0].Artifacts[0].Digest != state.Digest([]byte(got)) {
		t.Fatalf("lock does not match installed content: %+v", lock)
	}
	ownership, err := state.LoadOwnership(
		state.OwnershipPath(filepath.Join(h.home, ".local", "state")),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(ownership.Records) != 1 {
		t.Fatalf("want one ownership record, got %+v", ownership.Records)
	}
	code, out = h.run("add", h.source, "--harness", "claude", "--yes")
	if code != 0 || !strings.Contains(out, "unchanged") {
		t.Fatalf("reinstall should be a no-op, exit %d: %s", code, out)
	}
}

func TestAddRefusesUnmanagedAndLocallyModifiedFiles(t *testing.T) {
	h := newHarness(t)
	h.write("agents/reviewer.md", reviewer)
	target := filepath.Join(h.project, ".claude", "agents", "reviewer.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out := h.run("add", h.source, "--harness", "claude", "--yes")
	if code == 0 || !strings.Contains(out, "not managed by agnthub") {
		t.Fatalf("unmanaged file must be refused, exit %d: %s", code, out)
	}
	if h.installed(".claude/agents/reviewer.md") != "mine" {
		t.Fatal("unmanaged file was overwritten")
	}
	if _, err := os.Stat(filepath.Join(h.project, state.LockFileName)); !os.IsNotExist(err) {
		t.Fatal("refused plan must not write a lock")
	}

	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if code, out = h.run("add", h.source, "--harness", "claude", "--yes"); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if err := os.WriteFile(target, []byte("edited locally"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.write("agents/reviewer.md", strings.Replace(reviewer, "carefully", "quickly", 1))
	code, out = h.run("add", h.source, "--harness", "claude", "--yes")
	if code == 0 || !strings.Contains(out, "modified locally") {
		t.Fatalf("local edits must be protected, exit %d: %s", code, out)
	}
	if h.installed(".claude/agents/reviewer.md") != "edited locally" {
		t.Fatal("locally modified file was overwritten")
	}
}

func TestExplicitVariantWinsAndInvalidVariantNeverFallsBack(t *testing.T) {
	h := newHarness(t)
	h.write("agents/reviewer.md", reviewer)
	h.write(".claude/agents/reviewer.md", `---
name: reviewer
description: Native Claude reviewer
tools: Read, Grep
---
Native instructions.
`)
	code, out := h.run("add", h.source, "--harness", "claude", "--yes", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(h.installed(".claude/agents/reviewer.md"), "Native instructions.") {
		t.Fatal("authored variant must take precedence over conversion")
	}
	var plan struct {
		Items []struct {
			Variant string `json:"variant"`
		} `json:"items"`
	}
	if err := sonic.UnmarshalString(out, &plan); err != nil || plan.Items[0].Variant != "native" {
		t.Fatalf("json output should report native variant: %v %s", err, out)
	}

	h2 := newHarness(t)
	h2.write("agnthub.yaml", `schemaVersion: 1
agents:
  reviewer:
    portable: agents/reviewer.md
    variants:
      claude:
        path: claude/reviewer.md
`)
	h2.write("agents/reviewer.md", reviewer)
	h2.write("claude/reviewer.md", "---\nname: someone-else\ndescription: x\n---\nbody\n")
	code, out = h2.run("add", h2.source, "--harness", "claude", "--yes")
	if code == 0 || !strings.Contains(out, "invalid") {
		t.Fatalf("invalid explicit variant must fail, exit %d: %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(h2.project, ".claude")); !os.IsNotExist(err) {
		t.Fatal("nothing may be written when a variant is invalid")
	}
}

func TestInvalidDiscoveredVariantNeverFallsBackToConversion(t *testing.T) {
	variants := map[string]struct{ path, content string }{
		"copilot": {
			".github/agents/reviewer.agent.md",
			"---\nname: reviewer\ndescription: Copilot reviewer\nhandoffs-to: planner\n---\nbody\n",
		},
		"opencode": {
			".opencode/agents/reviewer.md",
			"---\ndescription: OpenCode reviewer\nmode: background\n---\nbody\n",
		},
		"claude": {".claude/agents/reviewer.md", "---\nname: reviewer\n---\nbody\n"},
	}
	for harnessID, variant := range variants {
		t.Run(harnessID, func(t *testing.T) {
			h := newHarness(t)
			h.write("agents/reviewer.md", reviewer)
			h.write(variant.path, variant.content)
			code, out := h.run("add", h.source, "--harness", harnessID, "--yes")
			if code == 0 || !strings.Contains(out, "invalid") {
				t.Fatalf(
					"an invalid %s variant must refuse, not convert the portable core; exit %d: %s",
					harnessID,
					code,
					out,
				)
			}
			if entries, _ := os.ReadDir(h.project); len(entries) != 0 {
				t.Fatalf("nothing may be written when a variant is invalid: %v", entries)
			}
			other := "omp"
			if code, out = h.run("add", h.source, "--harness", other, "--yes"); code != 0 {
				t.Fatalf("the invalid %s variant must not block %s, exit %d: %s",
					harnessID, other, code, out)
			}
		})
	}
}

func TestPortableBehavioralFieldsRefuseConversion(t *testing.T) {
	h := newHarness(t)
	h.write(
		"agents/reviewer.md",
		"---\nname: reviewer\ndescription: x\nreadonly: true\n---\nbody\n",
	)
	code, out := h.run("add", h.source, "--harness", "claude", "--yes")
	if code == 0 || !strings.Contains(out, `"readonly"`) {
		t.Fatalf("undeclared constraint must refuse conversion, exit %d: %s", code, out)
	}
}

func TestAddRequiresExplicitSelectionAndValidatesExtrasBeforeWriting(t *testing.T) {
	h := newHarness(t)
	h.write("agents/reviewer.md", reviewer)
	h.write("agents/planner.md", strings.ReplaceAll(reviewer, "reviewer", "planner"))
	if code, out := h.run("add", h.source, "--harness", "claude", "--yes"); code == 0 ||
		!strings.Contains(out, "choose agents") {
		t.Fatalf("multiple agents need an explicit selection: %s", out)
	}
	code, out := h.run(
		"add", h.source, "--agent", "reviewer", "--harness", "claude",
		"--extra", "omp.profile=hep", "--yes",
	)
	if code == 0 || !strings.Contains(out, "not selected") {
		t.Fatalf("extra for an unselected harness must fail: %s", out)
	}
	if _, err := os.Stat(filepath.Join(h.project, ".claude")); !os.IsNotExist(err) {
		t.Fatal("nothing may be written when extras are invalid")
	}
	if code, out = h.run(
		"add",
		h.source,
		"--agent",
		"reviewer",
		"--harness",
		"claude",
	); code == 0 ||
		!strings.Contains(out, "--yes") {
		t.Fatalf("non-interactive install without --yes must refuse: %s", out)
	}
}
