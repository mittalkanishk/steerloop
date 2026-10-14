<!-- Title: STLOOP-xxx: what changed -->

# Pull Request Details

STLOOP-xxx

## What Changed

-

## How to Test

1. `go test -race ./...`
2. From the repo root, in a terminal:

```bash
mise run steerloop -- run --model <provider:model> --tools examples/tools.yaml "<prompt>"
```

Say what you expect to see. If the change is a terminal session, confirm the answer is printed, then `>`, and that another line continues with the earlier messages. Ctrl+C exits. The same command with `< /dev/null` exits after the answer.
