// Package runcmd is `steerloop2 run`. Start at run.go.
package runcmd

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/mittalkanishk/steerloop/cmd/steerloop2/internal/cli"
)

// Command is `steerloop2 run`. It implements cli.Command.
type Command struct {
	env cli.Environment
}

func New(env cli.Environment) *Command {
	return &Command{
		env: env,
	}
}

// Run reads top to bottom: parse, validate, wire, execute.
func (c *Command) Run(ctx context.Context, args []string) int {
	
	opts, err := ParseOptions(args, c.env.Stderr())
	if errors.Is(err, flag.ErrHelp) {
		return cli.ExitOK
	}
	if err != nil {
		return cli.ExitUsage // the flag package has already reported it
	}

	if err := opts.Validate(); err != nil {
		fmt.Fprintf(c.env.Stderr(), "steerloop2: %v\n", err)
		return cli.ExitUsage
	}

	run, err := Wire(ctx, c.env, opts)
	if err != nil {
		fmt.Fprintf(c.env.Stderr(), "steerloop2: %v\n", err)
		return cli.ExitUsage
	}

	return run.Execute(ctx)
}
