package category_test

import (
	"strings"
	"testing"

	"github.com/agnthub-sh/agnthub/category"
)

func TestTaxonomyEntriesAreUniqueValidAndNamed(t *testing.T) {
	all := category.All()
	if len(all) != category.Len() || len(all) == 0 {
		t.Fatalf("All() has %d entries, Len() = %d", len(all), category.Len())
	}
	seen := map[string]bool{}
	for i, c := range all {
		if !category.ValidSlug(c.Slug) {
			t.Errorf("slug %q is not slug-shaped", c.Slug)
		}
		if seen[c.Slug] {
			t.Errorf("duplicate slug %q", c.Slug)
		}
		seen[c.Slug] = true
		if c.Name == "" || c.Covers == "" {
			t.Errorf("%s lacks a name or coverage", c.Slug)
		}
		if got, ok := category.Lookup(c.Slug); !ok || got != c || category.Index(c.Slug) != i {
			t.Errorf("Lookup/Index(%q) disagree with All()", c.Slug)
		}
	}
}

func TestFromDivisionNamesOnlyKnownCategories(t *testing.T) {
	for folder, want := range map[string]string{
		"engineering": "software-engineering", "Security": "security", "testing": "testing-qa",
		"marketing": "marketing", "game-development": "game-development",
		"specialized": "", "spatial-computing": "", "gis": "",
	} {
		if got := category.FromDivision(folder); got != want {
			t.Errorf("FromDivision(%q) = %q, want %q", folder, got, want)
		}
	}
	for _, folder := range []string{
		"engineering", "design", "product", "support", "academic",
		"devops", "data", "ai", "paid-media", "strategy", "hr", "writing", "docs", "health",
		"web3", "mobile", "frontend", "backend", "architecture", "database", "localization",
	} {
		if slug := category.FromDivision(folder); !category.Known(slug) {
			t.Errorf("division %q maps to unknown category %q", folder, slug)
		}
	}
}

func TestAllReturnsACopy(t *testing.T) {
	first := category.All()
	first[0].Name = "changed"
	if category.All()[0].Name == "changed" {
		t.Fatal("All() exposes the shared taxonomy")
	}
}

func TestUnknownSlugsAreNotKnown(t *testing.T) {
	if category.Known("quantum-computing") || category.Index("quantum-computing") != -1 {
		t.Fatal("unknown slug reported as known")
	}
	if _, ok := category.Lookup(""); ok {
		t.Fatal("empty slug reported as known")
	}
}

func TestValidSlugBoundaries(t *testing.T) {
	for slug, want := range map[string]bool{
		"security":              true,
		"devops-ci-cd":          true,
		"web3":                  true,
		"a":                     true,
		strings.Repeat("a", 64): true,
		strings.Repeat("a", 65): false,
		"":                      false,
		"Security":              false,
		"code_review":           false,
		"code review":           false,
		"-security":             false,
		"security-":             false,
		"code--review":          false,
		"sécurité":              false,
		"../etc":                false,
		"security\n":            false,
	} {
		if got := category.ValidSlug(slug); got != want {
			t.Errorf("ValidSlug(%q) = %v, want %v", slug, got, want)
		}
	}
}
