package steerloop

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
)

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

// NewToolInfo builds an Eino tool definition from a name, a description and a
// JSON Schema object for the parameters. A nil schema means no parameters.
func NewToolInfo(name, desc string, params map[string]any) (*schema.ToolInfo, error) {
	ti := &schema.ToolInfo{Name: name, Desc: desc}
	if params == nil {
		return ti, nil
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("tool %q marshal params: %w", name, err)
	}
	js := &jsonschema.Schema{}
	if err := js.UnmarshalJSON(raw); err != nil {
		return nil, fmt.Errorf("tool %q parse params: %w", name, err)
	}
	ti.ParamsOneOf = schema.NewParamsOneOfByJSONSchema(js)
	return ti, nil
}
