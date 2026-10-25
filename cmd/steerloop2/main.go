// Command steerloop2 runs an agent loop from the terminal: run, replay,
// tools validate, version and help. It is built interface-first and nested:
// each interface is declared in the file that uses it.
//
// Read in this order:
//
//	internal/cmds/run/run.go   what happens, and what it needs
//	internal/cmds/run/wire.go  what fills each need
//	then any package under internal/ that wire.go names
package main

import (
	"context"
	"os"

	"github.com/mittalkanishk/steerloop/cmd/steerloop2/internal/cli"
	helpcmd "github.com/mittalkanishk/steerloop/cmd/steerloop2/internal/cmds/help"
	replaycmd "github.com/mittalkanishk/steerloop/cmd/steerloop2/internal/cmds/replay"
	runcmd "github.com/mittalkanishk/steerloop/cmd/steerloop2/internal/cmds/run"
	toolsvalidatecmd "github.com/mittalkanishk/steerloop/cmd/steerloop2/internal/cmds/tools_validate"
	versioncmd "github.com/mittalkanishk/steerloop/cmd/steerloop2/internal/cmds/version"
	"github.com/mittalkanishk/steerloop/cmd/steerloop2/internal/osenv"
)

func main() {
	os.Exit(newApp(osenv.Real{}).Run(context.Background(), os.Args[1:]))
}

// newApp registers every command against env. main passes the real OS;
// the CLI tests pass a fake one.
func newApp(env cli.Environment) *cli.App {
	help := helpcmd.New(env)
	version := versioncmd.New(env)

	return cli.NewApp(env, map[string]cli.Command{
		"run":       runcmd.New(env),
		"replay":    replaycmd.New(env),
		"tools":     toolsvalidatecmd.New(env),
		"version":   version,
		"--version": version,
		"help":      help,
		"-h":        help,
		"--help":    help,
	})
}
