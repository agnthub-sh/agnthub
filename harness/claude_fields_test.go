package harness

import (
	"flag"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agnthub-sh/agnthub/agent"
)

var claudeDocs = flag.Bool(
	"claude-docs",
	false,
	"compare the pinned Claude Code frontmatter fields and hook events with the current reference (network)",
)

// The e2e workflow runs this every night: a field Claude Code starts to
// recognize must not keep being omitted as one it ignores.
func TestClaudeFieldsMatchTheDocs(t *testing.T) {
	text := claudeReference(t, "https://code.claude.com/docs/en/sub-agents.md")
	start := strings.Index(text, "### Frontmatter reference")
	if start < 0 {
		t.Fatal("the reference no longer has a Frontmatter reference section")
	}
	section := text[start:]
	if end := strings.Index(section[1:], "\n### "); end > 0 {
		section = section[:end+1]
	}
	rows := regexp.MustCompile("(?m)^\\| `([A-Za-z]+)` \\| (?:Yes|No) \\|").
		FindAllStringSubmatch(section, -1)
	if len(rows) == 0 {
		t.Fatal("found no fields in the Frontmatter reference table")
	}
	var documented []string
	for _, row := range rows {
		documented = append(documented, row[1])
	}
	for _, field := range documented {
		if !slices.Contains(claudeFields, field) {
			t.Errorf("Claude Code now documents %q; conversion would omit it as ignored. "+
				"Decide how it converts and add it to claudeFields.", field)
		}
	}
	for _, field := range claudeFields {
		if !slices.Contains(documented, field) {
			t.Logf("claudeFields pins %q, which the reference no longer lists", field)
		}
	}
}

// claudeReference fetches a Claude Code reference page, when -claude-docs asks
// for it.
func claudeReference(t *testing.T, url string) string {
	t.Helper()
	if !*claudeDocs {
		t.Skip("run with -claude-docs to fetch the Claude Code reference")
	}
	client := &http.Client{Timeout: time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	page, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("fetch %s: %v %s", url, err, resp.Status)
	}
	return string(page)
}

// The e2e workflow runs this every night: an event Claude Code starts to run
// must not keep being omitted as one it ignores.
func TestClaudeHookEventsMatchTheDocs(t *testing.T) {
	text := claudeReference(t, "https://code.claude.com/docs/en/hooks.md")
	start := strings.Index(text, "| Event | When it fires |")
	if start < 0 {
		t.Fatal("the hooks reference no longer has its event table")
	}
	table, _, _ := strings.Cut(text[start:], "\n\n")
	rows := regexp.MustCompile("(?m)^\\| `([A-Za-z]+)` \\|").FindAllStringSubmatch(table, -1)
	if len(rows) == 0 {
		t.Fatal("found no events in the hooks reference table")
	}
	var documented []string
	for _, row := range rows {
		documented = append(documented, row[1])
	}
	for _, event := range documented {
		if !slices.Contains(claudeHookEvents, event) {
			t.Errorf("Claude Code now documents hook event %q; conversion would omit it as "+
				"ignored. Add it to claudeHookEvents.", event)
		}
	}
	for _, event := range claudeHookEvents {
		if !slices.Contains(documented, event) {
			t.Logf("claudeHookEvents pins %q, which the reference no longer lists", event)
		}
	}
}

// Every field Claude Code recognizes is converted, noted or refused for its
// meaning, never omitted as one it ignores.
func TestRecognizedClaudeFieldsAreNeverIgnored(t *testing.T) {
	for _, field := range claudeFields {
		if field == "name" || field == "description" {
			continue
		}
		raw := "---\nname: reviewer\ndescription: Reviews code\n" + field + ": x\n---\nReview.\n"
		sem, err := claudeAdapter{}.Semantics("reviewer", &agent.VariantFile{
			Harness: agent.Claude, Path: ".claude/agents/reviewer.md", Raw: []byte(raw),
		})
		if err != nil {
			t.Fatalf("%s: %v", field, err)
		}
		if slices.Contains(sem.Notes, "omitted "+field+": Claude Code ignores it") {
			t.Errorf("%s is a Claude Code field but was omitted as ignored", field)
		}
	}
}
