package mcpserver

import (
	"regexp"
	"slices"
	"strings"

	"github.com/heynemann/agnthub/agent"
	"github.com/heynemann/agnthub/harness"
)

// CommandInput selects what an install command installs, as the site's install builder does.
type CommandInput struct {
	// Source is the GitHub owner/repo.
	Source string
	// Agents are installed with --agent; empty lets the CLI ask. Ignored with All.
	Agents []string
	// Harnesses are installed into with --harness; empty lets the CLI ask. Ignored with All.
	Harnesses []agent.HarnessID
	All       bool
	Global    bool
	// OMPProfile is the omp profile (--extra omp.profile=…); empty leaves it unset.
	OMPProfile string
}

// BuildCommand renders the `npx agnthub add …` command exactly as the site's install builder
// (web/src/lib/command.ts) does: harnesses in display order, defaults omitted, every token
// shell-quoted. An omp profile that does not apply (omp not selected) is left out.
func BuildCommand(in *CommandInput) (string, error) {
	args := []string{"npx", "agnthub", "add", in.Source}
	if in.All {
		args = append(args, "--all")
	} else {
		for _, id := range in.Agents {
			args = append(args, "--agent", id)
		}
		for _, h := range agent.AllHarnesses {
			if slices.Contains(in.Harnesses, h) {
				args = append(args, "--harness", string(h))
			}
		}
	}
	if in.Global {
		args = append(args, "--global")
	}
	if in.OMPProfile != "" &&
		(in.All || len(in.Harnesses) == 0 || slices.Contains(in.Harnesses, agent.OMP)) {
		extra := "omp.profile=" + in.OMPProfile
		// The CLI's own validation of the setting, so the command never fails on it.
		if _, err := harness.ResolveSettings(
			harness.Default(), []string{extra}, []agent.HarnessID{agent.OMP},
		); err != nil {
			return "", err
		}
		args = append(args, "--extra", extra)
	}
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = ShellQuote(arg)
	}
	return strings.Join(quoted, " "), nil
}

// safeArg matches arguments that never need quoting in POSIX sh, bash or zsh. A leading
// '=' is excluded because zsh expands `=word` to a command path.
var safeArg = regexp.MustCompile(`^[A-Za-z0-9._/:@+-][A-Za-z0-9._/:=@+-]*$`)

// ShellQuote quotes arg for POSIX sh, bash and zsh unless it is safe as is.
func ShellQuote(arg string) string {
	if safeArg.MatchString(arg) {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}
