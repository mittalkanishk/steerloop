// Package cli is the leaf of steerloop: command dispatch, the environment
// contract and exit codes. It imports nothing from steerloop, so every
// other package can import it without a cycle.
package cli

import (
	"context"
	"fmt"
	"io"
)

// Usage is the top-level help: every command, one line each.
const Usage = `steerloop runs an agent loop from the terminal.

Usage:
  steerloop run --model <spec> --user-prompt <text> [flags]   run one loop
  steerloop replay --path <file.jsonl> [flags]                play back a recorded run
  steerloop tools validate --path <manifest>                  check a tool manifest
  steerloop version                                           print the version
  steerloop help                                              show this help

Run 'steerloop <command> -h' for a command's flags.
`

// Command is one `steerloop <name>` subcommand. → internal/cmds/run
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

// App dispatches `steerloop <command>` to the Command registered for it.
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
		fmt.Fprint(a.env.Stderr(), Usage)
		return ExitUsage
	}

	cmd, ok := a.commands[args[0]]
	if !ok {
		fmt.Fprintf(a.env.Stderr(), "steerloop: unknown command %q\n\n%s", args[0], Usage)
		return ExitUsage
	}

	return cmd.Run(ctx, args[1:])
}
