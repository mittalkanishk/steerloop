package cli

import "github.com/mittalkanishk/steerloop/loop2/event"

// Exit codes.
const (
	ExitOK            = 0
	ExitError         = 1
	ExitUsage         = 2
	ExitMaxIterations = 3
	ExitTimeout       = 4
	ExitAwaitingHuman = 5
	ExitStopped       = 130
)

// ExitCodeFor maps how a run ended to the process exit code.
func ExitCodeFor(reason event.DoneReason) int {
	switch reason {
	case event.ReasonFinished:
		return ExitOK
	case event.ReasonMaxIterations:
		return ExitMaxIterations
	case event.ReasonTimeout:
		return ExitTimeout
	case event.ReasonStopped:
		return ExitStopped
	case event.ReasonAwaitingHuman:
		return ExitAwaitingHuman
	default:
		return ExitError
	}
}
