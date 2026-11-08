package cli_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
)

type listedIgnore struct {
	Agents []struct {
		ID string `json:"id"`
	} `json:"agents"`
	Ignore *struct {
		All     bool     `json:"all"`
		Paths   []string `json:"paths"`
		Applied bool     `json:"applied"`
	} `json:"ignore"`
}

func (h *harnessT) listIgnore(args ...string) listedIgnore {
	h.t.Helper()
	code, out := h.run(append([]string{"add", h.source, "--list", "--json"}, args...)...)
	var listed listedIgnore
	if code != 0 || sonic.UnmarshalString(out, &listed) != nil {
		h.t.Fatalf("add --list --json exit %d: %s", code, out)
	}
	return listed
}

func TestAddRefusesASourceThatAsksToBeIgnored(t *testing.T) {
	h := newHarness(t)
	h.write("agents/reviewer.md", reviewer)
	h.write(".agnthub-ignore", "# not for agnthub\n")

	listed := h.listIgnore()
	if len(listed.Agents) != 0 || listed.Ignore == nil || !listed.Ignore.All ||
		!listed.Ignore.Applied {
		t.Fatalf("--list must report the whole source ignored: %+v", listed)
	}
	if code, out := h.run("add", h.source, "--list"); code != 0 ||
		!strings.Contains(out, "asks agnthub to ignore it") {
		t.Fatalf("--list must say the source asks to be ignored, exit %d: %s", code, out)
	}
	code, out := h.run("add", h.source, "--harness", "claude", "--yes")
	if code == 0 || !strings.Contains(out, "asks agnthub to ignore it") ||
		!strings.Contains(out, "--include-ignored") {
		t.Fatalf("add must refuse an ignored source and name the override, exit %d: %s", code, out)
	}

	listed = h.listIgnore("--include-ignored")
	if len(listed.Agents) != 1 || listed.Ignore == nil || listed.Ignore.Applied {
		t.Fatalf("--include-ignored must list the agents and report the rules unapplied: %+v",
			listed)
	}
	if code, out := h.run("add", h.source, "--harness", "claude", "--yes",
		"--include-ignored"); code != 0 {
		t.Fatalf("--include-ignored must install, exit %d: %s", code, out)
	}
	h.installed(".claude/agents/reviewer.md")
}

func TestAddHintsAtIgnoredPathsForAMissingAgent(t *testing.T) {
	h := newHarness(t)
	h.write("agents/reviewer.md", reviewer)
	h.write("agents/internal.md", agentFile("internal"))
	h.write("agnthub.yaml", "ignore: [agents/internal.md]\n")

	listed := h.listIgnore()
	if len(listed.Agents) != 1 || listed.Agents[0].ID != "reviewer" || listed.Ignore == nil ||
		listed.Ignore.All || !slices.Equal(listed.Ignore.Paths, []string{"agents/internal.md"}) {
		t.Fatalf("--list must hide the ignored agent and report the path: %+v", listed)
	}
	code, out := h.run("add", h.source, "--agent", "internal", "--harness", "claude", "--yes")
	if code == 0 || !strings.Contains(out, "agents/internal.md") ||
		!strings.Contains(out, "--include-ignored") {
		t.Fatalf("a missing agent must point at the ignored paths, exit %d: %s", code, out)
	}
	if code, out := h.run("add", h.source, "--agent", "internal", "--harness", "claude", "--yes",
		"--include-ignored"); code != 0 {
		t.Fatalf("--include-ignored must install the ignored agent, exit %d: %s", code, out)
	}
	h.installed(".claude/agents/internal.md")
}

func TestAddIncludeIgnoredAppliesToDependencySources(t *testing.T) {
	h := newHarness(t)
	h.writeReviewerSource(reviewerManifest)
	tools := toolsRepo("1111")
	tools.files[".agnthub-ignore"] = "/\n"
	git := fakeGit{"acme/tools#v1": tools}

	code, out := h.runWith(git, nil, "add", h.source, "--agent", "reviewer",
		"--harness", "claude", "--yes")
	if code == 0 || !strings.Contains(out, "acme/tools#v1 asks agnthub to ignore it") {
		t.Fatalf("a dependency in an ignored source must say so, exit %d: %s", code, out)
	}
	if code, out := h.runWith(git, nil, "add", h.source, "--agent", "reviewer",
		"--harness", "claude", "--yes", "--include-ignored"); code != 0 {
		t.Fatalf("--include-ignored must resolve dependencies too, exit %d: %s", code, out)
	}
	h.installed(".claude/agents/linter.md")
}

func TestIgnoreRulesNeverBreakInstalledAgents(t *testing.T) {
	h := newHarness(t)
	h.write("agents/reviewer.md", reviewer)
	if code, out := h.run("add", h.source, "--harness", "claude", "--yes"); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	// The owner opts out after the install, and changes the agent.
	h.write(".agnthub-ignore", "")
	h.write("agents/reviewer.md", strings.Replace(reviewer, "carefully", "twice", 1))

	if code, out := h.run("check"); code != 0 || strings.Contains(out, "no longer") {
		t.Fatalf("check must still see the installed agent, exit %d: %s", code, out)
	}
	if code, out := h.run("update", "--yes"); code != 0 ||
		!strings.Contains(h.installed(".claude/agents/reviewer.md"), "Review the diff twice.") {
		t.Fatalf("update must keep updating the installed agent, exit %d: %s", code, out)
	}
	h.resetMachine(".claude")
	if code, out := h.run("restore", "--yes"); code != 0 {
		t.Fatalf("restore must reproduce the installed agent, exit %d: %s", code, out)
	}
	h.installed(".claude/agents/reviewer.md")
}
