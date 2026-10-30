package skills

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Version is a strict Semantic Versioning 2.0.0 version: MAJOR.MINOR.PATCH with
// optional dot-separated prerelease and build identifiers, no "v" prefix and no
// leading zeros in numeric identifiers.
type Version struct {
	Major, Minor, Patch uint64
	// Prerelease holds the dot-separated prerelease identifiers, if any.
	Prerelease []string
	// Build is the build metadata, which never affects precedence.
	Build string
	raw   string
}

// ErrNotSemver reports a value that is not a strict semantic version.
var ErrNotSemver = errors.New("not a strict semantic version")

// ParseVersion parses a strict semantic version.
func ParseVersion(raw string) (Version, error) {
	v := Version{raw: raw}
	rest := raw
	if i := strings.IndexByte(rest, '+'); i >= 0 {
		v.Build = rest[i+1:]
		rest = rest[:i]
		if !validIdentifiers(v.Build, false) {
			return Version{}, fmt.Errorf("%q: %w (invalid build metadata)", raw, ErrNotSemver)
		}
	}
	if i := strings.IndexByte(rest, '-'); i >= 0 {
		pre := rest[i+1:]
		rest = rest[:i]
		if !validIdentifiers(pre, true) {
			return Version{}, fmt.Errorf("%q: %w (invalid prerelease)", raw, ErrNotSemver)
		}
		v.Prerelease = strings.Split(pre, ".")
	}
	core := strings.Split(rest, ".")
	if len(core) != 3 {
		return Version{}, fmt.Errorf("%q: %w (want MAJOR.MINOR.PATCH)", raw, ErrNotSemver)
	}
	numbers := [3]*uint64{&v.Major, &v.Minor, &v.Patch}
	for i, part := range core {
		if !numeric(part) {
			return Version{}, fmt.Errorf("%q: %w", raw, ErrNotSemver)
		}
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return Version{}, fmt.Errorf("%q: %w", raw, ErrNotSemver)
		}
		*numbers[i] = n
	}
	return v, nil
}

// numeric reports a nonempty decimal without a leading zero.
func numeric(s string) bool {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// validIdentifiers checks dot-separated [0-9A-Za-z-] identifiers; prerelease
// numeric identifiers must not have leading zeros.
func validIdentifiers(s string, prerelease bool) bool {
	if s == "" {
		return false
	}
	for ident := range strings.SplitSeq(s, ".") {
		if ident == "" {
			return false
		}
		allDigits := true
		for i := range len(ident) {
			c := ident[i]
			switch {
			case c >= '0' && c <= '9':
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '-':
				allDigits = false
			default:
				return false
			}
		}
		if prerelease && allDigits && !numeric(ident) {
			return false
		}
	}
	return true
}

// String returns the version as written.
func (v Version) String() string {
	return v.raw
}

// IsPrerelease reports a version with prerelease identifiers.
func (v Version) IsPrerelease() bool {
	return len(v.Prerelease) > 0
}

// Compare orders versions by semantic-version precedence, ignoring build metadata.
func (v Version) Compare(o Version) int {
	for _, pair := range [3][2]uint64{{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Patch, o.Patch}} {
		if pair[0] != pair[1] {
			if pair[0] < pair[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(v.Prerelease) == 0 && len(o.Prerelease) == 0:
		return 0
	case len(v.Prerelease) == 0:
		return 1
	case len(o.Prerelease) == 0:
		return -1
	}
	for i := 0; i < len(v.Prerelease) && i < len(o.Prerelease); i++ {
		if c := compareIdentifier(v.Prerelease[i], o.Prerelease[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(v.Prerelease) < len(o.Prerelease):
		return -1
	case len(v.Prerelease) > len(o.Prerelease):
		return 1
	default:
		return 0
	}
}

func compareIdentifier(a, b string) int {
	aNum, bNum := numeric(a), numeric(b)
	switch {
	case aNum && bNum:
		if len(a) != len(b) {
			if len(a) < len(b) {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	case aNum:
		return -1
	case bNum:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

func (v Version) sameCore(o Version) bool {
	return v.Major == o.Major && v.Minor == o.Minor && v.Patch == o.Patch
}

// Bounds are optional inclusive minimum and maximum versions of a Skill.
type Bounds struct {
	Min, Max *Version
}

// ErrBounds reports invalid declared bounds.
var ErrBounds = errors.New("invalid skill version bounds")

// ParseBounds validates declared minVersion/maxVersion values; empty means unbounded.
func ParseBounds(minVersion, maxVersion string) (Bounds, error) {
	var b Bounds
	for _, bound := range []struct {
		name, raw string
		dst       **Version
	}{{"minVersion", minVersion, &b.Min}, {"maxVersion", maxVersion, &b.Max}} {
		if bound.raw == "" {
			continue
		}
		v, err := ParseVersion(bound.raw)
		if err != nil {
			return Bounds{}, fmt.Errorf("%w: %s %w", ErrBounds, bound.name, err)
		}
		*bound.dst = &v
	}
	if b.Min != nil && b.Max != nil && b.Min.Compare(*b.Max) > 0 {
		return Bounds{}, fmt.Errorf(
			"%w: minVersion %s is above maxVersion %s", ErrBounds, b.Min, b.Max,
		)
	}
	return b, nil
}

// Bounded reports whether either bound is set.
func (b Bounds) Bounded() bool {
	return b.Min != nil || b.Max != nil
}

// String describes the bounds, e.g. ">=1.2.0 <=2.0.0" or "any version".
func (b Bounds) String() string {
	var parts []string
	if b.Min != nil {
		parts = append(parts, ">="+b.Min.String())
	}
	if b.Max != nil {
		parts = append(parts, "<="+b.Max.String())
	}
	if len(parts) == 0 {
		return "any version"
	}
	return strings.Join(parts, " ")
}

// Check reports why a declared metadata.version does not satisfy the bounds, or ""
// when it does. Unbounded requirements accept anything, including no version.
// Bounded requirements need a strict semantic version inside both inclusive
// bounds; a prerelease satisfies them only when a bound is itself a prerelease
// of the same MAJOR.MINOR.PATCH, so prereleases are never picked up implicitly.
func (b Bounds) Check(declared string, present bool) string {
	if !b.Bounded() {
		return ""
	}
	if !present {
		return "has no metadata.version, so it cannot satisfy " + b.String()
	}
	v, err := ParseVersion(declared)
	if err != nil {
		return fmt.Sprintf(
			"metadata.version %q is not a strict semantic version, so it cannot satisfy %s",
			declared, b,
		)
	}
	if b.Min != nil && v.Compare(*b.Min) < 0 || b.Max != nil && v.Compare(*b.Max) > 0 {
		return fmt.Sprintf("version %s is outside %s", v, b)
	}
	if v.IsPrerelease() && !b.allowsPrerelease(v) {
		return fmt.Sprintf(
			"version %s is a prerelease; %s has no prerelease bound for %d.%d.%d",
			v, b, v.Major, v.Minor, v.Patch,
		)
	}
	return ""
}

func (b Bounds) allowsPrerelease(v Version) bool {
	for _, bound := range []*Version{b.Min, b.Max} {
		if bound != nil && bound.IsPrerelease() && bound.sameCore(v) {
			return true
		}
	}
	return false
}
