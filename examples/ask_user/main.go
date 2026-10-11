// Ask_user shows a pause tool: when the model calls ask_user, the loop hands
// the call to a function that waits for a person, and the answer becomes the
// tool's result.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop"
	"github.com/mittalkanishk/steerloop/steerlooptest"
)

func main() {
	askUser, err := steerloop.NewToolInfo("ask_user", "Ask the user a question.", map[string]any{
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
	executor := steerloop.ToolExecutorFunc(func(context.Context, string, string) (string, error) {
		return "", nil
	})

	cfg := steerloop.DefaultConfig()
	cfg.Pauses = map[string]steerloop.PauseFunc{
		"ask_user": func(ctx context.Context, in steerloop.PauseIn) (string, bool) {
			var args struct {
				Question string `json:"question"`
			}
			_ = json.Unmarshal([]byte(in.Arguments), &args)
			in.Emit(steerloop.EventAwaitingInput, steerloop.AwaitingInputData{Question: args.Question})
			return in.Block(ctx) // returns the next Inject
		},
	}

	loop, err := steerloop.New(cfg, chatModel, []*schema.ToolInfo{askUser}, executor, nil)
	if err != nil {
		log.Fatal(err)
	}

	stdin := bufio.NewReader(os.Stdin)
	for evt := range loop.Start(context.Background(), []*schema.Message{{Role: schema.User, Content: "Write a report."}}) {
		switch data := evt.Data.(type) {
		case steerloop.AwaitingInputData:
			fmt.Printf("%s\n> ", data.Question)
			answer, _ := stdin.ReadString('\n')
			loop.Inject(answer)
		case steerloop.DoneData:
			fmt.Printf("%s: %s\n", data.Reason, data.FinalContent)
		}
	}
}
