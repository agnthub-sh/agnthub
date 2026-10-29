package harness_test

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/heynemann/agnthub/internal/agent"
	"github.com/heynemann/agnthub/internal/harness"
	"github.com/heynemann/agnthub/internal/platform/env"
)

func codexInstaller(t *testing.T) harness.Installer {
	t.Helper()
	installer, err := harness.Default().Installer(agent.Codex)
	if err != nil {
		t.Fatal(err)
	}
	return installer
}

func portableDoc(frontmatter map[string]any, body string) *agent.Document {
	return &agent.Document{Path: "agents/reviewer.md", Frontmatter: frontmatter, Body: body}
}

func TestCodexConvertPreservesInstructionTextExactly(t *testing.T) {
	codex := codexInstaller(t)
	bodies := map[string]string{
		"plain":           "Review the diff carefully.\n",
		"leading newline": "\nStarts after a blank line.\n",
		"no newline":      "Single line without a trailing newline",
		"quotes":          "Say \"hi\".\nTriple \"\"\" quotes and four \"\"\"\" here.\nEnds with quotes\"\"",
		"backslashes":     "Path C:\\Users\\x and \\n literal and \\\\ double.\n",
		"whitespace":      "Tabs\there\r\nCRLF line\r\n  trailing spaces  \n\n\n",
		"unicode":         "Grüße — 日本語 ✓ emoji 🚀 and a control \x01 char.\n",
		"toml-ish":        "name = \"evil\"\n[mcp_servers.x]\ncommand = \"rm\"\n'''literal'''\n",
	}
	for label, body := range bodies {
		t.Run(label, func(t *testing.T) {
			artifact, err := codex.Convert("reviewer", portableDoc(
				map[string]any{"name": "reviewer", "description": "Reviews \"code\"\nwell"},
				body,
			))
			if err != nil {
				t.Fatal(err)
			}
			if !artifact.Generated || artifact.Converter != "codex/v1" ||
				artifact.FileName != "reviewer.toml" {
				t.Fatalf("unexpected artifact metadata: %+v", artifact)
			}
			var fields map[string]any
			if err := toml.Unmarshal(artifact.Content, &fields); err != nil {
				t.Fatalf("generated TOML does not parse: %v\n%s", err, artifact.Content)
			}
			if len(fields) != 3 || fields["name"] != "reviewer" ||
				fields["description"] != "Reviews \"code\"\nwell" ||
				fields["developer_instructions"] != body {
				t.Fatalf("round trip changed the agent:\n%#v\n%s", fields, artifact.Content)
			}
			native, err := codex.Native("reviewer", &agent.VariantFile{
				Harness: agent.Codex, Path: "reviewer.toml", Raw: artifact.Content,
			})
			if err != nil || !bytes.Equal(native.Content, artifact.Content) {
				t.Fatalf("generated agent must satisfy native validation: %v", err)
			}
		})
	}
}

func TestCodexConvertRefusesNonPortableFields(t *testing.T) {
	codex := codexInstaller(t)
	for _, key := range []string{"tools", "model", "sandbox_mode", "readonly", "mcp_servers"} {
		_, err := codex.Convert("reviewer", portableDoc(
			map[string]any{"name": "reviewer", "description": "x", key: "value"},
			"body\n",
		))
		var incompatible *harness.IncompatibleError
		if !errors.As(err, &incompatible) {
			t.Fatalf("%s must refuse conversion, got %v", key, err)
		}
	}
	artifact, err := codex.Convert("reviewer", portableDoc(
		map[string]any{"name": "reviewer", "description": "x", "license": "MIT"},
		"body\n",
	))
	if err != nil || len(artifact.Notes) == 0 {
		t.Fatalf("harmless metadata is omitted with a note: %v %+v", err, artifact)
	}
	if _, err := codex.Convert("reviewer", portableDoc(
		map[string]any{"name": "reviewer", "description": "x"},
		"invalid \xff utf-8\n",
	)); err == nil {
		t.Fatal("text TOML cannot represent must refuse conversion")
	}
}

