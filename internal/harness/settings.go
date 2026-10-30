package harness

import (
	"fmt"
	"slices"
	"strings"

	"github.com/heynemann/agnthub/internal/agent"
)

// Extra is one parsed --extra <harness>.<key>=<value> argument.
type Extra struct {
	Harness agent.HarnessID
	Key     string
	Value   string
}

// ParseExtra parses one --extra argument. The value is everything after the first
// equals sign, preserved literally.
func ParseExtra(raw string) (Extra, error) {
	qualified, value, found := strings.Cut(raw, "=")
	if !found {
		return Extra{}, fmt.Errorf("--extra %q: want <harness>.<key>=<value>", raw)
	}
	harnessName, key, qualifiedOK := strings.Cut(qualified, ".")
	if !qualifiedOK || harnessName == "" || key == "" {
		return Extra{}, fmt.Errorf(
			"--extra %q: setting must be qualified by its harness, e.g. omp.profile=work",
			raw,
		)
	}
	id, err := agent.ParseHarness(harnessName)
	if err != nil {
		return Extra{}, fmt.Errorf("--extra %q: %w", raw, err)
	}
	return Extra{Harness: id, Key: key, Value: value}, nil
}

// ResolveSettings validates --extra arguments against adapter declarations and the
// selected Harnesses. Nothing is written before this succeeds.
func ResolveSettings(
	registry *Registry,
	raw []string,
	selected []agent.HarnessID,
) (map[agent.HarnessID]Settings, error) {
	resolved := map[agent.HarnessID]Settings{}
	for _, item := range raw {
		extra, err := ParseExtra(item)
		if err != nil {
			return nil, err
		}
		adapter, ok := registry.Get(extra.Harness)
		if !ok {
			return nil, fmt.Errorf("--extra %q: unknown harness %q", item, extra.Harness)
		}
		spec, ok := findSetting(adapter, extra.Key)
		if !ok {
			supported := SettingKeys(adapter)
			if len(supported) == 0 {
				return nil, fmt.Errorf(
					"--extra %q: %s has no supported settings",
					item,
					adapter.Name(),
				)
			}
			return nil, fmt.Errorf(
				"--extra %q: %s does not support setting %q (supported: %s)",
				item, adapter.Name(), extra.Key, strings.Join(supported, ", "),
			)
		}
		if !slices.Contains(selected, extra.Harness) {
			return nil, fmt.Errorf(
				"--extra %q: harness %s is not selected; add --harness %s",
				item, extra.Harness, extra.Harness,
			)
		}
		settings := resolved[extra.Harness]
		if settings == nil {
			settings = Settings{}
			resolved[extra.Harness] = settings
		}
		if _, duplicate := settings[extra.Key]; duplicate {
			return nil, fmt.Errorf("--extra %s.%s given more than once", extra.Harness, extra.Key)
		}
		if spec.Validate != nil {
			if err := spec.Validate(extra.Value); err != nil {
				return nil, fmt.Errorf("--extra %q: %w", item, err)
			}
		}
		settings[extra.Key] = extra.Value
	}
	return resolved, nil
}

func findSetting(adapter Adapter, key string) (SettingSpec, bool) {
	for _, spec := range adapter.Settings() {
		if spec.Key == key {
			return spec, true
		}
	}
	return SettingSpec{}, false
}
