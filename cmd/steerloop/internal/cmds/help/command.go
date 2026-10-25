// Package helpcmd is `steerloop help`.
package helpcmd

import (
	"context"
	"fmt"

	"github.com/mittalkanishk/steerloop/cmd/steerloop/internal/cli"
)

// HelpCommand prints the usage on stdout: help was asked for, so it is
// output, not an error. It implements cli.Command.
type HelpCommand struct {
	env cli.Environment
}

func New(env cli.Environment) *HelpCommand {
	return &HelpCommand{
		env: env,
	}
}

func (c *HelpCommand) Run(ctx context.Context, args []string) int {
	fmt.Fprint(c.env.Stdout(), cli.Usage)
	return cli.ExitOK
}
