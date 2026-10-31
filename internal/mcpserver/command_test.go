package mcpserver_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/internal/mcpserver"
)

// The cases mirror the site's install builder tests (web/src/lib/command.test.ts).
func TestBuildCommandMatchesTheSiteBuilder(t *testing.T) {
	cases := []struct {
		name string
		in   mcpserver.CommandInput
		want string
	}{
		{
			name: "nothing selected lets the CLI ask",
			in:   mcpserver.CommandInput{Source: "octo/agents"},
			want: "npx agnthub add octo/agents",
		},
		{
			name: "harnesses in display order, global, profile",
			in: mcpserver.CommandInput{
				Source: "octo/agents", Agents: []string{"reviewer", "planner"},
				Harnesses: []agent.HarnessID{agent.OMP, agent.Claude}, Global: true,
				OMPProfile: "work",
			},
			want: "npx agnthub add octo/agents --agent reviewer --agent planner " +
				"--harness claude --harness omp --global --extra omp.profile=work",
		},
		{
			name: "all drops the selection but keeps the profile",
			in: mcpserver.CommandInput{
				Source: "octo/agents", All: true, Harnesses: []agent.HarnessID{agent.Codex},
				OMPProfile: "work",
			},
			want: "npx agnthub add octo/agents --all --extra omp.profile=work",
		},
		{
			name: "a profile without omp selected is left out",
			in: mcpserver.CommandInput{
				Source: "octo/agents", Harnesses: []agent.HarnessID{agent.Codex},
				OMPProfile: "work",
			},
			want: "npx agnthub add octo/agents --harness codex",
		},
		{
			name: "every harness becomes --harness all",
			in: mcpserver.CommandInput{
				Source: "octo/agents", Agents: []string{"reviewer"},
				Harnesses: []agent.HarnessID{
					agent.OMP, agent.Gemini, agent.Cursor, agent.Copilot,
					agent.Codex, agent.OpenCode, agent.Claude,
				},
				OMPProfile: "work",
			},
			want: "npx agnthub add octo/agents --agent reviewer --harness all " +
				"--extra omp.profile=work",
		},
		{
			name: "one harness short keeps the explicit list",
			in: mcpserver.CommandInput{
				Source: "octo/agents",
				Harnesses: []agent.HarnessID{
					agent.Claude, agent.OpenCode, agent.Codex, agent.Copilot,
					agent.Cursor, agent.Gemini,
				},
			},
			want: "npx agnthub add octo/agents --harness claude --harness opencode " +
				"--harness codex --harness copilot --harness cursor --harness gemini",
		},
	}
	for _, tc := range cases {
		got, err := mcpserver.BuildCommand(&tc.in)
		if err != nil || got != tc.want {
			t.Errorf("%s: got %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
}

func TestBuildCommandRejectsInvalidOMPProfiles(t *testing.T) {
	valid := []string{"default", "work", "a", "team.v2", "my_profile-1", strings.Repeat("a", 64)}
	invalid := []string{
		"Work", " work", "-x", ".hidden", "a/b", "end.", "con", "lpt1.txt",
		strings.Repeat("a", 65), "x'y", "$(id)", "work; rm -rf ~",
	}
	for _, profile := range valid {
		in := mcpserver.CommandInput{Source: "o/r", OMPProfile: profile}
		if _, err := mcpserver.BuildCommand(&in); err != nil {
			t.Errorf("%q should be accepted: %v", profile, err)
		}
	}
	for _, profile := range invalid {
		in := mcpserver.CommandInput{
			Source: "o/r", Harnesses: []agent.HarnessID{agent.OMP}, OMPProfile: profile,
		}
		if _, err := mcpserver.BuildCommand(&in); err == nil {
			t.Errorf("%q should be rejected", profile)
		}
	}
}

func TestShellQuoteRoundTripsHostileArguments(t *testing.T) {
	for _, safe := range []string{"octo/agents", "omp.profile=work"} {
		if got := mcpserver.ShellQuote(safe); got != safe {
			t.Errorf("%q should stay unquoted, got %q", safe, got)
		}
	}
	hostile := []string{
		"it's", "$(id)", "`id`", "$SECRET", "${SECRET}", "a\tb\nc", "*", "~", "=ls", "!!",
		"; rm -rf /", `"quoted"`, `\`, "''", "omp.profile=work; echo $SECRET",
	}
	for _, shell := range []string{"sh", "bash", "zsh"} {
		path, err := exec.LookPath(shell)
		if err != nil {
			continue
		}
		for _, arg := range hostile {
			script := "printf %s " + mcpserver.ShellQuote(arg)
			out, err := exec.CommandContext(context.Background(), path, "-c", script).Output()
			if err != nil || string(out) != arg {
				t.Errorf("%s: %q came back as %q (%v)", shell, arg, out, err)
			}
		}
	}
}
