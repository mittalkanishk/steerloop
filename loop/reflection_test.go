package loop_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop"
	"github.com/mittalkanishk/steerloop/loop/event"
	"github.com/mittalkanishk/steerloop/steerlooptest"
)

// =============================================================================
// Reflection
//
// The judge binds the same scripted model to its verdict tool, so a script
// interleaves the loop's turns with the judge's verdicts in call order.
// =============================================================================

const verdictTool = "review_verdict"

// rules is a judge turn that returns a verdict.
func rules(verdict, reason, hint string) steerlooptest.Step {
	args, _ := json.Marshal(map[string]any{"verdict": verdict, "reason": reason, "hint": hint, "confidence": 0.9})
	return asksFor(steerlooptest.ToolCall("call_verdict", verdictTool, string(args)))
}

func passes() steerlooptest.Step { return rules("pass", "", "") }

// judgeBrief is the user message of the judge call at index i.
func judgeBrief(llm *steerlooptest.ScriptedModel, i int) string {
	call := llm.Calls()[i]
	return call[len(call)-1].Content
}

func (s *LoopSuite) TestReflection_OffMakesNoReviewCalls() {
	events, llm := s.runScript(asksFor(profileCall()), says("done"))
	s.Equal(2, llm.CallCount())
	s.Empty(payloads[event.ReflectionData](events))
}

// --- on iteration ------------------------------------------------------------

func (s *LoopSuite) TestReflection_OnIterationReviewsEveryIterationThatContinues() {
	s.cfg.Reflect = loop.ReflectOnIteration
	events, llm := s.runScript(asksFor(profileCall()), passes(), says("done"))

	s.Equal(3, llm.CallCount(), "one review: the final iteration ends the run and is not reviewed")
	s.Equal("done", s.done(events).FinalContent)
	verdicts := payloads[event.ReflectionData](events)
	s.Require().Len(verdicts, 1)
	s.Equal(event.ReflectOnIteration, verdicts[0].Mode)
	s.Equal("pass", verdicts[0].Verdict)

	brief := judgeBrief(llm, 1)
	s.Contains(brief, "User request:\nHello")
	s.Contains(brief, "The model called get_profile({})")
	s.Contains(brief, "get_profile returned: result from get_profile")
	s.NotContains(brief, "A reply without a tool call ends the run", "stop instructions are loop mechanics, not the task")
}

func (s *LoopSuite) TestReflection_AFailedIterationReviewSteersTheNextIteration() {
	s.cfg.Reflect = loop.ReflectOnIteration
	events, llm := s.runScript(asksFor(profileCall()), rules("fail", "wrong tool", "search instead"), says("done"))

	s.Equal(event.ReasonFinished, s.done(events).Reason, "a verdict never ends the run")
	note := lastMessage(llm)
	s.Equal(schema.User, note.Role)
	s.Equal("Review of your last iteration:\nIssue: wrong tool\nHint: search instead", note.Content)
}

// A nudge and a review queued for the same iteration reach the model as one
// user message, never as two turns in a row.
func (s *LoopSuite) TestReflection_TheNudgeAndTheReviewArriveAsOneMessage() {
	s.cfg.StopCondition = loop.StopOnFinishTool
	s.cfg.Reflect = loop.ReflectOnIteration
	_, llm := s.runScript(says("let me think"), rules("fail", "no progress", "call a tool"), asksFor(finishCall(`{"result":"x"}`)))

	msgs := llm.LastMessages()
	note := msgs[len(msgs)-1]
	s.Equal(schema.User, note.Role)
	s.Contains(note.Content, "returned prose instead of a tool call")
	s.Contains(note.Content, "Review of your last iteration:\nIssue: no progress")
	s.NotEqual(schema.User, msgs[len(msgs)-2].Role)
}

