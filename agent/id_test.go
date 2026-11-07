package agent_test

import (
	"strings"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
)

func TestNormalizeIDDerivesIdentifiersFromNames(t *testing.T) {
	cases := map[string]string{
		"code-reviewer":             "code-reviewer",
		"code_reviewer":             "code_reviewer",
		"Search Relevance Engineer": "search-relevance-engineer",
		"Café Planner (v2)":         "cafe-planner-v2",
		"  --Ops & Release!! ":      "ops-release",
		"ŁÓDŹ_ops":                  "lodz_ops",
		"Straße":                    "strasse",
		"FabricAdmin":               "fabricadmin",
		"code.reviewer":             "code-reviewer",
		"_private":                  "private",
		"计划":                        "",
		"🔎":                         "",
	}
	for name, want := range cases {
		if got := agent.NormalizeID(name); got != want {
			t.Errorf("NormalizeID(%q) = %q, want %q", name, got, want)
		}
	}
	long := agent.NormalizeID(strings.Repeat("Word ", 30))
	if !agent.ValidID(long) {
		t.Errorf("a long name must still yield a valid identifier, got %q", long)
	}
}

func TestIDFromPathStripsAgentExtensions(t *testing.T) {
	cases := map[string]string{
		"engineering/engineering-search-relevance-engineer.md": "engineering-search-relevance-engineer",
		".github/agents/FabricAdmin.agent.md":                  "fabricadmin",
		".codex/agents/Issue Triager.toml":                     "issue-triager",
		"agents/Search Relevance Engineer.md":                  "search-relevance-engineer",
	}
	for name, want := range cases {
		if got := agent.IDFromPath(name); got != want {
			t.Errorf("IDFromPath(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestDisplayNameIsAnyNonIdentifierName(t *testing.T) {
	for name, want := range map[string]bool{
		"Search Relevance Engineer": true, "FabricAdmin": true, "code.reviewer": true,
		"code-reviewer": false, "": false, "   ": false,
	} {
		if got := agent.DisplayName(name); got != want {
			t.Errorf("DisplayName(%q) = %t, want %t", name, got, want)
		}
	}
}
