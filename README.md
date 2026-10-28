# steerloop

**The loop inside Claude Code, in 3,000 lines of Go you can read in a weekend.**

Every agentic CLI (Claude Code, Codex, Cursor's agent, aider) is the same thing: a loop that calls a model, runs the tool it asked for, feeds the result back and repeats. In the real tools that loop is buried under a hundred thousand lines of UI and telemetry. steerloop is the loop on its own: the permission prompt, Ctrl-C, typing while it works, the transcript file, the retry after a 529, each one a test you can read and thirty lines you can change. The model is an interface, so a JSON file can play it: every behaviour runs offline, in seconds, for free.

![A real Claude run in steerloop: the model searches the code, the permission gate stops make_dir, a message typed mid-run steers the next call, and the recorded run is replayed with no model](docs/demo/steer.gif)

A real run with Claude Sonnet 5.5: the model searches the code, the harness asks before `make_dir` and the answer is no, a line typed while it works reaches the model's next call, and the recorded run is replayed without the model. To try steerloop with no API key, use a scripted model: `--model script:docs/demo/script.json`.

It is the specimen for the video series *How Claude Code Works*. It is also a library: a controllable agent harness built on [CloudWeGo Eino](https://github.com/cloudwego/eino), with control over a run that is already in flight:

- **Steer it.** Stop it, inject a user message, or change its iteration cap and temperature while it runs.
- **Watch it.** A typed event stream: model turns, tool calls, tool results, the final outcome.
- **Decide how it ends.** End on a text reply, on a `finish` tool call, or only when told to.
- **Judge it.** Optionally have a second model call review each iteration, or the final answer, and send the run back when it drifted.
- **Pause it for a person.** Hand named tool calls to a handler that can wait for a human answer.
- **Survive a flaky provider.** Retries on transient failures, and a watchdog that aborts a streamed turn that stops producing chunks.
- **Audit it.** One record per model turn with the request, the reply, token counts and cost.

It also ships a CLI, `steerloop`, that runs a loop from the terminal over command-line tools you declare in a YAML file, records every run, and replays a recording without a model.

Status: v0. The API may change before v1.

## Install

```sh
go get github.com/mittalkanishk/steerloop/loop                         # library
go install github.com/mittalkanishk/steerloop/cmd/steerloop@latest     # CLI
```

Requires Go 1.26 or later.

## Library

```go
import (
	"github.com/mittalkanishk/steerloop/loop"
	"github.com/mittalkanishk/steerloop/loop/event"
)

l, err := loop.New(loop.DefaultConfig(), chatModel, tools, executor, nil)
if err != nil {
	return err
}

messages := []*schema.Message{{Role: schema.User, Content: "Upper-case the word hello."}}
for evt := range l.Start(ctx, messages) {
	switch data := evt.Data.(type) {
	case event.ToolCallData:
		fmt.Println("tool call:", data.Name, data.Arguments)
	case event.ToolResultData:
		fmt.Println("tool result:", data.Result)
	case event.DoneData:
		fmt.Println(data.Reason, data.FinalContent)
	}
}
```

- `chatModel` is any Eino `model.ToolCallingChatModel` (Anthropic, OpenAI, Ollama and others from [eino-ext](https://github.com/cloudwego/eino-ext)).
- `tools` are Eino `schema.ToolInfo` values; `loop.NewToolInfo` builds one from a JSON Schema.
- `executor` implements `loop.ToolExecutor`: `ExecuteTool(ctx, name, arguments) (string, error)`. An error is returned to the model as the tool's result, so it can react.
- The hooks on `loop.Config` (event sink, history compactor, call options, model identity, cost estimator, error classifier) are interfaces too. Every one is optional.

Runnable programs are in [`examples/`](examples): [`minimal`](examples/minimal/main.go), [`steer`](examples/steer/main.go) and [`ask_user`](examples/ask_user/main.go). They use a scripted model and need no API key.

### Controls

| Call | Effect |
|---|---|
| `l.Stop()` | Ends the run at the next iteration boundary, or at once if a pause is waiting. Cannot be lost. Does not interrupt a model call or tool already running; cancel the context for that. |
| `l.Inject(text)` | Adds a user message the model sees on its next call. Inside a pause, it is the reply being waited for. |
| `l.ModifyConfig(loop.ConfigChange{...})` | Changes `MaxIterations` or `Temperature`. Nil fields are left as they are. |

`Inject` and `ModifyConfig` return false when the control queue is full. `registry.Registry[K]` (in `loop/registry`) keeps running loops by a key of your choice (a session or user ID), so a different goroutine or request handler can stop or steer them.

### Stop conditions

| Value | The loop ends when |
|---|---|
| `StopOnNoToolFinish` | the model replies without calling a tool |
| `StopOnFinishTool` | the model calls the tool named `finish`; its arguments are the result |
| `StopOnExternal` | it is stopped, hits `MaxIterations`, or times out |

Each condition tells the model its rule in a system message placed after your own system messages. `Config.StopInstructions` replaces that text. When a limit ends the run, `DoneData.FinalContent` holds the model's last reply.

### Reflection

| `Config.Reflect` | What is reviewed |
|---|---|
| `ReflectOff` (default) | nothing |
| `ReflectOnIteration` | every iteration that does not end the run. A failed review adds a note, the issue and a hint, before the next iteration. |
| `ReflectOnFinish` | the final answer, against your system prompt and the user's request. If it drifted, the run continues with the judge's notes, at most `MaxReopens` times (default 1). |

The judge is a separate call to the same model, forced to answer through a `review_verdict` tool, and it sees none of the run's tools. Reviews never count toward `MaxIterations`, and a failed review is reported and skipped. `IterationReviewPrompt` and `FinishReviewPrompt` replace the built-in instructions.

### Events

Every event has `Type`, `Data`, `Iteration` and `Timestamp`. The payloads are in `loop/event`.

| Type | Data | When |
|---|---|---|
| `thinking` | `ThinkingData` | before each model call |
| `llm_turn` | `LLMTurnData` | after a model call that asked for tools: request, reply, tokens, cost |
| `tool_call` | `ToolCallData` | before a tool runs |
| `tool_result` | `ToolResultData` | after a tool runs |
| `response` | `ResponseData` | the model replied with text |
| `reflection` | `ReflectionData` | a judge's verdict: mode, pass or fail, reason, hint |
| `progress` | `ProgressData` | a control was applied |
| `awaiting_input` | `AwaitingInputData` | a pause is waiting for a person |
| `error` | `ErrorData` | a failure; `Fatal` says whether the run ended |
| `done` | `DoneData` | always last, exactly once |

Intermediate events are dropped when the channel is full, and `DoneData.DroppedEvents` says how many. The `done` event is not dropped: the loop waits for a reader. `l.Result()` returns the outcome without reading events at all.

### Pausing for a person

```go
// askUser handles ask_user calls in place of the executor.
type askUser struct{}

func (askUser) Handle(ctx context.Context, call pause.Call, s pause.Session) (string, bool) {
	s.Emit(event.AwaitingInputData{Question: "Which file?"})
	return s.AwaitReply(ctx) // returns the next l.Inject
}

cfg.Pauses = map[string]pause.Handler{"ask_user": askUser{}}
```

The reply becomes the tool's result. A handler can also run tools and call the model through the `pause.Session`. Returning `false` ends the run with reason `awaiting_human`, for callers that resume later with a new loop.

### Testing your agent

`loop.New` takes the model and the tool executor as interfaces (`model.ToolCallingChatModel`, `loop.ToolExecutor`), so a test can pass mocks of both and run the loop without a provider. To try an agent from the command line without a key, give `steerloop run` a scripted model: `--model script:<file.json>` plays a list of replies (text, tool calls, errors, slow or chunked streams).

## CLI

```sh
export ANTHROPIC_API_KEY=...      # or OPENAI_API_KEY
steerloop run --model anthropic:<model> --tools examples/tools.yaml \
  --user-prompt "Where is FinishToolName defined?"
```

The prompt is required and always goes in `--user-prompt`. Words after the flags are rejected, and stdin is never read as the prompt; it stays free for answering the run.

The trace goes to stderr and the final answer to stdout:

```
── iteration 1
  ▸ grep_repo {"pattern":"^const FinishToolName"}
  ✓ grep_repo ./loop/config.go:41:const FinishToolName = "finish"

── iteration 2

● finished · 2 iteration(s) · 1482 tokens

It is defined once, in loop/config.go (line 41), as the string "finish".
recorded to /path/to/repo/run-recordings/20261025T101508Z-1856286955.jsonl
```

While it runs, type to steer it:

| Input | Effect |
|---|---|
| any text | injected as a user message before the next model call; also answers `ask_user` and confirm questions |
| `/stop` | graceful stop |
| Ctrl-C | graceful stop; a second Ctrl-C aborts |

A run is one turn. To keep talking, use `steerloop chat`.

### `steerloop chat`

```sh
steerloop chat --model anthropic:<model> --tools examples/tools.yaml
```

`chat` is a session at the terminal. It takes the same flags as `run`, except `--output` and `--control-file`, and `--user-prompt` is optional: without it, chat starts at the prompt. The prompt is `❯ `, between two faint rules. Each line you type there starts a new turn in the same conversation, with the history kept, and while a turn runs you steer it as above. However a turn ends (an answer, `/stop`, Ctrl-C, max iterations, a timeout, an error), the prompt comes back. An empty line asks again. Ctrl-D ends the chat (exit 0); Ctrl-C at the prompt, or a second Ctrl-C during a turn, ends it with exit 130. One recording holds every turn. Without a terminal, `chat` refuses and points you to `run`.

### `steerloop run` flags

| Flag | Default | |
|---|---|---|
| `--model` | required | `anthropic:<model>`, `openai:<model>` or `script:<file.json>` (a scripted model: no key, no network) |
| `--user-prompt` | required | the user message |
| `--system-prompt` | | a file holding the system prompt |
| `--tools` | | a tool manifest (YAML) |
| `--stop-condition` | `no-tool-finish` | `no-tool-finish`, `finish-tool` or `external-finish` |
| `--reflect` | `off` | `off`, `on-iteration` or `on-finish` |
| `--max-iterations` | `12` | cap on model calls |
| `--iteration-timeout` | `1m` | time for one model call |
| `--total-timeout` | `10m` | time for the whole run |
| `--temperature` | `-1` | below 0 leaves it to the provider |
| `--max-tokens` | `4096` | cap on reply length, for providers that need one |
| `--ask-user` | | give the model an `ask_user` tool |
| `--skip-confirm` | | run tools marked `confirm` without asking |
| `--output` | `text` | `text`, or `jsonl` for one JSON event per line on stdout |
| `--output-record-path` | `run-recordings` | where every event is recorded (below); `""` turns recording off |
| `--control-file` | | steer the run from a file or FIFO (below) |
| `--debug` | | write the loop's debug log to stderr |

### Recording and replay

Every run is recorded as JSON lines. A folder (the default `run-recordings`, under the repository root) gets one new file per run, readable only by you: a recording holds your prompts and tool output. A path ending in `.jsonl` is used as it is.

```sh
steerloop replay --path run-recordings/<file>.jsonl --speed 0     # instant
steerloop replay --path run.jsonl --max-wait 2s                  # real time, long pauses capped
```

A replay makes no model call and runs no tool. It prints what the live run printed and exits with the code the run ended with. `--speed` scales the recorded gaps (1, the default, is real time; 0 is instant), and `--max-wait` caps any single pause, such as a slow model call or a wait for a person.

### Control file

`--control-file` reads one command per line from a file, or live from a FIFO, so a program can steer a run the way a person does at the terminal:

```jsonl
{"type":"inject","message":"Also count the lines in README.md.","wait_ms":3000}
{"type":"modify_config","max_iterations":20,"temperature":0.1}
{"type":"stop","wait_ms":60000}
```

`wait_ms` delays a line, counted from the previous one. A bad line is reported and skipped. A FIFO stays open for the whole run, so separate writers (`echo ... > ctl`) can steer it one after another.

### Other commands

| | |
|---|---|
| `steerloop tools validate --path <manifest>` | check a manifest and list its tools |
| `steerloop version` | print the version |
| `steerloop help` | list the commands; `steerloop <command> -h` lists a command's flags |

Exit codes: 0 finished, 1 error, 2 usage, 3 max iterations, 4 timeout, 5 awaiting a human, 130 stopped.

### Tool manifest

```yaml
tools:
  - name: grep_repo
    description: Search files under the current directory for a regular expression.
    parameters:
      type: object
      properties:
        pattern: { type: string }
      required: [pattern]
    command: ["grep", "-rn", "--", "{{.pattern}}", "."]
    timeout: 20s
    confirm: false
```

What the CLI guarantees about a manifest tool:

- The command is an argv list and is never passed to a shell. A value expands inside its own element, so `; rm -rf /` arrives at the program as literal text.
- A value that would begin an element with `-` is rejected unless the tool sets `allow_flag_values: true`. Put `--` before values where the program supports it.
- Arguments are checked against `parameters` before the command runs: required names, unknown names, top-level types and enums.
- `confirm: true` asks before each run. With no terminal, no `--control-file` and no `--skip-confirm`, the tool is not run.
- Output is capped (64 KiB by default) and a non-zero exit is returned to the model as an error.
- Only declared tools can run. There is no built-in shell or file tool.

A tool still runs with your permissions and environment. The manifest is the security boundary, so declare narrow tools.

## Limitations

- Tool calls in one model turn run one after another, not in parallel.
- `Stop` waits for the current model call or tool to return.
- The loop holds the conversation in memory. Persisting and resuming it is the caller's job; `l.Messages()` and the `llm_turn` events give you the data.
- `HistoryCompactor` is a hook. No summarization strategy is included.
- Token usage of reflection reviews is not yet counted in `DoneData.LLMUsage`.
- `/stop` is the only slash command.
- A replay of a chat shows each turn's events, not the prompts typed at `❯ `.
- The CLI's argument validation covers required names, unknown names, top-level types and enums, not the whole of JSON Schema.
- The CLI supports Anthropic and OpenAI models. The library accepts any Eino chat model.
- MCP servers are not supported as a tool source yet.

## Development

```sh
go test -race ./...                                    # no network, no API key
go test ./cmd/steerloop -update                        # rewrite golden files
mise run mocks                                         # regenerate the _mocks folders from .mockery.yml
STEERLOOP_LIVE=1 STEERLOOP_LIVE_MODEL=openai:<model> \
  go test ./cmd/steerloop -run TestCLISuite -testify.m LiveSmoke
vhs docs/demo/steer.tape                               # re-record the demo GIF (needs ANTHROPIC_API_KEY)
```

## License

Apache-2.0. See [LICENSE](LICENSE).
