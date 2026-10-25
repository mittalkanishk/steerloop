package engine

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop/event"
	"github.com/mittalkanishk/steerloop/loop/internal/reflection"
)

// reviewIteration judges the iteration that just ran. The run goes on
// whatever the verdict: off track only queues a note for the next
// iteration, and a failed review is reported and skipped.
func (e *Engine) reviewIteration(ctx context.Context, t turn) {
	iteration := e.d.State.Messages()[t.start:]
	v, err := e.d.Reviewer.AfterIteration(ctx, e.task(), iteration)
	if err != nil {
		e.d.Emitter.Emit(event.ErrorData{Error: "iteration review failed: " + err.Error()})
		return
	}
	e.emitVerdict(event.ReflectOnIteration, v)
	if v.Failed() {
		e.note(reviewNote("Review of your last iteration:", v))
	}
}

// finish ends a run the model finished, unless the finish review finds the
// answer drifted from the task: then reopen records why and the run goes
// on, at most MaxReopens times. It returns whether the run goes on. A
// failed review is reported and the answer stands.
func (e *Engine) finish(ctx context.Context, answer string, reopen func(note string)) bool {
	if e.reopens < e.d.MaxReopens {
		v, err := e.d.Reviewer.AtFinish(ctx, e.task(), answer)
		if err != nil {
			e.d.Emitter.Emit(event.ErrorData{Error: "finish review failed: " + err.Error()})
		} else {
			e.emitVerdict(event.ReflectOnFinish, v)
			if v.Failed() {
				e.reopens++
				reopen(reviewNote("A review found your answer drifts from the task:", v))
				return true
			}
		}
	}
	e.d.Finish.Done(event.ReasonFinished, answer)
	return false
}

// emitVerdict reports a verdict. The zero Verdict means no review ran.
func (e *Engine) emitVerdict(mode string, v reflection.Verdict) {
	if v.Verdict == "" {
		return
	}
	e.d.Emitter.Emit(event.ReflectionData{
		Mode:       mode,
		Verdict:    v.Verdict,
		Reason:     v.Reason,
		Hint:       v.Hint,
		Confidence: v.Confidence,
	})
}

// reviewNote is the one message a failed verdict sends the model.
func reviewNote(title string, v reflection.Verdict) string {
	note := title + "\nIssue: " + v.Reason
	if v.Hint != "" {
		note += "\nHint: " + v.Hint
	}
	return note
}

// task is what the run was asked to do: the caller's system messages and
// the first user message. The stop instructions are loop mechanics, not
// the task.
func (e *Engine) task() []*schema.Message {
	var out []*schema.Message
	for _, m := range e.d.State.Messages() {
		switch {
		case m.Role == schema.System && m.Content != e.d.StopInstructions:
			out = append(out, m)
		case m.Role == schema.User:
			return append(out, m)
		}
	}
	return out
}

// note queues a user-role message for the next iteration. flushNotes sends
// them as one message, so a nudge and a review never arrive as two turns.
func (e *Engine) note(text string) {
	if text != "" {
		e.notes = append(e.notes, text)
	}
}

func (e *Engine) flushNotes() {
	if len(e.notes) == 0 {
		return
	}
	e.d.State.Append(&schema.Message{Role: schema.User, Content: strings.Join(e.notes, "\n\n")})
	e.notes = e.notes[:0]
}
