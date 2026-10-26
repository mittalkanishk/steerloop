# Testing the CLI by hand

Every `steerloop` command in one place, each with the result to expect.
Sections 1–8 use scripted models from `cmd/steerloop/testdata`: no API key,
no network. Section 9 uses a real model.

Run everything from the repository root, in one shell.

## 0. Setup

```sh
export GOWORK=off
go build -o /tmp/steerloop-bin/steerloop ./cmd/steerloop
export PATH=/tmp/steerloop-bin:$PATH
T=cmd/steerloop/testdata             # scripted models and tools.yaml
R=/tmp/steerloop-manual              # scratch: recordings, control files
rm -rf $R && mkdir -p $R
```

`echo $?` after a command prints its exit code:

| Code | Meaning |
|---|---|
| 0 | finished |
| 1 | error (model or tool failure) |
| 2 | usage error (bad flag, missing file) |
| 3 | hit `--max-iterations` |
| 4 | hit a timeout |
| 5 | waiting on a person who was not there |
| 130 | stopped (`/stop`, a control file or Ctrl-C) |

## 1. version, help, tools validate

```sh
steerloop version                                  # steerloop vX.Y.Z (goX.Y)            exit 0
steerloop help                                     # usage for every command            exit 0
steerloop run -h                                   # every run flag                     exit 0
steerloop replay -h                                # --path, --speed, --max-wait        exit 0
steerloop frobnicate                               # unknown command "frobnicate"       exit 2

steerloop tools validate --path $T/tools.yaml      # ok: 4 tool(s), danger asks first   exit 0
steerloop tools validate --path README.md          # invalid manifest: parse manifest…  exit 1
steerloop tools validate                           # --path is required                 exit 2
```

## 2. A run, its output and its recording

```sh
# A tool call, then an answer. Recorded to $R/a.jsonl.
steerloop run --model script:$T/tool_then_answer.json --tools $T/tools.yaml \
  --user-prompt hi --output-record-path $R/a.jsonl
# iteration 1: say → "hello from the tool"; finished · 2 iteration(s) · 310 tokens   exit 0

# The same run as JSONL on stdout, recording off.
steerloop run --model script:$T/tool_then_answer.json --tools $T/tools.yaml \
  --user-prompt hi --output jsonl --output-record-path ""
# one event per line: thinking, llm_turn, tool_call, tool_result, …, done            exit 0

# Default recording: a new file in run-recordings/ per run.
steerloop run --model script:$T/tool_then_answer.json --tools $T/tools.yaml --user-prompt hi
# ends with "recorded to …/run-recordings/<file>.jsonl"                             exit 0

# System prompt and the debug log (on stderr).
echo "You answer in French." > $R/system.txt
steerloop run --model script:$T/tool_then_answer.json --tools $T/tools.yaml \
  --system-prompt $R/system.txt --debug --user-prompt hi --output-record-path ""
# DEBUG "agent loop seed" shows the system prompt first                              exit 0
```

## 3. Replay

```sh
steerloop replay --path $R/a.jsonl                       # same trace, original pace   exit 0
steerloop replay --path $R/a.jsonl --speed 0             # instantly                   exit 0
steerloop replay --path $R/a.jsonl --speed 2 --max-wait 1s  # 2x, no pause over 1s     exit 0
steerloop replay --path /nope.jsonl                      # no such file or directory   exit 2
steerloop replay --path $R/a.jsonl --speed 0 --max-wait 1s  # --max-wait has no effect  exit 2
```

## 4. How a run ends

