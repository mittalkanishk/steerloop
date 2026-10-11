package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/mittalkanishk/steerloop"
)

// replayCommand re-renders a recording. It makes no model call and runs no
// tool. Its exit code is the one the recorded run ended with.
func replayCommand(e env, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(e.stderr, "usage: steerloop replay <file.jsonl>")
		return exitUsage
	}
	f, err := os.Open(args[0])
	if err != nil {
		fmt.Fprintf(e.stderr, "steerloop: %v\n", err)
		return exitError
	}
	defer f.Close()

	records, err := readRecords(f)
	if err != nil {
		fmt.Fprintf(e.stderr, "steerloop: %s: %v\n", args[0], err)
		return exitError
	}
	if len(records) == 0 {
		fmt.Fprintf(e.stderr, "steerloop: %s: recording is empty\n", args[0])
		return exitError
	}

	renderer := &textRenderer{trace: e.stderr, final: e.stdout}
	code := exitError
	sawDone := false
	for _, rec := range records {
		renderer.render(rec)
		if rec.Type == string(steerloop.EventDone) {
			var done steerloop.DoneData
			if json.Unmarshal(rec.Data, &done) == nil {
				code = exitCodeFor(done.Reason)
				sawDone = true
			}
		}
	}
	if !sawDone {
		fmt.Fprintf(e.stderr, "steerloop: %s: recording has no done event\n", args[0])
	}
	return code
}

func exitCodeFor(reason steerloop.DoneReason) int {
	switch reason {
	case steerloop.DoneReasonFinished:
		return exitOK
	case steerloop.DoneReasonMaxIterations:
		return exitMaxIterations
	case steerloop.DoneReasonTimeout:
		return exitTimeout
	case steerloop.DoneReasonStopped:
		return exitStopped
	case steerloop.DoneReasonAwaitingHuman:
		return exitAwaitingHuman
	default:
		return exitError
	}
}
