package replaycmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/mittalkanishk/steerloop/cmd/steerloop/internal/cli"
)

// Timestamps are data that come from the recording: each event.Event has a timestamp.
// The clock acts: replay subtracts two timestamps to get the gap and divides by --speed option and aks
// the clock to wait that long before rendering the next event.
type Clock interface {
	Sleep(ctx context.Context, duration time.Duration) error
}

// Command is `steerloop replay`. It implements cli.Command.
type ReplayCommand struct {
	env   cli.Environment
	clock Clock
}

func New(env cli.Environment) *ReplayCommand {
	return &ReplayCommand{
		env:   env,
		clock: &ReplayClock{},
	}
}

func (c *ReplayCommand) Run(ctx context.Context, args []string) int {
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

	replay, err := NewReplay(c.env, c.clock, opts)
	if err != nil {
		fmt.Fprintf(c.env.Stderr(), "steerloop: %v\n", err)
		return cli.ExitUsage
	}

	return replay.Execute(ctx)
}
