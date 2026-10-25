package reflection

import (
	"context"

	"github.com/cloudwego/eino/schema"
)

// OnIteration judges every iteration against the task. The judge sees the
// task and only that iteration, written out as text, so it needs none of
// the run's tools and is not anchored on the whole history.
type OnIteration struct {
	Off
	judge Judge
}

func NewOnIteration(judge Judge) OnIteration {
	return OnIteration{judge: judge}
}

func (r OnIteration) AfterIteration(ctx context.Context, task, iteration []*schema.Message) (Verdict, error) {
	return r.judge.Rule(ctx, brief(task, "Latest iteration", describe(iteration)))
}
