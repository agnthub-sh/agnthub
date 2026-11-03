package harness_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/harness"
)

const pluginAgent = "---\nname: reviewer\ndescription: Reviews code.\n" +
	"permissionMode: acceptEdits\nhooks:\n  Stop:\n    - hooks:\n        - type: command\n" +
	"          command: echo done\nmcpServers:\n  - github\ntools: Read, Grep\n" +
	"---\nYou review code.\n"

func pluginVariant(plugin, content string) *agent.VariantFile {
	return &agent.VariantFile{
		Harness: agent.Claude, Path: "plugins/review/agents/reviewer.md",
		Raw: []byte(content), Plugin: plugin,
	}
}

// Claude Code ignores permissionMode, hooks, mcpServers and initialPrompt in a
// plugin's agents; installed into an agents directory they would take effect,
// so a plugin agent installs without them, with a note.
func TestPluginAgentsInstallWithoutTheFieldsPluginsIgnore(t *testing.T) {
	artifact, err := installer(
		t,
		agent.Claude,
	).Native("reviewer", pluginVariant("review", pluginAgent))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := agent.ParseMarkdown(artifact.FileName, artifact.Content)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"permissionMode", "hooks", "mcpServers"} {
		if _, kept := doc.Frontmatter[key]; kept {
			t.Errorf("%s installed from a plugin agent", key)
		}
	}
	if doc.Frontmatter["tools"] != "Read, Grep" || doc.Body != "You review code.\n" {
		t.Errorf("other fields must stay as authored:\n%s", artifact.Content)
	}
	if notes := strings.Join(artifact.Notes, "\n"); strings.Count(notes, "omitted") != 3 {
		t.Errorf("each omission needs a note, got %q", notes)
	}

	// The same file outside a plugin keeps every field.
	standalone, err := installer(t, agent.Claude).Native("reviewer", pluginVariant("", pluginAgent))
	if err != nil || string(standalone.Content) != pluginAgent {
		t.Fatalf("a standalone variant installs byte for byte: %v\n%s", err, standalone.Content)
	}
}

func TestPluginAgentsConvertAsThePluginDeliversThem(t *testing.T) {
	content := strings.Replace(pluginAgent, "tools: Read, Grep\n", "", 1)
	def := &agent.Definition{
		ID: "reviewer",
		Variants: map[agent.HarnessID]*agent.VariantFile{
			agent.Claude: pluginVariant("review", content),
		},
	}
	for _, target := range nonClaude {
		artifact, _, _, err := harness.Default().Generate(installer(t, target), def)
		if err != nil {
			t.Errorf("%s: %v", target, err)
			continue
		}
		if !strings.Contains(strings.Join(artifact.Notes, "\n"), "plugin review") {
			t.Errorf(
				"%s: notes do not say what the plugin form ignored: %v",
				target,
				artifact.Notes,
			)
		}
	}
	// Outside a plugin these fields are behavior other harnesses cannot carry.
	_, err := generateFrom(t, agent.Claude, agent.OpenCode, ".claude/agents/reviewer.md", content)
	requireIncompatible(t, err)
}

func TestPluginAgentsThatNeedTheirPluginAreRefused(t *testing.T) {
	for _, body := range []string{
		"Run ${CLAUDE_PLUGIN_ROOT}/scripts/check.sh first.\n",
		"Cache results in ${CLAUDE_PLUGIN_DATA}.\n",
		"Use the ${user_config.api_endpoint} endpoint.\n",
	} {
		content := "---\nname: reviewer\ndescription: Reviews code.\n---\n" + body
		_, err := installer(t, agent.Claude).Native("reviewer", pluginVariant("review", content))
		var invalid *harness.InvalidVariantError
		if !errors.As(err, &invalid) {
			t.Errorf("%q: want InvalidVariantError, got %v", body, err)
		}
		def := &agent.Definition{
			ID: "reviewer",
			Variants: map[agent.HarnessID]*agent.VariantFile{
				agent.Claude: pluginVariant("review", content),
			},
		}
		if _, _, _, err := harness.Default().
			Generate(installer(t, agent.OpenCode), def); err == nil {
			t.Errorf("%q: converted an agent that needs its plugin", body)
		}
	}
}
