package agent_test

import (
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

func TestLenientParsingRejectsStructuredInvalidHeaders(t *testing.T) {
	for _, raw := range []string{
		"---\nname: a\ndescription: x: y\ntools:\n  - Read\n---\nBody\n",
		"---\nname: a\nname: b: c\n---\nBody\n",
		"---\nnot a key value line: [\n---\nBody\n",
	} {
		if _, err := agent.ParseMarkdownLenient("a.md", []byte(raw)); err == nil {
			t.Errorf("expected rejection for %q", raw)
		}
	}
}
