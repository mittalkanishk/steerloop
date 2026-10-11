// Package steerloop is a controllable agent loop for CloudWeGo Eino.
//
// A Loop calls a tool-calling chat model, executes the tools it asks for, feeds
// the results back and repeats until a stop condition is met. While it runs,
// the caller can:
//
//   - read a typed stream of events (model turns, tool calls, tool results);
//   - stop it, inject a user message, or change its configuration;
//   - hand named tool calls to a pause function, for example to ask a human.
//
// The loop also retries transient model failures, aborts a streamed turn that
// stops producing chunks, and emits an audit record of every model turn.
//
// A minimal run:
//
//	loop, err := steerloop.New(steerloop.DefaultConfig(), chatModel, tools, executor, nil)
//	if err != nil {
//		return err
//	}
//	for evt := range loop.Start(ctx, messages) {
//		if done, ok := evt.Data.(steerloop.DoneData); ok {
//			fmt.Println(done.Reason, done.FinalContent)
//		}
//	}
//
// The model is any Eino ToolCallingChatModel. Tools are described with Eino
// ToolInfo values and executed through the ToolExecutor the caller supplies.
package steerloop
