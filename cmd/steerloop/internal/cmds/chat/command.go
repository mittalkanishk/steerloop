// Package chatcmd is `steerloop chat`. Start at chat.go.
package chatcmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mittalkanishk/steerloop/cmd/steerloop/internal/agent"
	"github.com/mittalkanishk/steerloop/cmd/steerloop/internal/cli"
)

// Command is `steerloop chat`. It implements cli.Command.
type Command struct {
	env cli.Environment
}

func New(env cli.Environment) *Command {
	return &Command{
		env: env,
	}
}

// Run reads top to bottom: parse, validate, build, execute.
func (c *Command) Run(ctx context.Context, args []string) int {

	opts, err := ParseOptions(args, c.env.Stderr())
	if errors.Is(err, flag.ErrHelp) {
		return cli.ExitOK
	}
	if err != nil {
		return cli.ExitUsage // the flag package has already reported it
	}

	if err := opts.Validate(); err != nil {
		// Validate joins every problem, one per line.
		for line := range strings.SplitSeq(err.Error(), "\n") {
			fmt.Fprintf(c.env.Stderr(), "steerloop: %s\n", line)
		}
		return cli.ExitUsage
	}

	if !c.env.StdinIsTerminal() {
		fmt.Fprintln(c.env.Stderr(), "steerloop: chat needs a terminal; use `steerloop run` for one turn")
		return cli.ExitUsage
	}

	chat, err := NewChat(ctx, c.env, opts)
	if err != nil {
		fmt.Fprintf(c.env.Stderr(), "steerloop: %v\n", err)
		return cli.ExitUsage
	}

	return chat.Execute(ctx)
}

// ParseOptions reads the `steerloop chat` command line: the flags shared
// with run, and an optional first prompt.
func ParseOptions(args []string, stderr io.Writer) (agent.Options, error) {
	var opts agent.Options
	flags := flag.NewFlagSet("steerloop chat", flag.ContinueOnError)
	opts.AddFlags(flags)
	flags.StringVar(&opts.UserPrompt, "user-prompt", "", "the first prompt; without it, chat starts at the prompt")

	err := agent.ParseFlags(flags, args, stderr)
	return opts, err
}
