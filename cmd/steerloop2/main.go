// Command steerloop2 is the `run` command of steerloop in an interface-first,
// nested shape. Each interface is declared in the file that uses it.
//
// Read in this order:
//
//	internal/runcmd/run.go   what happens, and what it needs
//	internal/runcmd/wire.go  what fills each need
//	then any package under internal/ that wire.go names
package main

import (
	"context"
	"os"

	"github.com/mittalkanishk/steerloop/cmd/steerloop2/internal/cli"
	cmds "github.com/mittalkanishk/steerloop/cmd/steerloop2/internal/cmds"
	"github.com/mittalkanishk/steerloop/cmd/steerloop2/internal/osenv"
)

func main() {
	env := osenv.Real{}

	app := cli.NewApp(env, map[string]cli.Command{
		"run": cmds.New(env),
	})

	os.Exit(app.Run(context.Background(), os.Args[1:]))
}
