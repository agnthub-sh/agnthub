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

var claudeDocs = flag.Bool("claude-docs", false,
	"compare the pinned Claude Code frontmatter fields with the current reference (network)")

// The e2e workflow runs this every night: a field Claude Code starts to
// recognize must not keep being omitted as one it ignores.
func TestClaudeFieldsMatchTheDocs(t *testing.T) {
	if !*claudeDocs {
		t.Skip("run with -claude-docs to fetch the Claude Code subagent reference")
	}
	client := &http.Client{Timeout: time.Minute}
	resp, err := client.Get("https://code.claude.com/docs/en/sub-agents.md")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	page, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("fetch the reference: %v %s", err, resp.Status)
	}
	text := string(page)
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
