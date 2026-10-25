package reflection

import (
	"context"

	"github.com/cloudwego/eino/schema"
)

type OnFinish struct {
	Off
	judge Judge
}

func NewOnFinish(judge Judge) OnFinish {
	return OnFinish{judge: judge}
}

func (r OnFinish) AtFinish(ctx context.Context, task []*schema.Message, answer string) (Verdict, error) {
	return r.judge.Rule(ctx, brief(task, "Answer", answer))
}
