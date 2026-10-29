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

	"github.com/heynemann/agnthub/internal/harness"
	"github.com/heynemann/agnthub/internal/platform/env"
	"github.com/heynemann/agnthub/internal/source"
)

// Version is set at build time.
var Version = "dev"

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
	root.AddCommand(a.addCommand(ctx), a.listCommand(), a.removeCommand(ctx))
	return root
}

func (a *App) harnessContext() harness.Context {
	return harness.Context{Env: a.Env, ProjectRoot: a.WorkDir, LookPath: a.LookPath}
}
