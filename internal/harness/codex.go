package harness

import (
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/heynemann/agnthub/internal/agent"
)

// codexAdapter describes Codex standalone TOML agents.
type codexAdapter struct{}

func (codexAdapter) ID() agent.HarnessID { return agent.Codex }

func (codexAdapter) Name() string { return "Codex" }

func (codexAdapter) Layout() NativeLayout {
	return NativeLayout{Dir: ".codex/agents", Suffixes: []string{".toml"}}
}

func (codexAdapter) Settings() []SettingSpec { return nil }

func (codexAdapter) Identify(filePath string, raw []byte) (NativeAgent, error) {
	var fields map[string]any
	if err := toml.Unmarshal(raw, &fields); err != nil {
		return NativeAgent{}, fmt.Errorf("%s: invalid TOML: %w", filePath, err)
	}
	name, _ := fields["name"].(string)
	description, _ := fields["description"].(string)
	instructions, _ := fields["developer_instructions"].(string)
	switch {
	case name == "":
		return NativeAgent{}, fmt.Errorf("%s: requires a string name", filePath)
	case strings.TrimSpace(description) == "":
		return NativeAgent{}, fmt.Errorf("%s: requires a description", filePath)
	case strings.TrimSpace(instructions) == "":
		return NativeAgent{}, fmt.Errorf("%s: requires developer_instructions", filePath)
	}
	return NativeAgent{ID: name, Description: description}, nil
}
