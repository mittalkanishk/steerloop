package dispatch

import (
	"context"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop/event"
)

// session implements pause.Session for one handler call. Each method is
// what the old PauseIn closures did, now with a name.
type session struct {
	x        *Dispatcher
	awaiting *event.AwaitingInputData // set when the handler emits AwaitingInput
}

func (s *session) Messages() []*schema.Message { return s.x.d.History.Messages() }

func (s *session) Tools() []*schema.ToolInfo { return s.x.d.Tools }

func (s *session) Execute(ctx context.Context, name, args string) (string, error) {
	return s.x.execute(ctx, name, args)
}

func (s *session) CallModel(ctx context.Context, tools []*schema.ToolInfo, msgs []*schema.Message) (*schema.Message, error) {
	m := model.ToolCallingChatModel(s.x.d.Model)
	if len(tools) > 0 {
		bound, err := s.x.d.Model.WithTools(tools)
		if err != nil {
			return nil, err
		}
		m = bound
	}
	return s.x.d.Caller.Call(ctx, m, msgs)
}

func (s *session) Emit(data event.Data) {
	switch d := data.(type) {
	case event.AwaitingInputData:
		s.awaiting = &d
	case *event.AwaitingInputData:
		s.awaiting = d
	}
	s.x.d.Emitter.Emit(data)
}

// AwaitReply waits for the next injected message. If the loop is stopped
// or ctx ends first, the run ends as stopped.
func (s *session) AwaitReply(ctx context.Context) (string, bool) {
	reply, ok := s.x.d.Replies.WaitInject(ctx)
	if !ok {
		s.x.d.Ender.EndStopped()
	}
	return reply, ok
}
