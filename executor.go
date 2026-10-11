package steerloop

import "context"

// ToolExecutor runs the tools the model asks for. The loop calls ExecuteTool
// once per tool call and feeds the result back to the model. An error is not
// fatal: the model receives it as the tool's result and can react.
type ToolExecutor interface {
	// ExecuteTool runs the named tool with its JSON arguments and returns the
	// text result.
	ExecuteTool(ctx context.Context, name string, arguments string) (string, error)
}

// ToolExecutorFunc adapts a function to ToolExecutor.
type ToolExecutorFunc func(ctx context.Context, name string, arguments string) (string, error)

// ExecuteTool calls f.
func (f ToolExecutorFunc) ExecuteTool(ctx context.Context, name string, arguments string) (string, error) {
	return f(ctx, name, arguments)
}

// FinishToolName is the reserved tool name that ends a StopOnFinishTool run.
// Its arguments become DoneData.FinalContent. It never reaches the
// ToolExecutor.
const FinishToolName = "finish"
