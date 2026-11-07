package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/gitdiff"
	"github.com/agnthub-sh/agnthub/preview"
	"github.com/agnthub-sh/agnthub/source"
)

// withoutComments drops the leading "#" lines a diff starts with.
func withoutComments(patch string) string {
	for strings.HasPrefix(patch, "#") {
		_, patch, _ = strings.Cut(patch, "\n")
	}
	return patch
}

// TestFreshPreviewIsTheCLIDiffOnAFreshMachine proves preview.Fresh, which the
// hosted registry runs, renders byte for byte what `agnthub add --diff` prints in
// an empty project, for every harness, both scopes, dependencies and skills.
func TestFreshPreviewIsTheCLIDiffOnAFreshMachine(t *testing.T) {
	cases := map[string]struct {
		global    bool
		harnesses []string
		skills    bool
	}{
		"project, every harness, agent dependencies": {
			harnesses: []string{
				"claude",
				"opencode",
				"codex",
				"copilot",
				"cursor",
				"gemini",
				"omp",
			},
		},
		"user scope, every harness": {
			global: true,
			harnesses: []string{
				"claude",
				"opencode",
				"codex",
				"copilot",
				"cursor",
				"gemini",
				"omp",
			},
		},
		"project, skill dependency": {
			harnesses: []string{"claude", "codex", "omp"}, skills: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			git := dependencyRepos("v1")
			agentID := "reviewer"
			var vars map[string]string
			if tc.skills {
				h.writeSkillAgent(boundsV1)
				f := newSkillFixture(h, map[string]string{"code-review": "1.2.0"})
				vars = map[string]string{"FAKE_SRC": f.src, "FAKE_ARGS": f.args}
				files := map[string]string{}
				for _, rel := range []string{"agnthub.yaml", "agents/reviewer.md"} {
					files[rel] = readSource(t, h, rel)
				}
				agentsRepo(git, "r-skill", files)
				for key, repo := range f.git() {
					git[key] = repo
				}
			}
			args := []string{"add", "acme/agents", "--agent", agentID}
			ids := make([]agent.HarnessID, 0, len(tc.harnesses))
			for _, id := range tc.harnesses {
				args = append(args, "--harness", id)
				ids = append(ids, agent.HarnessID(id))
			}
			if tc.global {
				args = append(args, "--global")
			}
			code, cliPatch, stderr := h.runSplit(git, vars, append(args, "--diff")...)
			if code != 0 {
				t.Fatalf("exit %d: %s", code, stderr)
			}

			loc, err := source.ParseRemote("acme/agents")
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := git.Fetch(context.Background(), loc)
			if err != nil {
				t.Fatal(err)
			}
			// A user-scope lock records absolute paths, so the preview pretends to run in
			// this home, which must not exist while it plans.
			home := ""
			if tc.global {
				home = h.home
				if err := os.RemoveAll(h.home); err != nil {
					t.Fatal(err)
				}
			}
			result, err := preview.Fresh(context.Background(), preview.Request{
				Snapshot: snapshot, Agents: []string{agentID}, Harnesses: ids, Global: tc.global,
				Home: home, Opener: source.Opener{Git: git},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Blocked) > 0 {
				t.Fatalf("unexpected blocked items: %v", result.Blocked)
			}
			got := (&gitdiff.Patch{Files: result.Files}).String()
			if got != withoutComments(cliPatch) {
				t.Fatalf("preview differs from the CLI diff:\n--- preview\n%s\n--- cli\n%s",
					got, cliPatch)
			}
			for _, file := range result.Files {
				if file.Action != gitdiff.Create {
					t.Fatalf("a fresh machine only gets new files: %+v", file)
				}
			}
		})
	}
}

func readSource(t *testing.T, h *harnessT, rel string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(h.source, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
