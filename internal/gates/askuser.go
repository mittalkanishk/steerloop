// Package gates holds pause handlers for tool calls that need a person
// before they can finish. Each type implements pause.Handler and touches
// only loop2's public types, so any front end in this module (the CLI, a
// future server) can use it.
package gates

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop2"
	"github.com/mittalkanishk/steerloop/loop2/event"
	"github.com/mittalkanishk/steerloop/loop2/pause"
)

// AskUserToolName is the tool the model calls to ask the person a question.
const AskUserToolName = "ask_user"

// AskUserToolInfo describes the ask_user tool to the model.
func AskUserToolInfo() (*schema.ToolInfo, error) {
	return loop2.NewToolInfo(AskUserToolName, "Ask the person running this session a question and wait for the answer.", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"question": map[string]any{"type": "string", "description": "The question to ask."},
			"options":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional suggested answers."},
		},
		"required": []string{"question"},
	})
}

// AskUser answers an ask_user call with the person's next reply.
type AskUser struct {
	humanAvailable bool
}

func NewAskUser(humanAvailable bool) AskUser {
	return AskUser{
		humanAvailable: humanAvailable,
	}
}

func (g AskUser) Handle(ctx context.Context, call pause.Call, s pause.Session) (string, bool) {
	var args struct {
		Question string   `json:"question"`
		Options  []string `json:"options"`
	}
	_ = json.Unmarshal([]byte(call.Arguments), &args)
	if strings.TrimSpace(args.Question) == "" {
		return AskUserToolName + ` needs a non-empty "question".`, true
	}

	s.Emit(event.AwaitingInput, event.AwaitingInputData{Question: args.Question, Options: args.Options})
	if !g.humanAvailable {
		return "", false // ends the run as awaiting a human
	}

	// AwaitReply waits for the next injected message. The front end's
	// steering injects one per reply, so the reply becomes this tool's result.
	return s.AwaitReply(ctx)
}
