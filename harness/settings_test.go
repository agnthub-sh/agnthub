package harness_test

import (
	"strings"
	"testing"

	"github.com/heynemann/agnthub/agent"
	"github.com/heynemann/agnthub/harness"
)

func TestResolveSettingsAcceptsScopedRepeatableExtras(t *testing.T) {
	_, err := harness.ResolveSettings(
		harness.Default(),
		[]string{"omp.profile=work=2"},
		[]agent.HarnessID{agent.Claude, agent.OMP},
	)
	if err == nil || !strings.Contains(err.Error(), `"work=2"`) {
		t.Fatalf("value after the first '=' must reach validation literally, got %v", err)
	}
	settings, err := harness.ResolveSettings(
		harness.Default(),
		[]string{"omp.profile=hep"},
		[]agent.HarnessID{agent.Claude, agent.OMP},
	)
	if err != nil {
		t.Fatal(err)
	}
	if settings[agent.OMP]["profile"] != "hep" || len(settings[agent.Claude]) != 0 {
		t.Fatalf("setting must apply to omp only: %v", settings)
	}
}

func TestResolveSettingsRejectsBeforeAnyWrite(t *testing.T) {
	selected := []agent.HarnessID{agent.OMP}
	cases := map[string]struct {
		extras []string
		want   string
	}{
		"unqualified":        {[]string{"profile=hep"}, "qualified"},
		"missing value sign": {[]string{"omp.profile"}, "want <harness>.<key>=<value>"},
		"unknown harness":    {[]string{"emacs.profile=x"}, "unknown harness"},
		"unknown key":        {[]string{"omp.model=x"}, "does not support setting"},
		"duplicate":          {[]string{"omp.profile=a", "omp.profile=b"}, "more than once"},
		"invalid value":      {[]string{"omp.profile=../etc"}, "invalid omp profile"},
		"empty value":        {[]string{"omp.profile="}, "invalid omp profile"},
		"shell syntax":       {[]string{"omp.profile=$(rm -rf ~)"}, "invalid omp profile"},
		"unselected harness": {[]string{"omp.profile=hep"}, "not selected"},
		"no settings":        {[]string{"claude.profile=x"}, "has no supported settings"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			harnesses := selected
			if name == "unselected harness" {
				harnesses = []agent.HarnessID{agent.Claude}
			}
			if name == "no settings" {
				harnesses = []agent.HarnessID{agent.Claude}
			}
			_, err := harness.ResolveSettings(harness.Default(), tc.extras, harnesses)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}
