package toolsvalidatecmd

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

const usage = "usage: steerloop tools validate --path <manifest>"

// Options is the `steerloop tools validate` command line.
type Options struct {
	Path string // the tool manifest (YAML)
}

// ParseOptions reads `validate` and then its flags. `tools` has one
// subcommand today; anything else is a usage error.
func ParseOptions(args []string, stderr io.Writer) (Options, error) {
	var opts Options

	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return opts, errors.New("missing subcommand")
	}
	switch args[0] {
	case "validate":
	case "help", "-h", "--help":
		fmt.Fprintln(stderr, usage)
		return opts, flag.ErrHelp
	default:
		err := fmt.Errorf("unknown subcommand %q", args[0])
		fmt.Fprintf(stderr, "steerloop: tools: %v\n%s\n", err, usage)
		return opts, err
	}

	flags := flag.NewFlagSet("steerloop tools validate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opts.Path, "path", "", "path to the tool manifest (YAML)")
	if err := flags.Parse(args[1:]); err != nil {
		return opts, err
	}

	if flags.NArg() > 0 {
		err := fmt.Errorf("unexpected argument %q: flags must come before it, and a value with spaces needs quotes", flags.Arg(0))
		fmt.Fprintf(stderr, "steerloop: %v\n", err)
		return opts, err
	}
	return opts, nil
}

// Validate reports every problem with the options, one per line.
func (o Options) Validate() error {
	var errs []error
	if strings.TrimSpace(o.Path) == "" {
		errs = append(errs, errors.New("--path is required"))
	}
	return errors.Join(errs...)
}
