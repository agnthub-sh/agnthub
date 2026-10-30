package source

import (
	"testing"

	"github.com/heynemann/agnthub/platform/env"
)

func TestGenericGitIdentityKeepsPortAndRefusesPasswords(t *testing.T) {
	a, err := Parse("https://git.example.com:8443/team/agents.git#v1", "/", env.Env{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parse("https://git.example.com/team/agents.git", "/", env.Env{})
	if err != nil {
		t.Fatal(err)
	}
	if a.Kind != KindGit || a.Key() == b.Key() {
		t.Fatalf("different ports must be different sources: %q vs %q", a.Key(), b.Key())
	}
	if a.Ref != "v1" {
		t.Fatalf("ref = %q", a.Ref)
	}
	if _, err := Parse("https://u:pw@git.example.com/team/agents.git", "/", env.Env{}); err == nil {
		t.Fatal("URLs with passwords must be refused")
	}
	for _, input := range []string{"owner/repo#-x", "owner/repo#a..b", "file:///tmp/repo"} {
		if _, err := Parse(input, "/", env.Env{}); err == nil {
			t.Fatalf("%q must be refused", input)
		}
	}
}
