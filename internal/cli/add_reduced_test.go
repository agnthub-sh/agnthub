package cli_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
)

// growthHacker is a minimized
// https://github.com/easonwang00/Startup-Agents/blob/dd10c9e1f21e/.claude/agents/growth-hacker.md;
// researcher a reviewer that runs shell commands but never edits.
const (
	growthHacker = "---\nname: growth-hacker\ndescription: Designs viral loops and runs " +
		"growth experiments.\ntools: Read, Write, Edit, Glob, Grep, Bash, WebFetch, WebSearch\n" +
		"---\nGrow.\n"
	shellReviewer = "---\nname: researcher\ndescription: Checks compliance with project " +
		"conventions.\ntools: [Read, Glob, Grep, Bash]\n---\nCheck.\n"
)

func TestAddShowsReducedCapabilityProminently(t *testing.T) {
	h := newHarness(t)
	h.write(".claude/agents/growth-hacker.md", growthHacker)
	code, out := h.run("add", h.source, "--agent", "growth-hacker", "--harness", "copilot",
		"--yes")
	if code != 0 || !strings.Contains(out, "! reduced: growth-hacker on copilot goes without "+
		"web search") {
		t.Fatalf("exit %d: want the reduced line\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(h.project, ".github", "agents",
		"growth-hacker.agent.md")); err != nil {
		t.Fatal(err)
	}
}

func TestAddWithoutShellNeedsTheFlag(t *testing.T) {
	h := newHarness(t)
	h.write(".claude/agents/researcher.md", shellReviewer)
	code, out := h.run("add", h.source, "--agent", "researcher", "--harness", "cursor", "--json")
	if code == 0 || !strings.Contains(out, "run again with --allow-without-shell") {
		t.Fatalf("exit %d: want a refusal naming the flag\n%s", code, out)
	}
	var plan struct {
		Items []struct {
			Action  string   `json:"action"`
			Reduced []string `json:"reduced"`
			OptIn   string   `json:"optIn"`
		} `json:"items"`
	}
	if err := sonic.Unmarshal([]byte(out), &plan); err != nil || len(plan.Items) != 1 {
		t.Fatalf("plan JSON: %v\n%s", err, out)
	}
	if item := plan.Items[0]; item.Action != "incompatible" ||
		item.OptIn != "allow-without-shell" ||
		!slices.Equal(item.Reduced, []string{"shell"}) {
		t.Fatalf("want the opt-in in --json, got %+v", item)
	}
	if _, err := os.Stat(filepath.Join(h.project, ".cursor")); !os.IsNotExist(err) {
		t.Fatal("nothing may be written without the opt-in")
	}
	code, out = h.run("add", h.source, "--agent", "researcher", "--harness", "cursor", "--yes",
		"--allow-without-shell")
	if code != 0 || !strings.Contains(out, "! reduced: researcher on cursor goes without shell") {
		t.Fatalf("exit %d: want an install without shell\n%s", code, out)
	}
	if !strings.Contains(h.installed(".cursor/agents/researcher.md"), "readonly: true") {
		t.Fatal("want a read-only Cursor agent")
	}
	// update needs the flag again: the opt-in is per command.
	h.write(
		".claude/agents/researcher.md",
		strings.Replace(shellReviewer, "Check.", "Check all.", 1),
	)
	if code, out = h.run("update", "--yes"); code == 0 {
		t.Fatalf("update without the flag must refuse, exit 0\n%s", out)
	}
	if code, out = h.run("update", "--yes", "--allow-without-shell"); code != 0 {
		t.Fatalf("update with the flag: exit %d\n%s", code, out)
	}
}
