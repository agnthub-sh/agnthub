// Command agnthub installs agents from source repositories into coding harnesses.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"golang.org/x/term"

	"github.com/heynemann/agnthub/internal/cli"
	"github.com/heynemann/agnthub/platform/env"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	workDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: cannot determine working directory:", err)
		return 1
	}
	app := cli.NewApp(cli.App{
		Env:         env.FromProcess(),
		WorkDir:     workDir,
		Stdin:       os.Stdin,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		Interactive: term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())),
	})
	return app.Run(ctx, os.Args[1:])
}
