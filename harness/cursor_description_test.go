package harness_test

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/agnthub-sh/agnthub/agent"
)

// cursorLine returns the raw value of the first top-level line of key in a
// Markdown file's frontmatter, as Cursor's line reader keeps it.
func cursorLine(content []byte, key string) string {
	header, _, _ := strings.Cut(strings.TrimPrefix(string(content), "---\n"), "\n---")
	for line := range strings.SplitSeq(header, "\n") {
		if value, ok := strings.CutPrefix(line, key+":"); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// requireOneLineDescription fails unless Cursor reads the description of
// content on one line with words of description: as written, or as a quoted
// scalar whose escapes Cursor shows as written.
func requireOneLineDescription(t *testing.T, content []byte, description string) {
	t.Helper()
	raw := cursorLine(content, "description")
	var read string
	if err := yaml.Unmarshal([]byte(raw), &read); err != nil {
		t.Fatalf("description line %q: %v", raw, err)
	}
	if want := strings.Join(strings.Fields(description), " "); read != want {
		t.Errorf("Cursor reads %q, want %q", read, want)
	}
}

// Descriptions from the refusal census that no single-line form Cursor reads
// unchanged can carry: each needs quoting (": ") and contains both quote
// characters. Minimized; instructions shortened.
var quotedDescriptionAgents = map[string]string{
	// https://github.com/ilya-kozyrev/delamain/blob/bd5848aba729/agents/worker-medium.md
	"worker-medium": "---\nname: reviewer\ndescription: General-purpose subagent pinned to " +
		"medium effort, so it does not inherit the session's effort. Choose the model per call " +
		"with the Agent tool's `model` parameter. Use it for bounded work from a finished " +
		"design: executing a specified change, a \"medium\" audit.\neffort: medium\n---\n" +
		"Do the bounded work.\n",
	// https://github.com/LING71671/Open-ClaudeCode/blob/1f0fab6b70c9/plugins/hookify/agents/conversation-analyzer.md
	"conversation-analyzer": "---\nname: reviewer\ndescription: |-\n  Use this agent when " +
		"analyzing conversation transcripts to find behaviors worth preventing with hooks. " +
		"Examples: <example>Context: User is running /hookify command without arguments\\n" +
		"user: \"/hookify\"\\nassistant: \"I'll analyze the conversation\"</example>\n" +
		"model: inherit\ncolor: yellow\ntools: [\"Read\", \"Grep\"]\n---\nAnalyze the transcript.\n",
	// https://github.com/Connected-Dot/cnctd_ai/blob/1db570c50002/.claude/agents/session-summary-writer.md
	"session-summary-writer": "---\nname: reviewer\ndescription: \"Use this agent when the " +
		"user wants to generate a session summary. This includes when the user explicitly asks " +
		"to 'wrap up', 'end session', or uses the /session-summary command. Examples:\\n\\n" +
		"<example>\\nuser: \\\"Let's wrap up\\\"\\n</example>\"\nmodel: opus\n---\n" +
		"Write the summary.\n",
}

// Cursor reads one line per key and keeps the value as written, so conversion
// writes the description's words on one line, quoted with the fewest escapes
// when it must be, and Cursor's loader reads it (checked in the e2e suite).
func TestCursorConversionFoldsAnyDescription(t *testing.T) {
	for name, source := range quotedDescriptionAgents {
		doc, err := agent.ParseMarkdownLenient("reviewer.md", []byte(source))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		artifact, err := generateFrom(t, agent.Claude, agent.Cursor,
			".claude/agents/reviewer.md", source)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		requireOneLineDescription(t, artifact.Content, doc.String("description"))
		if hasNote(artifact.Notes, "description") {
			t.Errorf("%s: folding keeps the meaning, so it is not noted: %q", name, artifact.Notes)
		}
	}
}

// Authored Cursor variants from the refusal census whose description spans
// lines; Cursor would read only ">-", "|" or the first line (minimized).
var multiLineCursorVariants = map[string]string{
	// https://github.com/devsecopstx/atx-trusted-advisor/blob/d0d922ccddda/.cursor/agents/backend.md
	"backend": "---\nname: reviewer\ndescription: |\n  Backend work for aTx Finance — Next.js " +
		"API routes & modules, MongoDB, Kotlin Spring\n  (`services/atxfinance-backend`), " +
		"Yahoo/strategy integrations, strong TypeScript.\nmodel: inherit\nis_background: true\n" +
		"---\n\nYou are the backend specialist.\n",
	// https://github.com/vskadorva/ai-assisted-qa-automation-2/blob/07d666e5eefb/.cursor/agents/bug-reporter.md
	"bug-reporter": "---\nname: reviewer\ndescription: Files a structured Jira bug for a " +
		"confirmed defect and links\n  it to the story. Use once triage confirms a real app bug.\n" +
		"model: composer-2.5[]\nreadonly: true\n---\n\nFile the bug.\n",
	// https://github.com/TuTejsy/Moonlit/blob/7680ffa7864a/.cursor/agents/moonlit-paywall-flow.md
	"moonlit-paywall-flow": "---\nname: reviewer\ndescription: >-\n  Moonlit Adapty paywall " +
		"flow implementer. Wires useShowPaywallModal, LOCKED_CONTENT\n  placement. Modes: new " +
		"call site, new variant.\n  Invoke with /moonlit-paywall-flow.\nmodel: inherit\n" +
		"readonly: false\nis_background: false\n---\n\nWire the paywall.\r\n",
}

// Native rewrites only the description onto one line; every other line is
// kept, restrictions included, and the variant converts elsewhere.
func TestCursorNativeFoldsMultiLineDescriptions(t *testing.T) {
	cursor := installer(t, agent.Cursor)
	for name, source := range multiLineCursorVariants {
		doc, err := agent.ParseMarkdown("reviewer.md", []byte(source))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		artifact, err := cursor.Native("reviewer", variant(agent.Cursor, source))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		requireOneLineDescription(t, artifact.Content, doc.String("description"))
		keep := func(text string) []string {
			var lines []string
			inDescription := false
			for line := range strings.SplitSeq(text, "\n") {
				switch {
				case strings.HasPrefix(line, "description:"):
					inDescription = true
				case inDescription && strings.HasPrefix(line, "  "):
				default:
					inDescription = false
					lines = append(lines, line)
				}
			}
			return lines
		}
		if got, want := keep(string(artifact.Content)), keep(source); strings.Join(got, "\n") !=
			strings.Join(want, "\n") {
			t.Errorf("%s: other lines changed:\n%s", name, artifact.Content)
		}
		if _, err := cursor.Native("reviewer", variant(agent.Cursor,
			string(artifact.Content))); err != nil {
			t.Errorf("%s: the folded file is not a valid Cursor agent: %v", name, err)
		}
		readonly := doc.Frontmatter["readonly"] == true
		if got := cursorLine(artifact.Content, "readonly") == "true"; got != readonly {
			t.Errorf("%s: readonly %t, want %t", name, got, readonly)
		}
		if _, err := generateFrom(t, agent.Cursor, agent.Claude, ".cursor/agents/reviewer.md",
			source); err != nil {
			t.Errorf("%s: does not convert to Claude Code: %v", name, err)
		}
	}
}
