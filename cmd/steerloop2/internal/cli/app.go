// Package cli is the leaf of steerloop2: command dispatch, the environment
// contract and exit codes. It imports nothing from steerloop2, so every
// other package can import it without a cycle.
package cli

import (
	"context"
	"fmt"
	"io"
)

// Usage is the top-level help: every command, one line each.
const Usage = `steerloop2 runs an agent loop from the terminal.

Usage:
  steerloop2 run --model <spec> --user-prompt <text> [flags]   run one loop
  steerloop2 replay --path <file.jsonl> [flags]                 play back a recorded run
  steerloop2 tools validate --path <manifest>                   check a tool manifest
  steerloop2 version                                            print the version
  steerloop2 help                                               show this help

Run 'steerloop2 <command> -h' for a command's flags.
`

// Command is one `steerloop2 <name>` subcommand. → internal/cmds/run
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
		fmt.Fprint(a.env.Stderr(), Usage)
		return ExitUsage
	}

	cmd, ok := a.commands[args[0]]
	if !ok {
		fmt.Fprintf(a.env.Stderr(), "steerloop2: unknown command %q\n\n%s", args[0], Usage)
		return ExitUsage
	}

	return cmd.Run(ctx, args[1:])
}
