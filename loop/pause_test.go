package loop_test

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop"
	"github.com/mittalkanishk/steerloop/loop/event"
	"github.com/mittalkanishk/steerloop/loop/pause"
	"github.com/mittalkanishk/steerloop/steerlooptest"
)

// =============================================================================
// Pauses
// =============================================================================

func (s *LoopSuite) TestPause_TheReplyBecomesTheToolResult() {
	var seenCall pause.Call
	var seenMessages []*schema.Message
	loop, llm := s.pausingLoop(pauseFunc(func(_ context.Context, call pause.Call, ps pause.Session) (string, bool) {
		seenCall, seenMessages = call, ps.Messages()
		return "blue", true
	}))
	events := s.run(loop)

	s.Equal(event.ReasonFinished, s.done(events).Reason)
	s.Zero(s.exec.CallCount(), "a pause tool never reaches the executor")
	s.Equal("call_1", seenCall.ID)
	s.Equal(askUserTool, seenCall.Name)
	s.JSONEq(`{"question":"colour?"}`, seenCall.Arguments)
	s.Len(seenMessages, 4, "the seed messages, the stop instructions and the assistant tool call")
	s.Equal("blue", payload[event.ToolResultData](s, events).Result)

	result := lastMessage(llm)
	s.Equal(schema.Tool, result.Role)
	s.Equal("blue", result.Content)
	s.Equal("call_1", result.ToolCallID)
}

func (s *LoopSuite) TestPause_AnEmptyReplyIsFilledIn() {
	loop, llm := s.pausingLoop(pauseFunc(func(context.Context, pause.Call, pause.Session) (string, bool) {
		return "", true
	}))
	s.run(loop)
	s.Equal("(no response)", lastMessage(llm).Content)
}

func (s *LoopSuite) TestPause_AwaitReplyReturnsTheNextInject() {
	blocked := make(chan struct{})
	loop, llm := s.pausingLoop(pauseFunc(func(ctx context.Context, _ pause.Call, ps pause.Session) (string, bool) {
		ps.Emit(event.AwaitingInputData{Question: "colour?"})
		close(blocked)
		return ps.AwaitReply(ctx)
	}))
	events := loop.Start(context.Background(), seedConversation())
	<-blocked
	s.Require().True(loop.Inject("green"))

	all := s.collect(events)
	s.Equal(event.ReasonFinished, s.done(all).Reason)
	s.Equal("colour?", payload[event.AwaitingInputData](s, all).Question)
	s.Equal("green", lastMessage(llm).Content)
}

func (s *LoopSuite) TestPause_StopWhileBlockedEndsTheRunAtOnce() {
	l, llm, blocked := s.blockedPausingLoop()
	events := l.Start(context.Background(), seedConversation())
	<-blocked

	l.Stop()

	s.Equal(event.ReasonStopped, s.done(s.collect(events)).Reason)
	s.Equal(loop.StateStopped, l.State())
	s.Equal(1, llm.CallCount())
}

func (s *LoopSuite) TestPause_ContextCancellationWhileBlockedStopsTheRun() {
	l, _, blocked := s.blockedPausingLoop()
	ctx, cancel := context.WithCancel(context.Background())
	events := l.Start(ctx, seedConversation())
	<-blocked

	cancel()

	s.Equal(event.ReasonStopped, s.done(s.collect(events)).Reason)
	s.Equal(loop.StateStopped, l.State())
}

func (s *LoopSuite) TestPause_ConfigChangesApplyWhileBlocked() {
	l, llm, blocked := s.blockedPausingLoop()
	events := l.Start(context.Background(), seedConversation())
	<-blocked

	temperature := 0.9
	s.Require().True(l.ModifyConfig(loop.ConfigChange{Temperature: &temperature}))
	s.Require().True(l.Inject("ok"))

	s.collect(events)
	s.Require().NotNil(llm.LastOptions().Temperature)
	s.InDelta(0.9, *llm.LastOptions().Temperature, 0.001)
}

