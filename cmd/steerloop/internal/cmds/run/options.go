package runcmd

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mittalkanishk/steerloop/cmd/steerloop/internal/bootstrap"
)

// Options is the `steerloop run` command line: the flags shared with chat,
// and the ones only a single run has.
type Options struct {
	bootstrap.Options
	// Output Options
	Output string
	// Steering/Loop Control Options
	ControlPath string // give the loop a stream of commands that arrive over time, same as if a person typed it at the terminal
}

// ParseOptions reads flags into Options and reports its own errors to stderr.
func ParseOptions(args []string, stderr io.Writer) (Options, error) {
	var opts Options
	flags := flag.NewFlagSet("steerloop run", flag.ContinueOnError)
	opts.AddFlags(flags)
	flags.StringVar(&opts.UserPrompt, "user-prompt", "", "the user prompt (required)")
	flags.StringVar(&opts.Output, "output", "text", "text or jsonl")
	// Steering the loop
	flags.StringVar(&opts.ControlPath, "control-file", "", `steer the run from JSONL lines in this file or FIFO: {"type":"stop"}, {"type":"inject","message":"..."} or {"type":"modify_config","max_iterations":N,"temperature":T}; "wait_ms" delays a line`)

	err := bootstrap.ParseFlags(flags, args, stderr)
	return opts, err
}

// Validate is bootstrap.Options.Validate, plus the prompt and --output. It
// reports every problem at once, not just the first.
func (o Options) Validate() error {
	var errs []error
	if strings.TrimSpace(o.UserPrompt) == "" {
		errs = append(errs, errors.New("--user-prompt is required"))
	}
	if err := o.Options.Validate(); err != nil {
		errs = append(errs, err)
	}
	if o.Output != "text" && o.Output != "jsonl" {
		errs = append(errs, fmt.Errorf("--output must be text or jsonl, not %q", o.Output))
	}
	return errors.Join(errs...)
}
