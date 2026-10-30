// Package cli implements the agnthub command-line interface.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

	"github.com/heynemann/agnthub/internal/adoption"
	"github.com/heynemann/agnthub/internal/harness"
	"github.com/heynemann/agnthub/internal/platform/env"
	"github.com/heynemann/agnthub/internal/skills"
	"github.com/heynemann/agnthub/internal/source"
)

// Version and Commit are set at build time (see .goreleaser.yaml).
var (
	Version = "dev"
	Commit  = ""
)

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
	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}
	fmt.Fprintln(a.Stderr, "error:", err)
	return 1
}

func (a *App) rootCommand(ctx context.Context) *cobra.Command {
	root := &cobra.Command{
		Use:           "agnthub",
		Short:         "Install agents from source repositories into your coding harnesses",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	if Commit != "" {
		root.SetVersionTemplate("agnthub version {{.Version}} (commit " + Commit + ")\n")
	}
	root.AddCommand(
		a.addCommand(ctx), a.listCommand(), a.removeCommand(ctx),
		a.checkCommand(ctx), a.updateCommand(ctx), a.restoreCommand(ctx),
	)
	return root
}

func (a *App) harnessContext() harness.Context {
	return harness.Context{Env: a.Env, ProjectRoot: a.WorkDir, LookPath: a.LookPath}
}
