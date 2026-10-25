// Package versioncmd is `steerloop2 version`.
package versioncmd

import (
	"context"
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/mittalkanishk/steerloop/cmd/steerloop2/internal/cli"
)

// VersionCommand prints the module version and the Go version it was built
// with. It implements cli.Command.
type VersionCommand struct {
	env cli.Environment
}

func New(env cli.Environment) *VersionCommand {
	return &VersionCommand{
		env: env,
	}
}

func (c *VersionCommand) Run(ctx context.Context, args []string) int {
	if len(args) > 0 {
		fmt.Fprintf(c.env.Stderr(), "steerloop2: version takes no arguments, got %q\n", args[0])
		return cli.ExitUsage
	}
	fmt.Fprintf(c.env.Stdout(), "steerloop2 %s (%s)\n", moduleVersion(), runtime.Version())
	return cli.ExitOK
}

// moduleVersion is the version `go install` stamped into the binary, or
// "(devel)" for a local build.
func moduleVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}
