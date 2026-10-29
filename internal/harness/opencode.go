package harness

import (
	"fmt"
	"path"
	"strings"

	"github.com/heynemann/agnthub/internal/agent"
)

// opencodeAdapter describes OpenCode agents: the file name is the identity.
type opencodeAdapter struct{}

func (opencodeAdapter) ID() agent.HarnessID { return agent.OpenCode }

func (opencodeAdapter) Name() string { return "OpenCode" }

func (opencodeAdapter) Layout() NativeLayout {
	return NativeLayout{Dir: ".opencode/agents", Suffixes: []string{".md"}}
}

func (opencodeAdapter) Settings() []SettingSpec { return nil }

func (opencodeAdapter) Identify(filePath string, raw []byte) (NativeAgent, error) {
	doc, err := agent.ParseMarkdown(filePath, raw)
	if err != nil {
		return NativeAgent{}, err
	}
	description, ok := doc.Frontmatter["description"].(string)
	if !ok || strings.TrimSpace(description) == "" {
		return NativeAgent{}, fmt.Errorf("%s: frontmatter requires a description", filePath)
	}
	return NativeAgent{
		ID:          strings.TrimSuffix(path.Base(filePath), ".md"),
		Description: description,
	}, nil
}
