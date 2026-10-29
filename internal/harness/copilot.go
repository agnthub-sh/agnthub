package harness

import (
	"fmt"
	"path"
	"strings"

	"github.com/heynemann/agnthub/internal/agent"
)

// copilotAdapter describes GitHub Copilot CLI agents: the file name is the identity.
type copilotAdapter struct{}

func (copilotAdapter) ID() agent.HarnessID { return agent.Copilot }

func (copilotAdapter) Name() string { return "GitHub Copilot CLI" }

func (copilotAdapter) Layout() NativeLayout {
	return NativeLayout{Dir: ".github/agents", Suffixes: []string{".agent.md", ".md"}}
}

func (copilotAdapter) Settings() []SettingSpec { return nil }

func (copilotAdapter) Identify(filePath string, raw []byte) (NativeAgent, error) {
	doc, err := agent.ParseMarkdown(filePath, raw)
	if err != nil {
		return NativeAgent{}, err
	}
	description, ok := doc.Frontmatter["description"].(string)
	if !ok || strings.TrimSpace(description) == "" {
		return NativeAgent{}, fmt.Errorf("%s: frontmatter requires a description", filePath)
	}
	base := path.Base(filePath)
	id := strings.TrimSuffix(strings.TrimSuffix(base, ".md"), ".agent")
	return NativeAgent{ID: id, Description: description}, nil
}
