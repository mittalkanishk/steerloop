I have verified every command and output the script will show. Writing it now.

Here is the pilot. Every command on screen was run from the repo today, and the output quoted is what it actually prints. Record in a real terminal, not a pipe, so the prompts appear.

# Pilot: "Claude Code Clone"

**Premise for the whole series.** One cannot do a lot to a loop once it starts. You press enter and wait 
and pray for the results. This series is about building: a loop you can steer, watch, pause for a human, and replay, in about two thousand lines of Go. Episode one asks one question: what happens when an agent is about to do something you do not want?

**Running demo for this episode.** The CLI with a scripted model, so there is no API key, no network, and the result is the same every take.

## 0:00–0:50. Cold open. No intro, no face. Terminal only.

Type, slowly, and read it aloud as you type:

```sh
steerloop run --model script:cmd/steerloop/testdata/confirm.json \ 
  --tools cmd/steerloop/testdata/tools.yaml "clean up"
```

The trace appears and stops at a question:

```text
[step 1]
  → danger {"target":"build/"}
  ? Run danger {"target":"build/"}? [y / n]
```

Say: "An agent just asked a tool to remove my build directory. It is waiting. For me." Pause two full seconds. Type n. The trace continues:

```text
  ← danger: The user declined to run this tool.
done: finished · 2 step(s)
```

Say: "The model never knew the command did not run. It got a sentence back, and it carried on. Hold that thought. That is the whole series."

## 0:50–2:00. The question.

Cut to you, or stay on the terminal, your call. Say:

> "Here is a thing nobody tells you when you start building agents. The model is the easy part. You call it, it answers, sometimes it asks for a tool. The hard part is the loop around it. Who runs the tool? What if the tool is dangerous? What if the model is wrong at step three of twelve and you can see it, and all you can do is watch? What if it finishes and you want to know exactly what it saw at every turn, with the tokens and the cost? Most agent code has no answer. The loop is a for loop and a prayer."

> "This series is one repo, called steerloop. It is a harness. A harness is the loop around the model. This one you can steer while it runs, watch as a stream of events, pause for a person, and replay from a file. We are going to build it up from a for loop, and by the end you will be able to read every line of it."

## 2:00–3:30. Show the loop in the smallest form that exists.

Open examples/minimal/main.go. Scroll once, slowly, then stop on the range loop:

```go
for evt := range loop.Start(context.Background(), messages) {
    switch data := evt.Data.(type) {
    case steerloop.ToolCallData:
        fmt.Printf("tool call:   %s %s\n", data.Name, data.Arguments)
    case steerloop.ToolResultData:
        fmt.Printf("tool result: %s\n", data.Result)
    case steerloop.DoneData:
        fmt.Printf("%s: %s\n", data.Reason, data.FinalContent)
    }
}
```

Say: "This is the only thing you need to know today. You give the loop a model, some tools and a function that runs them. You start it. It hands you events. Tool call. Tool result. Done. That is an agent. Everything else in this series is what we add to this loop so that the thing from the cold open can happen."

Run `go run ./examples/minimal` and leave the three lines on screen.

## 3:30–5:30. Steer it while it is running.

Say: "Now the part that made me write this. Watch what the loop does when I interrupt it."

Run `go run ./examples/steer`. The output arrives over about a second:

```text
step 1  Still researching…
step 1  control: User instruction received
step 1  control: Configuration updated
step 2  Still researching…
step 3  Still researching…
step 4  Still researching…
done: stopped after 4 steps
```

Say: "Three things happened while that loop was running, from a different goroutine. I injected a message, which the model sees on its very next call. I raised the iteration cap from three to fifty, which is why it did not stop at three. And then I stopped it. Not killed. Stopped. It finished the step it was on and ended cleanly with a reason."

Open examples/steer/main.go and point at the three lines: `loop.Inject(...)`, `loop.SendControlSignal(...)`, `loop.Stop()`. Say: "Three calls. That is the entire steering API. Episode three is how they get into a loop that is busy talking to a model without a race."

## 5:30–7:00. Watch it. Then prove it.

Say: "Everything the loop does is an event. So if you write the events to a file, you have a recording. And a recording you can play back."

Run:

```sh
steerloop run --model script:cmd/steerloop/testdata/tool_then_answer.json \
  --tools cmd/steerloop/testdata/tools.yaml --record run.jsonl "hi"
```

When `The tool printed: hello from the tool` appears, press Ctrl-D so the `>` prompt does not take over. Then:

```sh
steerloop replay run.jsonl
```

Say: "Same trace. No model was called. No tool ran. The file is the audit." Open run.jsonl and scroll to the `llm_turn` line. Say: "One line per model turn: the full request the model saw, its reply, input tokens, output tokens, cost. When someone asks you why the agent did that, this line is the answer."

## 7:00–8:30. The thing from the cold open, explained.

Say: "Back to the start. How did the loop stop at a question?"

Open cmd/steerloop/testdata/tools.yaml and show the danger tool with `confirm: true`. Then show the README's pause example:

```go
cfg.Pauses = map[string]steerloop.PauseFunc{
    "ask_user": func(ctx context.Context, in steerloop.PauseIn) (string, bool) {
        in.Emit(steerloop.EventAwaitingInput, steerloop.AwaitingInputData{Question: "Which file?"})
        return in.Block(ctx) // returns the next loop.Inject
    },
}
```

Say: "A pause is a tool call the loop hands to a function instead of running. That function can ask a person and block. Whatever the person types becomes the tool's result. And if nobody is there to answer, the function returns false, and the run ends with a reason called awaiting human, so a server can pick it up later. Here is that case."

Run the cold-open command again, piped so there is no terminal:

```sh
echo | steerloop run --model script:cmd/steerloop/testdata/confirm.json \
  --tools cmd/steerloop/testdata/tools.yaml "clean up"
```

Point at:

```text
  ← danger: This tool needs approval and nobody is available to give it. It was not run.
```

Say: "No human, no run. The model is told why. It does not get to decide."

## 8:30–9:30. The map of the series.

Say, over a slide or just the terminal:

> "Here is where we are going.
> Episode two: the loop itself. We open loop.go and follow one event from Start to the print.
> Episode three: steering. How Inject, Stop and a config change reach a loop that is mid-call, and the one control that is never allowed to be lost.
> Episode four: tools from a YAML file, and why the command is an argv list that never sees a shell.
> Episode five: pausing for a person, and what a run looks like when the person comes back tomorrow.
> Episode six: the recording. Events, dropped events, and the replay command.
> Episode seven: testing an agent with no API key, with a scripted model that can be slow, chunked, or broken on purpose."

## 9:30–10:00. Close.

Back on the terminal. Say: "One more time." Run the cold-open command, type n at the prompt, let it finish. Say: "The model is replaceable. The loop is the product. See you in episode two."

---

## Notes for the recording

- Build the binary once with `go install ./cmd/steerloop` so the on-screen commands are short, or keep `go run ./cmd/steerloop` if you prefer to show it is source.
- Run from the repo root so the testdata paths resolve.
- The cold-open prompt only appears when stdin is a real terminal. If you record through a wrapper that pipes stdin, you will get the "nobody is available" line instead, which is the 7:00 beat, not the opener.
- The `>` after a finished answer is the session waiting for your next prompt. Ctrl-D ends it. Mention it or cut it, but do not type into it in this episode.