const codexNative = `# authored
name = "reviewer"
description = "Native Codex reviewer"
model = "gpt-5.4-mini"
model_reasoning_effort = "high"
developer_instructions = '''
Native instructions.
'''

[features]
shell_tool = false

[skills]
include_instructions = false
bundled = { enabled = false }

[[skills.config]]
path = "/skills/docs/SKILL.md"
enabled = false
`

func TestCodexNativeInstallsAuthoredBytes(t *testing.T) {
	artifact, err := codexInstaller(t).Native("reviewer", &agent.VariantFile{
		Harness: agent.Codex, Path: ".codex/agents/reviewer.toml", Raw: []byte(codexNative),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(artifact.Content) != codexNative || artifact.Generated ||
		artifact.FileName != "reviewer.toml" {
		t.Fatalf("native variant must install as authored: %+v", artifact)
	}
}

func TestCodexNativeRejectsInvalidAndParentOverridingVariants(t *testing.T) {
	base := "name = \"reviewer\"\ndescription = \"d\"\ndeveloper_instructions = \"i\"\n"
	cases := map[string]string{
		"invalid toml":          "name = ",
		"name mismatch":         "name = \"other\"\ndescription = \"d\"\ndeveloper_instructions = \"i\"\n",
		"missing instructions":  "name = \"reviewer\"\ndescription = \"d\"\n",
		"sandbox":               base + "sandbox_mode = \"danger-full-access\"\n",
		"approval":              base + "approval_policy = \"never\"\n",
		"provider":              base + "model_provider = \"openrouter\"\n",
		"mcp":                   base + "[mcp_servers.x]\ncommand = \"x\"\n",
		"unknown key":           base + "frobnicate = 1\n",
		"feature enabled":       base + "[features]\nshell_tool = true\n",
		"unsupported feature":   base + "[features]\nweb_search_request = false\n",
		"skill enabled":         base + "[[skills.config]]\npath = \"/s\"\nenabled = true\n",
		"non-string model":      base + "model = 5\n",
		"bundled skills enable": base + "[skills.bundled]\nenabled = true\n",
	}
	codex := codexInstaller(t)
	for label, raw := range cases {
		_, err := codex.Native("reviewer", &agent.VariantFile{
			Harness: agent.Codex, Path: "reviewer.toml", Raw: []byte(raw),
		})
		var invalid *harness.InvalidVariantError
		if !errors.As(err, &invalid) {
			t.Errorf("%s: want InvalidVariantError, got %v", label, err)
		}
	}
}

func TestCodexDestinationsHonorCodexHome(t *testing.T) {
	codex := codexInstaller(t)
	ctx := harness.Context{
		Env:         env.FromMap(map[string]string{"HOME": "/home/u"}),
		ProjectRoot: "/work/project",
	}
	project, err := codex.Destination(ctx, harness.ScopeProject, nil)
	if err != nil || project.Dir != filepath.Join("/work/project", ".codex", "agents") ||
		!project.Shared {
		t.Fatalf("project destination: %+v %v", project, err)
	}
	global, err := codex.Destination(ctx, harness.ScopeGlobal, nil)
	if err != nil || global.Dir != filepath.Join("/home/u", ".codex", "agents") {
		t.Fatalf("default global destination: %+v %v", global, err)
	}
	ctx.Env = ctx.Env.With("CODEX_HOME", "/opt/codex")
	global, err = codex.Destination(ctx, harness.ScopeGlobal, nil)
	if err != nil || global.Dir != filepath.Join("/opt/codex", "agents") {
		t.Fatalf("CODEX_HOME global destination: %+v %v", global, err)
	}
	ctx.Env = ctx.Env.With("CODEX_HOME", "relative/codex")
	global, err = codex.Destination(ctx, harness.ScopeGlobal, nil)
	if err != nil || global.Dir != filepath.Join("/home/u", ".codex", "agents") {
		t.Fatalf("relative CODEX_HOME must be ignored: %+v %v", global, err)
	}
}
