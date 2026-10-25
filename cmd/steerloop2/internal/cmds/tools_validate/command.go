package toolsvalidatecmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mittalkanishk/steerloop/cmd/steerloop2/internal/cli"
	executor "github.com/mittalkanishk/steerloop/internal/tools"
)

// ToolsValidateCommand is `steerloop2 tools validate`. It checks a tool
// manifest the way `run --tools` would, without calling a model or running
// a tool. It implements cli.Command.
type ToolsValidateCommand struct {
	env cli.Environment
}

func New(env cli.Environment) *ToolsValidateCommand {
	return &ToolsValidateCommand{
		env: env,
	}
}

// Run reads top to bottom: parse, validate, load the manifest, report.
func (c *ToolsValidateCommand) Run(ctx context.Context, args []string) int {

	opts, err := ParseOptions(args, c.env.Stderr())
	if errors.Is(err, flag.ErrHelp) {
		return cli.ExitOK
	}
	if err != nil {
		return cli.ExitUsage // already reported
	}

	if err := opts.Validate(); err != nil {
		for line := range strings.SplitSeq(err.Error(), "\n") {
			fmt.Fprintf(c.env.Stderr(), "steerloop2: %s\n", line)
		}
		return cli.ExitUsage
	}

	// Load reports every problem in the manifest, one per line.
	manifest, err := executor.Load(opts.Path)
	if err != nil {
		fmt.Fprintf(c.env.Stderr(), "invalid manifest:\n%v\n", err)
		return cli.ExitError
	}

	report(c.env.Stdout(), manifest)
	return cli.ExitOK
}

// report lists each tool, the program it runs, and whether it asks first.
func report(w io.Writer, m *executor.ToolManifestExecutor) {
	fmt.Fprintf(w, "ok: %d tool(s)\n", len(m.Tools))
	for _, t := range m.Tools {
		confirm := ""
		if t.RequiresConfirmation {
			confirm = " (asks before running)"
		}
		fmt.Fprintf(w, "  %s: %s%s\n", t.Name, t.Command[0], confirm)
	}
}
