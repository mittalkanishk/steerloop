package loop2_test

import (
	"context"

	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop2"
	"github.com/mittalkanishk/steerloop/loop2/event"
	"github.com/mittalkanishk/steerloop/steerlooptest"
)

// =============================================================================
// Stop policies
// =============================================================================

// stopInstructionsSeen is the system message the loop placed after the
// caller's own on the model's first call.
func stopInstructionsSeen(llm *steerlooptest.ScriptedModel) *schema.Message {
	return llm.Calls()[0][1]
}

func (s *LoopSuite) TestStopPolicy_EachConditionSeedsItsInstructions() {
	for condition, phrase := range map[loop2.StopCondition]string{
		loop2.StopOnNoToolFinish: "A reply without a tool call ends the run",
		loop2.StopOnFinishTool:   "The run ends only when you call the finish tool",
		loop2.StopOnExternal:     "This run does not end when you finish a task",
	} {
		s.cfg.StopCondition = condition
		s.cfg.MaxIterations = 1
		_, llm := s.runScript(says("hi"))

		first := llm.Calls()[0]
		s.Require().Len(first, 3, "seed system, stop instructions, user")
		s.Equal("You are a helpful assistant.", first[0].Content)
		s.Equal(schema.System, stopInstructionsSeen(llm).Role)
		s.Contains(stopInstructionsSeen(llm).Content, phrase)
		s.Equal(schema.User, first[2].Role)
	}
}

func (s *LoopSuite) TestStopPolicy_CustomInstructionsReplaceTheBuiltIn() {
	s.cfg.StopInstructions = "Stop when the user says so."
	_, llm := s.runScript(says("hi"))
	s.Equal("Stop when the user says so.", stopInstructionsSeen(llm).Content)
}

// Providers such as Anthropic accept system messages only before the first
// user message, so the instructions go after every system message.
func (s *LoopSuite) TestStopPolicy_InstructionsGoAfterEverySystemMessage() {
	llm := steerlooptest.NewScriptedModel(says("hi"))
	loop := s.newLoop(llm)
	s.collect(loop.Start(context.Background(), []*schema.Message{
		{Role: schema.System, Content: "one"},
		{Role: schema.System, Content: "two"},
		{Role: schema.User, Content: "Hello"},
	}))

	first := llm.Calls()[0]
	s.Require().Len(first, 4)
	s.Equal([]schema.RoleType{schema.System, schema.System, schema.System, schema.User},
		[]schema.RoleType{first[0].Role, first[1].Role, first[2].Role, first[3].Role})
	s.Equal("one", first[0].Content)
	s.Equal("two", first[1].Content)
	s.Contains(first[2].Content, "A reply without a tool call ends the run")
}

// A conversation carried over from an earlier run already holds the
// instructions, so a resumed run does not add them again.
func (s *LoopSuite) TestStopPolicy_AResumedConversationIsNotSeededTwice() {
	first := s.newLoop(steerlooptest.NewScriptedModel(says("hi")))
	s.run(first)
	history := append(first.Messages(), &schema.Message{Role: schema.User, Content: "And again"})

	llm := steerlooptest.NewScriptedModel(says("again"))
	s.collect(s.newLoop(llm).Start(context.Background(), history))

	seeded := 0
	for _, m := range llm.Calls()[0] {
		if m.Role == schema.System && m.Content == stopInstructionsSeen(llm).Content {
			seeded++
		}
	}
	s.Equal(1, seeded)
}

func (s *LoopSuite) TestStopPolicy_NoToolFinishEndsOnTheFirstTextReply() {
	events, llm := s.runScript(asksFor(profileCall()), says("the answer"), says("never asked"))

	done := s.done(events)
	s.Equal(event.ReasonFinished, done.Reason)
	s.Equal("the answer", done.FinalContent)
	s.Equal(2, done.Iterations)
	s.Equal(2, llm.CallCount())
}

func (s *LoopSuite) TestStopPolicy_FinishToolNeverReachesTheExecutor() {
	s.cfg.StopCondition = loop2.StopOnFinishTool
	events, _ := s.runScript(asksFor(finishCall(`{"result":"done"}`)))

	s.Equal(`{"result":"done"}`, s.done(events).FinalContent)
	s.Equal(loop2.FinishToolName, payload[event.ToolCallData](s, events).Name, "the finish call is still reported")
	s.Zero(s.exec.CallCount(), "the loop recognises finish by name; it is not a tool to run")
}

func (s *LoopSuite) TestStopPolicy_ExternalFinishIgnoresTextAndFinishCalls() {
	s.cfg.StopCondition = loop2.StopOnExternal
	s.cfg.MaxIterations = 3
	events, _ := s.runScript(says("status: waiting"), asksFor(finishCall(`{"result":"x"}`)), says("still waiting"))

	done := s.done(events)
	s.Equal(event.ReasonMaxIterations, done.Reason, "only a limit or a stop ends the run")
	s.Equal(3, done.Iterations)
	s.Equal("still waiting", done.FinalContent, "a limit-ended run returns its last reply")
	s.Equal([]string{loop2.FinishToolName}, s.exec.CalledTools(), "finish is an ordinary tool here")
}

func (s *LoopSuite) TestStopPolicy_ExternalFinishEndsOnStop() {
	s.cfg.StopCondition = loop2.StopOnExternal
	llm, release := heldModel("working on it")
	loop := s.newLoop(llm)
	events := loop.Start(context.Background(), seedConversation())

	s.waitForModelCall(llm)
	loop.Stop()
	release()

	s.Equal(event.ReasonStopped, s.done(s.collect(events)).Reason)
}

func (s *LoopSuite) TestStopPolicy_TheAuditRecordNamesTheCondition() {
	for condition, label := range map[loop2.StopCondition]string{
		loop2.StopOnNoToolFinish: "no_tool_finish",
		loop2.StopOnFinishTool:   "finish_tool",
		loop2.StopOnExternal:     "external_finish",
	} {
		s.cfg.StopCondition = condition
		s.cfg.MaxIterations = 1
		events, _ := s.runScript(asksFor(profileCall()))
		s.Equal(label, payload[event.LLMTurnData](s, events).StopCondition)
	}
}
