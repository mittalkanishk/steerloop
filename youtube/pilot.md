# How Claude Code Works · Episode 1: The permission prompt

Every command and output below was run from the repo today (`GOWORK=off`, repo root). Record in a real terminal, not through a pipe, or the prompt will not appear.

**The one thing this episode must land.** When Claude Code asks "Allow? y/n" and you press n, the model did not run anything and did not get told "no". It got a sentence, as a tool result, and carried on. The permission system is thirty lines, and the model is not in them.

**Running demo.** The CLI with a scripted model: no API key, no network, same output every take.

**On screen, in order:** Claude Code (30s) → our terminal → `confirm.json` → `tools.yaml` → `run.go` `confirmPause` → terminal → `main_test.go` → terminal.

---

## 0:00–0:45. You've seen this.

Screen recording of Claude Code (or Cursor's agent) in a real project. Ask it to "clean up the build artifacts". It proposes `rm -rf build/` and the prompt appears: `Allow Bash(rm -rf build/)? [y/n]`. Press n. It says something like "Understood, I'll leave build/ alone" and finishes.

Say, over it:

> "You have pressed n on this a hundred times. Here is a question you have probably never asked: what did the model just see? Did it see 'no'? Did it see an error? Did it see anything? Hold that. In ten minutes you will have read the exact code that answers it, and it is thirty lines."

## 0:45–1:45. What this is.

Face or slide. Say:

> "This series is called How Claude Code Works. Not Anthropic's source, I have not read it. The thing inside it, which is the same thing inside Codex, Cursor's agent and aider: a loop around a model. In the real tools that loop is buried under a hundred thousand lines of UI, providers and telemetry. Here it is two thousand lines of Go, in a repo called steerloop, link below. It does nothing Claude Code does not. It does it in a size you can read in a weekend.

> Four words, because every episode uses them. A **model** is the thing you send text to and get text back from. A black box. A **tool** is a function the model is allowed to ask for. A **loop**, or harness, calls the model, runs the tool it asked for, hands the result back, and calls the model again until it is done. An **agent** is a model plus a loop plus tools. That is all it is.

> If you can read a Go for loop and a struct, you can follow this. Each episode: a behaviour you already know from Claude Code, the same behaviour in ours, the test that states it, and the thirty lines behind it."

Slide for the four words if you have one; otherwise just say them.

## 1:45–2:45. Same thing, in ours.

Terminal. Say: "I am going to tell an agent to clean up. It will ask a tool to delete my build directory. Watch where it stops."

Type, slowly:

```sh
steerloop run --model script:cmd/steerloop/testdata/confirm.json \
  --tools cmd/steerloop/testdata/tools.yaml --prompt "clean up"
```

It prints and stops at the question:

```text
model script:cmd/steerloop/testdata/confirm.json · stop no-tool-calls · max 12 steps

[step 1] "thinking...": Reasoning (step 1/12)…
  → danger {"target":"build/"}
  ? Run danger {"target":"build/"}? [y / n]
```

Say: "Same prompt. Same wait. For me." Pause two seconds. Type `n`. It continues:

```text
  ← danger: The user declined to run this tool.

[step 2] "thinking...": Reasoning (step 2/12)…

done: finished · 2 step(s) · 0 tokens

Finished.
```

Point at the `←` line. Say: "That arrow is a tool result going back to the model. Read it. It is a sentence. Nothing else happened. Let's find out why."

Press Ctrl-D to leave the `>` prompt.

## 2:45–4:00. Here is the model.

Open `cmd/steerloop/testdata/confirm.json`. It is seven lines:

```json
{
  "steps":
  [
    {"tool_calls": [{"id": "call_1", "name": "danger", "arguments": {"target": "build/"}}]},
    {"content": "Finished."}
  ]
}
```

Say: "This is the entire model for that run. A script. First time it is called, it asks for a tool named danger with target build/. Second time, whatever it is told, it says Finished. There is no Claude here. There is no network. And the prompt still appeared, which tells you something: the prompt is not the model's idea."

Open `cmd/steerloop/testdata/tools.yaml`, scroll to `danger`:

```yaml
  - name: danger
    description: A tool that must be approved before it runs.
    parameters:
      type: object
      properties:
        target: { type: string }
      required: [target]
    command: ["echo", "removed {{.target}}"]
    confirm: true
```

Say: "One line. `confirm: true`. The tool is declared as data: a name, a schema the model sees, a command, and a flag that says ask first. Claude Code has the same flag; it is the allow-list in your settings file. The model asked for danger. Whether danger runs was never the model's call. That is the first thing to take away from this series: the model never runs anything. It emits text that looks like a request. Something else decides."

## 4:00–5:30. The thirty lines.

Open `cmd/steerloop/run.go` at `confirmPause`. Do not scroll anywhere else in the file. Read it top to bottom, slowly:

```go
func confirmPause(autoApprove, humanAvailable bool) steerloop.PauseFunc {
	run := func(ctx context.Context, in steerloop.PauseIn) (string, bool) {
		out, err := in.Execute(ctx, in.Name, in.Arguments)
		if err != nil {
			return fmt.Sprintf("Error executing tool %q: %s", in.Name, err.Error()), true
		}
		return out, true
	}
	return func(ctx context.Context, in steerloop.PauseIn) (string, bool) {
		if autoApprove {
			return run(ctx, in)
		}
		if !humanAvailable {
			return "This tool needs approval and nobody is available to give it. It was not run.", true
		}
		in.Emit(steerloop.EventAwaitingInput, steerloop.AwaitingInputData{
			Question: fmt.Sprintf("Run %s %s?", in.Name, oneLine(in.Arguments, maxResultPreview)),
			Options:  []string{"y", "n"},
		})
		reply, ok := in.Block(ctx)
		if !ok {
			return "", false
		}
		switch strings.ToLower(strings.TrimSpace(reply)) {
		case "y", "yes":
			return run(ctx, in)
		default:
			return "The user declined to run this tool.", true
		}
	}
}
```

Say, pointing as you go:

> "A pause function. The loop hands it a tool call instead of running it. It returns a string, and that string becomes the tool's result. That is the whole contract.

> Four branches. `autoApprove`: that is `--yes`, Claude Code calls it `--dangerously-skip-permissions`. Run it, return the output. No human available: no terminal, you are in CI or a pipe. Return a sentence, do not run it. Otherwise: emit a question, block until somebody types, and if they typed y, run it; anything else, return a sentence.

> Look at what every branch has in common. It returns a string. The tool's real output, or an English sentence. The model cannot tell the difference. It is not supposed to."

## 5:30–6:30. Prove the model got a sentence.

Terminal. Run the same command with `--verbose`, piped so there is no terminal:

```sh
echo | steerloop run --verbose --model script:cmd/steerloop/testdata/confirm.json \
  --tools cmd/steerloop/testdata/tools.yaml --prompt "clean up" 2>&1 | grep diff
```

Two lines come back. Point at the first:

```text
msg="agent loop diff" iteration=1 diff="1 assistant tool_calls: danger\n2 tool This tool needs approval and nobody is available to give it. It was not run."
```

Say: "That is the conversation the model gets on its second call. Message 1, the assistant asked for danger. Message 2, role `tool`, content: a sentence. Not an error. Not a 'no'. A sentence the model reads like any other tool output, and then it says Finished. In Claude Code, when you press n and it says 'understood', this is what it understood."

Then, fast, the third branch:

```sh
steerloop run --yes --model script:cmd/steerloop/testdata/confirm.json \
  --tools cmd/steerloop/testdata/tools.yaml --prompt "clean up"
```

```text
  → danger {"target":"build/"}
  ← danger: removed build/
```

Say: "Same model, same script, same tool. One flag. Three runs, three different strings went back, and the model never knew which world it was in. That is the second thing to take away: a tool result is just a string. A refusal, an error and a success look the same to the model."

Ctrl-D.

## 6:30–7:45. The behaviour file.

Say: "I did not write steerloop by hand, an agent did. So how do I know it does what I just told you? The same way you will know for every episode: the test file says it. I call it the behaviour file."

Open `cmd/steerloop/main_test.go` at `TestRun_ConfirmOnTerminal`. Do not explain the suite, do not open the helpers. Read the four cases aloud, pointing at each `s.Contains`:

> "Type y: stderr contains `removed build/`. Type anything else: `The user declined`, and `removed build/` is not there. Pass `--yes`: `removed build/`, no question asked. No terminal: `nobody is available`, and it was not run. Four sentences. That is the permission system, specified."

Run:

```sh
go test ./cmd/steerloop -run 'TestCLISuite/TestRun_ConfirmOnTerminal' -v
```

```text
--- PASS: TestCLISuite (0.03s)
    --- PASS: TestCLISuite/TestRun_ConfirmOnTerminal (0.02s)
PASS
```

Say: "Green. Every episode goes in this order. The behaviour you know. The test that states it. Then the code. If a test name and a sentence I say ever disagree, believe the test."

## 7:45–8:45. Where this goes.

Terminal or slide. Say, one breath per line:

> "Each episode is one thing you have already seen.
> The spinner: Thinking, reading file, running command, thinking. That is the loop itself, one file.
> Typing a correction while it works, and it picks it up next turn.
> Pressing Esc, and why that one signal can never be lost.
> Which tools it is allowed to touch, and why ours never sees a shell.
> When it asks you a question and waits, including the version where you come back tomorrow.
> The transcript file, and 'why did it do that?', with the tokens and the cost.
> /compact, and what the model actually sees from a long conversation.
> The retry after 'overloaded'.
> And how any of this is testable when the model is a paid network call. Spoiler: you saw the answer today. The model was a JSON file."

## 8:45–9:30. Build yours.

Say:

> "Homework, and it is small. Clone the repo. Run the three commands from today: type n, pipe it, pass --yes. Then run the --verbose one and find the sentence in the diff.

> Then, in any language, write one function. It takes a tool name and its arguments, and returns a string. If a human typed y, the string is the tool's output. Otherwise it is a sentence explaining why not. That function is Claude Code's permission system. Keep it. Every episode you will add one piece to it, and by the end you will have your own harness and this repo will be the answer key."

## 9:30–10:00. Close.

Terminal. Say: "One more time." Run the demo command, type `n`, let it finish.

Say: "The model asked. The loop decided. The model got a sentence and moved on. The model is replaceable. The loop is the product. Next episode: the spinner. What one step of an agent actually does, in one file."

---

## Notes for the recording

- `go install ./cmd/steerloop` once so the on-screen commands are short, or use `go run ./cmd/steerloop` to show it is source.
- Run from the repo root so the testdata paths resolve. Prefix `go` commands with `GOWORK=off` from this checkout, or record from a clean clone; the parent `Enterprise/go.work` does not list this module.
- The `? Run danger … [y / n]` prompt only appears when stdin is a real terminal. Through a pipe you get the "nobody is available" line, which is the 5:30 beat, not the 1:45 beat.
- The `>` after `Finished.` is the session waiting for the next prompt. Ctrl-D ends it. Do not type into it in this episode.
- `--verbose` writes slog debug lines to stderr; `grep diff` keeps only the transcript lines. If the timestamp prefix is noisy on camera, `cut -d' ' -f3-` after the grep.
- The Claude Code recording at 0:00 is the only part that needs a real API key and a real project. Record it once, separately, and never again.
- Not shown in this episode on purpose: `examples/minimal`, `examples/steer`, `--record`/`replay`, exit codes. They each have their own episode. Previewing them here cost two minutes in the earlier draft and landed nothing.
