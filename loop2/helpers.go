package loop2

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
)

// NewToolInfo builds an Eino tool definition from a name, a description and
// a JSON Schema object for the parameters. A nil schema means no parameters.
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

func positiveOr(v, fallback int) int {
	if v <= 0 {
		return fallback
	}
	return v
}

func durationOr(v, fallback time.Duration) time.Duration {
	if v <= 0 {
		return fallback
	}
	return v
}

func stringOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
