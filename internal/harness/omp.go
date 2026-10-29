package harness

import (
	"fmt"
	"regexp"

	"github.com/heynemann/agnthub/internal/agent"
)

// ompProfilePattern accepts profile names usable as a single directory component.
var ompProfilePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ompAdapter describes omp (oh-my-pi) agents and their profile setting.
type ompAdapter struct{}

func (ompAdapter) ID() agent.HarnessID { return agent.OMP }

func (ompAdapter) Name() string { return "omp" }

func (ompAdapter) Layout() NativeLayout {
	return NativeLayout{Dir: ".omp/agents", Suffixes: []string{".md"}}
}

func (ompAdapter) Settings() []SettingSpec {
	return []SettingSpec{{
		Key:         "profile",
		Description: "named omp profile whose agent directory and configuration apply",
		Validate:    validateOMPProfile,
	}}
}

func validateOMPProfile(value string) error {
	if !ompProfilePattern.MatchString(value) || value == "." || value == ".." {
		return fmt.Errorf(
			"invalid omp profile %q: use letters, digits, '.', '_' or '-' (max 64)",
			value,
		)
	}
	return nil
}

func (ompAdapter) Identify(filePath string, raw []byte) (NativeAgent, error) {
	doc, err := agent.ParseMarkdown(filePath, raw)
	if err != nil {
		return NativeAgent{}, err
	}
	return requireNamed(doc)
}
