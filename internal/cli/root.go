// Package cli implements the agnthub command-line interface.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime/debug"
	"strings"
	"time"

	"charm.land/fang/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/heynemann/agnthub/adoption"
	"github.com/heynemann/agnthub/harness"
	"github.com/heynemann/agnthub/platform/env"
	"github.com/heynemann/agnthub/skills"
	"github.com/heynemann/agnthub/source"
)

// Version and Commit are set at build time (see .goreleaser.yaml).
var (
	Version = "dev"
	Commit  = ""
)

// A `go install github.com/heynemann/agnthub/cmd/agnthub@vX.Y.Z` build carries no
// ldflags; it reports the module version Go recorded instead.
func init() {
	if Version != "dev" {
		return
	}
	if info, ok := debug.ReadBuildInfo(); ok && strings.HasPrefix(info.Main.Version, "v") {
		Version = strings.TrimPrefix(info.Main.Version, "v")
	}
}

// App holds process-level dependencies so commands stay testable.
type App struct {
	Env     env.Env
	WorkDir string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	// Interactive reports a terminal user who can answer prompts.
	Interactive bool
	// Prompter asks interactive users; nil selects the terminal UI.
	Prompter Prompter
	Now      func() time.Time
	Registry *harness.Registry
	Opener   source.Opener
	LookPath func(string) (string, error)
	// Adoption reports successful public GitHub installs; nil selects the production
	// reporter configured by AGNTHUB_API_URL or the user configuration's apiURL.
	Adoption *adoption.Reporter

	// styledStderr reports a terminal stderr that can show styled errors and a
	// transient progress line.
	styledStderr bool
}

// NewApp returns an App with production defaults for unset fields.
func NewApp(app App) *App {
	if app.Now == nil {
		app.Now = time.Now
	}
	if app.Registry == nil {
		app.Registry = harness.Default()
	}
	if app.LookPath == nil {
		app.LookPath = exec.LookPath
	}
	if app.Opener.Git == nil {
		app.Opener.Git = &source.GitFetcher{Env: app.Env}
	}
	if app.Prompter == nil {
		app.Prompter = &formPrompter{
			in:         app.Stdin,
			out:        app.Stdout,
			accessible: app.Env.Get("ACCESSIBLE") != "" || app.Env.Get("TERM") == "dumb",
		}
	}
	if app.Adoption == nil {
		// An unreadable configuration leaves the destination unknown: report nothing
		// rather than guess.
		configured, err := skills.LoadAPIURL(skills.ConfigPath(app.Env))
		if err != nil {
			app.Adoption = &adoption.Reporter{Env: app.Env}
		} else {
			app.Adoption = adoption.New(app.Env, Version, configured)
		}
	}
	app.styledStderr = isTerminal(app.Stderr) && app.Env.Get("TERM") != "dumb"
	return &app
}

// ExitError carries a process exit code for a failure already reported.
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("exit status %d", e.Code)
}

// Run executes the CLI with args and returns the process exit code.
func (a *App) Run(ctx context.Context, args []string) int {
	root := a.rootCommand(ctx)
	root.SetArgs(args)
	root.SetIn(a.Stdin)
	root.SetOut(a.Stdout)
	root.SetErr(a.Stderr)
	// fang styles help, usage errors and version output and adds completion and
	// man page generation; colorprofile strips styling when stdout is no terminal
	// or NO_COLOR / TERM=dumb ask for plain text.
	err := fang.Execute(ctx, root,
		fang.WithVersion(Version),
		fang.WithErrorHandler(a.reportError),
	)
	if err == nil {
		return 0
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}
	return 1
}

// reportError prints a failure on stderr: fang's styled block in a terminal,
// a single stable `error: ...` line otherwise. An ExitError was already reported.
func (a *App) reportError(w io.Writer, styles fang.Styles, err error) {
	if _, reported := errors.AsType[*ExitError](err); reported {
		return
	}
	if !a.styledStderr {
		fmt.Fprintln(a.Stderr, "error:", err)
		return
	}
	fang.DefaultErrorHandler(w, styles, err)
}

// Command groups shown by help, following skills.sh's sections.
const (
	groupManage  = "manage"
	groupUpdates = "updates"
	groupProject = "project"
	groupAgents  = "agents"
)

func (a *App) rootCommand(ctx context.Context) *cobra.Command {
	root := &cobra.Command{
		Use:   "agnthub",
		Short: "Install agents from source repositories into your coding harnesses",
		Long: `Install agents from source repositories into your coding harnesses.

agnthub installs agent definitions (--agent) into harnesses such as Claude Code,
OpenCode, Codex, GitHub Copilot CLI, Cursor, Gemini CLI and omp (--harness). It
previews every plan, records what it wrote in agnthub-lock.json and never
overwrites files it does not own.`,
		Example: `  # Choose agents and harnesses interactively
  agnthub add owner/repo
  agnthub add owner/repo --list
  agnthub add owner/repo --agent reviewer --harness claude --harness codex
  agnthub add owner/repo --all --global
  agnthub ls
  agnthub check
  agnthub update --yes
  agnthub rm reviewer`,
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	if Commit != "" {
		root.SetVersionTemplate("agnthub version {{.Version}} (commit " + Commit + ")\n")
	}
	root.AddGroup(
		&cobra.Group{ID: groupManage, Title: "Manage agents"},
		&cobra.Group{ID: groupUpdates, Title: "Updates"},
		&cobra.Group{ID: groupProject, Title: "Project"},
		&cobra.Group{ID: groupAgents, Title: "AI agents"},
	)
	for _, cmd := range []*cobra.Command{a.addCommand(ctx), a.listCommand(), a.removeCommand(ctx)} {
		cmd.GroupID = groupManage
		root.AddCommand(cmd)
	}
	for _, cmd := range []*cobra.Command{a.checkCommand(ctx), a.updateCommand(ctx)} {
		cmd.GroupID = groupUpdates
		root.AddCommand(cmd)
	}
	restore := a.restoreCommand(ctx)
	restore.GroupID = groupProject
	root.AddCommand(restore)
	mcpCmd := a.mcpCommand(ctx)
	mcpCmd.GroupID = groupAgents
	root.AddCommand(mcpCmd)
	return root
}

func (a *App) harnessContext() harness.Context {
	return harness.Context{Env: a.Env, ProjectRoot: a.WorkDir, LookPath: a.LookPath}
}

// isTerminal reports whether w is a terminal device.
func isTerminal(w io.Writer) bool {
	f, ok := w.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(f.Fd())
}