func (s *LoopSuite) TestPause_EndingWithoutAReplyHandsOverToAHuman() {
	type payloadForUI struct{ ID string }
	l, llm := s.pausingLoop(pauseFunc(func(_ context.Context, _ pause.Call, ps pause.Session) (string, bool) {
		ps.Emit(event.AwaitingInputData{
			Question: "Which colour?",
			Options:  []string{"red", "blue"},
			Payload:  payloadForUI{ID: "g-1"},
		})
		return "", false
	}))
	events := s.run(l)

	done := s.done(events)
	s.Equal(event.ReasonAwaitingHuman, done.Reason)
	s.Equal("Which colour?", done.FinalContent)
	s.Require().NotNil(done.Awaiting)
	s.Equal([]string{"red", "blue"}, done.Awaiting.Options)
	s.Equal(payloadForUI{ID: "g-1"}, done.Awaiting.Payload)
	s.Equal(loop.StateDone, l.State())
	s.Equal(1, llm.CallCount())
	s.Empty(payloads[event.ToolResultData](events), "no tool result is recorded for an unanswered pause")
}

func (s *LoopSuite) TestPause_AwaitingInputAcceptsAPointerPayload() {
	loop, _ := s.pausingLoop(pauseFunc(func(_ context.Context, _ pause.Call, ps pause.Session) (string, bool) {
		ps.Emit(&event.AwaitingInputData{Question: "Q?"})
		return "", false
	}))
	s.Equal(event.ReasonAwaitingHuman, s.done(s.run(loop)).Reason)
}

func (s *LoopSuite) TestPause_EndingWithoutAwaitingInputStopsTheRun() {
	for _, reply := range []string{"", "bye"} {
		s.Run(fmt.Sprintf("reply %q", reply), func() {
			loop, _ := s.pausingLoop(pauseFunc(func(context.Context, pause.Call, pause.Session) (string, bool) {
				return reply, false
			}))
			s.Equal(event.ReasonStopped, s.done(s.run(loop)).Reason)
		})
	}
}

func (s *LoopSuite) TestPause_CanRunToolsAndCallTheModel() {
	lookup, err := loop.NewToolInfo("lookup", "Look something up", nil)
	s.Require().NoError(err)

	var toolOut, modelOut string
	loop, llm := s.pausingLoop(pauseFunc(func(ctx context.Context, _ pause.Call, ps pause.Session) (string, bool) {
		toolOut, _ = ps.Execute(ctx, "lookup", `{"q":1}`)
		if reply, err := ps.CallModel(ctx, []*schema.ToolInfo{lookup}, ps.Messages()); err == nil && reply != nil {
			modelOut = reply.Content
		}
		return "done", true
	}))
	s.run(loop)

	s.Equal("result from lookup", toolOut)
	s.Equal([]string{"lookup"}, s.exec.CalledTools())
	s.Equal("thanks", modelOut, "CallModel plays the next scripted reply")
	s.Require().Len(llm.BoundTools(), 1)
	s.Equal("lookup", llm.BoundTools()[0][0].Name)
}

func (s *LoopSuite) TestPause_APanicBecomesAToolError() {
	loop, llm := s.pausingLoop(pauseFunc(func(context.Context, pause.Call, pause.Session) (string, bool) {
		panic("pause boom")
	}))
	events := s.run(loop)

	s.Equal(event.ReasonFinished, s.done(events).Reason)
	s.Contains(lastMessage(llm).Content, "pause boom")
	s.False(payload[event.ErrorData](s, events).Fatal)
}

func (s *LoopSuite) TestPause_OtherToolsStillReachTheExecutor() {
	s.cfg.Pauses = map[string]pause.Handler{
		askUserTool: pauseFunc(func(context.Context, pause.Call, pause.Session) (string, bool) { return "yes", true }),
	}
	events, _ := s.runScript(
		asksFor(
			profileCall(),
			steerlooptest.ToolCall("call_2", askUserTool, `{}`),
			steerlooptest.ToolCall("call_3", searchTool, `{"query":"x"}`),
		),
		says("ok"),
	)

	s.Equal([]string{profileTool, searchTool}, s.exec.CalledTools())
	s.Len(payloads[event.ToolResultData](events), 3)
}
