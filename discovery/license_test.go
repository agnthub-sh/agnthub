package discovery_test

import (
	"slices"
	"strings"
	"testing"
)

func TestTheRootLicenseFileAttributesEveryAgent(t *testing.T) {
	inv := discover(t, memTree(t, map[string]string{
		"agents/reviewer.md": portable("reviewer", "Reviews"),
		"agents/planner.md":  portable("planner", "Plans"),
		"COPYING":            "Copyright (c) 2020 Someone Else\n",
		"License.md":         "MIT License\n\nCopyright (c) 2025 Jane Doe\n\nPermission is hereby granted\n",
		"docs/LICENSE":       "Copyright (c) 1999 Not The Root\n",
	}))
	if inv.License == nil || inv.License.Path != "License.md" ||
		!slices.Equal(inv.License.Copyright, []string{"Copyright (c) 2025 Jane Doe"}) {
		t.Fatalf("license = %+v", inv.License)
	}
	for _, def := range inv.Agents {
		if def.License != inv.License {
			t.Errorf("%s: license %+v", def.ID, def.License)
		}
	}
}

func TestLicenseReadingNeverFailsDiscovery(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"no license file": {},
		"oversized":       {"LICENSE": "Copyright (c) 2025 Jane Doe\n" + strings.Repeat("x", 70<<10)},
		"other names":     {"LICENSE-THIRD-PARTY": "Copyright (c) 2025 Vendor\n"},
	} {
		files["agents/reviewer.md"] = portable("reviewer", "Reviews")
		inv := discover(t, memTree(t, files))
		if inv.License != nil || len(inv.Agents) != 1 || inv.Agents[0].License != nil {
			t.Errorf("%s: license %+v, agents %v", name, inv.License, inv.Agents)
		}
	}
}
