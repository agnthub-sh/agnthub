package harness_test

import (
	"path/filepath"
	"testing"

	"github.com/heynemann/agnthub/internal/agent"
	"github.com/heynemann/agnthub/internal/harness"
	"github.com/heynemann/agnthub/internal/platform/env"
)

func TestCopilotIdentityStripsAgentSuffixAndIgnoresDisplayName(t *testing.T) {
	copilot := installer(t, agent.Copilot)
	for path, want := range map[string]string{
		".github/agents/reviewer.agent.md": "reviewer",
		".github/agents/reviewer.md":       "reviewer",
	} {
		native, err := copilot.Identify(
			path,
			[]byte("---\nname: Code Reviewer\ndescription: Reviews\n---\nbody\n"),
		)
		if err != nil {
			t.Fatal(err)
		}
		if native.ID != want {
			t.Fatalf("%s: want identity %q, got %q", path, want, native.ID)
		}
	}
}

func TestCopilotConversionWritesNameDescriptionAndBody(t *testing.T) {
	artifact, err := convert(
		installer(t, agent.Copilot),
		"reviewer",
		portable(t, "---\nname: reviewer\ndescription: Reviews\n---\nReview it.\n"),
	)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := agent.ParseMarkdown(artifact.FileName, artifact.Content)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.FileName != "reviewer.agent.md" || doc.String("name") != "reviewer" ||
		doc.String("description") != "Reviews" || doc.Body != "Review it.\n" ||
		len(doc.Frontmatter) != 2 || artifact.Converter != "copilot/v2" || !artifact.Generated {
		t.Fatalf("unexpected conversion: %+v\n%s", artifact, artifact.Content)
	}
}

func TestCopilotConversionRefusesBehavioralFields(t *testing.T) {
	_, err := convert(
		installer(t, agent.Copilot),
		"reviewer",
		portable(t, "---\nname: reviewer\ndescription: Reviews\nreadonly: true\n---\nbody\n"),
	)
	requireIncompatible(t, err)
}

func TestCopilotNativeVariantKeepsToolsAndModelPolicy(t *testing.T) {
	raw := `---
name: Reviewer
description: Read-only reviewer
tools: ["read", "search"]
model: claude-sonnet-4.6
modelPolicy: required
include-custom-instructions: true
user-invocable: false
mcp-servers:
  docs:
    type: local
    command: docs-server
handoffs:
  - label: Implement
---
Review only.
`
	artifact, err := installer(t, agent.Copilot).Native(
		"reviewer",
		variant(agent.Copilot, raw),
	)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.FileName != "reviewer.agent.md" || string(artifact.Content) != raw {
		t.Fatalf("native variant must install byte-for-byte as <id>.agent.md: %+v", artifact)
	}
	if len(artifact.Notes) != 2 {
		t.Fatalf("IDE-only field and executable MCP servers must be noted, got %q", artifact.Notes)
	}
}

func TestCopilotRefusesVariantsTheCLIWouldDropOrIgnore(t *testing.T) {
	for name, frontmatter := range map[string]string{
		"missing description":     "name: r",
		"numeric description":     "description: 5",
		"tools map":               "description: d\ntools:\n  read: true",
		"non-string tool":         "description: d\ntools: [read, 3]",
		"non-boolean flag":        "description: d\ninclude-custom-instructions: 1",
		"mcp-servers list":        "description: d\nmcp-servers: [a]",
		"unknown model policy":    "description: d\nmodelPolicy: whatever",
		"unknown restrictive key": "description: d\npermission:\n  edit: deny",
		"vscode-only target":      "description: d\ntarget: vscode",
		"unknown target":          "description: d\ntarget: cloud",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := installer(t, agent.Copilot).Native(
				"reviewer",
				variant(agent.Copilot, "---\n"+frontmatter+"\n---\nbody\n"),
			)
			requireInvalid(t, err)
		})
	}
}

func TestCopilotDestinationsAreTargetSpecific(t *testing.T) {
	copilot := installer(t, agent.Copilot)
	ctx := harness.Context{
		Env:         env.FromMap(map[string]string{"HOME": "/home/u", "XDG_CONFIG_HOME": "/xdg"}),
		ProjectRoot: "/work/p",
	}
	project, err := copilot.Destination(ctx, harness.ScopeProject, nil)
	if err != nil || project.Dir != filepath.FromSlash("/work/p/.github/agents") ||
		!project.Shared {
		t.Fatalf("project destination: %+v %v", project, err)
	}
	global, err := copilot.Destination(ctx, harness.ScopeGlobal, nil)
	if err != nil || global.Dir != filepath.FromSlash("/home/u/.copilot/agents") {
		t.Fatalf("global destination ignores XDG and defaults to ~/.copilot: %+v %v", global, err)
	}
	ctx.Env = ctx.Env.With("COPILOT_HOME", "/ch")
	global, err = copilot.Destination(ctx, harness.ScopeGlobal, nil)
	if err != nil || global.Dir != filepath.FromSlash("/ch/agents") {
		t.Fatalf("COPILOT_HOME global destination: %+v %v", global, err)
	}
}
