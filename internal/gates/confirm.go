package gates

import (
	"context"
	"fmt"
	"strings"

	"github.com/mittalkanishk/steerloop/loop2/event"
	"github.com/mittalkanishk/steerloop/loop2/pause"
)

// Confirm asks before running a tool marked confirm.
type Confirm struct {
	autoApprove    bool
	humanAvailable bool
}

func NewConfirm(autoApprove, humanAvailable bool) Confirm {
	return Confirm{autoApprove: autoApprove, humanAvailable: humanAvailable}
}

func (g Confirm) Handle(ctx context.Context, call pause.Call, s pause.Session) (string, bool) {
	if g.autoApprove {
		return g.execute(ctx, call, s)
	}
	if !g.humanAvailable {
		return "This tool needs approval and nobody is available to give it. It was not run.", true
	}

	s.Emit(event.AwaitingInput, event.AwaitingInputData{
		Question: fmt.Sprintf("Run %s %s?", call.Name, call.Arguments),
		Options:  []string{"y", "n"},
	})
	reply, ok := s.AwaitReply(ctx) // same mechanism as AskUser
	if !ok {
		return "", false
	}

	switch strings.ToLower(strings.TrimSpace(reply)) {
	case "y", "yes":
		return g.execute(ctx, call, s)
	default:
		return "The user declined to run this tool.", true
	}
}

func (g Confirm) execute(ctx context.Context, call pause.Call, s pause.Session) (string, bool) {
	out, err := s.Execute(ctx, call.Name, call.Arguments)
	if err != nil {
		return fmt.Sprintf("Error executing tool %q: %s", call.Name, err.Error()), true
	}
	return out, true
}
