package replaycmd

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
)

type Options struct {
	Path    string
	Speed   float64
	MaxWait time.Duration
}

func ParseOptions(args []string, stderr io.Writer) (Options, error) {
	var opts Options
	flags := flag.NewFlagSet("steerloop replay", flag.ContinueOnError)
	flags.StringVar(&opts.Path, "path", "", "path to the jsonl event file")
	flags.Float64Var(&opts.Speed, "speed", 1, "speed the replay up or down")
	flags.DurationVar(&opts.MaxWait, "max-wait", 0, "cap any pause between events such as slow model call or a wait for human input, at this long; short gaps keep their original pace (0: no cap)")
	if err := flags.Parse(args); err != nil {
		return opts, err
	}

	if flags.NArg() > 0 {
		err := fmt.Errorf("unexpected argument %q: flags must come before it, and a value with spaces needs quotes", flags.Arg(0))
		fmt.Fprintf(stderr, "steerloop: %v\n", err)
		return opts, err
	}
	return opts, nil
}

func (o Options) Validate() error {

	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	// Recording
	if strings.TrimSpace(o.Path) == "" {
		fail("--path is required")
	}

	// Pace
	if math.IsNaN(o.Speed) || math.IsInf(o.Speed, 0) {
		fail("--speed must be a finite number")
	} else if o.Speed < 0 {
		fail("--speed must not be negative (0 replays instantly)")
	}
	if o.MaxWait < 0 {
		fail("--max-wait must not be negative (0 means no cap)")
	}
	if o.MaxWait > 0 && o.Speed == 0 {
		fail("--max-wait has no effect with --speed 0: an instant replay never waits")
	}

	return errors.Join(errs...)
}
