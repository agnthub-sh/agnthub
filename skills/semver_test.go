package skills_test

import (
	"errors"
	"testing"

	"github.com/heynemann/agnthub/skills"
)

func TestParseVersionAcceptsOnlyStrictSemver(t *testing.T) {
	for _, valid := range []string{
		"0.0.0", "1.2.3", "10.20.30", "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-0.3.7",
		"1.0.0-x.7.z.92", "1.0.0+20130313144700", "1.0.0-beta+exp.sha.5114f85",
	} {
		if _, err := skills.ParseVersion(valid); err != nil {
			t.Errorf("%q rejected: %v", valid, err)
		}
	}
	for _, invalid := range []string{
		"", "1", "1.0", "v1.2.3", "01.2.3", "1.02.3", "1.2.03", "1.2.3-", "1.2.3-01",
		"1.2.3-alpha..1", "1.2.3+", "1.2.3.4", " 1.2.3", "1.2.3 ", "latest", "1.2.x",
	} {
		if _, err := skills.ParseVersion(invalid); !errors.Is(err, skills.ErrNotSemver) {
			t.Errorf("%q accepted (err %v)", invalid, err)
		}
	}
}

func TestVersionPrecedenceFollowsSemver(t *testing.T) {
	ordered := []string{
		"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2",
		"1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.1.0", "2.0.0",
	}
	for i := 0; i+1 < len(ordered); i++ {
		a, _ := skills.ParseVersion(ordered[i])
		b, _ := skills.ParseVersion(ordered[i+1])
		if a.Compare(b) >= 0 || b.Compare(a) <= 0 {
			t.Errorf("want %s < %s", a, b)
		}
	}
	a, _ := skills.ParseVersion("1.0.0+build.1")
	b, _ := skills.ParseVersion("1.0.0+build.2")
	if a.Compare(b) != 0 {
		t.Error("build metadata must not affect precedence")
	}
}

func TestBoundsAreInclusiveAndNeedStrictVersions(t *testing.T) {
	bounds, err := skills.ParseBounds("1.2.0", "2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		version   string
		present   bool
		satisfied bool
	}{
		"lower bound":        {"1.2.0", true, true},
		"upper bound":        {"2.0.0", true, true},
		"inside":             {"1.9.9", true, true},
		"below":              {"1.1.9", true, false},
		"above":              {"2.0.1", true, false},
		"missing version":    {"", false, false},
		"non-semver version": {"1.5", true, false},
		"prefixed version":   {"v1.5.0", true, false},
		"prerelease of max":  {"2.0.0-rc.1", true, false},
		"build of max":       {"2.0.0+meta", true, true},
	}
	for name, tc := range cases {
		got := bounds.Check(tc.version, tc.present) == ""
		if got != tc.satisfied {
			t.Errorf("%s: %q satisfied=%v, want %v (%s)", name, tc.version, got, tc.satisfied,
				bounds.Check(tc.version, tc.present))
		}
	}
}

func TestPrereleasesNeedAnExplicitPrereleaseBound(t *testing.T) {
	cases := []struct {
		min, max, version string
		satisfied         bool
	}{
		{"1.0.0", "", "2.0.0-rc.1", false},
		{"2.0.0-rc.1", "", "2.0.0-rc.1", true},
		{"2.0.0-rc.1", "", "2.0.0-rc.2", true},
		{"2.0.0-rc.1", "", "2.0.0", true},
		{"2.0.0-rc.1", "", "2.1.0-rc.1", false},
		{"", "2.0.0-rc.5", "2.0.0-rc.2", true},
		{"", "2.0.0", "2.0.0-rc.2", false},
		{"2.0.0-beta.1", "", "2.0.0-alpha.9", false},
	}
	for _, tc := range cases {
		bounds, err := skills.ParseBounds(tc.min, tc.max)
		if err != nil {
			t.Fatal(err)
		}
		if got := bounds.Check(tc.version, true) == ""; got != tc.satisfied {
			t.Errorf("min %q max %q version %s: satisfied=%v, want %v", tc.min, tc.max,
				tc.version, got, tc.satisfied)
		}
	}
}

func TestUnboundedRequirementsAcceptAnyVersion(t *testing.T) {
	var bounds skills.Bounds
	for _, version := range []string{"", "1.0", "anything", "3.0.0-rc.1"} {
		if why := bounds.Check(version, version != ""); why != "" {
			t.Errorf("%q: %s", version, why)
		}
	}
}

func TestInvalidBoundsAreRejected(t *testing.T) {
	for _, pair := range [][2]string{{"1.0", ""}, {"", "v2.0.0"}, {"2.0.0", "1.0.0"}} {
		if _, err := skills.ParseBounds(pair[0], pair[1]); !errors.Is(err, skills.ErrBounds) {
			t.Errorf("%v accepted (err %v)", pair, err)
		}
	}
}