```sh
# no-tool-finish (default): a reply with no tool call ends it. See section 2.

# finish-tool: prose does not end it; calling finish does.
steerloop run --model script:$T/finish_tool.json --tools $T/tools.yaml \
  --stop-condition finish-tool --user-prompt hi --output-record-path ""
# 3 iterations, final answer {"result":"all done"}                                   exit 0

# external-finish: only a stop or a limit ends it; the last reply is kept.
steerloop run --model script:$T/tool_then_answer.json --tools $T/tools.yaml \
  --stop-condition external-finish --max-iterations 4 --user-prompt hi --output-record-path ""
# max_iterations · 4 iteration(s), prints "The tool printed: …"                     exit 3

# Iteration cap.
steerloop run --model script:$T/never_ends.json --tools $T/tools.yaml \
  --max-iterations 3 --user-prompt hi --output-record-path ""
# max_iterations · 3 iteration(s)                                                    exit 3

# Timeout: nap sleeps 5s, the run is allowed 1s.
steerloop run --model script:$T/nap.json --tools $T/tools.yaml \
  --total-timeout 1s --iteration-timeout 1s --user-prompt hi --output-record-path ""
# ✗ nap timed out; timeout · 1 iteration(s), after about 1s                          exit 4

# The model fails.
steerloop run --model script:$T/model_error.json --user-prompt hi --output-record-path ""
# error: 401 invalid api key                                                         exit 1
```

## 5. Reflection

The judge is the same model, so the script carries its `review_verdict` calls.

```sh
cat > $R/reopen.json <<'EOF'
{"steps": [
  {"content": "The answer is 41."},
  {"tool_calls": [{"id": "r1", "name": "review_verdict", "arguments": {"verdict": "fail", "reason": "off by one", "hint": "check the sum again", "confidence": 0.8}}]},
  {"content": "The answer is 42."},
  {"tool_calls": [{"id": "r2", "name": "review_verdict", "arguments": {"verdict": "pass", "reason": "correct now", "confidence": 0.9}}]}
]}
EOF
steerloop run --model script:$R/reopen.json --reflect on-finish \
  --user-prompt "what is the answer" --output-record-path ""
# ◆ on_finish fail: off by one, the run reopens, final answer "The answer is 42."   exit 0

# A judge that never gives a verdict: the error is shown, the answer stands.
steerloop run --model script:$T/tool_then_answer.json --tools $T/tools.yaml \
  --reflect on-finish --user-prompt hi --output-record-path ""
# error: finish review failed: judge returned no verdict                             exit 0
```

## 6. Steering

### From a control file (no terminal needed)

```sh
# inject: a message the model sees on its next call.
echo '{"type":"inject","message":"also say bye"}' > $R/inject.jsonl
steerloop run --model script:$T/never_ends.json --tools $T/tools.yaml --max-iterations 3 \
  --control-file $R/inject.jsonl --user-prompt hi --output-record-path ""
# "User instruction received: also say bye" after iteration 1                       exit 3

# modify_config: lower the iteration cap mid-run.
echo '{"type":"modify_config","max_iterations":2}' > $R/modify.jsonl
steerloop run --model script:$T/never_ends.json --tools $T/tools.yaml \
  --control-file $R/modify.jsonl --user-prompt hi --output-record-path ""
# "Configuration updated", then max_iterations · 2 iteration(s)                     exit 3

# stop, delayed with wait_ms.
echo '{"wait_ms":200,"type":"stop"}' > $R/stop.jsonl
steerloop run --model script:$T/slow_tools.json --tools $T/tools.yaml \
  --control-file $R/stop.jsonl --user-prompt hi --output-record-path ""
# stopped · 1 iteration(s)                                                           exit 130

# A FIFO: commands arrive while it runs. Two terminals.
mkfifo $R/ctl.fifo
steerloop run --model script:$T/never_ends.json --tools $T/tools.yaml --max-iterations 1000 \
  --control-file $R/ctl.fifo --user-prompt hi --output-record-path ""     # terminal 1
echo '{"type":"stop"}' > /tmp/steerloop-manual/ctl.fifo                   # terminal 2
# terminal 1: stopped · N iteration(s)                                               exit 130
```

### From the terminal (type while it runs)

