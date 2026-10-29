package harness_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/heynemann/agnthub/internal/agent"
	"github.com/heynemann/agnthub/internal/harness"
	"github.com/heynemann/agnthub/internal/platform/env"
)

func TestOpenCodeIdentityIsTheFileName(t *testing.T) {
	native, err := installer(t, agent.OpenCode).Identify(
		".opencode/agents/reviewer.md",
		[]byte("---\nname: Someone Else\ndescription: Reviews\nmode: subagent\n---\nbody\n"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if native.ID != "reviewer" {
		t.Fatalf("frontmatter name must not change OpenCode identity, got %q", native.ID)
	}
}

func TestOpenCodeConversionRegistersAnExplicitSubagent(t *testing.T) {
	artifact, err := installer(t, agent.OpenCode).Convert(
		"reviewer",
		portable(t, "---\nname: reviewer\ndescription: Reviews\nversion: 1\n---\nReview it.\n"),
	)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := agent.ParseMarkdown(artifact.FileName, artifact.Content)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.FileName != "reviewer.md" || doc.String("mode") != "subagent" ||
		doc.String("description") != "Reviews" || doc.Body != "Review it.\n" ||
		!artifact.Generated || artifact.Converter != "opencode/v1" {
		t.Fatalf("unexpected conversion: %+v\n%s", artifact, artifact.Content)
	}
	if _, named := doc.Frontmatter["name"]; named {
		t.Fatal("OpenCode identity is the file name; conversion must not add a name")
	}
}

func TestOpenCodeConversionRefusesBehavioralFields(t *testing.T) {
	_, err := installer(t, agent.OpenCode).Convert(
		"reviewer",
		portable(t, "---\nname: reviewer\ndescription: Reviews\ntools: Read\n---\nbody\n"),
	)
	requireIncompatible(t, err)
}

func TestOpenCodeNativeVariantKeepsAuthoredMeaning(t *testing.T) {
	raw := `---
description: Read-only reviewer
mode: subagent
model: openrouter/openai/gpt-4o-mini
temperature: 0.1
steps: 5
permission:
  edit: deny
  bash:
    "*": ask
    "git diff": allow
  my_custom_tool: deny
tools:
  write: false
---
Review only.
`
	artifact, err := installer(t, agent.OpenCode).Native(
		"reviewer",
		variant(agent.OpenCode, raw),
	)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.FileName != "reviewer.md" || string(artifact.Content) != raw || artifact.Generated {
		t.Fatalf("native variant must install byte-for-byte under the agent id: %+v", artifact)
	}
}

func TestOpenCodeNativeWithoutModeIsFlaggedAsPrimary(t *testing.T) {
	artifact, err := installer(t, agent.OpenCode).Native(
		"reviewer",
		variant(agent.OpenCode, "---\ndescription: Reviews\n---\nbody\n"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifact.Notes) == 0 {
		t.Fatal("a native agent without mode must be flagged as also primary")
	}
}

func TestOpenCodeRefusesVariantsOpenCodeWouldDrop(t *testing.T) {
	for name, frontmatter := range map[string]string{
		"missing description":    "mode: subagent",
		"unknown mode":           "description: d\nmode: helper",
		"model without provider": "description: d\nmodel: gpt-4o",
		"empty model id":         "description: d\nmodel: openrouter/",
		"unknown effect":         "description: d\npermission:\n  edit: maybe",
		"unknown pattern effect": "description: d\npermission:\n  bash:\n    \"*\": sometimes",
		"permission list":        "description: d\npermission:\n  - edit",
		"non-boolean tool":       "description: d\ntools:\n  write: nope",
		"non-boolean hidden":     "description: d\nhidden: maybe",
		"non-numeric temp":       "description: d\ntemperature: hot",
		"non-positive steps":     "description: d\nsteps: 0",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := installer(t, agent.OpenCode).Native(
				"reviewer",
				variant(agent.OpenCode, "---\n"+frontmatter+"\n---\nbody\n"),
			)
			requireInvalid(t, err)
		})
	}
}

func TestOpenCodeDestinationsAreTargetSpecific(t *testing.T) {
	opencode := installer(t, agent.OpenCode)
	ctx := harness.Context{
		Env:         env.FromMap(map[string]string{"HOME": "/home/u"}),
		ProjectRoot: "/work/p",
	}
	project, err := opencode.Destination(ctx, harness.ScopeProject, nil)
	if err != nil || project.Dir != filepath.FromSlash("/work/p/.opencode/agents") ||
		!project.Shared {
		t.Fatalf("project destination: %+v %v", project, err)
	}
	global, err := opencode.Destination(ctx, harness.ScopeGlobal, nil)
	if err != nil || global.Dir != filepath.FromSlash("/home/u/.config/opencode/agents") {
		t.Fatalf("default global destination: %+v %v", global, err)
	}
	ctx.Env = ctx.Env.With("XDG_CONFIG_HOME", "/xdg")
	global, err = opencode.Destination(ctx, harness.ScopeGlobal, nil)
	if err != nil || !strings.HasPrefix(global.Dir, filepath.FromSlash("/xdg/opencode/")) {
		t.Fatalf("XDG global destination: %+v %v", global, err)
	}
}
