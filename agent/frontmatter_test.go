package agent_test

import (
	"reflect"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
)

func TestLenientParsingAcceptsUnquotedColonsLikeClaudeCode(t *testing.T) {
	raw := []byte(
		"---\nname: reviewer\ndescription: Use when: reviewing. Example: user: hi\n---\nBody\n",
	)
	if _, err := agent.ParseMarkdown("a.md", raw); err == nil {
		t.Fatal("strict YAML parsing should reject the unquoted colon")
	}
	doc, err := agent.ParseMarkdownLenient("a.md", raw)
	if err != nil {
		t.Fatal(err)
	}
	if doc.String("description") != "Use when: reviewing. Example: user: hi" ||
		doc.Body != "Body\n" {
		t.Fatalf("unexpected parse: %+v", doc.Frontmatter)
	}
}

// Claude Code 2.1.309 loads such a header with its lists intact: an agent like
// this is listed with "Tools: All tools except Write, Edit".
func TestLenientParsingKeepsStructureAroundUnquotedColonsLikeClaudeCode(t *testing.T) {
	raw := []byte("---\nname: debugger\n" +
		`description: Use when: tests fail.\n\nExample: user: "it broke"` + "\n" +
		"model: sonnet\ndisallowedTools:\n  - Write\n  - Edit\nmaxTurns: 5\n---\nBody\n")
	doc, err := agent.ParseMarkdownLenient("a.md", raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.String(
		"description",
	); got != `Use when: tests fail.\n\nExample: user: "it broke"` {
		t.Errorf("description = %q", got)
	}
	if !reflect.DeepEqual(doc.Frontmatter["disallowedTools"], []any{"Write", "Edit"}) ||
		doc.Frontmatter["maxTurns"] != 5 || doc.String("model") != "sonnet" ||
		doc.Body != "Body\n" {
		t.Fatalf("unexpected parse: %#v", doc.Frontmatter)
	}
}

func TestLenientParsingRejectsStructuredInvalidHeaders(t *testing.T) {
	for _, raw := range []string{
		"---\nname: a\nname: b: c\n---\nBody\n",
		"---\nnot a key value line: [\n---\nBody\n",
		"---\nname: a\ndescription: x: y\ntools:\n  - Read\n - Grep\n---\nBody\n",
	} {
		if _, err := agent.ParseMarkdownLenient("a.md", []byte(raw)); err == nil {
			t.Errorf("expected rejection for %q", raw)
		}
	}
}
