// Command steerloop runs an agent loop from the terminal: a model, tools
// declared in a manifest, live steering, and a JSONL record of the run.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"

	"github.com/mittalkanishk/steerloop/internal/exectool"
)

// Exit codes.
const (
	exitOK            = 0
	exitError         = 1
	exitUsage         = 2
	exitMaxIterations = 3
	exitTimeout       = 4
	exitAwaitingHuman = 5
	exitStopped       = 130
)

const usage = `steerloop runs an agent loop from the terminal.

Usage:
  steerloop run --prompt <text> ...   run one loop
  steerloop replay <file.jsonl>       re-render a recorded run; no model call
  steerloop tools validate <file>     check a tool manifest
  steerloop version                   print version information

Run "steerloop run -h" for the flags of run.
`

func main() {
	
	signals := make(chan os.Signal, 2)
	
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	stat, _ := os.Stdin.Stat()

	e := env{
		stdin:           os.Stdin,
		stdout:          os.Stdout,
		stderr:          os.Stderr,
		stdinIsTerminal: stat != nil && stat.Mode()&os.ModeCharDevice != 0,
		getenv:          os.Getenv,
		signals:         signals,
	}

	os.Exit(dispatch(e, os.Args[1:]))
}

func dispatch(e env, args []string) int {

	if len(args) == 0 {
		fmt.Fprint(e.stderr, usage)
		return exitUsage
	}

	switch args[0] {

	case "run":
		return runCommand(e, args[1:])

	case "replay":
		return replayCommand(e, args[1:])

	case "tools":
		return toolsCommand(e, args[1:])

	case "version", "--version":
		fmt.Fprintf(e.stdout, "steerloop %s (%s)\n", moduleVersion(), runtime.Version())
		return exitOK

	case "help", "-h", "--help":
		fmt.Fprint(e.stdout, usage)
		return exitOK
		
	default:
		fmt.Fprintf(e.stderr, "steerloop: unknown command %q\n\n%s", args[0], usage)
		return exitUsage
	}
}

func moduleVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}

func toolsCommand(e env, args []string) int {
	if len(args) != 2 || args[0] != "validate" {
		fmt.Fprintln(e.stderr, "usage: steerloop tools validate <manifest>")
		return exitUsage
	}
	m, err := exectool.Load(args[1])
	if err != nil {
		fmt.Fprintf(e.stderr, "invalid manifest:\n%v\n", err)
		return exitError
	}
	fmt.Fprintf(e.stdout, "ok: %d tool(s)\n", len(m.Tools))
	for _, t := range m.Tools {
		confirm := ""
		if t.Confirm {
			confirm = " (asks before running)"
		}
		fmt.Fprintf(e.stdout, "  %s: %s%s\n", t.Name, t.Command[0], confirm)
	}
	return exitOK
}
