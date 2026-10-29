package harness

import (
	"fmt"
	"path"
	"strings"

	"github.com/heynemann/agnthub/internal/agent"
)

// cursorAdapter describes Cursor subagents: name falls back to the file name.
type cursorAdapter struct{}

func (cursorAdapter) ID() agent.HarnessID { return agent.Cursor }

func (cursorAdapter) Name() string { return "Cursor" }

func (cursorAdapter) Layout() NativeLayout {
	return NativeLayout{Dir: ".cursor/agents", Suffixes: []string{".md"}}
}

func (cursorAdapter) Settings() []SettingSpec { return nil }

func (cursorAdapter) Identify(filePath string, raw []byte) (NativeAgent, error) {
	doc, err := agent.ParseMarkdown(filePath, raw)
	if err != nil {
		return NativeAgent{}, err
	}
	description, ok := doc.Frontmatter["description"].(string)
	if !ok || strings.TrimSpace(description) == "" {
		return NativeAgent{}, fmt.Errorf("%s: frontmatter requires a description", filePath)
	}
	id, _ := doc.Frontmatter["name"].(string)
	if id == "" {
		id = strings.TrimSuffix(path.Base(filePath), ".md")
	}
	return NativeAgent{ID: id, Description: description}, nil
}
