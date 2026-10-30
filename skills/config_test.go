package skills_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/heynemann/agnthub/harness"
	"github.com/heynemann/agnthub/skills"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMissingConfigUsesSkillsShDefault(t *testing.T) {
	installer, err := skills.LoadInstaller(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	values := skills.Values{Source: "acme/skills#abc", Skill: "review", Target: "claude-code"}
	project := installer.Command(values, harness.ScopeProject)
	want := []string{
		"--yes", "skills", "add", "acme/skills#abc", "--skill", "review",
		"--agent", "claude-code", "--yes",
	}
	if installer.Executable != "npx" || !slices.Equal(project, want) {
		t.Fatalf("project command = %s %q", installer.Executable, project)
	}
	if global := installer.Command(values, harness.ScopeGlobal); !slices.Equal(
		global, append(want, "--global"),
	) {
		t.Fatalf("global command = %q", global)
	}
}

func TestPlaceholdersSubstituteLiterallyIntoSingleArguments(t *testing.T) {
	path := writeConfig(t, `skillInstaller:
  executable: /opt/wrapper
  args: ["install", "--from={source}", "{skill}", "{target}:{skill}", "{{skill}}", "{}"]
  projectArgs: ["--here"]
  globalArgs: ["--everywhere", "{target}"]
`)
	installer, err := skills.LoadInstaller(path)
	if err != nil {
		t.Fatal(err)
	}
	hostile := skills.Values{
		Source: "a b; rm -rf / $(whoami) `id` {skill}",
		Skill:  "x {target} *",
		Target: "t|u",
	}
	got := installer.Command(hostile, harness.ScopeProject)
	want := []string{
		"install", "--from=a b; rm -rf / $(whoami) `id` {skill}", "x {target} *",
		"t|u:x {target} *", "{x {target} *}", "{}", "--here",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("command = %q\nwant      %q", got, want)
	}
	if global := installer.Command(hostile, harness.ScopeGlobal); !slices.Equal(
		global[len(global)-2:], []string{"--everywhere", "t|u"},
	) {
		t.Fatalf("global args = %q", global)
	}
}

func TestInvalidConfigFailsBeforeRunning(t *testing.T) {
	cases := map[string]string{
		"unknown placeholder": "skillInstaller:\n  executable: npx\n  args: [\"{source}\", \"{ref}\"]\n",
		"unknown placeholder in scope args": "skillInstaller:\n  executable: npx\n  args: []\n" +
			"  globalArgs: [\"--home={home}\"]\n",
		"missing executable":  "skillInstaller:\n  args: [\"{source}\"]\n",
		"missing args":        "skillInstaller:\n  executable: npx\n",
		"relative executable": "skillInstaller:\n  executable: ./bin/install\n  args: []\n",
		"shell string args":   "skillInstaller:\n  executable: npx\n  args: \"skills add {source}\"\n",
		"unknown key":         "skillInstaller:\n  executable: npx\n  args: []\n  shell: true\n",
		"unknown top-level":   "installHooks: [\"curl x | sh\"]\n",
		"malformed yaml":      "skillInstaller: [\n",
	}
	for name, content := range cases {
		if _, err := skills.LoadInstaller(
			writeConfig(t, content),
		); !errors.Is(
			err,
			skills.ErrConfig,
		) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
