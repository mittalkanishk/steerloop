# steerloop

A controllable agent harness for Go, built on [CloudWeGo Eino](https://github.com/cloudwego/eino).

A `harness` (or loop) calls a tool-calling chat model, runs the tools it asks for, feeds the results back and repeats. What this package adds is control over a run that is already in flight:

- **Steer it.** Stop it, inject a user message, or change its iteration cap and temperature while it runs.
- **Watch it.** A typed event stream: model turns, tool calls, tool results, the final outcome.
- **Pause it for a person.** Hand named tool calls to a function that can wait for a human answer.
- **Survive a flaky provider.** Retries on transient failures, and a watchdog that aborts a streamed turn that stops producing chunks.
- **Audit it.** One record per model turn with the request, the reply, token counts and cost.

It also ships a CLI, `steerloop`, that runs a loop from the terminal over command-line tools you declare in a YAML file.

Status: v0. The API may change before v1.

## Install

```sh
go get github.com/mittalkanishk/steerloop                              # library
go install github.com/mittalkanishk/steerloop/cmd/steerloop@latest     # CLI
```

Requires Go 1.26 or later.

## Library

```go
loop, err := steerloop.New(steerloop.DefaultConfig(), chatModel, tools, executor, nil)
if err != nil {
	return err
}

messages := []*schema.Message{{Role: schema.User, Content: "Upper-case the word hello."}}
for evt := range loop.Start(ctx, messages) {
	switch data := evt.Data.(type) {
	case steerloop.ToolCallData:
		fmt.Println("tool call:", data.Name, data.Arguments)
	case steerloop.ToolResultData:
		fmt.Println("tool result:", data.Result)
	case steerloop.DoneData:
		fmt.Println(data.Reason, data.FinalContent)
	}
}
```

- `chatModel` is any Eino `model.ToolCallingChatModel` (Anthropic, OpenAI, Ollama and others from [eino-ext](https://github.com/cloudwego/eino-ext)).
- `tools` are Eino `schema.ToolInfo` values; `steerloop.NewToolInfo` builds one from a JSON Schema.
- `executor` implements `ExecuteTool(ctx, name, arguments) (string, error)`. An error is returned to the model as the tool's result, so it can react.

Runnable programs are in [`examples/`](examples): [`minimal`](examples/minimal/main.go), [`steer`](examples/steer/main.go) and [`ask_user`](examples/ask_user/main.go). They use a scripted model and need no API key.

### Controls

| Call | Effect |
|---|---|
| `loop.Stop()` | Ends the run at the next step boundary, or at once if a pause is waiting. Cannot be lost. Does not interrupt a model call or tool already running; cancel the context for that. |
| `loop.Inject(text)` | Adds a user message the model sees on its next call. Inside a pause, it is the reply being waited for. |
| `loop.SendControlSignal(NewModifyConfigControl(...))` | Changes `MaxIterations`, `Temperature` or `EnableReflection`. |

`Registry[K]` keeps running loops by a key of your choice (a session or user ID), so a different goroutine or request handler can stop or steer them.

### Stop conditions

| Value | The loop ends when |
|---|---|
| `StopOnNoToolCalls` | the model replies without calling a tool |
| `StopOnFinishTool` | the model calls the tool named `finish`; its arguments are the result |
| `StopOnExplicit` | it is stopped, hits `MaxIterations`, or times out |

### Events

Every event has `Type`, `Data`, `Iteration` and `Timestamp`.

| Type | Data | When |
|---|---|---|
| `thinking` | `ThinkingData` | before each model call |
| `llm_turn` | `LLMTurnData` | after a model call that asked for tools: request, reply, tokens, cost |
| `tool_call` | `ToolCallData` | before a tool runs |
| `tool_result` | `ToolResultData` | after a tool runs |
| `response` | `ResponseData` | the model replied with text |
| `reflection` | `ReflectionData` | the optional self-review reply |
| `progress` | `ProgressData` | a control was applied |
| `awaiting_input` | `AwaitingInputData` | a pause is waiting for a person |
| `error` | `ErrorData` | a failure; `Fatal` says whether the run ended |
| `done` | `DoneData` | always last, exactly once |

Intermediate events are dropped when the channel is full, and `DoneData.DroppedEvents` says how many. The `done` event is not dropped: the loop waits for a reader. `loop.Result()` returns the outcome without reading events at all.

### Pausing for a person

```go
cfg.Pauses = map[string]steerloop.PauseFunc{
	"ask_user": func(ctx context.Context, in steerloop.PauseIn) (string, bool) {
		in.Emit(steerloop.EventAwaitingInput, steerloop.AwaitingInputData{Question: "Which file?"})
		return in.Block(ctx) // returns the next loop.Inject
	},
}
```

The reply becomes the tool's result. Returning `false` ends the run with reason `awaiting_human`, for callers that resume later with a new loop.

### Testing your agent

`steerlooptest.ScriptedModel` plays a list of replies (text, tool calls, errors, slow or chunked streams) so loop behaviour can be tested without a provider. `steerlooptest.RecordingExecutor` records tool calls.

## CLI

```sh
export ANTHROPIC_API_KEY=...      # or OPENAI_API_KEY
steerloop run --model anthropic:<model> --tools examples/tools.yaml \
  --prompt "How many lines is README.md, and which files mention Registry?"
```

The prompt is required and always goes in `--prompt`. Words after the flags are rejected, and stdin is never read as the prompt; it stays free for answering the run.

The trace goes to stderr and the final answer to stdout:

```
model anthropic:<model> · stop no-tool-calls · max 12 steps
[step 1]
  → count_lines {"path":"README.md"}
  ← count_lines: 180 README.md
[step 2]
done: finished · 2 step(s) · 1482 tokens
README.md has 180 lines.
```

While it runs, type to steer it:

| Input | Effect |
|---|---|
| any text | injected as a user message before the next model call |
| `/stop` | graceful stop |
| `/max <n>`, `/temp <x>`, `/reflect on\|off` | change the configuration |
| Ctrl-C | graceful stop; a second Ctrl-C aborts |

Other commands and flags:

| | |
|---|---|
| `--output jsonl` | one JSON event per line on stdout |
| `--record run.jsonl` | also write every event to a file |
| `steerloop replay run.jsonl` | re-render a recording; makes no model call and runs no tool |
| `--ask` | give the model an `ask_user` tool that asks on the terminal |
| `--control file` | read controls as JSON lines from a file or FIFO, for scripting |
| `--stop finish-tool` | run until the model calls `finish` |
| `--model script:file.json` | play back a scripted model; no key, no network |
| `steerloop tools validate file` | check a manifest |

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
- `confirm: true` asks before each run. With no terminal and no `--yes`, the tool is not run.
- Output is capped (64 KiB by default) and a non-zero exit is returned to the model as an error.
- Only declared tools can run. There is no built-in shell or file tool.

A tool still runs with your permissions and environment. The manifest is the security boundary, so declare narrow tools.

## Limitations

- Tool calls in one model turn run one after another, not in parallel.
- `Stop` waits for the current model call or tool to return.
- The loop holds the conversation in memory. Persisting and resuming it is the caller's job; `GetRunningMessages` and the `llm_turn` events give you the data.
- `HistoryCompactor` is a hook. No summarization strategy is included.
- The CLI's argument validation covers required names, unknown names, top-level types and enums, not the whole of JSON Schema.
- The CLI supports Anthropic and OpenAI models. The library accepts any Eino chat model.
- MCP servers are not supported as a tool source yet.

## Development

```sh
go test -race ./...                       # no network, no API key
go test ./cmd/steerloop -update           # rewrite golden files
STEERLOOP_LIVE=1 STEERLOOP_LIVE_MODEL=openai:<model> go test -run LiveSmoke ./cmd/steerloop
```

## License

Apache-2.0. See [LICENSE](LICENSE).
