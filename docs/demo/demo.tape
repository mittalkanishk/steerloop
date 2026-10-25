# Records docs/demo/demo.gif. Run from the repository root: vhs docs/demo/demo.tape
# The model is scripted (docs/demo/script.json): no API key, no network.
# The tools are real: grep and wc from examples/tools.yaml run on a clean copy
# of the repository in /tmp, so local recordings and .git stay out of the search.

Output docs/demo/demo.gif

Set Shell "bash"
Set FontSize 15
Set Width 1200
Set Height 620
Set Padding 18
Set TypingSpeed 35ms
Set Theme "Catppuccin Mocha"

Hide
Type "export GOWORK=off PATH=/tmp/steerloop-demo:$PATH PS1='$ ' && rm -rf /tmp/steerloop-demo && mkdir -p /tmp/steerloop-demo/src && rsync -a --exclude .git --exclude run-recordings --exclude youtube ./ /tmp/steerloop-demo/src/ && cd /tmp/steerloop-demo/src && go build -o /tmp/steerloop-demo/steerloop ./cmd/steerloop && clear"
Enter
Sleep 6s
Show

Type "steerloop tools validate --path examples/tools.yaml"
Sleep 300ms
Enter
Sleep 2s

Type "steerloop run --model script:docs/demo/script.json --tools examples/tools.yaml \"
Enter
Type "  --output-record-path /tmp/steerloop-demo/run.jsonl --user-prompt 'Where is FinishToolName defined?'"
Sleep 300ms
Enter
Sleep 4s

Ctrl+L
Sleep 300ms
Type "steerloop replay --path /tmp/steerloop-demo/run.jsonl --speed 2"
Sleep 300ms
Enter
Sleep 4s
