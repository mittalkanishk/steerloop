// Command steerloop runs an agent loop from the terminal: run, chat,
// replay, tools validate, version and help. It is built interface-first and
// nested: each interface is declared in the file that uses it.
//
// Read in this order:
//
//	internal/cmds/run/run.go    one turn: what happens, what it needs, and
//	                            (NewRun) what fills each need
//	internal/cmds/chat/chat.go  a session of turns at a terminal
//	internal/agent              what both share: flags, loops, tools, recording
//	then any package under internal/ that NewRun or NewChat names
package main

import (
	"context"
	"os"

	"github.com/mittalkanishk/steerloop/cmd/steerloop/internal/cli"
	chatcmd "github.com/mittalkanishk/steerloop/cmd/steerloop/internal/cmds/chat"
	helpcmd "github.com/mittalkanishk/steerloop/cmd/steerloop/internal/cmds/help"
	replaycmd "github.com/mittalkanishk/steerloop/cmd/steerloop/internal/cmds/replay"
	runcmd "github.com/mittalkanishk/steerloop/cmd/steerloop/internal/cmds/run"
	toolsvalidatecmd "github.com/mittalkanishk/steerloop/cmd/steerloop/internal/cmds/tools_validate"
	versioncmd "github.com/mittalkanishk/steerloop/cmd/steerloop/internal/cmds/version"
	"github.com/mittalkanishk/steerloop/cmd/steerloop/internal/osenv"
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
		"chat":      chatcmd.New(env),
		"replay":    replaycmd.New(env),
		"tools":     toolsvalidatecmd.New(env),
		"version":   version,
		"--version": version,
		"help":      help,
		"-h":        help,
		"--help":    help,
	})
}
