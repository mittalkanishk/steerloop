package exectool

import (
	"context"
	"fmt"
)

// Unknown is the executor when no manifest is given: every call fails.
type Unknown struct{}

func (Unknown) ExecuteTool(_ context.Context, name, _ string) (string, error) {
	return "", fmt.Errorf("unknown tool %q", name)
}
