package reflection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type ModelCaller interface {
	// usage can be present inside the call itself.
	Call(ctx context.Context, model model.ToolCallingChatModel, msgs []*schema.Message, opts ...model.Option) (*schema.Message, error)
}

type Judge struct {
	caller ModelCaller
	model  model.ToolCallingChatModel
	prompt string
}

func NewJudge(caller ModelCaller, base model.ToolCallingChatModel, prompt string) (Judge, error) {

	model, err := base.WithTools([]*schema.ToolInfo{verdictInfo})
	if err != nil {
		return Judge{}, fmt.Errorf("finish review: %w", err)
	}

	return Judge{
		caller: caller,
		model:  model,
		prompt: prompt,
	}, nil
}

func (j Judge) Rule(ctx context.Context, brief string) (Verdict, error) {

	msgs := []*schema.Message{
		{Role: schema.System, Content: j.prompt},
		{Role: schema.User, Content: brief},
	}

	reply, err := j.caller.Call(ctx, j.model, msgs, model.WithToolChoice(schema.ToolChoiceForced, VerdictTool))
	if err != nil {
		return Verdict{}, err
	}

	for _, tc := range reply.ToolCalls {
		if tc.Function.Name != VerdictTool {
			continue
		}
		var v Verdict
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &v); err != nil {
			return Verdict{}, fmt.Errorf("read verdict: %w", err)
		}
		return v, nil
	}
	return Verdict{}, errors.New("judge returned no verdict")
}
