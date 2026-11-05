package harness

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/agnthub-sh/agnthub/agent"
)

// harmlessPortableKeys are descriptive metadata that never change runtime behavior;
// conversion omits them and reports the omission.
var harmlessPortableKeys = map[string]bool{
	"metadata": true,
	"license":  true,
	"tags":     true,
	"version":  true,
	"author":   true,
	"homepage": true,
}

// descriptiveKeys are documentation-only keys that authors put in native agent
// files and that Copilot CLI and Cursor ignore: both load an agent declaring
// them and act on none (checked against Copilot CLI 1.0.105 and Cursor
// 2026.10.22). Their authored Variants install as written, with a note. Codex
// and Gemini CLI refuse to load agents with keys they do not define, so these
// keys keep their Variants invalid there.
var descriptiveKeys = map[string]bool{
	"color":        true,
	"title":        true,
	"kind":         true,
	"emoji":        true,
	"vibe":         true,
	"category":     true,
	"status":       true,
	"domain":       true,
	"owner":        true,
	"team":         true,
	"tier":         true,
	"last_updated": true,
	"display_name": true,
	"examples":     true,
}

// PortableCore is the harness-neutral identity, description and instructions.
type PortableCore struct {
	ID           string
	Description  string
	Instructions string
	// Omitted lists harmless metadata keys not carried into the target.
	Omitted []string
}

// ExtractPortable validates a portable document for target and returns its core.
// Any declared field outside the portable core refuses conversion: agnthub never
// drops a behavioral constraint or rewrites it as prose.
func ExtractPortable(
	target agent.HarnessID,
	id string,
	doc *agent.Document,
) (PortableCore, error) {
	if doc == nil {
		return PortableCore{}, &IncompatibleError{
			Harness: target,
			Reason:  "no authored variant and no portable definition",
		}
	}
	core := PortableCore{ID: id, Instructions: doc.Body}
	keys := make([]string, 0, len(doc.Frontmatter))
	for key := range doc.Frontmatter {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		switch {
		case key == "name" || key == "description":
		case harmlessPortableKeys[key]:
			core.Omitted = append(core.Omitted, key)
		case target == agent.Gemini && slices.Contains(delegationKeys, key):
			return PortableCore{}, &IncompatibleError{
				Harness: target,
				Reason: fmt.Sprintf(
					"portable definition declares %q, but Gemini CLI subagents cannot invoke other agents",
					key,
				),
			}
		default:
			return PortableCore{}, &IncompatibleError{
				Harness: target,
				Reason: fmt.Sprintf(
					"portable definition declares %q, which has no verified equivalent; author a native %s variant",
					key,
					target,
				),
			}
		}
	}
	description, ok := doc.Frontmatter["description"].(string)
	if !ok || strings.TrimSpace(description) == "" {
		return PortableCore{}, fmt.Errorf(
			"%s: portable definition requires a description",
			doc.Path,
		)
	}
	core.Description = description
	if strings.TrimSpace(core.Instructions) == "" {
		return PortableCore{}, fmt.Errorf("%s: portable definition has no instructions", doc.Path)
	}
	return core, nil
}

// OmissionNotes formats omitted harmless keys for plan output.
func (c PortableCore) OmissionNotes() []string {
	if len(c.Omitted) == 0 {
		return nil
	}
	return []string{"omitted descriptive metadata: " + strings.Join(c.Omitted, ", ")}
}