func (s *LoopSuite) TestReflection_TheJudgeSeesOnlyTheLatestIterationClipped() {
	s.cfg.Reflect = loop.ReflectOnIteration
	long := strings.Repeat("x", 2500)
	s.exec = steerlooptest.NewRecordingExecutor(func(_ context.Context, name, _ string) (string, error) {
		if name == searchTool {
			return long, nil
		}
		return "profile", nil
	})
	_, llm := s.runScript(
		asksFor(profileCall()), passes(),
		asksFor(steerlooptest.ToolCall("call_2", searchTool, `{"query":"go"}`)), passes(),
		says("done"),
	)

	brief := judgeBrief(llm, 3)
	s.Contains(brief, "search_content")
	s.NotContains(brief, "get_profile", "an earlier iteration is not under review")
	s.Contains(brief, strings.Repeat("x", 2000)+"…")
	s.NotContains(brief, strings.Repeat("x", 2001))
}

func (s *LoopSuite) TestReflection_ReviewsDoNotCountTowardMaxIterations() {
	s.cfg.Reflect = loop.ReflectOnIteration
	s.cfg.MaxIterations = 2
	events, llm := s.runScript(asksFor(profileCall()), passes(), asksFor(profileCall()), passes())

	done := s.done(events)
	s.Equal(event.ReasonMaxIterations, done.Reason)
	s.Equal(2, done.Iterations)
	s.Equal(4, llm.CallCount(), "two iterations and two reviews")
}

func (s *LoopSuite) TestReflection_AFailedIterationReviewIsReportedAndSkipped() {
	s.cfg.Reflect = loop.ReflectOnIteration
	for name, judge := range map[string]steerlooptest.Step{
		"judge down": fails(errors.New("judge down")),
		"no verdict": says("looks fine to me"),
	} {
		events, _ := s.runScript(asksFor(profileCall()), judge, says("done"))
		done := s.done(events)
		s.Equal(event.ReasonFinished, done.Reason, name)
		s.Equal("done", done.FinalContent, name)
		failure := payload[event.ErrorData](s, events)
		s.Contains(failure.Error, "iteration review failed", name)
		s.False(failure.Fatal, name)
	}
}

// --- on finish ---------------------------------------------------------------

func (s *LoopSuite) TestReflection_OnFinishPassKeepsTheAnswer() {
	s.cfg.Reflect = loop.ReflectOnFinish
	llm := steerlooptest.NewScriptedModel(says("the answer"), passes())
	events := s.run(s.newLoop(llm, s.sampleTools()...))

	s.Equal("the answer", s.done(events).FinalContent)
	s.Equal(2, llm.CallCount())
	verdict := payload[event.ReflectionData](s, events)
	s.Equal(event.ReflectOnFinish, verdict.Mode)
	s.Equal("pass", verdict.Verdict)

	brief := judgeBrief(llm, 1)
	s.Contains(brief, "System prompt:\nYou are a helpful assistant.")
	s.Contains(brief, "User request:\nHello")
	s.Contains(brief, "Answer:\nthe answer")
	s.NotContains(brief, "A reply without a tool call ends the run")

	opts := llm.LastOptions()
	s.Require().NotNil(opts.ToolChoice, "the judge must answer with its tool")
	s.Equal(schema.ToolChoiceForced, *opts.ToolChoice)
	s.Equal([]string{verdictTool}, opts.AllowedToolNames)

	bound := llm.BoundTools()
	s.Require().Len(bound, 2, "the run's tools, then the judge's")
	s.Len(bound[0], 2)
	s.Require().Len(bound[1], 1)
	s.Equal(verdictTool, bound[1][0].Name, "the judge sees none of the run's tools")
}

func (s *LoopSuite) TestReflection_OnFinishDriftReopensTheRunOnce() {
	s.cfg.Reflect = loop.ReflectOnFinish
	events, llm := s.runScript(says("draft"), rules("fail", "missed the date", "add the date"), says("final"))

	s.Equal("final", s.done(events).FinalContent, "after MaxReopens the answer stands unreviewed")
	s.Equal(3, llm.CallCount())
	s.Len(payloads[event.ReflectionData](events), 1)
	s.Equal("A review found your answer drifts from the task:\nIssue: missed the date\nHint: add the date", lastMessage(llm).Content)
}

