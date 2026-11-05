package install_test

import (
	"strings"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/install"
)

// devexEngineer is the header of claude-craft's devex-engineer agent: every
// field it sets to Claude Code's default or leaves empty restricts nothing.
const devexEngineer = "---\nname: devex-engineer\n" +
	"description: Developer experience, CLI design, onboarding, tooling, DX metrics specialist\n" +
	"model: sonnet\nmaxTurns: 6\neffort: medium\nmemory: user\n" +
	"tools: [Read, Glob, Grep, Edit, Write, Bash, WebFetch, WebSearch]\n" +
	"disallowedTools: []\npermissionMode: default\n---\n\n# DevEx Engineer Agent\n"

func TestAssessTreatsClaudeDefaultFieldsAsAbsent(t *testing.T) {
	path := ".claude/agents/devex-engineer.md"
	def := &agent.Definition{
		ID: "devex-engineer",
		Variants: map[agent.HarnessID]*agent.VariantFile{
			agent.Claude: {Harness: agent.Claude, Path: path, Raw: []byte(devexEngineer)},
		},
	}
	for h, a := range assessAll(def) {
		switch {
		case h == agent.Claude:
			if a.Status != install.StatusNative {
				t.Errorf("claude: want the authored file installed as is, got %+v", a)
			}
		case h == agent.Copilot:
			// Copilot CLI installs it without web search, the one capability it
			// cannot grant; the default fields change nothing.
			if a.Status != install.StatusGenerated ||
				strings.Join(a.Reduced, ",") != "web search" ||
				a.OptIn != "" {
				t.Errorf("copilot: want only the web search reduced, got %+v", a)
			}
		case a.Status != install.StatusGenerated || a.SourcePath != path:
			t.Errorf("%s: want a conversion of %s, got %+v", h, path, a)
		}
	}
}
