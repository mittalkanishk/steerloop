# Records docs/demo/steer.gif. Run from the repository root with ANTHROPIC_API_KEY
# set: vhs docs/demo/steer.tape
#
# A real model (anthropic:claude-sonnet-5-5) and real tools from
# docs/demo/tools.yaml, run on a clean copy of the repository in /tmp. The take shows the loop calling
# tools, the permission gate in front of make_dir, a message typed while it
# runs, and a replay of the recorded run. The key is never typed on screen.

Output docs/demo/steer.gif

Set Shell "bash"
Set FontSize 13
Set Width 1500
Set Height 720
Set Padding 18
Set TypingSpeed 30ms
Set Theme "Catppuccin Mocha"
Set WaitTimeout 120s
Set WaitPattern /^\$\s*$/
Set PlaybackSpeed 1.5

Hide
Type "export GOWORK=off PATH=/tmp/steer-demo:$PATH PS1='$ ' && rm -rf /tmp/steer-demo && mkdir -p /tmp/steer-demo/src && rsync -a --exclude .git --exclude run-recordings --exclude youtube --exclude docs/demo --exclude mise.toml ./ /tmp/steer-demo/src/ && cp docs/demo/tools.yaml /tmp/steer-demo/src/tools.yaml && cd /tmp/steer-demo/src && go build -o /tmp/steer-demo/steerloop ./cmd/steerloop && clear"
Enter
Sleep 8s
Show

# 1. A real run: model, tools, and a task that takes a few iterations.
Type "steerloop run --model anthropic:claude-sonnet-5-5 --tools tools.yaml --max-iterations 5 \"
Enter
Type "  --output-record-path /tmp/steer-demo/run.jsonl \"
Enter
Type "  --user-prompt 'List the 3 stop conditions in loop/config.go, one short line each, and create a notes/ folder.'"
Sleep 400ms
Enter

# 2. The permission gate: make_dir is marked confirm, so the harness asks.
Wait+Screen /Run make_dir/
Sleep 1.2s
Type "n"
Enter

# 3. Steer it while it works: the next line typed reaches the model's next
#    call. Typed in one burst so it does not interleave with the trace.
Sleep 300ms
Type@5ms "Also give the README.md line for each (grep for: StopOn)"
Enter

# 4. The run ends and says where it was recorded; wait for the prompt.
Wait
Sleep 3s

# 5. Replay the recording: same trace, no model call, no tool run.
Ctrl+L
Sleep 300ms
Type "steerloop replay --path /tmp/steer-demo/run.jsonl --speed 5"
Sleep 300ms
Enter
Wait
Sleep 3s
