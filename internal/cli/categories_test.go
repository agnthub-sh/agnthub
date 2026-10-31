package cli_test

import (
	"slices"
	"testing"

	"github.com/bytedance/sonic"
)

func TestListReportsDeclaredCategoriesAndInstallIgnoresThem(t *testing.T) {
	h := newHarness(t)
	h.writeReviewerSource(
		"schemaVersion: 1\ncategories: [software-engineering, not-yet-known]\nagents:\n" +
			"  reviewer:\n    portable: agents/reviewer.md\n    categories: [code-review]\n" +
			"  planner:\n    portable: agents/planner.md\n",
	)

	code, out := h.run("add", h.source, "--list", "--json")
	var inventory struct {
		Categories []string `json:"categories"`
		Agents     []struct {
			ID         string   `json:"id"`
			Categories []string `json:"categories"`
		} `json:"agents"`
	}
	if code != 0 || sonic.UnmarshalString(out, &inventory) != nil {
		t.Fatalf("add --list --json exit %d: %s", code, out)
	}
	if !slices.Equal(inventory.Categories, []string{"software-engineering", "not-yet-known"}) {
		t.Fatalf("repository categories = %v", inventory.Categories)
	}
	got := map[string][]string{}
	for _, item := range inventory.Agents {
		got[item.ID] = item.Categories
	}
	if !slices.Equal(got["reviewer"], []string{"code-review"}) ||
		!slices.Equal(got["planner"], inventory.Categories) {
		t.Fatalf("agent categories = %v", got)
	}

	code, out = h.run(
		"add",
		h.source,
		"--agent",
		"reviewer",
		"--harness",
		"claude",
		"--yes",
		"--json",
	)
	if code != 0 || decodePlan(t, out).Status != "applied" {
		t.Fatalf(
			"a manifest with categories (including unknown ones) must install, exit %d: %s",
			code,
			out,
		)
	}
}