```sh
steerloop run --model script:$T/nap.json --tools $T/tools.yaml --user-prompt hi --output-record-path ""
```

- Type `hello` and Enter: "User instruction received: hello".
- Type `/stop` and Enter: stopped, exit 130.
- Press Ctrl-C once: stops after the current iteration, exit 130. Twice: aborts at once.

## 7. Approvals and questions

A piped or redirected stdin counts as nobody there, so these first runs
need no input.

```sh
# danger is marked confirm and nobody can approve it, so it is not run.
steerloop run --model script:$T/confirm.json --tools $T/tools.yaml \
  --user-prompt hi --output-record-path "" </dev/null
# danger: "This tool needs approval and nobody is available to give it."             exit 0

# --skip-confirm approves it.
steerloop run --model script:$T/confirm.json --tools $T/tools.yaml \
  --skip-confirm --user-prompt hi --output-record-path "" </dev/null
# danger: removed build/                                                             exit 0

# ask_user with nobody there: the run waits on a person and ends.
steerloop run --model script:$T/ask_user.json --ask-user \
  --user-prompt hi --output-record-path "" </dev/null
# ? Which colour? [red / blue]; awaiting_human                                       exit 5
```

The same runs in a terminal, without `</dev/null`, wait for you:

```sh
steerloop run --model script:$T/confirm.json --tools $T/tools.yaml --user-prompt hi --output-record-path ""
# answer y: danger runs ("removed build/"); answer n: it is refused
steerloop run --model script:$T/ask_user.json --ask-user --user-prompt hi --output-record-path ""
# type blue: "You chose blue."
```

## 8. Bad input (exit 2, every problem listed at once)

```sh
steerloop run --model "" --temperature 3 --max-iterations 0 --reflect bad --skip-confirm
# --user-prompt is required, --model is required, --temperature must be at most 2,
# --max-iterations must be at least 1, --reflect must be …, --skip-confirm has no effect without --tools
steerloop run --model script:x --stop-condition never --user-prompt hi
# --stop-condition must be no-tool-finish, finish-tool or external-finish, not "never"
steerloop run --model script:x --reflect on-finish --stop-condition external-finish --user-prompt hi
# --reflect on-finish has no effect with --stop-condition external-finish
steerloop run --model script:x --iteration-timeout 2m --total-timeout 1m --user-prompt hi
# --iteration-timeout (2m0s) must not exceed --total-timeout (1m0s)
steerloop run --model script:x --output yaml --user-prompt hi
# --output must be text or jsonl, not "yaml"
steerloop run --model script:x --user-prompt fix the bug
# unexpected argument "the": … a value with spaces needs quotes
steerloop run --model gemini:x --user-prompt hi
# unknown provider "gemini": use anthropic, openai or script
steerloop run --model script:/nope.json --user-prompt hi
# open /nope.json: no such file or directory
```

## 9. A real model

Needs `ANTHROPIC_API_KEY` (or `OPENAI_API_KEY` with `--model openai:<model>`).
Uses the demo manifest so the model can search this repository.

```sh
steerloop run --model anthropic:claude-sonnet-5-5 --tools docs/demo/tools.yaml --max-iterations 5 \
  --output-record-path $R/real.jsonl \
  --user-prompt 'List the 3 stop conditions in loop/config.go, one short line each, and create a notes/ folder.'
# grep_repo calls, then "Run make_dir?" (answer n), then the answer                  exit 0
# while it runs, type: Also give the README.md line for each

steerloop replay --path $R/real.jsonl --speed 5                # no model call, no tool run
```

The same with reflection and the finish tool:

```sh
steerloop run --model anthropic:claude-sonnet-5-5 --tools docs/demo/tools.yaml \
  --stop-condition finish-tool --reflect on-finish --output-record-path "" \
  --user-prompt 'How many lines is loop/config.go? Call finish with the number.'
# count_lines, finish, then ◆ on_finish pass/fail from the judge                     exit 0
```
