package harness

import (
	"github.com/heynemann/agnthub/internal/agent"
)

// geminiAdapter describes Gemini CLI local subagents.
type geminiAdapter struct{}

func (geminiAdapter) ID() agent.HarnessID { return agent.Gemini }

func (geminiAdapter) Name() string { return "Gemini CLI" }

func (geminiAdapter) Layout() NativeLayout {
	return NativeLayout{Dir: ".gemini/agents", Suffixes: []string{".md"}}
}

func (geminiAdapter) Settings() []SettingSpec { return nil }

func (geminiAdapter) Identify(filePath string, raw []byte) (NativeAgent, error) {
	doc, err := agent.ParseMarkdown(filePath, raw)
	if err != nil {
		return NativeAgent{}, err
	}
	return requireNamed(doc)
}
