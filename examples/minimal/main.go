// Minimal shows the smallest useful loop: a model, one tool, and the event
// stream. It uses a scripted model so it runs with no API key; swap in any
// Eino ToolCallingChatModel to talk to a real provider.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/Kanishkmittal55/steerloop"
	"github.com/Kanishkmittal55/steerloop/steerlooptest"
)

func main() {
	upper, err := steerloop.NewToolInfo("upper", "Upper-case a string.", map[string]any{
		"type":       "object",
		"properties": map[string]any{"text": map[string]any{"type": "string"}},
		"required":   []string{"text"},
	})
	if err != nil {
		log.Fatal(err)
	}

	chatModel := steerlooptest.NewScriptedModel(
		steerlooptest.Step{ToolCalls: []schema.ToolCall{steerlooptest.ToolCall("call_1", "upper", `{"text":"hello"}`)}},
		steerlooptest.Step{Content: "The tool returned HELLO."},
	)

	executor := steerloop.ToolExecutorFunc(func(_ context.Context, name, arguments string) (string, error) {
		var args struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return "", err // the model sees this as the tool's error
		}
		return strings.ToUpper(args.Text), nil
	})

	loop, err := steerloop.New(steerloop.DefaultConfig(), chatModel, []*schema.ToolInfo{upper}, executor, nil)
	if err != nil {
		log.Fatal(err)
	}

	messages := []*schema.Message{{Role: schema.User, Content: "Upper-case the word hello."}}
	for evt := range loop.Start(context.Background(), messages) {
		switch data := evt.Data.(type) {
		case steerloop.ToolCallData:
			fmt.Printf("tool call:   %s %s\n", data.Name, data.Arguments)
		case steerloop.ToolResultData:
			fmt.Printf("tool result: %s\n", data.Result)
		case steerloop.DoneData:
			fmt.Printf("%s: %s\n", data.Reason, data.FinalContent)
		}
	}
}
