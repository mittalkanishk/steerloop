// Ask_user shows a pause tool: when the model calls ask_user, the loop hands
// the call to a pause.Handler that waits for a person, and the answer becomes
// the tool's result.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop"
	"github.com/mittalkanishk/steerloop/loop/event"
	"github.com/mittalkanishk/steerloop/loop/pause"
	"github.com/mittalkanishk/steerloop/steerlooptest"
)

// askUser handles ask_user calls in place of the ToolExecutor: it announces
// the question, then waits for the next injected message as the answer.
type askUser struct{}

func (askUser) Handle(ctx context.Context, call pause.Call, s pause.Session) (string, bool) {
	var args struct {
		Question string `json:"question"`
	}
	_ = json.Unmarshal([]byte(call.Arguments), &args)
	s.Emit(event.AwaitingInputData{Question: args.Question})
	return s.AwaitReply(ctx) // returns the next Inject
}

// noTools is the ToolExecutor for a loop whose only tool is a pause tool.
type noTools struct{}

func (noTools) ExecuteTool(context.Context, string, string) (string, error) { return "", nil }

func main() {
	askUserInfo, err := loop.NewToolInfo("ask_user", "Ask the user a question.", map[string]any{
		"type":       "object",
		"properties": map[string]any{"question": map[string]any{"type": "string"}},
		"required":   []string{"question"},
	})
	if err != nil {
		log.Fatal(err)
	}

	chatModel := steerlooptest.NewScriptedModel(
		steerlooptest.Step{ToolCalls: []schema.ToolCall{
			steerlooptest.ToolCall("call_1", "ask_user", `{"question":"What should the report be called?"}`),
		}},
		steerlooptest.Step{Content: "Thanks. I will use that title."},
	)

	cfg := loop.DefaultConfig()
	cfg.Pauses = map[string]pause.Handler{"ask_user": askUser{}}

	loop, err := loop.New(cfg, chatModel, []*schema.ToolInfo{askUserInfo}, noTools{}, nil)
	if err != nil {
		log.Fatal(err)
	}

	stdin := bufio.NewReader(os.Stdin)
	for evt := range loop.Start(context.Background(), []*schema.Message{{Role: schema.User, Content: "Write a report."}}) {
		switch data := evt.Data.(type) {
		case event.AwaitingInputData:
			fmt.Printf("%s\n> ", data.Question)
			answer, _ := stdin.ReadString('\n')
			loop.Inject(answer)
		case event.DoneData:
			fmt.Printf("%s: %s\n", data.Reason, data.FinalContent)
		}
	}
}