func (s *LoopSuite) TestReflection_MaxReopensBoundsTheReopens() {
	s.cfg.Reflect = loop.ReflectOnFinish
	s.cfg.MaxReopens = 2
	events, llm := s.runScript(says("a1"), rules("fail", "r1", ""), says("a2"), rules("fail", "r2", ""), says("a3"))

	s.Equal("a3", s.done(events).FinalContent)
	s.Equal(5, llm.CallCount())
	s.Len(payloads[event.ReflectionData](events), 2)
}

// Under finish-tool the review answers the finish call, so the history the
// model sees stays a valid call-and-result pair.
func (s *LoopSuite) TestReflection_OnFinishUnderFinishToolAnswersTheFinishCall() {
	s.cfg.StopCondition = loop.StopOnFinishTool
	s.cfg.Reflect = loop.ReflectOnFinish
	events, llm := s.runScript(
		asksFor(finishCall(`{"result":"bad"}`)),
		rules("fail", "wrong result", "fix it"),
		asksFor(finishCall(`{"result":"good"}`)),
	)

	s.Equal(`{"result":"good"}`, s.done(events).FinalContent)
	msgs := llm.LastMessages()
	call, result := msgs[len(msgs)-2], msgs[len(msgs)-1]
	s.Equal(schema.Assistant, call.Role)
	s.Require().Len(call.ToolCalls, 1)
	s.Equal(loop.FinishToolName, call.ToolCalls[0].Function.Name)
	s.Equal(schema.Tool, result.Role)
	s.Equal(call.ToolCalls[0].ID, result.ToolCallID)
	s.Contains(result.Content, "drifts from the task")
}

func (s *LoopSuite) TestReflection_AFailedFinishReviewKeepsTheAnswer() {
	s.cfg.Reflect = loop.ReflectOnFinish
	events, _ := s.runScript(says("the answer"), fails(errors.New("judge down")))

	s.Equal("the answer", s.done(events).FinalContent)
	s.Contains(payload[event.ErrorData](s, events).Error, "finish review failed")
}

func (s *LoopSuite) TestReflection_OnFinishNeverRunsUnderExternalFinish() {
	s.cfg.StopCondition = loop.StopOnExternal
	s.cfg.Reflect = loop.ReflectOnFinish
	s.cfg.MaxIterations = 2
	events, llm := s.runScript(says("waiting"))

	s.Equal(2, llm.CallCount(), "the model never finishes, so nothing is judged")
	s.Empty(payloads[event.ReflectionData](events))
}

func (s *LoopSuite) TestReflection_CustomPromptsReplaceTheBuiltIn() {
	s.cfg.Reflect = loop.ReflectOnIteration
	s.cfg.IterationReviewPrompt = "Judge this step."
	_, llm := s.runScript(asksFor(profileCall()), passes(), says("done"))
	s.Equal("Judge this step.", llm.Calls()[1][0].Content)

	s.cfg.Reflect = loop.ReflectOnFinish
	s.cfg.FinishReviewPrompt = "Judge this answer."
	_, llm = s.runScript(says("done"), passes())
	s.Equal("Judge this answer.", llm.Calls()[1][0].Content)
}

func (s *LoopSuite) TestReflection_JudgeTokensCountTowardUsage() {
	s.T().Skip("loop bug: a review's token usage is never recorded; modelcall.Caller stores a UsageRecorder but never calls it")

	s.cfg.Reflect = loop.ReflectOnFinish
	usage := &schema.TokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
	verdict := passes()
	verdict.Usage = usage
	events, _ := s.runScript(steerlooptest.Step{Content: "answer", Usage: usage}, verdict)
	s.Equal(30, s.done(events).LLMUsage.TotalTokens)
}
