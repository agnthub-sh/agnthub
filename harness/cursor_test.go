package harness_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heynemann/agnthub/agent"
	"github.com/heynemann/agnthub/harness"
	"github.com/heynemann/agnthub/platform/env"
)

func installer(t *testing.T, id agent.HarnessID) harness.Installer {
	t.Helper()
	installer, err := harness.Default().Installer(id)
	if err != nil {
		t.Fatal(err)
	}
	return installer
}

func variant(h agent.HarnessID, content string) *agent.VariantFile {
	return &agent.VariantFile{Harness: h, Path: "agents/reviewer.md", Raw: []byte(content)}
}

func portable(t *testing.T, content string) *agent.Document {
	t.Helper()
	doc, err := agent.ParseMarkdown("agents/reviewer/AGENT.md", []byte(content))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func requireInvalid(t *testing.T, err error) {
	t.Helper()
	var invalid *harness.InvalidVariantError
	if !errors.As(err, &invalid) {
		t.Fatalf("want InvalidVariantError, got %v", err)
	}
}

func requireIncompatible(t *testing.T, err error) {
	t.Helper()
	var incompatible *harness.IncompatibleError
	if !errors.As(err, &incompatible) {
		t.Fatalf("want IncompatibleError, got %v", err)
	}
}

func TestCursorNativeReadonlyIsInstalledVerbatim(t *testing.T) {
	const content = "---\nname: reviewer\ndescription: Reviews code\nmodel: inherit\n" +
		"readonly: true\nis_background: false\n---\nReview the diff.\n"
	artifact, err := installer(t, agent.Cursor).Native("reviewer", variant(agent.Cursor, content))
	if err != nil {
		t.Fatal(err)
	}
	if string(artifact.Content) != content || artifact.Generated ||
		artifact.FileName != "reviewer.md" {
		t.Fatalf("native variant must be installed byte-for-byte: %+v", artifact)
	}
}

func TestCursorNativeRefusesReadonlyCursorWouldNotEnforce(t *testing.T) {
	cases := map[string]string{
		"string readonly":  "readonly: \"true\"\n",
		"yes readonly":     "readonly: yes\n",
		"trailing comment": "readonly: true # never edit\n",
		"nested readonly":  "metadata:\n  readonly: true\n",
		"background alias": "is_background: false\nbackground: true\n",
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			content := "---\nname: reviewer\ndescription: Reviews code\n" + extra + "---\nReview.\n"
			_, err := installer(t, agent.Cursor).Native("reviewer", variant(agent.Cursor, content))
			requireInvalid(t, err)
		})
	}
}

func TestCursorNativeRefusesUnenforcedOrMisidentifiedVariants(t *testing.T) {
	cases := map[string]struct{ id, content string }{
		"undocumented tools": {
			"reviewer", "---\nname: reviewer\ndescription: d\ntools: Read, Grep\n---\nBody\n",
		},
		"name mismatch": {"reviewer", "---\nname: other\ndescription: d\n---\nBody\n"},
		"quoted name":   {"reviewer", "---\nname: \"reviewer\"\ndescription: d\n---\nBody\n"},
		"filename name differs": {
			"code_reviewer", "---\ndescription: d\n---\nBody\n",
		},
		"multi-line description": {
			"reviewer", "---\nname: reviewer\ndescription: >\n  Reviews code\n---\nBody\n",
		},
		"empty body":    {"reviewer", "---\nname: reviewer\ndescription: d\n---\n"},
		"numeric model": {"reviewer", "---\nname: reviewer\ndescription: d\nmodel: 5\n---\nBody\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := installer(t, agent.Cursor).Native(tc.id, variant(agent.Cursor, tc.content))
			requireInvalid(t, err)
		})
	}
}

func TestCursorNativeAcceptsFilenameIdentity(t *testing.T) {
	const content = "---\ndescription: 'Reviews: code'\n---\nBody\n"
	if _, err := installer(
		t,
		agent.Cursor,
	).Native("reviewer", variant(agent.Cursor, content)); err != nil {
		t.Fatal(err)
	}
}

func TestCursorConversionRefusesRestrictionsInsteadOfDemotingThem(t *testing.T) {
	for _, field := range []string{"readonly: true", "tools: [read]", "model: fast"} {
		doc := portable(
			t,
			"---\nname: reviewer\ndescription: d\n"+field+"\n---\nNever edit files.\n",
		)
		_, err := convert(installer(t, agent.Cursor), "reviewer", doc)
		requireIncompatible(t, err)
	}
}

func TestCursorConversionIsReadableByCursor(t *testing.T) {
	descriptions := []string{
		"Reviews code",
		"Reviews: code with \"quotes\" and a description long enough that a YAML encoder might fold it across lines",
		"Reviews code\nacross several\nlines",
	}
	cursor := installer(t, agent.Cursor)
	for _, description := range descriptions {
		doc := portable(t, "---\nname: reviewer\ndescription: |-\n  "+
			strings.ReplaceAll(description, "\n", "\n  ")+"\nlicense: MIT\n---\nReview the diff.\n")
		artifact, err := convert(cursor, "reviewer", doc)
		if err != nil {
			t.Fatalf("%q: %v", description, err)
		}
		if !artifact.Generated || artifact.Converter != "cursor/v3" || len(artifact.Notes) == 0 {
			t.Fatalf("generated artifact must record converter and omissions: %+v", artifact)
		}
		// The generated file must pass the same checks as an authored Cursor variant.
		if _, err := cursor.Native(
			"reviewer",
			variant(agent.Cursor, string(artifact.Content)),
		); err != nil {
			t.Fatalf("%q: generated content is not a valid Cursor agent: %v", description, err)
		}
	}
}

func TestCursorDestinations(t *testing.T) {
	ctx := harness.Context{
		Env:         env.FromMap(map[string]string{"HOME": "/home/u"}),
		ProjectRoot: "/p",
	}
	cursor := installer(t, agent.Cursor)
	project, err := cursor.Destination(ctx, harness.ScopeProject, nil)
	if err != nil || project.Dir != filepath.Join("/p", ".cursor", "agents") || !project.Shared {
		t.Fatalf("project destination: %+v %v", project, err)
	}
	global, err := cursor.Destination(ctx, harness.ScopeGlobal, nil)
	if err != nil || global.Dir != filepath.Join("/home/u", ".cursor", "agents") {
		t.Fatalf("global destination: %+v %v", global, err)
	}
}
