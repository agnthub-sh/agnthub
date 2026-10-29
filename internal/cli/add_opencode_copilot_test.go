package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddInstallsPortableAgentIntoOpenCodeAndCopilot(t *testing.T) {
	h := newHarness(t)
	h.write("agents/reviewer.md", reviewer)
	code, out := h.run("add", h.source, "--harness", "opencode", "--harness", "copilot", "--yes")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if got := h.installed(
		".opencode/agents/reviewer.md",
	); !strings.Contains(
		got,
		"mode: subagent",
	) {
		t.Fatalf("OpenCode role must be an explicit subagent:\n%s", got)
	}
	if got := h.installed(
		".github/agents/reviewer.agent.md",
	); !strings.Contains(
		got,
		"name: reviewer",
	) {
		t.Fatalf("unexpected Copilot artifact:\n%s", got)
	}

	code, out = h.run("add", h.source, "-g", "-H", "opencode", "-H", "copilot", "--yes")
	if code != 0 {
		t.Fatalf("global exit %d: %s", code, out)
	}
	for _, rel := range []string{
		".config/opencode/agents/reviewer.md",
		".copilot/agents/reviewer.agent.md",
	} {
		if _, err := os.Stat(filepath.Join(h.home, rel)); err != nil {
			t.Fatalf("global install missing %s: %v", rel, err)
		}
	}
}

func TestInvalidOpenCodeOrCopilotVariantNeverFallsBackToConversion(t *testing.T) {
	for harnessName, variantPath := range map[string]string{
		"opencode": ".opencode/agents/reviewer.md",
		"copilot":  ".github/agents/reviewer.agent.md",
	} {
		t.Run(harnessName, func(t *testing.T) {
			h := newHarness(t)
			h.write("agnthub.yaml", `schemaVersion: 1
agents:
  reviewer:
    portable: agents/reviewer.md
    variants:
      `+harnessName+`:
        path: native/reviewer.md
`)
			h.write("agents/reviewer.md", reviewer)
			h.write(
				"native/reviewer.md",
				"---\ndescription: d\nmode: helper\ntarget: vscode\n---\nbody\n",
			)
			code, out := h.run(
				"add", h.source, "--agent", "reviewer", "--harness", harnessName, "--yes",
			)
			if code == 0 || !strings.Contains(out, "invalid") {
				t.Fatalf("invalid explicit variant must fail, exit %d: %s", code, out)
			}
			dir := filepath.Join(h.project, filepath.Dir(variantPath))
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("nothing may be written when a variant is invalid")
			}
		})
	}
}
