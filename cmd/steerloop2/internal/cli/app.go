// Package cli is the leaf of steerloop2: command dispatch, the environment
// contract and exit codes. It imports nothing from steerloop2, so every
// other package can import it without a cycle.
package cli

import (
	"context"
	"fmt"
	"io"
)

const usage = `steerloop2 runs an agent loop from the terminal.

Usage:
  steerloop2 run --model <spec> --prompt <text> [flags]
`

// Command is one `steerloop2 <name>` subcommand. → internal/runcmd
type Command interface {
	Run(ctx context.Context, args []string) int
}

// Environment is everything the CLI takes from the OS. → internal/osenv
// App is its top-level user; commands receive it from main.
type Environment interface {
	Getenv(key string) string
	Stdin() io.Reader
	Stdout() io.Writer
	Stderr() io.Writer
	StdinIsTerminal() bool
}

// App dispatches `steerloop2 <command>` to the Command registered for it.
// main supplies the commands, so this package never imports them.
type App struct {
	env      Environment
	commands map[string]Command
}

func NewApp(env Environment, commands map[string]Command) *App {
	return &App{
		env:      env,
		commands: commands,
	}
}

func (a *App) Run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(a.env.Stderr(), usage)
		return ExitUsage
	}

	cmd, ok := a.commands[args[0]]
	if !ok {
		fmt.Fprintf(a.env.Stderr(), "steerloop2: unknown command %q\n\n%s", args[0], usage)
		return ExitUsage
	}

	return cmd.Run(ctx, args[1:])
}
