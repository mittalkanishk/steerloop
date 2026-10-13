package steerloop

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// PauseFunc handles a call to a tool registered in LoopConfig.Pauses. The loop
// waits for it to return. reply becomes the tool's result. Returning cont
// false ends the run: with DoneReasonAwaitingHuman if the function emitted
// EventAwaitingInput, otherwise with DoneReasonStopped.
type PauseFunc func(ctx context.Context, in PauseIn) (reply string, cont bool)

// PauseIn is what a pause function can see and do.
type PauseIn struct {
	// CallID, Name and Arguments identify the tool call being handled.
	CallID    string
	Name      string
	Arguments string
	// Messages is a copy of the conversation so far.
	Messages []*schema.Message
	// Tools are the tools the loop was created with.
	Tools []*schema.ToolInfo
	// Execute runs a tool through the loop's ToolExecutor.
	Execute func(ctx context.Context, name, args string) (string, error)
	// Call makes a model call with the loop's retry and timeout handling. A
	// non-empty tools list binds the model to those tools for this call.
	Call func(ctx context.Context, tools []*schema.ToolInfo, msgs []*schema.Message) (*schema.Message, error)
	// Emit sends an event on the loop's event channel.
	Emit func(event EventType, data any)
	// Block waits for the next Inject and returns its message. ok is false
	// when the loop was stopped or its context ended while waiting.
	Block func(ctx context.Context) (reply string, ok bool)
}

// runPause hands a tool call to its pause function. It returns false when the
// run has ended.
func (l *Loop) runPause(ctx context.Context, pause PauseFunc, tc schema.ToolCall) bool {
	l.emitEvent(EventToolCall, ToolCallData{
		ToolCallID: tc.ID,
		Name:       tc.Function.Name,
		Arguments:  tc.Function.Arguments,
	})

	waitStart := time.Now()
	var awaiting *AwaitingInputData

	in := PauseIn{
		CallID:    tc.ID,
		Name:      tc.Function.Name,
		Arguments: tc.Function.Arguments,
		Messages:  l.GetRunningMessages(),
		Tools:     l.tools,
		Execute: func(ctx context.Context, name, args string) (string, error) {
			return l.executeTool(ctx, name, args)
		},
		Call: func(ctx context.Context, tools []*schema.ToolInfo, prompt []*schema.Message) (*schema.Message, error) {
			m := model.ToolCallingChatModel(l.llm)
			if len(tools) > 0 {
				bound, err := l.llm.WithTools(tools)
				if err != nil {
					return nil, err
				}
				m = bound
			}
			return l.callModel(ctx, m, prompt)
		},
		Emit: func(event EventType, data any) {
			if event == EventAwaitingInput {
				switch ad := data.(type) {
				case AwaitingInputData:
					awaiting = &ad
				case *AwaitingInputData:
					awaiting = ad
				}
			}
			l.emitEvent(event, data)
		},
		Block: func(ctx context.Context) (string, bool) {
			return l.waitInject(ctx)
		},
	}

	reply, cont, err := l.callPause(ctx, pause, in)
	if err != nil {
		// A pause function that panics is treated like a failed tool.
		reply, cont = fmt.Sprintf("Error executing tool %q: %s", tc.Function.Name, err.Error()), true
	}

	if !cont {
		switch {
		case awaiting != nil:
			l.emitDoneAwaiting(awaiting)
		case reply == "":
			l.setState(StateStopped)
			l.emitDone(DoneReasonStopped, "")
		}
		return false
	}
	if reply == "" {
		reply = "(no response)"
	}
	l.emitEvent(EventToolResult, ToolResultData{
		ToolCallID: tc.ID,
		Name:       tc.Function.Name,
		Result:     reply,
		DurationMs: time.Since(waitStart).Milliseconds(),
	})
	l.appendRunningMessages(&schema.Message{
		Role:       schema.Tool,
		Content:    reply,
		ToolCallID: tc.ID,
		ToolName:   tc.Function.Name,
	})
	return true
}

func (l *Loop) callPause(ctx context.Context, pause PauseFunc, in PauseIn) (reply string, cont bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("pause function panicked: %v", r)
			l.emitEvent(EventError, ErrorData{
				Error: fmt.Sprintf("pause %q panicked: %v", in.Name, r),
				Fatal: false,
			})
		}
	}()
	reply, cont = pause(ctx, in)
	return reply, cont, nil
}

// waitInject blocks until an Inject arrives, the loop is stopped, or ctx ends.
func (l *Loop) waitInject(ctx context.Context) (string, bool) {
	for {
		select {
		case ctrl := <-l.controlCh:
			switch ctrl.Type {
			case ControlStop:
				l.finishStopped()
				return "", false
			case ControlInject:
				data, _ := ctrl.Data.(InjectData)
				return data.Message, true
			case ControlModifyConfig:
				if data, ok := ctrl.Data.(ModifyConfigData); ok {
					l.applyConfigModification(data)
				}
			}
		case <-l.stopCh:
			l.finishStopped()
			return "", false
		case <-ctx.Done():
			l.setState(StateStopped)
			l.emitDone(DoneReasonStopped, "")
			return "", false
		}
	}
}
