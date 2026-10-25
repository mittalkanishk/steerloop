package reflection

import (
	"context"

	"github.com/cloudwego/eino/schema"
)

// Off never reviews and makes no model call. It returns the zero Verdict,
// which means no review ran.
type Off struct{}

func (Off) AfterIteration(context.Context, []*schema.Message, []*schema.Message) (Verdict, error) {
	return Verdict{}, nil
}

func (Off) AtFinish(context.Context, []*schema.Message, string) (Verdict, error) {
	return Verdict{}, nil
}
